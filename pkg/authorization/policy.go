// Package authorization evaluates permissions independently of the transport.
package authorization

import (
	"context"
	"errors"
	"fmt"

	"github.com/AY2627S1-CS3219-P1/FoC/pkg/api/errs"
)

// Role is the canonical application role. JWTs use "suspended_user" for the
// suspended role; their adapter converts that value before evaluating policies.
type Role string

const (
	RoleSuperAdmin Role = "super_admin"
	RoleAdmin      Role = "admin"
	RoleUser       Role = "user"
	RoleSuspended  Role = "suspended"
)

func (r Role) Valid() bool {
	switch r {
	case RoleSuperAdmin, RoleAdmin, RoleUser, RoleSuspended:
		return true
	default:
		return false
	}
}

// Principal is the caller information consumed by authorization policies.
type Principal interface {
	SubjectID() string
	RoleName() Role
}

type Decision uint8

const (
	Deny Decision = iota
	Allow
)

// Policy is implemented by role checks and, later, policies with dependencies
// such as a database. An error means the decision could not be made.
type Policy interface {
	Evaluate(context.Context, Principal) (Decision, error)
}

// RolePolicy permits only the roles supplied to NewRolePolicy.
type RolePolicy struct{ allowed []Role }

func NewRolePolicy(allowed ...Role) RolePolicy {
	return RolePolicy{allowed: append([]Role(nil), allowed...)}
}

func (p RolePolicy) Evaluate(_ context.Context, principal Principal) (Decision, error) {
	if principal == nil {
		return Deny, nil
	}
	role := principal.RoleName()
	if !role.Valid() {
		return Deny, nil
	}
	for _, allowed := range p.allowed {
		if role == allowed {
			return Allow, nil
		}
	}
	return Deny, nil
}

// Enforce turns a policy decision into the shared client-facing error types.
func Enforce(ctx context.Context, principal Principal, policy Policy) error {
	if principal == nil || principal.SubjectID() == "" {
		return errs.NewUnauthorizedError("authentication required")
	}
	if policy == nil {
		return errors.New("authorization policy is missing")
	}
	decision, err := policy.Evaluate(ctx, principal)
	if err != nil {
		return fmt.Errorf("evaluate authorization policy: %w", err)
	}
	if decision != Allow {
		return errs.NewForbiddenError("permission denied")
	}
	return nil
}
