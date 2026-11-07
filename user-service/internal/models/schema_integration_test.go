// Schema tests: run every migration up, down and up, then check constraints. Needs a throwaway TEST_DATABASE_URL.

package models_test

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/database"
	m "github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/models"
)

func setup(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	db, err := database.Open(dsn, 5, 5)
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	p, err := database.Migrator(db)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Up(ctx); err != nil {
		t.Fatalf("up: %v", err)
	}
	if _, err := p.DownTo(ctx, 0); err != nil {
		t.Fatalf("down: %v", err)
	}
	if _, err := p.Up(ctx); err != nil {
		t.Fatalf("up again: %v", err)
	}
	return db
}

func hash() []byte {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	h := sha256.Sum256(b)
	return h[:]
}

func newUser(t *testing.T, db *gorm.DB, email string) *m.User {
	t.Helper()
	u := &m.User{Email: email, DisplayName: "T", Role: m.RoleUser}
	if err := db.Create(u).Error; err != nil {
		t.Fatal(err)
	}
	return u
}

func TestSchema(t *testing.T) {
	db := setup(t)
	now := time.Now().UTC()

	admin := newUser(t, db, "admin@u.nus.edu")
	alice := newUser(t, db, "alice@u.nus.edu")

	t.Run("roles: seeded, default, FK enforced", func(t *testing.T) {
		var roles []m.Role
		if err := db.Find(&roles).Error; err != nil {
			t.Fatal(err)
		}
		if len(roles) != len(m.AllRoles) {
			t.Fatalf("seeded %d roles, want %d", len(roles), len(m.AllRoles))
		}
		for _, r := range roles {
			if !r.Name.Valid() {
				t.Errorf("DB role %q has no Go const", r.Name)
			}
		}
		if err := db.Exec(
			"INSERT INTO users (email, display_name) VALUES ('norole@u.nus.edu', 'N')",
		).Error; err != nil {
			t.Fatal(err)
		}
		var def m.User
		if err := db.First(&def, "email = ?", "norole@u.nus.edu").Error; err != nil {
			t.Fatal(err)
		}
		if def.Role != m.RoleUser || def.IsAdmin() || def.IsSuspended() {
			t.Fatalf("default role = %q", def.Role)
		}
		if err := db.Model(admin).Update("role", m.RoleSuperAdmin).Error; err != nil {
			t.Fatal(err)
		}
		var got m.User
		if err := db.First(&got, admin.ID).Error; err != nil {
			t.Fatal(err)
		}
		if got.Role != m.RoleSuperAdmin || !got.IsAdmin() {
			t.Fatalf("promotion not persisted: %q", got.Role)
		}
		if err := db.Model(alice).Update("role", "root").Error; err == nil {
			t.Fatal("expected FK violation for unknown role")
		}
		if err := db.Delete(&m.Role{}, "name = ?", m.RoleUser).Error; err == nil {
			t.Fatal("expected RESTRICT on deleting in-use role")
		}
	})

	t.Run("users: profile checks", func(t *testing.T) {
		if err := db.Model(alice).Update("phone_number", strings.Repeat("1", 21)).Error; err == nil {
			t.Fatal("expected phone number over 20 characters to fail")
		}
		if err := db.Model(alice).Update("display_name", "   ").Error; err == nil {
			t.Fatal("expected blank display name to fail")
		}
	})

	t.Run("profile_pictures: state checks, one active, cascade", func(t *testing.T) {
		picture := &m.ProfilePicture{ID: uuid.New(), UserID: alice.ID,
			ObjectKey: "profile/picture/" + uuid.NewString(), ContentType: "image/png",
			SizeBytes: 1, State: m.ProfilePicturePending, CreatedAt: now}
		if err := db.Create(picture).Error; err != nil {
			t.Fatal(err)
		}
		badType := *picture
		badType.ID = uuid.New()
		badType.ObjectKey = "profile/picture/" + uuid.NewString()
		badType.ContentType = "text/plain"
		if err := db.Create(&badType).Error; err == nil {
			t.Fatal("expected unsupported content type to fail")
		}
		badSize := *picture
		badSize.ID = uuid.New()
		badSize.ObjectKey = "profile/picture/" + uuid.NewString()
		badSize.SizeBytes = 0
		if err := db.Create(&badSize).Error; err == nil {
			t.Fatal("expected zero size to fail")
		}
		badState := *picture
		badState.ID = uuid.New()
		badState.ObjectKey = "profile/picture/" + uuid.NewString()
		badState.State = "unknown"
		if err := db.Create(&badState).Error; err == nil {
			t.Fatal("expected unknown state to fail")
		}

		active := *picture
		active.ID = uuid.New()
		active.ObjectKey = "profile/picture/" + uuid.NewString()
		active.State = m.ProfilePictureActive
		if err := db.Create(&active).Error; err != nil {
			t.Fatal(err)
		}
		secondActive := active
		secondActive.ID = uuid.New()
		secondActive.ObjectKey = "profile/picture/" + uuid.NewString()
		if err := db.Create(&secondActive).Error; err == nil {
			t.Fatal("expected a second active picture to fail")
		}

		// Deleting the active row clears the user's pointer.
		if err := db.Model(&m.User{}).Where("id = ?", alice.ID).
			Update("profile_picture_id", active.ID).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Delete(&m.ProfilePicture{}, active.ID).Error; err != nil {
			t.Fatal(err)
		}
		var current m.User
		if err := db.First(&current, alice.ID).Error; err != nil || current.ProfilePictureID != nil {
			t.Fatalf("picture pointer not cleared: %+v, %v", current, err)
		}

		cascaded := newUser(t, db, "cascade@u.nus.edu")
		orphan := &m.ProfilePicture{ID: uuid.New(), UserID: cascaded.ID,
			ObjectKey: "profile/picture/" + uuid.NewString(), ContentType: "image/jpeg",
			SizeBytes: 1, State: m.ProfilePicturePending, CreatedAt: now}
		if err := db.Create(orphan).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Exec("DELETE FROM users WHERE id = ?", cascaded.ID).Error; err != nil {
			t.Fatal(err)
		}
		var count int64
		if err := db.Model(&m.ProfilePicture{}).Where("id = ?", orphan.ID).Count(&count).Error; err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatalf("profile picture survived its user: %d", count)
		}
	})

	t.Run("users: soft delete frees the email", func(t *testing.T) {
		gone := newUser(t, db, "gone@u.nus.edu")
		if err := db.Delete(gone).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.First(&m.User{}, gone.ID).Error; err != gorm.ErrRecordNotFound {
			t.Fatalf("soft-deleted user still visible: %v", err)
		}
		newUser(t, db, "gone@u.nus.edu")
		if err := db.Create(&m.User{Email: "GONE@u.nus.edu", DisplayName: "Dup"}).Error; err == nil {
			t.Fatal("expected duplicate live email to fail")
		}
	})

	t.Run("role_changes: reason required for suspend/reinstate", func(t *testing.T) {
		blank := "  "
		reason := "spam"
		if err := db.Create(
			&m.RoleChange{UserID: alice.ID, FromRole: m.RoleUser, ToRole: m.RoleSuspended, Reason: &blank},
		).Error; err == nil {
			t.Fatal("expected blank suspend reason to fail")
		}
		if err := db.Create(
			&m.RoleChange{UserID: alice.ID, FromRole: m.RoleSuspended, ToRole: m.RoleUser},
		).Error; err == nil {
			t.Fatal("expected missing reinstate reason to fail")
		}
		if err := db.Create(
			&m.RoleChange{UserID: alice.ID, FromRole: m.RoleUser, ToRole: m.RoleUser, Reason: &reason},
		).Error; err == nil {
			t.Fatal("expected no-op change to fail")
		}
		suspend := &m.RoleChange{UserID: alice.ID, FromRole: m.RoleUser, ToRole: m.RoleSuspended, Reason: &reason}
		suspend.CreatedBy = &admin.ID
		if err := db.Create(suspend).Error; err != nil {
			t.Fatal(err)
		}
		if suspend.CreatedBy == nil || *suspend.CreatedBy != admin.ID {
			t.Fatalf("created_by = %v, want %v", suspend.CreatedBy, admin.ID)
		}
		if err := db.Create(
			&m.RoleChange{UserID: alice.ID, FromRole: m.RoleUser, ToRole: m.RoleAdmin},
		).Error; err != nil {
			t.Fatal(err)
		}
	})

	t.Run("admin bootstrap is single-winner", func(t *testing.T) {
		first := db.Clauses(clause.OnConflict{DoNothing: true}).
			Create(&m.AdminBootstrap{Singleton: true, UserID: admin.ID})
		second := db.Clauses(clause.OnConflict{DoNothing: true}).
			Create(&m.AdminBootstrap{Singleton: true, UserID: alice.ID})
		if first.Error != nil || second.Error != nil || first.RowsAffected != 1 || second.RowsAffected != 0 {
			t.Fatalf("first=%v/%d second=%v/%d", first.Error, first.RowsAffected, second.Error, second.RowsAffected)
		}
	})

	t.Run("magic link consumed exactly once", func(t *testing.T) {
		h := hash()
		ip := "10.0.0.1"
		tok := &m.AuthToken{
			TokenHash:   h,
			Purpose:     m.TokenPurposeLogin,
			Email:       alice.Email,
			UserID:      &alice.ID,
			RequestedIP: &ip,
			ExpiresAt:   now.Add(m.MagicLinkTTL),
		}
		if err := db.Create(tok).Error; err != nil {
			t.Fatal(err)
		}
		consume := func(hash []byte) int64 {
			return db.Model(&m.AuthToken{}).
				Where("token_hash = ? AND purpose = ? AND used_at IS NULL AND expires_at > now()", hash, m.TokenPurposeLogin).
				Update("used_at", gorm.Expr("now()")).RowsAffected
		}
		if n := consume(h); n != 1 {
			t.Fatalf("first consume = %d", n)
		}
		if n := consume(h); n != 0 {
			t.Fatalf("reuse consume = %d", n)
		}
		h2 := hash()
		expired := &m.AuthToken{
			TokenHash: h2,
			Purpose:   m.TokenPurposeLogin,
			Email:     alice.Email,
			UserID:    &alice.ID,
			ExpiresAt: now.Add(-10 * time.Minute),
		}
		expired.CreatedAt = now.Add(-20 * time.Minute)
		if err := db.Create(expired).Error; err != nil {
			t.Fatal(err)
		}
		if n := consume(h2); n != 0 {
			t.Fatalf("expired consume = %d", n)
		}
		if err := db.Create(
			&m.AuthToken{
				TokenHash: hash(),
				Purpose:   m.TokenPurposeLogin,
				Email:     "x@y.com",
				ExpiresAt: now.Add(time.Minute),
			},
		).Error; err == nil {
			t.Fatal("expected check violation for login token without user_id")
		}
	})

	t.Run("sessions logout-all", func(t *testing.T) {
		for i := 0; i < 3; i++ {
			if err := db.Create(
				&m.Session{
					TokenHash:  hash(),
					UserID:     alice.ID,
					LastSeenAt: now,
					ExpiresAt:  now.Add(30 * 24 * time.Hour),
				},
			).Error; err != nil {
				t.Fatal(err)
			}
		}
		n := db.Model(&m.Session{}).
			Where("user_id = ? AND revoked_at IS NULL", alice.ID).
			Update("revoked_at", now).
			RowsAffected
		if n != 3 {
			t.Fatalf("revoked %d", n)
		}
		var s m.Session
		if err := db.First(&s, "user_id = ?", alice.ID).Error; err != nil {
			t.Fatal(err)
		}
		if s.IsActive(now) {
			t.Fatal("revoked session still active")
		}
	})

	t.Run("domains, favourites, warnings", func(t *testing.T) {
		domain := &m.AllowedEmailDomain{Domain: "u.nus.edu"}
		if err := db.Create(domain).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Create(&m.AllowedEmailDomain{Domain: "U.NUS.EDU"}).Error; !errors.Is(err, gorm.ErrDuplicatedKey) {
			t.Fatal("expected case-insensitive duplicate domain to fail")
		}
		if err := db.Delete(domain).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Create(&m.AllowedEmailDomain{Domain: "u.nus.edu"}).Error; !errors.Is(err, gorm.ErrDuplicatedKey) {
			t.Fatalf("expected domain uniqueness across soft-deleted rows, got: %v", err)
		}
		if err := db.Unscoped().Delete(domain).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Create(&m.AllowedEmailDomain{Domain: "u.nus.edu"}).Error; err != nil {
			t.Fatalf("re-adding hard-deleted domain: %v", err)
		}
		sup := uuid.New()
		if err := db.Create(&m.FavouriteSupplier{UserID: alice.ID, SupplierID: sup}).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Create(&m.FavouriteSupplier{UserID: alice.ID, SupplierID: sup}).Error; err == nil {
			t.Fatal("expected duplicate favourite to fail")
		}
		ev := uuid.New()
		w := &m.AccountWarning{
			UserID:        alice.ID,
			RequestID:     uuid.New(),
			Reason:        "cancelled after pickup",
			SourceEventID: &ev,
		}
		if err := db.Create(w).Error; err != nil {
			t.Fatal(err)
		}
		if w.Status != m.WarningActive {
			t.Fatalf("default status = %q", w.Status)
		}
		dup := db.Clauses(clause.OnConflict{DoNothing: true}).
			Create(&m.AccountWarning{UserID: alice.ID, RequestID: uuid.New(), Reason: "dup", SourceEventID: &ev})
		if dup.Error != nil || dup.RowsAffected != 0 {
			t.Fatalf("replayed event created warning: %v/%d", dup.Error, dup.RowsAffected)
		}
		if err := db.Model(w).Update("status", m.WarningRemoved).Error; err == nil {
			t.Fatal("expected check violation")
		}
	})

	t.Run("hard-deleting a user cascades", func(t *testing.T) {
		if err := db.Delete(&m.AdminBootstrap{}, "singleton").Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Unscoped().Delete(&m.User{}, alice.ID).Error; err != nil {
			t.Fatal(err)
		}
		for _, tbl := range []string{"sessions", "auth_tokens", "favourite_suppliers", "account_warnings", "role_changes"} {
			var n int64
			if err := db.Table(tbl).Where("user_id = ?", alice.ID).Count(&n).Error; err != nil {
				t.Fatal(err)
			}
			if n != 0 {
				t.Errorf("%s still has %d rows", tbl, n)
			}
		}
	})
}
