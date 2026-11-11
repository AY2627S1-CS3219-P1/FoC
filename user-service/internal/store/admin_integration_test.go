// Store tests for the first super_admin bootstrap; see auth_integration_test.go for setup.

package store

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/models"
	userdomain "github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/user"
)

func bootstrapUser(email string) *models.User {
	return &models.User{Email: email, DisplayName: "Admin"}
}

// userCreatedEvents returns the user's user-created outbox rows, which Credit
// consumes to open their account.
func userCreatedEvents(t *testing.T, db *gorm.DB, userID uuid.UUID) []models.UserCreatedOutbox {
	t.Helper()
	var rows []models.UserCreatedOutbox
	if err := db.Where("user_id = ?", userID).Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	return rows
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
	outcome, err := store.Admin.Bootstrap(ctx, admin)
	if err != nil || outcome != BootstrapCreated || admin.ID == uuid.Nil || admin.Role != models.RoleSuperAdmin {
		t.Fatalf("bootstrap: %v, %v, %+v", outcome, err, admin)
	}
	if row := bootstrapRow(t, store); row.UserID != admin.ID {
		t.Fatalf("bootstrap row: %+v", row)
	}
	if events := userCreatedEvents(t, db, admin.ID); len(events) != 1 || events[0].Email != admin.Email ||
		events[0].DisplayName != admin.DisplayName || events[0].UserCreatedAt.Sub(admin.CreatedAt).Abs() > time.Millisecond {
		t.Fatalf("user-created events: %+v", events)
	}

	// A deliberate demotion must survive a rerun with the same or another email.
	if err := db.Model(&models.User{}).Where("id = ?", admin.ID).Update("role", models.RoleAdmin).Error; err != nil {
		t.Fatal(err)
	}
	for _, email := range []string{"admin@example.com", "other@example.com"} {
		if outcome, err := store.Admin.Bootstrap(
			ctx,
			bootstrapUser(email),
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
	if events := userCreatedEvents(t, db, admin.ID); len(events) != 1 {
		t.Fatalf("rerun queued another user-created event: %+v", events)
	}
}

func TestAdminBootstrapPromotesExistingUser(t *testing.T) {
	ctx := context.Background()
	store, db := setupAuthStore(t)
	user := createStoreUser(t, store, "user@example.com")

	got := bootstrapUser("user@example.com")
	outcome, err := store.Admin.Bootstrap(ctx, got)
	if err != nil || outcome != BootstrapPromoted || got.ID != user.ID || got.Role != models.RoleSuperAdmin {
		t.Fatalf("promote: %v, %v, %+v", outcome, err, got)
	}
	changes, err := store.RoleChanges(ctx, user.ID)
	if err != nil || len(changes) != 1 || changes[0].FromRole != models.RoleUser ||
		changes[0].ToRole != models.RoleSuperAdmin || changes[0].CreatedBy != nil {
		t.Fatalf("role changes: %+v, %v", changes, err)
	}
	if row := bootstrapRow(t, store); row.UserID != user.ID {
		t.Fatalf("bootstrap row: %+v", row)
	}
	// createStoreUser queues no event, like a user created before the outbox existed.
	if events := userCreatedEvents(t, db, user.ID); len(events) != 1 {
		t.Fatalf("user-created events: %+v", events)
	}
}

func TestAdminBootstrapPromotionKeepsRegistrationEvent(t *testing.T) {
	ctx := context.Background()
	store, db := setupAuthStore(t)
	user := createStoreUser(t, store, "user@example.com")
	registered := userdomain.CreatedEvent{
		EventID: uuid.New(), UserID: user.ID, Email: user.Email,
		DisplayName: user.DisplayName, CreatedAt: user.CreatedAt,
	}
	if err := store.UserCreatedOutbox.Append(ctx, registered); err != nil {
		t.Fatal(err)
	}

	if outcome, err := store.Admin.Bootstrap(ctx, bootstrapUser("user@example.com")); err != nil ||
		outcome != BootstrapPromoted {
		t.Fatalf("promote: %v, %v", outcome, err)
	}
	if events := userCreatedEvents(t, db, user.ID); len(events) != 1 || events[0].EventID != registered.EventID {
		t.Fatalf("user-created events: %+v", events)
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
			outcome, err := store.Admin.Bootstrap(ctx, bootstrapUser("admin@example.com"))
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
