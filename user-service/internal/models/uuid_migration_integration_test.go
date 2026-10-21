package models_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/database"
	m "github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/models"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

func TestUUIDDefaultsMigration(t *testing.T) {
	db := setup(t)
	ctx := context.Background()
	migrator, err := database.Migrator(db)
	if err != nil {
		t.Fatal(err)
	}

	// Fresh installations generate v7 IDs through every GORM-backed table.
	fresh := createUUIDResources(t, db, "fresh", 7)

	if _, err := migrator.DownTo(ctx, 5); err != nil {
		t.Fatalf("restore v4 defaults: %v", err)
	}
	legacy := createUUIDResources(t, db, "legacy", 4)

	// Upgrading an existing database changes defaults without rewriting its IDs.
	if _, err := migrator.Up(ctx); err != nil {
		t.Fatalf("upgrade defaults: %v", err)
	}
	assertUUIDResourcesPresent(t, db, fresh)
	assertUUIDResourcesPresent(t, db, legacy)
	upgraded := createUUIDResources(t, db, "upgraded", 7)

	// Rollback also preserves v7 rows and their foreign-key relationships.
	if _, err := migrator.DownTo(ctx, 5); err != nil {
		t.Fatalf("roll back defaults: %v", err)
	}
	assertUUIDResourcesPresent(t, db, fresh)
	assertUUIDResourcesPresent(t, db, legacy)
	assertUUIDResourcesPresent(t, db, upgraded)
	rolledBack := createUUIDResources(t, db, "rolled-back", 4)

	if _, err := migrator.Up(ctx); err != nil {
		t.Fatalf("replay defaults: %v", err)
	}
	assertUUIDResourcesPresent(t, db, rolledBack)
	createUUIDResources(t, db, "replayed", 7)
}

func createUUIDResources(t *testing.T, db *gorm.DB, label string, wantVersion uuid.Version) map[string]uuid.UUID {
	t.Helper()
	user := newUser(t, db, label+"@uuid.example")
	domain := &m.AllowedEmailDomain{Domain: label + ".example"}
	token := &m.AuthToken{
		TokenHash: hash(),
		Purpose:   m.TokenPurposeLogin,
		Email:     user.Email,
		UserID:    &user.ID,
		ExpiresAt: time.Now().Add(time.Hour),
	}
	session := &m.Session{
		TokenHash: hash(),
		UserID:    user.ID,
		ExpiresAt: time.Now().Add(time.Hour),
	}
	change := &m.RoleChange{UserID: user.ID, FromRole: m.RoleUser, ToRole: m.RoleAdmin}
	warning := &m.AccountWarning{
		UserID:    user.ID,
		RequestID: uuid.New(), // References from older producers remain valid.
		Reason:    "UUID migration test",
	}

	resources := []struct {
		table string
		model any
		id    *uuid.UUID
	}{
		{"allowed_email_domains", domain, &domain.ID},
		{"auth_tokens", token, &token.ID},
		{"sessions", session, &session.ID},
		{"role_changes", change, &change.ID},
		{"account_warnings", warning, &warning.ID},
	}
	ids := map[string]uuid.UUID{"users": user.ID}
	for _, resource := range resources {
		if err := db.Create(resource.model).Error; err != nil {
			t.Fatalf("create %s: %v", resource.table, err)
		}
		ids[resource.table] = *resource.id
	}

	for table, id := range ids {
		if id.Version() != wantVersion {
			t.Errorf("%s ID %s has version %d, want %d", table, id, id.Version(), wantVersion)
		}
	}

	return ids
}

func assertUUIDResourcesPresent(t *testing.T, db *gorm.DB, ids map[string]uuid.UUID) {
	t.Helper()
	for table, id := range ids {
		var exists bool
		query := fmt.Sprintf("SELECT EXISTS (SELECT 1 FROM %s WHERE id = ?)", table)
		if err := db.Raw(query, id).Scan(&exists).Error; err != nil || !exists {
			t.Fatalf("%s lost existing ID %s: exists=%v error=%v", table, id, exists, err)
		}
	}

	var relationships int64
	if err := db.Model(&m.Session{}).Where("user_id = ?", ids["users"]).Count(&relationships).Error; err != nil || relationships != 1 {
		t.Fatalf("session relationship changed: count=%d error=%v", relationships, err)
	}
}
