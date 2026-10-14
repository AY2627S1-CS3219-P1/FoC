package main

import (
	"context"
	"crypto/sha256"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/database"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/models"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/service"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/store"
	"github.com/google/uuid"
)

func TestAuthDomainPolicyUsesSameTransaction(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	// A policy bound to the pool rather than the transaction would wait forever
	// for a second connection. The context makes that mistake fail promptly.
	db, err := database.Open(dsn, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := database.Migrate(db); err != nil {
		t.Fatal(err)
	}
	domain := uuid.NewString() + ".example.com"
	if err := db.Exec("INSERT INTO allowed_email_domains (domain) VALUES (?)", domain).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Exec("DELETE FROM allowed_email_domains WHERE domain = ?", domain).Error; err != nil {
			t.Error(err)
		}
	})
	persistence := store.New(db)
	now := time.Now().UTC()
	digest := sha256.Sum256([]byte(uuid.NewString()))
	challenge := models.AuthToken{TokenHash: digest[:], Email: "user@" + domain,
		Purpose: models.TokenPurposeRegister, ExpiresAt: now.Add(time.Hour)}
	if err := persistence.AuthTokens.Create(context.Background(), &challenge); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Exec("DELETE FROM auth_tokens WHERE id = ?", challenge.ID).Error; err != nil {
			t.Error(err)
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	rollback := errors.New("roll back authentication")
	err = authTransactions(db)(ctx, func(tx service.Resources) error {
		if _, err := tx.AuthTokens.Consume(ctx, digest, models.TokenPurposeRegister, now); err != nil {
			return err
		}
		if allowed, err := tx.DomainPolicy.IsDomainAllowed(ctx, domain); err != nil || !allowed {
			t.Fatalf("transaction-bound domain check: allowed=%v err=%v", allowed, err)
		}
		return rollback
	})
	if !errors.Is(err, rollback) {
		t.Fatalf("authentication transaction did not roll back: %v", err)
	}
	if _, err := persistence.AuthTokens.Consume(ctx, digest, models.TokenPurposeRegister, now); err != nil {
		t.Fatalf("rollback left the magic link consumed: %v", err)
	}
}
