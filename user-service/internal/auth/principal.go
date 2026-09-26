// Package auth handles authentication: magic-link registration/login,
// opaque server-side sessions, and carrying the caller through the request
// context.
//
// Authorisation (may you do this?) is decided by each handler from the
// caller's role, e.g.
//
//	p, err := auth.Require(r.Context())
//	if err != nil { return err }                       // 401
//	if !p.IsSelf(id) && !p.CanManage(target.Role) {   // 403
//		return apperr.ErrForbidden
//	}
//
// Role hierarchy:
//
//	super_admin  manages admin, user, suspended   (can promote user -> admin)
//	admin        manages user, suspended          (suspend / reinstate)
//	user         manages nobody
//	suspended    manages nobody; blocked from mutating actions (U6.3-6.6)
//
// Nobody can change their own role, and super_admin can only be granted
// through first-signup bootstrap (U4.2).
package auth

import (
	"context"
	"net/http"

	"github.com/google/uuid"

	"user-service/internal/apperr"
	"user-service/internal/httpx"
	"user-service/internal/models"
)

// Principal is the authenticated caller. Built from the users row on every
// request, so role changes (incl. suspension) apply immediately.
type Principal struct {
	UserID    uuid.UUID
	Role      models.RoleName
	SessionID uuid.UUID
}

func (p Principal) IsSelf(id uuid.UUID) bool       { return p.UserID == id }
func (p Principal) HasRole(r models.RoleName) bool { return p.Role == r }
func (p Principal) IsSuperAdmin() bool             { return p.Role == models.RoleSuperAdmin }

// IsAdmin is true for admin and super_admin.
func (p Principal) IsAdmin() bool     { return p.Role.IsAdmin() }
func (p Principal) IsSuspended() bool { return p.Role == models.RoleSuspended }

// HasAnyRole reports whether the caller holds one of roles.
func (p Principal) HasAnyRole(roles ...models.RoleName) bool {
	for _, r := range roles {
		if p.Role == r {
			return true
		}
	}
	return false
}

// manages lists which target roles each role may administer.
var manages = map[models.RoleName]map[models.RoleName]bool{
	models.RoleSuperAdmin: {models.RoleAdmin: true, models.RoleUser: true, models.RoleSuspended: true},
	models.RoleAdmin:      {models.RoleUser: true, models.RoleSuspended: true},
}

// CanManage reports whether the caller may administer (edit, suspend,
// delete, change role of) a user whose current role is target.
func (p Principal) CanManage(target models.RoleName) bool {
	return manages[p.Role][target]
}

// CanAssignRole reports whether the caller may move user targetID from role
// `from` to role `to`:
//   - admin:       user <-> suspended
//   - super_admin: user/suspended <-> admin, plus everything admin can do
//   - nobody:      own role, anything involving super_admin
func (p Principal) CanAssignRole(targetID uuid.UUID, from, to models.RoleName) bool {
	if from == to || !to.Valid() || p.IsSelf(targetID) {
		return false
	}
	return p.CanManage(from) && p.CanManage(to)
}

type ctxKey struct{}

func WithPrincipal(ctx context.Context, p Principal) context.Context {
	return context.WithValue(ctx, ctxKey{}, p)
}

func FromContext(ctx context.Context) (Principal, bool) {
	p, ok := ctx.Value(ctxKey{}).(Principal)
	return p, ok
}

// Require returns the caller or apperr.ErrUnauthenticated.
func Require(ctx context.Context) (Principal, error) {
	p, ok := FromContext(ctx)
	if !ok {
		return Principal{}, apperr.ErrUnauthenticated
	}
	return p, nil
}

// RequireAuth rejects requests with no Principal (401). Mount it after
// Middleware on every protected route group (NFR-05.2.1).
func RequireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := FromContext(r.Context()); !ok {
			httpx.WriteError(w, r, nil, apperr.ErrUnauthenticated)
			return
		}
		next.ServeHTTP(w, r)
	})
}
