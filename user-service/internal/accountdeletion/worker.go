package accountdeletion

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"time"

	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/database/deletiondb"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/store"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// jobQueries is the subset of the deletion job persistence the worker needs.
// Declaring it here (rather than depending on the generated type) lets the worker
// be exercised against a fake in unit tests.
type jobQueries interface {
	ClaimDueJob(ctx context.Context, arg deletiondb.ClaimDueJobParams) (deletiondb.ClaimDueJobRow, error)
	RecordPendingCheck(ctx context.Context, arg deletiondb.RecordPendingCheckParams) error
	LockJobForFinalize(
		ctx context.Context,
		arg deletiondb.LockJobForFinalizeParams,
	) (deletiondb.LockJobForFinalizeRow, error)
}

const (
	// checkUnavailable and admissionGrace are the stable reason codes the worker
	// reports while a drain is in progress.
	checkUnavailable = "check_unavailable"
	admissionGrace   = "admission_grace"
)

// errDrainUnavailable is passed to recordPending when a drain returns without an
// error but reports the port unavailable; it marks the check transient so the
// worker backs off rather than retrying it every poll.
var errDrainUnavailable = errors.New("outstanding-work check unavailable")

// Worker claims pending jobs, drains each account's outstanding work and either
// postpones finalization or performs the hard delete. It runs for the life of
// the service and backs off on transient errors.
type Worker struct {
	plan    *Plan
	queries jobQueries
	now     func() time.Time
	poll    time.Duration
}

// NewWorker builds the drain worker from a Plan. sqlDB backs the claim, record
// and status statements; the Plan's GORM pool backs the finalization transaction.
func NewWorker(plan *Plan, sqlDB *sql.DB) *Worker {
	return &Worker{plan: plan, queries: deletiondb.New(sqlDB), now: plan.now, poll: plan.config.PollInterval}
}

// Run loops until the context ends, draining as much work as a poll finds and
// sleeping between polls. It never returns because of a drained port: an
// unavailable Order or Credit service postpones rather than aborts the loop.
func (w *Worker) Run(ctx context.Context) {
	ticker := time.NewTicker(w.poll)
	defer ticker.Stop()
	for {
		if ctx.Err() != nil {
			return
		}
		if err := w.drainOnce(ctx); err != nil && ctx.Err() == nil {
			slog.ErrorContext(ctx, "account deletion drain failed", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// drainOnce claims and processes up to the per-pass batch limit of due jobs. No
// claimable job is not an error; it just means nothing more to do this poll.
func (w *Worker) drainOnce(ctx context.Context) error {
	for processed := 0; processed < defaultDrainLimit; processed++ {
		now := w.now().UTC()
		leaseToken := uuid.New()
		claimed, err := w.queries.ClaimDueJob(ctx, deletiondb.ClaimDueJobParams{
			LeaseToken: leaseToken, LeaseUntil: now.Add(w.plan.config.Lease),
		})
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if err := w.processJob(ctx, claimed, leaseToken, now); err != nil {
			return err
		}
	}
	return nil
}

func (w *Worker) processJob(
	ctx context.Context,
	claimed deletiondb.ClaimDueJobRow,
	leaseToken uuid.UUID,
	now time.Time,
) error {
	// Bound one drain call so a slow or hung service reads as "unknown", never
	// as "empty".
	drainCtx, cancel := context.WithTimeout(ctx, rpcDeadline)
	result, err := w.plan.drain.GetUserOutstandingWork(drainCtx, claimed.UserID)
	cancel()
	if err != nil || !result.Available {
		// A dependency error or an explicit "could not complete" answer both
		// mean the port is unknown, so the check is transient and backs off.
		checkErr := err
		if checkErr == nil {
			checkErr = errDrainUnavailable
		}
		return w.recordPending(ctx, claimed, leaseToken, []string{checkUnavailable}, checkErr)
	}
	if result.HasOutstandingWork {
		return w.recordPending(ctx, claimed, leaseToken, result.Reasons, nil)
	}
	if !now.After(claimed.AdmissionsSafeAfter) {
		return w.recordPending(ctx, claimed, leaseToken, []string{admissionGrace}, nil)
	}
	return w.finalizeJob(ctx, claimed, leaseToken, now)
}

// finalizeJob re-leases the job under the worker's lease, locks the user row so
// session issuance cannot race it, hard-deletes the account and records the
// committed fact, all in one transaction. A row that is gone (another replica
// finalized first, or the lease was lost) is a no-op success.
func (w *Worker) finalizeJob(
	ctx context.Context,
	claimed deletiondb.ClaimDueJobRow,
	leaseToken uuid.UUID,
	now time.Time,
) error {
	leaseUntil := now.Add(w.plan.config.Lease)
	return w.plan.gdb.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		queries := deletiondb.New(tx.Statement.ConnPool)
		locked, err := queries.LockJobForFinalize(ctx, deletiondb.LockJobForFinalizeParams{
			UserID: claimed.UserID, LeaseToken: leaseToken, LeaseUntil: leaseUntil,
		})
		if errors.Is(err, sql.ErrNoRows) {
			// The job — and therefore the users row — is already gone.
			return nil
		}
		if err != nil {
			return err
		}
		if locked.UserID == uuid.Nil {
			// The lease was lost to another replica between claim and finalize.
			return nil
		}

		scope := store.New(tx)
		user, err := scope.Users.GetByIDForUpdate(ctx, locked.UserID)
		if errors.Is(err, store.ErrNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		if !user.IsDeleting {
			// Defensive: a deletion job must only exist for a deleting account.
			// Never hard-delete a live account; drop the inconsistent job so it
			// does not spin on every poll.
			return queries.DeleteJob(ctx, locked.UserID)
		}
		return finalize(ctx, tx, *user, locked.DeletionRequestID, now, queries)
	})
}

// recordPending stores the outcome of a postponing check: the waiting reasons,
// the classified error, a scheduled next attempt, and the released lease so a
// later poll or another replica can retry. The lease guard stops a stale replica
// from clearing a lease another replica is mid-flight on. Transient dependency
// failures back off (bounded at 5 minutes); a known "not ready yet" result
// retries on the next poll.
func (w *Worker) recordPending(
	ctx context.Context,
	claimed deletiondb.ClaimDueJobRow,
	leaseToken uuid.UUID,
	reasons []string,
	checkErr error,
) error {
	next := w.now().UTC().Add(w.retryAfter(claimed.AttemptCount, checkErr != nil))
	lastError := sql.NullString{}
	if checkErr != nil {
		lastError = sql.NullString{String: checkErr.Error(), Valid: true}
	}
	return w.queries.RecordPendingCheck(ctx, deletiondb.RecordPendingCheckParams{
		UserID:         claimed.UserID,
		LeaseToken:     leaseToken,
		LastCheckedAt:  w.now().UTC(),
		NextAttemptAt:  next,
		WaitingReasons: reasons,
		LastError:      lastError,
	})
}

// retryAfter chooses how long to wait before the next drain. A dependency
// failure backs off exponentially, capped at maxBackoff; a known not-ready result
// (work outstanding or the admission horizon not yet passed) retries on the next
// poll so a ready account is finalized promptly.
func (w *Worker) retryAfter(attempt int32, transient bool) time.Duration {
	if !transient {
		return w.poll
	}
	backoff := w.poll
	for i := int32(0); i < attempt && backoff < maxBackoff; i++ {
		backoff *= 2
		if backoff <= 0 || backoff >= maxBackoff {
			backoff = maxBackoff
			break
		}
	}
	if backoff > maxBackoff {
		backoff = maxBackoff
	}
	return backoff
}
