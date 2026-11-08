package accountdeletion

import (
	"context"
	"time"

	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/jwt"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/models"
	"github.com/google/uuid"
)

// SessionIssuer produces a replacement session's tokens. The auth service
// implements it against its JWT codec so the acceptance transaction can rotate
// the caller's session and issue an access token that carries the deleting
// lifecycle state. It returns the JWT adapter's own token struct so the
// acceptance layer can hand it straight to the HTTP adapter.
type SessionIssuer interface {
	Issue(ctx context.Context, sessions SessionStore, user *models.User, now time.Time) (jwt.AuthTokens, error)
}

// SessionStore is the session persistence the issuer needs to create the
// replacement session inside the acceptance transaction.
type SessionStore interface {
	Create(ctx context.Context, session *models.Session) error
	UpdateTokenHash(ctx context.Context, id uuid.UUID, tokenHash []byte) error
}

// WorkResult is the outcome of one outstanding-work check. Available reports
// whether the dependency answered; a false value means the check could not be
// completed and finalization must postpone rather than treat it as clean.
type WorkResult struct {
	HasOutstandingWork bool
	Reasons            []string
	Available          bool
}

// Drainer is the consumer-owned outstanding-work port. The Worker calls it once
// per claimed job; the concrete implementation (see compositeDrainer) calls the
// Order and Credit internal services. A timeout, error or unavailability counts
// as outstanding, never as clean.
type Drainer interface {
	GetUserOutstandingWork(ctx context.Context, userID uuid.UUID) (WorkResult, error)
}

// AccessRevoker is the PR 228 access-token revocation hook. Until PR 228 is
// merged it has a no-op default: acceptance hands it the swept session IDs and
// finalization never depends on the call succeeding. A revocation write failure
// is by design not evidence that old tokens became unusable, so callers ignore
// its error. It owns no revocation store of its own; a later PR 228
// implementation supplies the behaviour.
type AccessRevoker interface {
	RevokeAccess(ctx context.Context, userID uuid.UUID, sessionIDs []uuid.UUID) error
}

// noopRevoker is the default AccessRevoker. It records nothing and succeeds, so
// a missing PR 228 integration can never block acceptance or finalization.
type noopRevoker struct{}

func (noopRevoker) RevokeAccess(context.Context, uuid.UUID, []uuid.UUID) error { return nil }

// NewNoopRevoker returns the default, do-nothing AccessRevoker.
func NewNoopRevoker() AccessRevoker { return noopRevoker{} }
