package accountdeletion

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/database/deletiondb"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/outbox"
	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"
)

type fakeFactQueries struct {
	facts    []deletiondb.ClaimCommittedFactRow
	idx      int
	claimErr error
	retried  []uuid.UUID
	marked   []uuid.UUID
}

func (f *fakeFactQueries) ClaimCommittedFact(
	_ context.Context,
	_ deletiondb.ClaimCommittedFactParams,
) (deletiondb.ClaimCommittedFactRow, error) {
	if f.claimErr != nil {
		return deletiondb.ClaimCommittedFactRow{}, f.claimErr
	}
	if f.idx >= len(f.facts) {
		return deletiondb.ClaimCommittedFactRow{}, sql.ErrNoRows
	}
	fact := f.facts[f.idx]
	f.idx++
	return fact, nil
}

func (f *fakeFactQueries) RetryFact(_ context.Context, arg deletiondb.RetryFactParams) error {
	f.retried = append(f.retried, arg.EventID)
	return nil
}

func (f *fakeFactQueries) MarkFactPublished(_ context.Context, arg deletiondb.MarkFactPublishedParams) error {
	f.marked = append(f.marked, arg.EventID)
	return nil
}

type fakePublisher struct {
	err   error
	calls int
}

func (p *fakePublisher) Publish(_ context.Context, _ string, _ []byte, _ proto.Message) error {
	p.calls++
	return p.err
}

func freshFact(eventID uuid.UUID) deletiondb.ClaimCommittedFactRow {
	return deletiondb.ClaimCommittedFactRow{
		EventID:           eventID,
		UserID:            uuid.New(),
		DeletionRequestID: uuid.New(),
		DeletedAt:         time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
		PayloadVersion:    1,
		LeaseToken:        uuid.NullUUID{UUID: uuid.New(), Valid: true},
	}
}

func newTestRelay(fq *fakeFactQueries, pub outbox.Publisher, now func() time.Time) *Relay {
	return &Relay{queries: fq, publisher: pub, now: now, poll: time.Second}
}

func TestRelayDrain(t *testing.T) {
	now := func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) }

	t.Run("publish success marks the fact published", func(t *testing.T) {
		eventID := uuid.New()
		fq := &fakeFactQueries{facts: []deletiondb.ClaimCommittedFactRow{freshFact(eventID)}}
		pub := &fakePublisher{}
		r := newTestRelay(fq, pub, now)

		if err := r.Drain(context.Background(), 1); err != nil {
			t.Fatalf("Drain: %v", err)
		}
		if pub.calls != 1 {
			t.Errorf("publish calls = %d, want 1", pub.calls)
		}
		if len(fq.marked) != 1 || fq.marked[0] != eventID {
			t.Errorf("marked = %v, want [%s]", fq.marked, eventID)
		}
		if len(fq.retried) != 0 {
			t.Errorf("unexpected retries: %v", fq.retried)
		}
	})

	t.Run("publish failure retries with the same event id and does not mark published", func(t *testing.T) {
		eventID := uuid.New()
		fq := &fakeFactQueries{facts: []deletiondb.ClaimCommittedFactRow{freshFact(eventID)}}
		pub := &fakePublisher{err: errors.New("broker down")}
		r := newTestRelay(fq, pub, now)

		// A failed publish is deferred and returned so the run loop can log it.
		if err := r.Drain(context.Background(), 1); err == nil {
			t.Fatal("expected deferred publish error on first pass")
		}
		if len(fq.retried) != 1 || fq.retried[0] != eventID {
			t.Errorf("retried = %v, want [%s] (a stable event id keeps replay dedup)", fq.retried, eventID)
		}
		if len(fq.marked) != 0 {
			t.Errorf("fact marked published despite failure: %v", fq.marked)
		}

		// The fact is re-claimed after the retry; a healthy broker acks it.
		pub.err = nil
		fq.idx = 0
		if err := r.Drain(context.Background(), 1); err != nil {
			t.Fatalf("Drain second pass: %v", err)
		}
		if len(fq.marked) != 1 || fq.marked[0] != eventID {
			t.Errorf("marked = %v, want [%s]", fq.marked, eventID)
		}
	})

	t.Run("claim error propagates", func(t *testing.T) {
		fq := &fakeFactQueries{claimErr: errors.New("query failed")}
		r := newTestRelay(fq, &fakePublisher{}, now)
		if err := r.Drain(context.Background(), 1); err == nil {
			t.Fatal("expected claim error to propagate")
		}
	})

	t.Run("no facts returns nil", func(t *testing.T) {
		fq := &fakeFactQueries{facts: nil}
		r := newTestRelay(fq, &fakePublisher{}, now)
		if err := r.Drain(context.Background(), 1); err != nil {
			t.Errorf("Drain with no facts: %v", err)
		}
	})
}
