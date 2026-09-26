package auth

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"user-service/internal/models"
)

var (
	errLinkInvalid = errors.New("link invalid, expired or used")
	errEmailTaken  = errors.New("email already registered")
)

type Repository interface {
	// DomainAllowed is true when the whitelist is empty or contains domain.
	DomainAllowed(ctx context.Context, domain string) (bool, error)
	// UserByEmail returns nil, nil when there is no such user.
	UserByEmail(ctx context.Context, email string) (*models.User, error)
	// RecentLinkCounts counts links issued since `since` for email and for ip.
	RecentLinkCounts(ctx context.Context, email, ip string, since time.Time) (byEmail, byIP int64, err error)
	CreateToken(ctx context.Context, t *models.AuthToken) error
	// Register consumes a register token, creates the user (email taken from
	// the token), bootstraps the first super_admin and opens a session, all
	// in one tx. Returns errLinkInvalid / errEmailTaken.
	Register(ctx context.Context, tokenHash []byte, now time.Time, u *models.User, s *models.Session) error
	// Login consumes a login token and opens a session. Returns errLinkInvalid.
	Login(ctx context.Context, tokenHash []byte, now time.Time, s *models.Session) (*models.User, error)
	// ActiveSession returns nil, nil, nil when no live session matches.
	ActiveSession(ctx context.Context, tokenHash []byte, now time.Time) (*models.Session, *models.User, error)
	TouchSession(ctx context.Context, id uuid.UUID, now time.Time) error
	RevokeSession(ctx context.Context, id uuid.UUID, now time.Time) error
	RevokeUserSessions(ctx context.Context, userID uuid.UUID, now time.Time) (int64, error)
}

type gormRepository struct{ db *gorm.DB }

func NewRepository(db *gorm.DB) Repository { return &gormRepository{db: db} }

func (r *gormRepository) DomainAllowed(ctx context.Context, domain string) (bool, error) {
	var ok bool
	err := r.db.WithContext(ctx).Raw(`
		SELECT NOT EXISTS (SELECT 1 FROM allowed_email_domains)
		    OR EXISTS (SELECT 1 FROM allowed_email_domains WHERE domain = ?)`, domain).
		Scan(&ok).Error
	return ok, err
}

func (r *gormRepository) UserByEmail(ctx context.Context, email string) (*models.User, error) {
	var u models.User
	err := r.db.WithContext(ctx).Take(&u, "email = ?", email).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &u, nil
}

func (r *gormRepository) RecentLinkCounts(ctx context.Context, email, ip string, since time.Time) (int64, int64, error) {
	var out struct{ ByEmail, ByIP int64 }
	err := r.db.WithContext(ctx).Raw(`
		SELECT count(*) FILTER (WHERE email = ?)                        AS by_email,
		       count(*) FILTER (WHERE ? <> '' AND requested_ip = ?::inet) AS by_ip
		FROM auth_tokens WHERE created_at > ?`,
		email, ip, nullIfEmpty(ip), since).Scan(&out).Error
	return out.ByEmail, out.ByIP, err
}

func (r *gormRepository) CreateToken(ctx context.Context, t *models.AuthToken) error {
	return r.db.WithContext(ctx).Create(t).Error
}

// consume atomically marks a token used. Covers invalid, expired and reused
// links in one statement and is race-free (U1.2.4-7, U2.1.3-5).
func consume(tx *gorm.DB, hash []byte, purpose models.TokenPurpose, now time.Time) (*models.AuthToken, error) {
	var t models.AuthToken
	err := tx.Raw(`
		UPDATE auth_tokens SET used_at = ?
		WHERE token_hash = ? AND purpose = ? AND used_at IS NULL AND expires_at > ?
		RETURNING *`, now, hash, purpose, now).Scan(&t).Error
	if err != nil {
		return nil, err
	}
	if t.ID == uuid.Nil {
		return nil, errLinkInvalid
	}
	return &t, nil
}

func (r *gormRepository) Register(ctx context.Context, hash []byte, now time.Time, u *models.User, s *models.Session) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		t, err := consume(tx, hash, models.TokenPurposeRegister, now)
		if err != nil {
			return err
		}
		u.Email = t.Email
		u.Role = models.RoleUser
		if err := tx.Create(u).Error; err != nil {
			if errors.Is(err, gorm.ErrDuplicatedKey) {
				return errEmailTaken
			}
			return err
		}

		// U4.2: exactly one first signup wins the singleton row.
		res := tx.Exec(`INSERT INTO admin_bootstrap (user_id) VALUES (?) ON CONFLICT DO NOTHING`, u.ID)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 1 {
			u.Role = models.RoleSuperAdmin
			if err := tx.Model(u).Update("role", u.Role).Error; err != nil {
				return err
			}
			reason := "first signup (admin bootstrap)"
			if err := tx.Create(&models.RoleChange{
				UserID: u.ID, FromRole: models.RoleUser, ToRole: models.RoleSuperAdmin, Reason: &reason,
			}).Error; err != nil {
				return err
			}
		}

		s.UserID = u.ID
		return tx.Create(s).Error
	})
}

func (r *gormRepository) Login(ctx context.Context, hash []byte, now time.Time, s *models.Session) (*models.User, error) {
	var u models.User
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		t, err := consume(tx, hash, models.TokenPurposeLogin, now)
		if err != nil {
			return err
		}
		if err := tx.Take(&u, "id = ?", t.UserID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return errLinkInvalid
			}
			return err
		}
		s.UserID = u.ID
		return tx.Create(s).Error
	})
	if err != nil {
		return nil, err
	}
	return &u, nil
}

func (r *gormRepository) ActiveSession(ctx context.Context, hash []byte, now time.Time) (*models.Session, *models.User, error) {
	var s models.Session
	err := r.db.WithContext(ctx).
		Take(&s, "token_hash = ? AND revoked_at IS NULL AND expires_at > ?", hash, now).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	var u models.User
	if err := r.db.WithContext(ctx).Take(&u, "id = ?", s.UserID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil, nil
		}
		return nil, nil, err
	}
	return &s, &u, nil
}

func (r *gormRepository) TouchSession(ctx context.Context, id uuid.UUID, now time.Time) error {
	return r.db.WithContext(ctx).Model(&models.Session{}).Where("id = ?", id).Update("last_seen_at", now).Error
}

func (r *gormRepository) RevokeSession(ctx context.Context, id uuid.UUID, now time.Time) error {
	return r.db.WithContext(ctx).Model(&models.Session{}).
		Where("id = ? AND revoked_at IS NULL", id).Update("revoked_at", now).Error
}

func (r *gormRepository) RevokeUserSessions(ctx context.Context, userID uuid.UUID, now time.Time) (int64, error) {
	res := r.db.WithContext(ctx).Model(&models.Session{}).
		Where("user_id = ? AND revoked_at IS NULL", userID).Update("revoked_at", now)
	return res.RowsAffected, res.Error
}

func nullIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
