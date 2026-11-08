//go:build integration

// Integration test for the per-process worker pool: the configured concurrency
// must bound concurrent Evaluate calls exactly, and every ingested job must be
// processed to a terminal state through the durable store.
package jobs_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/AY2627S1-CS3219-P1/FoC/content-moderation-service/internal/jobs"
	"github.com/jackc/pgx/v5/pgxpool"
)

// poolT aliases pgxpool.Pool so the helper signatures above stay readable.
type poolT = pgxpool.Pool

// blockingEvaluator records peak concurrency and blocks each Evaluate call on
// enter until released, making the concurrency cap observable deterministically.
type blockingEvaluator struct {
	mu      sync.Mutex
	current int
	peak    int
	entered chan struct{}
	done    chan struct{}
	calls   atomic.Int64
}

func newBlockingEvaluator() *blockingEvaluator {
	return &blockingEvaluator{
		entered: make(chan struct{}, 64),
		done:    make(chan struct{}, 64),
	}
}

func (e *blockingEvaluator) Evaluate(_ context.Context, _ jobs.EvaluationInput) (jobs.Assessment, error) {
	e.calls.Add(1)
	e.mu.Lock()
	e.current++
	if e.current > e.peak {
		e.peak = e.current
	}
	e.mu.Unlock()
	e.entered <- struct{}{}

	select {
	case <-e.done:
	case <-time.After(3 * time.Second): // fail-safe so the test cannot hang
	}

	e.mu.Lock()
	e.current--
	e.mu.Unlock()
	return jobs.Assessment{Outcome: jobs.OutcomeApproved, PolicyVersion: "review-text-v1"}, nil
}

func (e *blockingEvaluator) waitEntered(t *testing.T, n int) {
	t.Helper()
	for range n {
		select {
		case <-e.entered:
		case <-time.After(3 * time.Second):
			t.Fatalf("only %d/%d evaluations entered", n-len(e.entered), n)
		}
	}
}

func (e *blockingEvaluator) release(n int) {
	for range n {
		e.done <- struct{}{}
	}
}

func (e *blockingEvaluator) peakConcurrency() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.peak
}

func ingestJobs(t *testing.T, pool *poolT, n int) {
	t.Helper()
	store := jobs.NewJobStore(pool, jobs.DefaultPolicy(), nil)
	for range n {
		if _, err := store.Ingest(context.Background(), ingestInput("pool me")); err != nil {
			t.Fatalf("ingest: %v", err)
		}
	}
}

func countTerminal(t *testing.T, pool *poolT) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM moderation_jobs WHERE status = 'completed'`).Scan(&n); err != nil {
		t.Fatalf("count terminal: %v", err)
	}
	return n
}

func TestPoolRespectsConcurrencyCap(t *testing.T) {
	pool := newTestPool(t)
	activateTestConfig(t, pool)
	ingestJobs(t, pool, 4)

	eval := newBlockingEvaluator()
	p := jobs.NewPool(pool, eval, jobs.NewLimiter(2), jobs.PoolConfig{
		Concurrency:       2,
		Lease:             30 * time.Second,
		HeartbeatInterval: 5 * time.Second,
		PollInterval:      10 * time.Millisecond,
		LLMRequestTimeout: 10 * time.Second,
		Policy:            jobs.DefaultPolicy(),
	}, nil)

	ctx, cancel := context.WithCancel(context.Background())
	p.Start(ctx)

	// Both workers must be inside Evaluate simultaneously with concurrency=2:
	// the first blocks on enter, so the second worker's claim+evaluate can only
	// proceed concurrently.
	eval.waitEntered(t, 2)
	if peak := eval.peakConcurrency(); peak != 2 {
		cancel()
		t.Fatalf("peak concurrency = %d, want exactly 2", peak)
	}
	eval.release(2)
	// The remaining two jobs proceed without blocking assertions; just let the
	// evaluator return immediately now.
	go func() {
		for range 2 {
			<-eval.entered
			eval.release(1)
		}
	}()

	waitFor(t, func() bool { return countTerminal(t, pool) == 4 }, 5*time.Second, "4 completed jobs")
	cancel()
	p.Wait()

	if got := eval.calls.Load(); got != 4 {
		t.Fatalf("evaluate calls = %d, want 4", got)
	}
	if peak := eval.peakConcurrency(); peak > 2 {
		t.Fatalf("peak concurrency = %d exceeds the cap of 2", peak)
	}
}

func TestPoolConcurrencyOneSerializes(t *testing.T) {
	pool := newTestPool(t)
	activateTestConfig(t, pool)
	ingestJobs(t, pool, 3)

	eval := newBlockingEvaluator()
	p := jobs.NewPool(pool, eval, jobs.NewLimiter(1), jobs.PoolConfig{
		Concurrency:       1,
		Lease:             30 * time.Second,
		HeartbeatInterval: 5 * time.Second,
		PollInterval:      10 * time.Millisecond,
		LLMRequestTimeout: 10 * time.Second,
		Policy:            jobs.DefaultPolicy(),
	}, nil)

	ctx, cancel := context.WithCancel(context.Background())
	p.Start(ctx)

	// With one worker, evaluations must never overlap.
	for range 3 {
		eval.waitEntered(t, 1)
		if peak := eval.peakConcurrency(); peak != 1 {
			cancel()
			t.Fatalf("peak concurrency = %d, want 1", peak)
		}
		eval.release(1)
	}
	waitFor(t, func() bool { return countTerminal(t, pool) == 3 }, 5*time.Second, "3 completed jobs")
	cancel()
	p.Wait()
}
