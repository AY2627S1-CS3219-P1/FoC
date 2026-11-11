// Migration 00019 tests: queue the missing user-created event for a bootstrapped superadmin.

package models_test

import (
	"context"
	"testing"

	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/database"
	m "github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/models"
)

const (
	beforeBootstrapBackfill = 18
	bootstrapBackfill       = 19
)

func TestBootstrapBackfillQueuesMissingUserCreatedEvent(t *testing.T) {
	ctx := context.Background()
	db := setup(t)
	p, err := database.Migrator(db)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := p.Up(ctx); err != nil {
			t.Errorf("restore migrations: %v", err)
		}
	})
	if _, err := p.DownTo(ctx, beforeBootstrapBackfill); err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("TRUNCATE users, user_created_outbox CASCADE").Error; err != nil {
		t.Fatal(err)
	}

	admin := m.User{Email: "admin@example.com", DisplayName: "Admin", Role: m.RoleSuperAdmin}
	if err := db.Create(&admin).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&m.AdminBootstrap{Singleton: true, UserID: admin.ID}).Error; err != nil {
		t.Fatal(err)
	}

	events := func() []m.UserCreatedOutbox {
		t.Helper()
		var rows []m.UserCreatedOutbox
		if err := db.Find(&rows).Error; err != nil {
			t.Fatal(err)
		}
		return rows
	}

	if _, err := p.UpTo(ctx, bootstrapBackfill); err != nil {
		t.Fatal(err)
	}
	got := events()
	if len(got) != 1 || got[0].UserID != admin.ID || got[0].Email != admin.Email ||
		got[0].DisplayName != admin.DisplayName || got[0].PayloadVersion != 1 || got[0].PublishedAt != nil {
		t.Fatalf("user-created events: %+v", got)
	}

	// Reapplying keeps the existing event instead of queueing another.
	if _, err := p.DownTo(ctx, beforeBootstrapBackfill); err != nil {
		t.Fatal(err)
	}
	if _, err := p.UpTo(ctx, bootstrapBackfill); err != nil {
		t.Fatal(err)
	}
	if again := events(); len(again) != 1 || again[0].EventID != got[0].EventID {
		t.Fatalf("reapplied user-created events: %+v", again)
	}
}

func TestBootstrapBackfillSkipsDeletedAdmin(t *testing.T) {
	ctx := context.Background()
	db := setup(t)
	p, err := database.Migrator(db)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := p.Up(ctx); err != nil {
			t.Errorf("restore migrations: %v", err)
		}
	})
	if _, err := p.DownTo(ctx, beforeBootstrapBackfill); err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("TRUNCATE users, user_created_outbox CASCADE").Error; err != nil {
		t.Fatal(err)
	}

	admin := m.User{Email: "admin@example.com", DisplayName: "Admin", Role: m.RoleSuperAdmin}
	if err := db.Create(&admin).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&m.AdminBootstrap{Singleton: true, UserID: admin.ID}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Delete(&admin).Error; err != nil {
		t.Fatal(err)
	}

	if _, err := p.UpTo(ctx, bootstrapBackfill); err != nil {
		t.Fatal(err)
	}
	var count int64
	if err := db.Model(&m.UserCreatedOutbox{}).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("deleted admin queued %d user-created events", count)
	}
}
