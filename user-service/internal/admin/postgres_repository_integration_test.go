package admin

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"os"
	"testing"

	"github.com/AY2627S1-CS3219-P1/FoC/pkg/api"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/database"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/database/alloweddomaindb"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestAllowedDomainErrors(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	db, err := database.Open(dsn, 5, 5)
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
	ctx := context.Background()
	tx, err := sqlDB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tx.Rollback() })
	// Start with an empty allowlist inside a transaction that is rolled back.
	if _, err := tx.ExecContext(ctx, "DELETE FROM allowed_email_domains"); err != nil {
		t.Fatal(err)
	}
	actorID := uuid.New()
	if _, err := tx.ExecContext(ctx, "INSERT INTO users (id, email, display_name) VALUES ($1, $2, 'Admin')", actorID, actorID.String()+"@example.com"); err != nil {
		t.Fatal(err)
	}
	var repo AllowedDomainRepository = NewPostgresAllowedAdminRepository(alloweddomaindb.New(tx))
	service := NewAllowedDomainService(&repo)
	domains, err := service.ListAllowedDomains(ctx)
	if err != nil || len(domains) != 0 {
		t.Fatalf("empty allowlist should succeed: %v, %v", domains, err)
	}
	if err := service.AllowDomain(ctx, "example.com", actorID); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		domain string
		want   bool
	}{{"example.com", true}, {"EXAMPLE.COM", true}, {"sub.example.com", false}, {"absent.example.com", false}} {
		if allowed, err := service.IsDomainAllowed(ctx, tc.domain); err != nil || allowed != tc.want {
			t.Fatalf("domain membership for %q: allowed=%v err=%v", tc.domain, allowed, err)
		}
	}

	// Each rejected statement uses a savepoint so PostgreSQL does not leave
	// the transaction aborted before the next assertion.
	rejectedInsert := func(domain string, actor uuid.UUID) error {
		t.Helper()
		if _, err := tx.ExecContext(ctx, "SAVEPOINT rejected_insert"); err != nil {
			t.Fatal(err)
		}
		insertErr := service.AllowDomain(ctx, domain, actor)
		if _, err := tx.ExecContext(ctx, "ROLLBACK TO SAVEPOINT rejected_insert"); err != nil {
			t.Fatal(err)
		}
		if _, err := tx.ExecContext(ctx, "RELEASE SAVEPOINT rejected_insert"); err != nil {
			t.Fatal(err)
		}
		return insertErr
	}
	duplicate := rejectedInsert("EXAMPLE.COM", actorID)
	assertAllowedDomainError(t, duplicate, http.StatusConflict)
	var pgErr *pgconn.PgError
	if !errors.As(duplicate, &pgErr) || pgErr.Code != "23505" {
		t.Fatalf("original duplicate error was lost: %v", duplicate)
	}
	assertAllowedDomainError(t, rejectedInsert("invalid domain", actorID), http.StatusBadRequest)
	assertAllowedDomainError(t, service.RemoveAllowedDomain(ctx, uuid.New()), http.StatusNotFound)

	// An unknown database failure must not be misreported as invalid input
	// or a domain duplicate. The handler will return a generic internal error.
	unexpected := rejectedInsert("other.example.com", uuid.New())
	var external api.ExternalError
	if !errors.As(unexpected, &pgErr) || pgErr.Code != "23503" || errors.As(unexpected, &external) {
		t.Fatalf("unexpected database error was misclassified: %v", unexpected)
	}
	domains, err = service.ListAllowedDomains(ctx)
	if err != nil || len(domains) != 1 {
		t.Fatalf("list allowed domains: %v, %v", domains, err)
	}
	if err := service.RemoveAllowedDomain(ctx, domains[0].Id); err != nil {
		t.Fatal(err)
	}
	if allowed, err := service.IsDomainAllowed(ctx, "example.com"); err != nil || allowed {
		t.Fatalf("hard-deleted domain remains allowed: allowed=%v err=%v", allowed, err)
	}
	assertAllowedDomainError(t, service.RemoveAllowedDomain(ctx, domains[0].Id), http.StatusNotFound)
	if err := service.AllowDomain(ctx, "example.com", actorID); err != nil {
		t.Fatalf("re-adding hard-deleted domain: %v", err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ListAllowedDomains(ctx); !errors.Is(err, sql.ErrTxDone) {
		t.Fatalf("original database failure was lost: %v", err)
	}
	if allowed, err := service.IsDomainAllowed(ctx, "example.com"); allowed || !errors.Is(err, sql.ErrTxDone) {
		t.Fatalf("domain lookup lost database failure: allowed=%v err=%v", allowed, err)
	}
}

func assertAllowedDomainError(t *testing.T, err error, status int) {
	t.Helper()
	var external api.ExternalError
	if !errors.As(err, &external) || external.Code() != status {
		t.Fatalf("expected shared error with status %d, got: %v", status, err)
	}
}
