//go:build integration

// Integration tests for the outbox relay: lease-claim publication, exactly-once
// marking, retry after publisher failure, and the one-terminal-event-per-job
// generation constraint. Runs against the throwaway database.
package outbox_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/AY2627S1-CS3219-P1/FoC/content-moderation-service/database"
	"github.com/AY2627S1-CS3219-P1/FoC/content-moderation-service/internal/config"
	"github.com/AY2627S1-CS3219-P1/FoC/content-moderation-service/internal/jobs"
	"github.com/AY2627S1-CS3219-P1/FoC/content-moderation-service/internal/outbox"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// fakePublisher captures publishes and can fail the first n calls.
type fakePublisher struct {
	mu           sync.Mutex
	failFirst    int
	published    []string // event keys, hex-encoded
	lastMessages []string // message names
}

func (f *fakePublisher) Publish(_ context.Context, topic string, key []byte, message protoreflect.ProtoMessage) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failFirst > 0 {
		f.failFirst--
		return errors.New("broker unavailable")
	}
	f.published = append(f.published, topic+"|"+string(key))
	f.lastMessages = append(f.lastMessages, string(message.ProtoReflect().Descriptor().Name()))
	return nil
}

func (f *fakePublisher) Close() error { return nil }

func (f *fakePublisher) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.published)
}

// setup migrates and returns a truncated pool.
func setup(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url := testDatabaseURL(t)
	ctx := context.Background()
	if err := database.Migrate(ctx, url); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	pool := database.Connect(url)
	t.Cleanup(pool.Close)
	if _, err := pool.Exec(ctx, `
		TRUNCATE moderation_jobs, moderation_attempts, moderation_outbox,
			moderation_quarantine, moderation_config_pauses, moderation_config_versions,
			moderation_config_state, moderation_admin_audit`); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	return pool
}

func testDatabaseURL(t *testing.T) string {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping integration test")
	}
	return url
}

// completedJob drives one job through the full durable path so the outbox holds
// exactly one real terminal event, and returns (job id, review id).
func completedJob(t *testing.T, pool *pgxpool.Pool) (uuid.UUID, uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	if err := config.NewStore(pool).EnsureConfigState(ctx); err != nil {
		t.Fatalf("ensure config state: %v", err)
	}
	store := config.NewStore(pool)
	id, err := store.InsertConfigVersion(ctx, "test-model", []byte(`{}`),
		config.OutputModeJSONSchema, "review-text-v1", "fp", "tester")
	if err != nil {
		t.Fatalf("insert config version: %v", err)
	}
	if err := store.ActivateConfigVersion(ctx, id, "tester", nil); err != nil {
		t.Fatalf("activate config: %v", err)
	}

	reviewID := uuid.New()
	in := jobs.IngestionInput{
		SourceEventID:       uuid.New(),
		ModerationRequestID: uuid.New(),
		ReviewID:            reviewID,
		ReviewerID:          uuid.New(),
		ContentRevision:     1,
		Comment:             "relay me",
	}
	jobStore := jobs.NewJobStore(pool, jobs.DefaultPolicy(), nil)
	if _, err := jobStore.Ingest(ctx, in); err != nil {
		t.Fatalf("ingest: %v", err)
	}
	_, claimed, err := jobStore.Claim(ctx, "relay-worker", 30*time.Second)
	if err != nil || claimed == nil {
		t.Fatalf("claim: %v %v", claimed, err)
	}
	if err := jobStore.Complete(ctx, *claimed, jobs.Assessment{
		Outcome: jobs.OutcomeApproved, PolicyVersion: "review-text-v1",
	}); err != nil {
		t.Fatalf("complete: %v", err)
	}
	return claimed.Job.ID, reviewID
}

func outboxRows(t *testing.T, pool *pgxpool.Pool, jobID uuid.UUID) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM moderation_outbox WHERE job_id = $1`, jobID).Scan(&n); err != nil {
		t.Fatalf("count outbox: %v", err)
	}
	return n
}

func TestRelayPublishesExactlyOnce(t *testing.T) {
	pool := setup(t)
	jobID, reviewID := completedJob(t, pool)

	pub := &fakePublisher{}
	relay := outbox.NewRelay(pool, pub, outbox.RelayConfig{
		PollInterval: 10 * time.Millisecond, Lease: 5 * time.Second,
	}, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	relay.Start(ctx)
	t.Cleanup(func() { cancel(); relay.Wait() })

	waitFor(t, func() bool { return pub.count() >= 1 }, 3*time.Second, "publication")
	// Wait for the durable published mark before stopping the relay: cancel
	// between Publish and markPublished would legitimately leave the row
	// unpublished (at-least-once semantics).
	waitFor(t, func() bool {
		var published int
		if err := pool.QueryRow(context.Background(),
			`SELECT count(*) FROM moderation_outbox WHERE published_at IS NOT NULL`).Scan(&published); err != nil {
			t.Fatalf("check published: %v", err)
		}
		return published == 1
	}, 3*time.Second, "outbox row marked published")
	cancel()

	pub.mu.Lock()
	wantKey := reviewID.String() // key is the review UUID encoded as UTF-8 text
	got := pub.published[0]
	messages := pub.lastMessages[0]
	pub.mu.Unlock()
	if got != "moderation.review_moderation_finished|"+wantKey {
		t.Fatalf("published = %q, want topic|review-uuid key", got)
	}
	if messages != "ReviewModerationFinished" {
		t.Fatalf("message = %q, want ReviewModerationFinished", messages)
	}

	// A second relay pass must not republish the marked row.
	relayOnceIdle := outbox.NewRelay(pool, pub, outbox.RelayConfig{
		PollInterval: 10 * time.Millisecond, Lease: 5 * time.Second,
	}, nil)
	idleCtx, idleCancel := context.WithCancel(context.Background())
	relayOnceIdle.Start(idleCtx)
	t.Cleanup(func() { idleCancel(); relayOnceIdle.Wait() })
	time.Sleep(100 * time.Millisecond)
	idleCancel()
	if n := pub.count(); n != 1 {
		t.Fatalf("publication count = %d after second relay, want 1 (no redelivery)", n)
	}
	if n := outboxRows(t, pool, jobID); n != 1 {
		t.Fatalf("outbox rows = %d, want 1", n)
	}
}

func TestRelayRetriesAfterPublisherFailureWithSameEvent(t *testing.T) {
	pool := setup(t)
	_, reviewID := completedJob(t, pool)

	pub := &fakePublisher{failFirst: 1}
	relay := outbox.NewRelay(pool, pub, outbox.RelayConfig{
		PollInterval: 10 * time.Millisecond, Lease: 5 * time.Second,
	}, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	relay.Start(ctx)
	t.Cleanup(func() { cancel(); relay.Wait() })

	waitFor(t, func() bool { return pub.count() >= 1 }, 3*time.Second, "eventual publication")
	cancel()

	// After one failure and one success, exactly one successful publication
	// carrying the original event id (via the review key) must exist.
	pub.mu.Lock()
	got := pub.published
	pub.mu.Unlock()
	if len(got) != 1 {
		t.Fatalf("publications = %d, want 1", len(got))
	}
	if got[0] != "moderation.review_moderation_finished|"+reviewID.String() {
		t.Fatalf("published = %q, want the review-keyed completion event", got[0])
	}
}

func TestOneTerminalEventPerJobGeneration(t *testing.T) {
	pool := setup(t)
	jobID, _ := completedJob(t, pool)

	// The unique (job_id, run_generation) constraint must reject a second
	// terminal event for the same generation.
	_, err := pool.Exec(context.Background(), `
		INSERT INTO moderation_outbox (event_id, job_id, run_generation, payload_version, payload, available_at)
		VALUES (gen_random_uuid(), $1, 1, 1, '{}'::jsonb, now())`, jobID)
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23505" {
		t.Fatalf("second insert error = %v, want unique violation 23505", err)
	}
}

func waitFor(t *testing.T, cond func() bool, within time.Duration, what string) {
	t.Helper()
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("condition %q not met within %v", what, within)
}

func TestIdleRelayShutdownDoesNotWaitForPollInterval(t *testing.T) {
	pool := setup(t)
	relay := outbox.NewRelay(pool, &fakePublisher{}, outbox.RelayConfig{
		PollInterval: time.Minute, Lease: time.Minute,
	}, nil)
	ctx, cancel := context.WithCancel(context.Background())
	relay.Start(ctx)
	cancel()
	done := make(chan struct{})
	go func() { relay.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("relay did not stop promptly")
	}
}

// A publisher blocked inside an active lease exposes concurrent relay theft.
type blockingPublisher struct {
	entered chan struct{}
	unblock chan struct{}
}

func (p *blockingPublisher) Publish(ctx context.Context, _ string, _ []byte, _ protoreflect.ProtoMessage) error {
	select {
	case p.entered <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	select {
	case <-p.unblock:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (*blockingPublisher) Close() error { return nil }

func TestConcurrentRelaysDoNotStealLivePublication(t *testing.T) {
	pool := setup(t)
	completedJob(t, pool)
	pub := &blockingPublisher{entered: make(chan struct{}, 2), unblock: make(chan struct{})}
	cfg := outbox.RelayConfig{PollInterval: time.Millisecond, Lease: 5 * time.Second}
	a, b := outbox.NewRelay(pool, pub, cfg, nil), outbox.NewRelay(pool, pub, cfg, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer func() { cancel(); a.Wait(); b.Wait() }()
	a.Start(ctx)
	select {
	case <-pub.entered:
	case <-time.After(time.Second):
		t.Fatal("first relay never published")
	}
	b.Start(ctx)
	select {
	case <-pub.entered:
		t.Fatal("second relay stole live publication")
	case <-time.After(100 * time.Millisecond):
	}
	close(pub.unblock)
	waitFor(t, func() bool {
		var n int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM moderation_outbox WHERE published_at IS NOT NULL`).
			Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n == 1
	}, time.Second, "durable publication")
	select {
	case <-pub.entered:
		t.Fatal("live event published twice")
	default:
	}
}

func TestRelayPayloadVersionCompatibility(t *testing.T) {
	for _, version := range []int{0, 2} {
		t.Run(fmt.Sprint(version), func(t *testing.T) {
			pool := setup(t)
			completedJob(t, pool)
			if _, err := pool.Exec(
				context.Background(),
				`UPDATE moderation_outbox SET payload_version=$1`,
				version,
			); err != nil {
				t.Fatal(err)
			}
			pub := &fakePublisher{}
			relay := outbox.NewRelay(
				pool,
				pub,
				outbox.RelayConfig{PollInterval: time.Millisecond, Lease: time.Second},
				nil,
			)
			ctx, cancel := context.WithCancel(context.Background())
			defer func() { cancel(); relay.Wait() }()
			relay.Start(ctx)
			if version == 0 {
				waitFor(t, func() bool { return pub.count() == 1 }, time.Second, "legacy v1 payload published")
			} else {
				time.Sleep(100 * time.Millisecond)
				if pub.count() != 0 {
					t.Fatal("unsupported payload schema published")
				}
			}
		})
	}
}
