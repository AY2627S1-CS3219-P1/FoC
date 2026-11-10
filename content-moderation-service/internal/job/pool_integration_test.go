//go:build integration

package job_test

import (
	"context"
	"fmt"
	moderationjob "github.com/AY2627S1-CS3219-P1/FoC/content-moderation-service/internal/job"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/AY2627S1-CS3219-P1/FoC/content-moderation-service/internal/moderation"
	"github.com/jackc/pgx/v5/pgxpool"
)

type poolT = pgxpool.Pool

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

func (e *blockingEvaluator) AssessReview(
	_ context.Context,
	_ moderation.ReviewInput,
) (moderation.ReviewAssessment, error) {
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
	return moderation.ReviewAssessment{Outcome: moderation.OutcomeApproved, PolicyVersion: "review-text-v1"}, nil
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
	store := moderationjob.NewService(pool, moderationjob.DefaultRetrySettings(), nil, "test-model")
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

	ingestJobs(t, pool, 4)

	eval := newBlockingEvaluator()
	p := moderationjob.NewPool(
		moderationjob.NewService(pool, moderationjob.DefaultRetrySettings(), nil, "test-model"),
		eval,

		moderationjob.PoolConfig{
			Concurrency:       2,
			Lease:             30 * time.Second,
			HeartbeatInterval: 5 * time.Second,
			PollInterval:      10 * time.Millisecond,
			LLMRequestTimeout: 10 * time.Second,
			RetrySettings:     moderationjob.DefaultRetrySettings(),
		},
		nil,
	)

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

	ingestJobs(t, pool, 3)

	eval := newBlockingEvaluator()
	p := moderationjob.NewPool(
		moderationjob.NewService(pool, moderationjob.DefaultRetrySettings(), nil, "test-model"),
		eval,

		moderationjob.PoolConfig{
			Concurrency:       1,
			Lease:             30 * time.Second,
			HeartbeatInterval: 5 * time.Second,
			PollInterval:      10 * time.Millisecond,
			LLMRequestTimeout: 10 * time.Second,
			RetrySettings:     moderationjob.DefaultRetrySettings(),
		},
		nil,
	)

	ctx, cancel := context.WithCancel(context.Background())
	p.Start(ctx)

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

func TestIndependentPoolsScaleConcurrency(t *testing.T) {
	for _, tc := range []struct{ replicas, concurrency int }{{1, 3}, {2, 2}} {
		t.Run(fmt.Sprintf("%d-pools-%d-workers", tc.replicas, tc.concurrency), func(t *testing.T) {
			pool := newTestPool(t)

			total := tc.replicas * tc.concurrency
			ingestJobs(t, pool, total)
			eval := newBlockingEvaluator()
			ctx, cancel := context.WithCancel(context.Background())
			var pools []*moderationjob.Pool
			t.Cleanup(func() {
				cancel()
				eval.release(total)
				for _, p := range pools {
					p.Wait()
				}
			})
			for range tc.replicas {
				p := moderationjob.NewPool(
					moderationjob.NewService(pool, moderationjob.DefaultRetrySettings(), nil, "test-model"),
					eval,

					moderationjob.PoolConfig{
						Concurrency: tc.concurrency, Lease: 30 * time.Second,
						HeartbeatInterval: time.Second, PollInterval: 10 * time.Millisecond,
						LLMRequestTimeout: 10 * time.Second, RetrySettings: moderationjob.DefaultRetrySettings(),
					},
					nil,
				)
				pools = append(pools, p)
				p.Start(ctx)
			}
			eval.waitEntered(t, total)
			if peak := eval.peakConcurrency(); peak != total {
				t.Fatalf("peak = %d, want %d", peak, total)
			}
			eval.release(total)
			waitFor(
				t,
				func() bool { return countTerminal(t, pool) == total },
				5*time.Second,
				"all replica jobs completed",
			)
			if calls := eval.calls.Load(); calls != int64(total) {
				t.Fatalf("calls = %d, want %d distinct jobs", calls, total)
			}
		})
	}
}
