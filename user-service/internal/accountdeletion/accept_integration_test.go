package accountdeletion

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/database"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/database/deletiondb"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/jwt"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/models"
)

// stubIssuer is a SessionIssuer that returns empty tokens without persisting a
// replacement session; these tests observe session sweeping directly.
type stubIssuer struct{}

func (stubIssuer) Issue(context.Context, SessionStore, *models.User, time.Time) (jwt.AuthTokens, error) {
	return jwt.AuthTokens{}, nil
}

func openDeletionDB(t *testing.T) *gorm.DB {
	t.Helper()
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
	for _, table := range []string{"users", "sessions", "account_deletion_jobs", "user_deleted_outbox",
		"auth_tokens", "user_created_outbox", "admin_bootstrap"} {
		if err := db.Exec("TRUNCATE " + table + " CASCADE").Error; err != nil {
			t.Fatalf("truncate %s: %v", table, err)
		}
	}
	return db
}

func insertUserRow(t *testing.T, db *gorm.DB, role models.RoleName) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if err := db.Create(&models.User{ID: id, Email: id.String() + "@example.com",
		DisplayName: "U", Role: role}).Error; err != nil {
		t.Fatal(err)
	}
	return id
}

func insertLiveSessions(t *testing.T, db *gorm.DB, userID uuid.UUID, n int, now time.Time) []uuid.UUID {
	t.Helper()
	ids := make([]uuid.UUID, 0, n)
	for i := 0; i < n; i++ {
		h := sha256.Sum256([]byte(uuid.New().String()))
		s := &models.Session{UserID: userID, TokenHash: h[:], ExpiresAt: now.Add(time.Hour), LastSeenAt: now}
		if err := db.Create(s).Error; err != nil {
			t.Fatal(err)
		}
		ids = append(ids, s.ID)
	}
	return ids
}

func liveSessionCount(t *testing.T, db *gorm.DB, userID uuid.UUID) int {
	t.Helper()
	var n int64
	if err := db.Model(&models.Session{}).
		Where("user_id = ? AND revoked_at IS NULL", userID).Count(&n).Error; err != nil {
		t.Fatal(err)
	}
	return int(n)
}

func testPlan(db *gorm.DB) *Plan {
	return NewPlan(db, Config{InitialDrainDelay: time.Minute, AdmissionsGracePeriod: 30 * time.Second},
		stubIssuer{}, nil)
}

func TestPlanAcceptSweepsSessionsOnFirstAcceptance(t *testing.T) {
	db := openDeletionDB(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	uid := insertUserRow(t, db, models.RoleUser)
	insertLiveSessions(t, db, uid, 3, now)

	if _, err := testPlan(db).Accept(ctx, &models.User{ID: uid}, now); err != nil {
		t.Fatalf("accept: %v", err)
	}
	if got := liveSessionCount(t, db, uid); got != 0 {
		t.Fatalf("first acceptance left %d live sessions, want 0 (all swept)", got)
	}
	var user models.User
	if err := db.First(&user, "id = ?", uid).Error; err != nil {
		t.Fatal(err)
	}
	if !user.IsDeleting || user.DeletionRequestedAt == nil {
		t.Fatalf("flag not set: %+v", user)
	}
}

func TestPlanAcceptRetrySkipsSweepAndPreservesRequestedAt(t *testing.T) {
	db := openDeletionDB(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	uid := insertUserRow(t, db, models.RoleUser)
	insertLiveSessions(t, db, uid, 1, now)

	plan := testPlan(db)
	first, err := plan.Accept(ctx, &models.User{ID: uid}, now)
	if err != nil {
		t.Fatalf("first accept: %v", err)
	}
	// Simulate the replacement session the real issuer would have created.
	insertLiveSessions(t, db, uid, 1, now)

	retry, err := plan.Accept(ctx, &models.User{ID: uid}, now.Add(5*time.Minute))
	if err != nil {
		t.Fatalf("retry accept: %v", err)
	}
	if !retry.RequestedAt.Equal(first.RequestedAt) {
		t.Fatalf("retry moved requested_at: %v -> %v", first.RequestedAt, retry.RequestedAt)
	}
	if got := liveSessionCount(t, db, uid); got != 1 {
		t.Fatalf("retry re-swept sessions: %d live, want 1 (replacement untouched)", got)
	}
}

func TestPlanAcceptRejectsProtectedAccounts(t *testing.T) {
	db := openDeletionDB(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)

	admin := insertUserRow(t, db, models.RoleAdmin)
	if _, err := testPlan(db).Accept(ctx, &models.User{ID: admin}, now); err == nil {
		t.Fatal("admin acceptance was not rejected")
	}

	// A demoted first-admin keeps its admin_bootstrap link and must still be
	// rejected before any mutation.
	boot := insertUserRow(t, db, models.RoleUser)
	if err := db.Exec("INSERT INTO admin_bootstrap (user_id) VALUES (?)", boot).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := testPlan(db).Accept(ctx, &models.User{ID: boot}, now); err == nil {
		t.Fatal("bootstrapped account acceptance was not rejected")
	}
	var user models.User
	if err := db.First(&user, "id = ?", boot).Error; err != nil {
		t.Fatal(err)
	}
	if user.IsDeleting {
		t.Fatal("rejected bootstrap account was mutated")
	}
}

func TestDeletionLifecycleTriggerIsIrreversible(t *testing.T) {
	db := openDeletionDB(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	uid := insertUserRow(t, db, models.RoleUser)
	if _, err := testPlan(db).Accept(ctx, &models.User{ID: uid}, now); err != nil {
		t.Fatalf("accept: %v", err)
	}

	if err := db.Exec("UPDATE users SET is_deleting = false WHERE id = ?", uid).Error; err == nil {
		t.Fatal("trigger allowed is_deleting true -> false")
	}
	if err := db.Exec("UPDATE users SET deletion_requested_at = now() + interval '1 hour' WHERE id = ?",
		uid).Error; err == nil {
		t.Fatal("trigger allowed deletion_requested_at to move")
	}
	// An unrelated profile edit must still succeed.
	if err := db.Exec("UPDATE users SET display_name = 'Changed' WHERE id = ?", uid).Error; err != nil {
		t.Fatalf("profile edit blocked by lifecycle trigger: %v", err)
	}
}

func TestClaimDueJobRespectsNextAttemptAt(t *testing.T) {
	db := openDeletionDB(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	q := deletiondb.New(sqlDB)

	future := insertUserRow(t, db, models.RoleUser)
	if err := q.InsertJob(ctx, deletiondb.InsertJobParams{
		UserID: future, DeletionRequestID: uuid.New(),
		AdmissionsSafeAfter: now, NextAttemptAt: now.Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := q.ClaimDueJob(ctx, deletiondb.ClaimDueJobParams{
		LeaseToken: uuid.New(), LeaseUntil: now.Add(time.Minute),
	}); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("a job whose next_attempt_at is in the future was claimed: %v", err)
	}

	due := insertUserRow(t, db, models.RoleUser)
	if err := q.InsertJob(ctx, deletiondb.InsertJobParams{
		UserID: due, DeletionRequestID: uuid.New(),
		AdmissionsSafeAfter: now, NextAttemptAt: now.Add(-time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	row, err := q.ClaimDueJob(ctx, deletiondb.ClaimDueJobParams{
		LeaseToken: uuid.New(), LeaseUntil: now.Add(time.Minute),
	})
	if err != nil {
		t.Fatalf("due job not claimed: %v", err)
	}
	if row.UserID != due {
		t.Fatalf("claimed %v, want the due job %v", row.UserID, due)
	}
}

func TestFinalizeHardDeletesAndWritesOneFact(t *testing.T) {
	db := openDeletionDB(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	uid := insertUserRow(t, db, models.RoleUser)
	insertLiveSessions(t, db, uid, 1, now)

	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	requestID := uuid.New()
	if err := deletiondb.New(sqlDB).InsertJob(ctx, deletiondb.InsertJobParams{
		UserID: uid, DeletionRequestID: requestID,
		AdmissionsSafeAfter: now.Add(time.Hour), NextAttemptAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	h := sha256.Sum256([]byte("challenge"))
	if err := db.Create(&models.AuthToken{TokenHash: h[:], Purpose: models.TokenPurposeRegister,
		Email: uid.String() + "@example.com", ExpiresAt: now.Add(time.Hour)}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&models.UserCreatedOutbox{EventID: uuid.New(), UserID: uid,
		Email: uid.String() + "@example.com", DisplayName: "U", UserCreatedAt: now,
		PayloadVersion: 1}).Error; err != nil {
		t.Fatal(err)
	}

	var user models.User
	if err := db.First(&user, "id = ?", uid).Error; err != nil {
		t.Fatal(err)
	}
	txErr := db.Transaction(func(tx *gorm.DB) error {
		return finalize(ctx, tx, user, requestID, now, deletiondb.New(tx.Statement.ConnPool))
	})
	if txErr != nil {
		t.Fatalf("finalize: %v", txErr)
	}

	for _, check := range []struct {
		name  string
		query string
	}{
		{"user", "SELECT COUNT(*) FROM users WHERE id = $1"},
		{"sessions", "SELECT COUNT(*) FROM sessions WHERE user_id = $1"},
		{"job", "SELECT COUNT(*) FROM account_deletion_jobs WHERE user_id = $1"},
		{"email challenge", "SELECT COUNT(*) FROM auth_tokens WHERE email = $1"},
		{"unpublished registration", "SELECT COUNT(*) FROM user_created_outbox WHERE user_id = $1"},
	} {
		var n int64
		if err := sqlDB.QueryRowContext(ctx, check.query, uid).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 0 {
			t.Fatalf("%s rows = %d, want 0 after finalize", check.name, n)
		}
	}
	var facts int64
	if err := sqlDB.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM user_deleted_outbox WHERE user_id = $1", uid).Scan(&facts); err != nil {
		t.Fatal(err)
	}
	if facts != 1 {
		t.Fatalf("user_deleted facts = %d, want exactly 1", facts)
	}
}

// TestWorkerRefusesToFinalizeLiveAccount proves a stray job never causes the
// worker to hard-delete an account whose is_deleting flag is false: the guard
// drops the inconsistent job instead (self-heal, no data loss).
func TestWorkerRefusesToFinalizeLiveAccount(t *testing.T) {
	db := openDeletionDB(t)
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	uid := insertUserRow(t, db, models.RoleUser) // is_deleting = false
	if err := deletiondb.New(sqlDB).InsertJob(ctx, deletiondb.InsertJobParams{
		UserID: uid, DeletionRequestID: uuid.New(),
		AdmissionsSafeAfter: now.Add(-time.Hour), NextAttemptAt: now.Add(-time.Minute),
	}); err != nil {
		t.Fatal(err)
	}

	plan := NewPlan(db, Config{InitialDrainDelay: time.Minute}, stubIssuer{}, nil)
	w := NewWorker(plan, sqlDB)
	w.now = func() time.Time { return now }
	if err := w.drainOnce(ctx); err != nil {
		t.Fatalf("drain: %v", err)
	}

	var users int64
	if err := db.Model(&models.User{}).Where("id = ?", uid).Count(&users).Error; err != nil {
		t.Fatal(err)
	}
	if users != 1 {
		t.Fatal("worker hard-deleted a live account")
	}
	var jobs int64
	if err := sqlDB.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM account_deletion_jobs WHERE user_id = $1", uid).Scan(&jobs); err != nil {
		t.Fatal(err)
	}
	if jobs != 0 {
		t.Fatalf("stale job not cleared: %d", jobs)
	}
}
