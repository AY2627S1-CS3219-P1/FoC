package auth

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/models"
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

// NewRepository uses db for magic-link and session persistence.
func NewRepository(db *gorm.DB) Repository { return &gormRepository{db: db} }

// DomainAllowed reports whether the whitelist is empty or contains domain,
// using the database's case-insensitive comparison. Query errors are propagated.
func (r *gormRepository) DomainAllowed(ctx context.Context, domain string) (bool, error) {
	var ok bool
	err := r.db.WithContext(ctx).Raw(`
		SELECT NOT EXISTS (SELECT 1 FROM allowed_email_domains)
		    OR EXISTS (SELECT 1 FROM allowed_email_domains WHERE domain = ?)`, domain).
		Scan(&ok).Error
	return ok, err
}

// UserByEmail looks up an email case-insensitively, returning nil, nil if absent.
// Other database errors are returned unchanged.
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

// RecentLinkCounts counts tokens created strictly after since for email and ip,
// including used and expired tokens of either purpose. An empty ip yields a zero
// IP count. Database errors, including invalid IP casts, are propagated.
func (r *gormRepository) RecentLinkCounts(ctx context.Context, email, ip string, since time.Time) (int64, int64, error) {
	var out struct{ ByEmail, ByIP int64 }
	err := r.db.WithContext(ctx).Raw(`
		SELECT count(*) FILTER (WHERE email = ?)                        AS by_email,
		       count(*) FILTER (WHERE ? <> '' AND requested_ip = ?::inet) AS by_ip
		FROM auth_tokens WHERE created_at > ?`,
		email, ip, nullIfEmpty(ip), since).Scan(&out).Error
	return out.ByEmail, out.ByIP, err
}

// CreateToken stores t with its supplied token hash and expiry without invalidating
// older tokens. Database errors are returned unchanged.
func (r *gormRepository) CreateToken(ctx context.Context, t *models.AuthToken) error {
	return r.db.WithContext(ctx).Create(t).Error
}

// consume atomically marks a token used. Covers invalid, expired and reused
// links in one statement and is race-free (U1.2.4-7, U2.1.3-5).
// It returns the updated token, or errLinkInvalid if the hash or purpose does not
// match an unused token expiring strictly after now. Database errors are propagated.
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

// Register consumes an unused registration token identified by its SHA-256 hash,
// valid strictly after now, and creates u and s in one transaction. It replaces
// u.Email with the token email and sets u.Role to user, or super_admin when it claims
// the bootstrap row (also recording that role change). It sets s.UserID to u.ID;
// callers supply the session hash and expiry. Mutations to u and s can survive rollback.
// Invalid, expired, or used links return errLinkInvalid. A GORM duplicate-key error
// creating u becomes errEmailTaken; other database and transaction errors propagate.
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

// Login consumes an unused login token identified by its SHA-256 hash, valid
// strictly after now, and creates s for the token's user in one transaction.
// It returns that user and sets s.UserID; callers supply the session hash and expiry.
// Mutations to s can survive rollback. Invalid, expired, or used links and missing
// users return errLinkInvalid; other database and transaction errors propagate.
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

// ActiveSession returns the unrevoked session matching the SHA-256 token hash
// and its user, provided the session expires strictly after now. It returns
// nil, nil, nil if either is absent. It does not filter by user role or touch
// last_seen_at. Other database errors are propagated.
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

// TouchSession sets last_seen_at to now, even for expired or revoked sessions.
// A missing session is a no-op; database errors are propagated.
func (r *gormRepository) TouchSession(ctx context.Context, id uuid.UUID, now time.Time) error {
	return r.db.WithContext(ctx).Model(&models.Session{}).Where("id = ?", id).Update("last_seen_at", now).Error
}

// RevokeSession sets revoked_at to now if the session has not been revoked,
// including expired sessions. Missing or already revoked sessions are no-ops;
// database errors are propagated.
func (r *gormRepository) RevokeSession(ctx context.Context, id uuid.UUID, now time.Time) error {
	return r.db.WithContext(ctx).Model(&models.Session{}).
		Where("id = ? AND revoked_at IS NULL", id).Update("revoked_at", now).Error
}

// RevokeUserSessions sets revoked_at to now on all unrevoked sessions for the
// user, including expired sessions. It returns the affected row count and any
// database error.
func (r *gormRepository) RevokeUserSessions(ctx context.Context, userID uuid.UUID, now time.Time) (int64, error) {
	res := r.db.WithContext(ctx).Model(&models.Session{}).
		Where("user_id = ? AND revoked_at IS NULL", userID).Update("revoked_at", now)
	return res.RowsAffected, res.Error
}

// nullIfEmpty returns nil for an empty string, or a pointer to a copy of s.
func nullIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
