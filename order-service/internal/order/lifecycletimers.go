package order

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
)

const lifecycleTimerBatchSize = 50

// The two lifecycle policies share bounded discovery, retry pacing and shutdown.
// Policy-specific locking and mutation stay in ExpiryTimers/DeliveryTimers.
type lifecycleTimerScanner struct {
	kind     string
	now      func() time.Time
	discover func(context.Context, time.Time, string, int32) ([]string, error)
	attempt  func(context.Context, string) error

	scanMu  sync.Mutex
	after   string
	retries map[string]lifecycleTimerRetry
}

type lifecycleTimerRetry struct {
	nextAttempt time.Time
	delay       time.Duration
	seen        bool
}

func newLifecycleTimerScanner(
	kind string,
	testNow func() time.Time,
	discover func(context.Context, time.Time, string, int32) ([]string, error),
	attempt func(context.Context, string) error,
) *lifecycleTimerScanner {
	now := testNow
	if now == nil {
		now = time.Now
	}
	return &lifecycleTimerScanner{
		kind:     kind,
		now:      now,
		discover: discover,
		attempt:  attempt,
		after:    uuid.Nil.String(),
		retries:  make(map[string]lifecycleTimerRetry),
	}
}

// This adapter borrows an existing transaction. The Mutator still owns
// lock -> fresh relationships -> build -> Apply, without nesting transactions.
type borrowedTimerRepository struct{ tx Tx }

func (r borrowedTimerRepository) Within(_ context.Context, fn func(Tx) error) error { return fn(r.tx) }

type lifecycleTimerClock interface {
	ProofServerTime(context.Context) (time.Time, error)
}

func lockedLifecycleTime(ctx context.Context, clock lifecycleTimerClock, testNow func() time.Time) (time.Time, error) {
	if testNow != nil {
		return testNow().UTC().Truncate(time.Microsecond), nil
	}

	now, err := clock.ProofServerTime(ctx)
	if err != nil {
		return time.Time{}, err
	}
	return now.UTC().Truncate(time.Microsecond), nil
}

// ScanOnce handles one bounded page. Advancing past failed candidates prevents a
// broken oldest order from starving the rest; another sweep retries it. Cursor
// state is advisory: a new process starts from the persisted overdue orders.
func (s *lifecycleTimerScanner) ScanOnce(ctx context.Context) error {
	s.scanMu.Lock()
	defer s.scanMu.Unlock()

	scan, cancel := context.WithTimeout(ctx, 5*time.Second)
	ids, err := s.discover(scan, s.now().UTC(), s.after, lifecycleTimerBatchSize)
	cancel()
	if err != nil {
		return err
	}

	type result struct {
		id  string
		err error
	}
	jobs := make(chan string)
	results := make(chan result, len(ids))
	var workers sync.WaitGroup
	for range 4 {
		workers.Go(func() {
			for id := range jobs {
				attempt, cancel := context.WithTimeout(ctx, 5*time.Second)
				err := s.attempt(attempt, id)
				cancel()
				results <- result{id: id, err: err}
			}
		})
	}

	for _, id := range ids {
		retry, failedBefore := s.retries[id]
		if failedBefore {
			retry.seen = true
			s.retries[id] = retry
			if retry.nextAttempt.After(s.now().UTC()) {
				continue
			}
		}
		select {
		case jobs <- id:
		case <-ctx.Done():
			close(jobs)
			workers.Wait()
			return ctx.Err()
		}
	}
	close(jobs)
	workers.Wait()
	if err := ctx.Err(); err != nil {
		return err
	}

	close(results)
	for result := range results {
		if result.err == nil {
			delete(s.retries, result.id)
			continue
		}
		retry := s.retries[result.id]
		retry.delay = min(30*time.Second, max(time.Second, 2*retry.delay))
		retry.nextAttempt = s.now().UTC().Add(retry.delay)
		retry.seen = true
		s.retries[result.id] = retry
		slog.WarnContext(
			ctx,
			"Order lifecycle attempt deferred",
			"timer",
			s.kind,
			"order_id",
			result.id,
			"error",
			result.err,
			"retry_at",
			retry.nextAttempt,
		)
	}

	if len(ids) == lifecycleTimerBatchSize {
		s.after = ids[len(ids)-1]
	} else {
		s.after = uuid.Nil.String()
		// Forget failures no longer found in the persisted overdue set, such as
		// orders cancelled by another instance. Retry pacing is advisory only.
		for id, retry := range s.retries {
			if !retry.seen {
				delete(s.retries, id)
			} else {
				retry.seen = false
				s.retries[id] = retry
			}
		}
	}
	return nil
}

// Run scans immediately after startup, independently of Kafka, Credit and HTTP
// admissions. Only discovery failures back off the whole scan. Individual order
// failures have separate retry pacing and never slow healthy-order discovery.
func (s *lifecycleTimerScanner) Run(ctx context.Context) {
	delay := time.Second
	for ctx.Err() == nil {
		if err := s.ScanOnce(ctx); err != nil {
			if ctx.Err() != nil {
				return
			}
			slog.ErrorContext(ctx, "Order lifecycle scan deferred", "timer", s.kind, "error", err, "retry_after", delay)
		} else {
			delay = time.Second
		}

		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		delay = min(30*time.Second, 2*delay)
	}
}
