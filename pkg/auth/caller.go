package auth

import (
	"context"
	"time"
)

// Caller is the verified identity carried through a request.
type Caller struct {
	ID        string
	SessionID string
	Role      string
	Admin     bool
	// IsDeleting reports an access token issued after the account's
	// irreversible deletion was accepted. Services classify their start-work
	// methods against this state; existing-work completion stays permitted.
	IsDeleting bool
	IssuedAt   time.Time
	ExpiresAt  time.Time
	TokenID    string
}

type callerKey struct{}

func WithCaller(ctx context.Context, caller Caller) context.Context {
	return context.WithValue(ctx, callerKey{}, caller)
}

func CallerFromContext(ctx context.Context) (Caller, bool) {
	caller, ok := ctx.Value(callerKey{}).(Caller)
	return caller, ok && caller.ID != ""
}
