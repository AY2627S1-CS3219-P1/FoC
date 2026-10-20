package order

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
)

const expiryTimerBatchSize = 50

// ExpiryTimerRepository discovers candidates only. Eligibility is checked again
// by the Mutator under the parent lock, never from the scan's snapshot.
type ExpiryTimerRepository interface {
	Repository
	DueExpiryTimers(context.Context, time.Time, string, int32) ([]string, error)
}

type ExpiryTimerOptions struct {
	Now   func() time.Time
	NewID func() string
}

type ExpiryTimers struct {
	repository ExpiryTimerRepository
	mutator    *Mutator
	now        func() time.Time
	newID      func() string

	scanMu  sync.Mutex
	after   string
	retries map[string]expiryTimerRetry
}

type expiryTimerRetry struct {
	nextAttempt time.Time
	delay       time.Duration
	seen        bool
}

func NewExpiryTimers(repository ExpiryTimerRepository, options ExpiryTimerOptions) *ExpiryTimers {
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.NewID == nil {
		options.NewID = uuid.NewString
	}

	return &ExpiryTimers{
		repository: repository,
		mutator:    NewMutator(repository),
		now:        options.Now,
		newID:      options.NewID,
		after:      uuid.Nil.String(),
		retries:    make(map[string]expiryTimerRetry),
	}
}

// Expire preserves the requester's original deadline and financial observations.
// Assignment recovery, not this local transaction, compensates late Credit work.
func (s *ExpiryTimers) Expire(ctx context.Context, id string) error {
	err := s.mutator.Mutate(ctx, id, func(before Order, offers []Offer) (*Change, error) {
		now := s.now().UTC().Truncate(time.Microsecond)
		if before.Status != AwaitingCourier || before.ExpiresAt.After(now) {
			return nil, nil
		}

		actor := Actor{Kind: System}
		next, _, err := Transition(before, Expire, actor)
		if err != nil {
			return nil, err
		}

		after := cloneOrder(before)
		reason := "request_expired"
		after.Status = next
		after.ClosedAt, after.CloseReason = &now, &reason
		after.UpdatedAt = now
		after.Revision++
		details, err := json.Marshal(struct {
			Deadline time.Time `json:"deadline"`
		}{Deadline: before.ExpiresAt})
		if err != nil {
			return nil, err
		}
		change, err := NewChange(&before, after, actor, Expire, details, s.newID)
		if err != nil {
			return nil, err
		}

		for _, offer := range offers {
			if offer.Status != OfferOpen && offer.Status != OfferAccepting {
				continue
			}
			expected := offer.Status
			offer.Status, offer.UpdatedAt = OfferVoid, now
			change.Offers = append(change.Offers, OfferChange{Offer: offer, ExpectedStatus: expected})
		}
		return &change, nil
	})
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	return err
}

// ScanOnce handles one bounded page. Advancing past failed candidates prevents a
// broken oldest order from starving the rest; another sweep retries it. Cursor
// state is advisory: a new process starts from the persisted overdue orders.
func (s *ExpiryTimers) ScanOnce(ctx context.Context) error {
	s.scanMu.Lock()
	defer s.scanMu.Unlock()

	scan, cancel := context.WithTimeout(ctx, 5*time.Second)
	ids, err := s.repository.DueExpiryTimers(scan, s.now().UTC(), s.after, expiryTimerBatchSize)
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
				err := s.Expire(attempt, id)
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
		slog.WarnContext(ctx, "Order expiry attempt deferred", "order_id", result.id, "error", result.err, "retry_at", retry.nextAttempt)
	}

	if len(ids) == expiryTimerBatchSize {
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
func (s *ExpiryTimers) Run(ctx context.Context) {
	delay := time.Second
	for ctx.Err() == nil {
		if err := s.ScanOnce(ctx); err != nil {
			if ctx.Err() != nil {
				return
			}
			slog.ErrorContext(ctx, "Order expiry scan deferred", "error", err, "retry_after", delay)
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
