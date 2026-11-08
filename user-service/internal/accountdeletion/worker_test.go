package accountdeletion

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/database/deletiondb"
	"github.com/google/uuid"
)

type fakeDrainer struct {
	result WorkResult
	err    error
}

func (f *fakeDrainer) GetUserOutstandingWork(context.Context, uuid.UUID) (WorkResult, error) {
	return f.result, f.err
}

type fakeJobQueries struct {
	claimed    deletiondb.ClaimDueJobRow
	claimErr   error
	pending    *deletiondb.RecordPendingCheckParams
	pendingErr error
	lockErr    error
}

func (f *fakeJobQueries) ClaimDueJob(
	_ context.Context,
	_ deletiondb.ClaimDueJobParams,
) (deletiondb.ClaimDueJobRow, error) {
	if f.claimErr != nil {
		return deletiondb.ClaimDueJobRow{}, f.claimErr
	}
	if f.claimed.UserID == uuid.Nil {
		return deletiondb.ClaimDueJobRow{}, sql.ErrNoRows
	}
	return f.claimed, nil
}

func (f *fakeJobQueries) RecordPendingCheck(_ context.Context, arg deletiondb.RecordPendingCheckParams) error {
	f.pending = &arg
	return f.pendingErr
}

func (f *fakeJobQueries) LockJobForFinalize(
	_ context.Context,
	_ deletiondb.LockJobForFinalizeParams,
) (deletiondb.LockJobForFinalizeRow, error) {
	return deletiondb.LockJobForFinalizeRow{}, f.lockErr
}

func newTestWorker(
	drain Drainer,
	jobs *fakeJobQueries,
	now func() time.Time,
	admissionGrace, lease, poll time.Duration,
) *Worker {
	p := &Plan{
		config: Config{AdmissionsGracePeriod: admissionGrace, Lease: lease, PollInterval: poll},
		drain:  drain,
		now:    now,
	}
	return &Worker{plan: p, queries: jobs, now: now, poll: poll}
}

func dueJob(admissionsSafeAfter time.Time, attempt int32) deletiondb.ClaimDueJobRow {
	return deletiondb.ClaimDueJobRow{
		UserID:              uuid.New(),
		DeletionRequestID:   uuid.New(),
		AdmissionsSafeAfter: admissionsSafeAfter,
		AttemptCount:        attempt,
	}
}

func TestWorkerDrainOncePostpone(t *testing.T) {
	now := func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) }

	cases := []struct {
		name          string
		drain         Drainer
		attempt       int32
		wantReasons   []string
		wantTransient bool
	}{
		{
			name: "outstanding work postpones on the next poll",
			drain: &fakeDrainer{result: WorkResult{
				HasOutstandingWork: true,
				Reasons:            []string{"order_work"},
				Available:          true,
			}},
			wantReasons: []string{"order_work"},
		},
		{
			name:        "dependency error postpones and backs off",
			drain:       &fakeDrainer{err: errors.New("rpc timeout")},
			wantReasons: []string{checkUnavailable},
		},
		{
			name:        "unavailable answer postpones and backs off",
			drain:       &fakeDrainer{result: WorkResult{Available: false}},
			wantReasons: []string{checkUnavailable},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			jobs := &fakeJobQueries{claimed: dueJob(now().Add(-time.Hour), 1)}
			w := newTestWorker(tc.drain, jobs, now, time.Hour, 30*time.Second, time.Minute)
			if err := w.drainOnce(context.Background()); err != nil {
				t.Fatalf("drainOnce: %v", err)
			}
			if jobs.pending == nil {
				t.Fatal("expected a pending check to be recorded")
			}
			if len(jobs.pending.WaitingReasons) != len(tc.wantReasons) {
				t.Errorf("reasons = %v, want %v", jobs.pending.WaitingReasons, tc.wantReasons)
			}
			for _, r := range tc.wantReasons {
				found := false
				for _, got := range jobs.pending.WaitingReasons {
					if got == r {
						found = true
					}
				}
				if !found {
					t.Errorf("reason %q missing from %v", r, jobs.pending.WaitingReasons)
				}
			}
			// The lease token is carried on the record-pending statement so only
			// the holder can clear its own lease (the SQL sets it NULL on match).
			if jobs.pending.LeaseToken == uuid.Nil {
				t.Errorf("expected the claiming lease token to identify the released row")
			}
		})
	}
}

func TestWorkerDrainOncePostponeOnHorizon(t *testing.T) {
	now := func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) }
	// Clean drain but the finalization horizon has not passed yet.
	drain := &fakeDrainer{result: WorkResult{HasOutstandingWork: false, Available: true}}
	jobs := &fakeJobQueries{claimed: dueJob(now().Add(time.Hour), 0)}
	w := newTestWorker(drain, jobs, now, time.Hour, 30*time.Second, time.Minute)

	if err := w.drainOnce(context.Background()); err != nil {
		t.Fatalf("drainOnce: %v", err)
	}
	if jobs.pending == nil {
		t.Fatal("expected a pending check")
	}
	if len(jobs.pending.WaitingReasons) != 1 || jobs.pending.WaitingReasons[0] != admissionGrace {
		t.Errorf("reasons = %v, want [%s]", jobs.pending.WaitingReasons, admissionGrace)
	}
	wantNext := now().UTC().Add(time.Minute)
	if !jobs.pending.NextAttemptAt.Equal(wantNext) {
		t.Errorf("next attempt = %v, want %v (fast retry when ready yet, not backoff)",
			jobs.pending.NextAttemptAt, wantNext)
	}
}

func TestWorkerDrainOnceNoLease(t *testing.T) {
	now := func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) }
	jobs := &fakeJobQueries{claimed: dueJob(now().UTC(), 0)}
	jobs.claimed.UserID = uuid.Nil // ClaimDueJob returns sql.ErrNoRows

	w := newTestWorker(&fakeDrainer{}, jobs, now, time.Hour, 30*time.Second, time.Minute)
	if err := w.drainOnce(context.Background()); err != nil {
		t.Fatalf("drainOnce with no claimable job: %v", err)
	}
	if jobs.pending != nil {
		t.Errorf("no pending check expected, got %+v", jobs.pending)
	}
}

func TestWorkerBackoffProgression(t *testing.T) {
	now := func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) }
	w := newTestWorker(&fakeDrainer{}, &fakeJobQueries{}, now, time.Hour, 30*time.Second, time.Minute)

	cases := []struct {
		attempt   int32
		transient bool
		want      time.Duration
	}{
		{0, false, time.Minute},
		{0, true, time.Minute},
		{1, true, 2 * time.Minute},
		{2, true, 4 * time.Minute},
		{3, true, maxBackoff},
		{10, true, maxBackoff},
		{5, false, time.Minute},
	}
	for _, tc := range cases {
		if got := w.retryAfter(tc.attempt, tc.transient); got != tc.want {
			t.Errorf("retryAfter(%d, %v) = %v, want %v", tc.attempt, tc.transient, got, tc.want)
		}
	}
}
