// Store tests for the first super_admin bootstrap; see auth_integration_test.go for setup.

package store

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/models"
)

func bootstrapUser(email string) *models.User {
	return &models.User{Email: email, DisplayName: "Admin"}
}

func bootstrapRow(t *testing.T, store *Store) models.AdminBootstrap {
	t.Helper()
	var row models.AdminBootstrap
	if err := store.db.Take(&row).Error; err != nil {
		t.Fatal(err)
	}
	return row
}

func TestAdminBootstrapCreatesAndIsIdempotent(t *testing.T) {
	ctx := context.Background()
	store, db := setupAuthStore(t)

	admin := bootstrapUser("admin@example.com")
	outcome, _, err := store.Admin.Bootstrap(ctx, admin, time.Now().UTC())
	if err != nil || outcome != BootstrapCreated || admin.ID == uuid.Nil || admin.Role != models.RoleSuperAdmin {
		t.Fatalf("bootstrap: %v, %v, %+v", outcome, err, admin)
	}
	if row := bootstrapRow(t, store); row.UserID != admin.ID {
		t.Fatalf("bootstrap row: %+v", row)
	}

	// A deliberate demotion must survive a rerun with the same or another email.
	if err := db.Model(&models.User{}).Where("id = ?", admin.ID).Update("role", models.RoleAdmin).Error; err != nil {
		t.Fatal(err)
	}
	for _, email := range []string{"admin@example.com", "other@example.com"} {
		if outcome, _, err := store.Admin.Bootstrap(
			ctx,
			bootstrapUser(email),
			time.Now().UTC(),
		); err != nil ||
			outcome != BootstrapAlreadyDone {
			t.Fatalf("rerun %s: %v, %v", email, outcome, err)
		}
	}
	if got, err := store.GetByID(ctx, admin.ID); err != nil || got.Role != models.RoleAdmin {
		t.Fatalf("demoted admin restored: %+v, %v", got, err)
	}
	if _, err := store.GetByEmail(ctx, "other@example.com"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("rerun created user: %v", err)
	}
}

func TestAdminBootstrapPromotesExistingUser(t *testing.T) {
	ctx := context.Background()
	store, db := setupAuthStore(t)
	user := createStoreUser(t, store, "user@example.com")

	// The promoted user's live sessions must be revoked in the same
	// transaction as the role change.
	sessionHash := testDigest("promoted-device")
	session := models.Session{UserID: user.ID, TokenHash: sessionHash[:],
		LastSeenAt: time.Now().UTC(), ExpiresAt: time.Now().UTC().Add(time.Hour)}
	if err := store.Sessions.Create(ctx, &session); err != nil {
		t.Fatal(err)
	}

	got := bootstrapUser("user@example.com")
	outcome, revoked, err := store.Admin.Bootstrap(ctx, got, time.Now().UTC())
	if err != nil || outcome != BootstrapPromoted || got.ID != user.ID || got.Role != models.RoleSuperAdmin {
		t.Fatalf("promote: %v, %v, %+v", outcome, err, got)
	}
	if len(revoked) != 1 || revoked[0] != session.ID {
		t.Fatalf("revoked sessions = %v, want [%s]", revoked, session.ID)
	}
	var revokedCount int64
	if err := db.Model(&models.Session{}).
		Where("id = ? AND revoked_at IS NOT NULL", session.ID).
		Count(&revokedCount).Error; err != nil || revokedCount != 1 {
		t.Fatalf("session not revoked in the bootstrap transaction: count=%d err=%v", revokedCount, err)
	}
	changes, err := store.RoleChanges(ctx, user.ID)
	if err != nil || len(changes) != 1 || changes[0].FromRole != models.RoleUser ||
		changes[0].ToRole != models.RoleSuperAdmin || changes[0].CreatedBy != nil {
		t.Fatalf("role changes: %+v, %v", changes, err)
	}
	if row := bootstrapRow(t, store); row.UserID != user.ID {
		t.Fatalf("bootstrap row: %+v", row)
	}
}

func TestAdminBootstrapUnchangedSuperAdmin(t *testing.T) {
	ctx := context.Background()
	store, db := setupAuthStore(t)
	superAdmin := createStoreUser(t, store, "admin@example.com")
	if err := db.Model(&models.User{}).Where("id = ?", superAdmin.ID).
		Update("role", models.RoleSuperAdmin).Error; err != nil {
		t.Fatal(err)
	}

	// An existing super admin has no role change, so no session revocation.
	sessionHash := testDigest("unchanged-device")
	session := models.Session{UserID: superAdmin.ID, TokenHash: sessionHash[:],
		LastSeenAt: time.Now().UTC(), ExpiresAt: time.Now().UTC().Add(time.Hour)}
	if err := store.Sessions.Create(ctx, &session); err != nil {
		t.Fatal(err)
	}

	got := bootstrapUser("admin@example.com")
	outcome, revoked, err := store.Admin.Bootstrap(ctx, got, time.Now().UTC())
	if err != nil || outcome != BootstrapUnchanged || got.ID != superAdmin.ID {
		t.Fatalf("unchanged: %v, %v, %+v", outcome, err, got)
	}
	if len(revoked) != 0 {
		t.Fatalf("unchanged bootstrap revoked sessions: %v", revoked)
	}
	var revokedCount int64
	if err := db.Model(&models.Session{}).
		Where("id = ? AND revoked_at IS NOT NULL", session.ID).
		Count(&revokedCount).Error; err != nil || revokedCount != 0 {
		t.Fatalf("unchanged bootstrap revoked sessions: count=%d err=%v", revokedCount, err)
	}
	if row := bootstrapRow(t, store); row.UserID != superAdmin.ID {
		t.Fatalf("bootstrap row: %+v", row)
	}
}

func TestAdminBootstrapConcurrent(t *testing.T) {
	ctx := context.Background()
	store, db := setupAuthStore(t)

	// Hold inserts into users until every caller is blocked on a lock, so all of
	// them race past their "already bootstrapped?" check if nothing serialises them.
	// n+1 must fit setupAuthStore's pool of 5 connections.
	const n = 4
	gate := db.Begin()
	if err := gate.Exec("LOCK TABLE users IN SHARE MODE").Error; err != nil {
		t.Fatal(err)
	}
	released := false
	defer func() {
		if !released {
			gate.Rollback()
		}
	}()
	var (
		wg       sync.WaitGroup
		mu       sync.Mutex
		outcomes []BootstrapOutcome
		errs     []error
	)
	for range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			outcome, _, err := store.Admin.Bootstrap(ctx, bootstrapUser("admin@example.com"), time.Now().UTC())
			mu.Lock()
			defer mu.Unlock()
			outcomes = append(outcomes, outcome)
			if err != nil {
				errs = append(errs, err)
			}
		}()
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		var waiting int64
		if err := gate.Raw("SELECT count(DISTINCT pid) FROM pg_locks WHERE NOT granted").
			Scan(&waiting).Error; err != nil {
			t.Fatal(err)
		}
		if waiting >= n {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("only %d of %d callers blocked", waiting, n)
		}
		time.Sleep(10 * time.Millisecond)
	}
	released = true
	if err := gate.Rollback().Error; err != nil {
		t.Fatal(err)
	}
	wg.Wait()
	if len(errs) > 0 {
		t.Fatalf("errors: %v", errs)
	}
	created := 0
	for _, o := range outcomes {
		if o == BootstrapCreated {
			created++
		} else if o != BootstrapAlreadyDone {
			t.Fatalf("outcome: %v", o)
		}
	}
	var users, rows int64
	db.Model(&models.User{}).Count(&users)
	db.Model(&models.AdminBootstrap{}).Count(&rows)
	if created != 1 || users != 1 || rows != 1 {
		t.Fatalf("created %d, users %d, rows %d", created, users, rows)
	}
}
