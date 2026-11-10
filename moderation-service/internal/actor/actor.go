// Package actor reads the verified caller that pkg/auth puts on the request
// context.
package actor

import (
	"context"

	"github.com/google/uuid"

	"github.com/AY2627S1-CS3219-P1/FoC/pkg/api/errs"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/auth"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/auth/tokenclaims"
)

// ErrMissing reports a request without a verified caller.
var ErrMissing = errs.NewUnauthorizedError("invalid or missing access token")

// Caller is the verified request caller. Role semantics are methods, so call
// sites never compare the raw role string.
type Caller struct {
	auth.Caller
}

// IsSuspended reports a caller whose account is suspended.
func (c Caller) IsSuspended() bool {
	return tokenclaims.Role(c.Role) == tokenclaims.RoleSuspendedUser
}

// From returns the verified caller.
func From(ctx context.Context) (Caller, error) {
	caller, ok := auth.CallerFromContext(ctx)
	if !ok {
		return Caller{}, ErrMissing
	}
	return Caller{Caller: caller}, nil
}

// ID returns the verified caller's user ID.
func ID(ctx context.Context) (uuid.UUID, error) {
	caller, ok := auth.CallerFromContext(ctx)
	if !ok {
		return uuid.Nil, ErrMissing
	}
	id, err := uuid.Parse(caller.ID)
	if err != nil || id == uuid.Nil {
		return uuid.Nil, ErrMissing
	}
	return id, nil
}
