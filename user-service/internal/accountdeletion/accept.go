package accountdeletion

import (
	"context"
	"errors"
	"time"

	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/database/deletiondb"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/jwt"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/models"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/store"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// ErrAlreadyDeleted is returned when a user row to accept no longer exists.
var ErrAlreadyDeleted = errors.New("account no longer exists")

// ErrDeletingForbidden is returned when an account cannot request deletion (an
// active administrator, or the possibly-demoted first bootstrap admin whose
// admin_bootstrap row still links it). The handler maps it to
// connect.CodeFailedPrecondition.
var ErrDeletingForbidden = errors.New("administered accounts cannot request deletion")

// AcceptResult is the outcome of acceptance: the (possibly pre-existing) request
// time, the locked user snapshot, and the replacement session's tokens.
type AcceptResult struct {
	RequestedAt time.Time
	User        models.User
	Tokens      jwt.AuthTokens
}

// Plan coordinates acceptance, the drain worker and the deletion relay. It is
// built once in the composition root with the GORM pool, config, the session
// issuer, the outstanding-work drainer and the (optional) access-token revoker;
// the worker and relay reuse it so the timing knobs stay consistent. Pieces are
// injectable so acceptance and the worker can be unit-tested against fakes.
type Plan struct {
	gdb     *gorm.DB
	config  Config
	issuer  SessionIssuer
	drain   Drainer
	revoker AccessRevoker
	now     func() time.Time
}

// NewPlan builds a Plan. The caller owns gdb (the service GORM pool) and sqlDB;
// the worker and relay open their own statements against sqlDB. drain may be nil
// when only acceptance is wired, and revoker defaults to a no-op stub when nil.
func NewPlan(gdb *gorm.DB, config Config, issuer SessionIssuer, drain Drainer) *Plan {
	if drain == nil {
		drain = noopDrainer{}
	}
	return &Plan{gdb: gdb, config: config, issuer: issuer, drain: drain, revoker: NewNoopRevoker(), now: time.Now}
}

// SetIssuer installs the session issuer after construction. The plan and the
// auth Service own each other: the service needs the plan as its AccountDeletion
// port, and the plan needs the service as its session issuer, so the
// composition root builds the plan first and injects the issuer once the
// service exists. Acception dereferences the issuer only when it runs, so the
// plan is safe to build with a nil issuer in the interim.
func (p *Plan) SetIssuer(issuer SessionIssuer) {
	p.issuer = issuer
}

type noopDrainer struct{}

func (noopDrainer) GetUserOutstandingWork(context.Context, uuid.UUID) (WorkResult, error) {
	return WorkResult{Available: true}, nil
}

// Accept marks the account permanently deleting inside a single transaction and
// rotates the caller's session. Acceptance is idempotent: an already-deleting
// account keeps its original request time, skips its session sweep (it was
// swept when it first accepted) and still receives a fresh session.
// Administered accounts — an active admin or the (possibly demoted) first
// bootstrap admin — are rejected before any write.
//
// Accept does not authenticate the caller's session: it assumes the caller has
// run a read-only liveness check and is presenting a live session, so it rotates
// whatever sessions it is told are live. The auth Service provides that
// guarantee before calling.
func (p *Plan) Accept(ctx context.Context, user *models.User, now time.Time) (AcceptResult, error) {
	if user == nil {
		return AcceptResult{}, errors.New("cannot accept deletion for a nil user")
	}
	var result AcceptResult
	err := p.gdb.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		queries := deletiondb.New(tx.Statement.ConnPool)
		scope := store.New(tx)

		locked, err := scope.Users.GetByIDForUpdate(ctx, user.ID)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				return ErrAlreadyDeleted
			}
			return err
		}
		// Protected accounts are rejected before any write. The role check covers
		// active admins and super-admins; the bootstrap check catches a demoted
		// first-admin row that still links the user via admin_bootstrap.
		if locked.IsAdmin() {
			return ErrDeletingForbidden
		}
		bootstrapped, err := scope.Admin.IsBootstrapped(ctx, locked.ID)
		if err != nil {
			return err
		}
		if bootstrapped {
			return ErrDeletingForbidden
		}

		requestedAt := locked.DeletionRequestedAt
		wasDeleting := locked.IsDeleting
		if !wasDeleting {
			if err := scope.Users.AcceptDeletion(ctx, locked.ID, now); err != nil {
				return err
			}
			if err := queries.InsertJob(ctx, deletiondb.InsertJobParams{
				UserID:              locked.ID,
				DeletionRequestID:   uuid.New(),
				AdmissionsSafeAfter: now.Add(p.config.AdmissionsGracePeriod),
				NextAttemptAt:       now.Add(p.config.InitialDrainDelay),
			}); err != nil {
				return err
			}
			locked.IsDeleting = true
			locked.DeletionRequestedAt = &now
			requestedAt = &now
		}

		// On first acceptance, capture and sweep the account's live sessions so
		// the revocation hook can invalidate their matching access tokens; the
		// sweep rotates the caller out of every device before the replacement
		// session is issued below. An already-deleting account (an idempotent
		// retry) skips the sweep: its sessions were swept when it first accepted,
		// so the retry only rotates the caller's own session.
		if !wasDeleting {
			sessionIDs, err := scope.Sessions.LiveSessionIDsForUser(ctx, locked.ID)
			if err != nil {
				sessionIDs = nil
			}
			if _, err := scope.Sessions.RevokeAllForUser(ctx, locked.ID, now); err != nil {
				return err
			}
			// PR 228 revocation hook: best-effort, never blocks acceptance.
			_ = p.revoker.RevokeAccess(ctx, locked.ID, sessionIDs)
		}

		tokens, err := p.issuer.Issue(ctx, scope.Sessions, locked, now)
		if err != nil {
			return err
		}

		result = AcceptResult{RequestedAt: *requestedAt, User: *locked, Tokens: tokens}
		return nil
	})
	if err != nil {
		return AcceptResult{}, err
	}
	return result, nil
}

// finalize hard-deletes the account under the worker's lease and records the
// committed deletion fact. It runs inside the worker's transaction, so it uses
// the passed transaction rather than opening its own. It deletes the email-bound
// magic-link challenges (which have a NULL user_id and so do not cascade),
// cancels any not-yet-published users.create fact, then hard-deletes the user so
// the cascade clears the sessions, login magic-link tokens and favourites and the
// deletion job. The whole step is atomic: a fact-insert failure rolls everything
// back and leaves the account undeleted.
func finalize(
	ctx context.Context,
	tx *gorm.DB,
	user models.User,
	requestID uuid.UUID,
	deletedAt time.Time,
	queries *deletiondb.Queries,
) error {
	if err := clearEmailChallenges(ctx, tx, user.Email); err != nil {
		return err
	}
	if err := cancelUnpublishedRegistrations(ctx, tx, user.ID); err != nil {
		return err
	}
	res := tx.Unscoped().Delete(&models.User{}, user.ID)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return nil
	}
	return queries.InsertDeletedFact(ctx, deletiondb.InsertDeletedFactParams{
		EventID:           uuid.New(),
		UserID:            user.ID,
		DeletionRequestID: requestID,
		DeletedAt:         deletedAt,
	})
}

// clearEmailChallenges removes the register-purpose magic-link tokens left for a
// user. They carry a NULL user_id (see auth_tokens) so they do not cascade with
// the user row; they must be dropped by email before the row is gone.
func clearEmailChallenges(ctx context.Context, tx *gorm.DB, email string) error {
	return tx.Unscoped().Where("email = ? AND purpose = ? AND deleted_at IS NULL",
		email, models.TokenPurposeRegister).Delete(&models.AuthToken{}).Error
}

// cancelUnpublishedRegistrations drops any not-yet-published users.create fact
// for the user so the relay cannot publish a registration for an account that is
// about to be physically removed. Published facts are left untouched; they were
// already delivered.
func cancelUnpublishedRegistrations(ctx context.Context, tx *gorm.DB, userID uuid.UUID) error {
	return tx.Where("user_id = ? AND published_at IS NULL", userID).
		Unscoped().Delete(&models.UserCreatedOutbox{}).Error
}
