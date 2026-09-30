package authorization_test

import (
	"context"
	"errors"
	"testing"

	"connectrpc.com/connect"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/api"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/authorization"
)

type principal struct {
	id   string
	role authorization.Role
}

func (p principal) SubjectID() string            { return p.id }
func (p principal) RoleName() authorization.Role { return p.role }

type failingPolicy struct{ err error }

func (p failingPolicy) Evaluate(context.Context, authorization.Principal) (authorization.Decision, error) {
	return authorization.Deny, p.err
}

func TestRolePolicy(t *testing.T) {
	ctx := context.Background()
	admin := authorization.NewRolePolicy(authorization.RoleAdmin, authorization.RoleSuperAdmin)
	for _, tc := range []struct {
		role authorization.Role
		want connect.Code
	}{
		{authorization.RoleAdmin, 0},
		{authorization.RoleSuperAdmin, 0},
		{authorization.RoleUser, connect.CodePermissionDenied},
		{authorization.RoleSuspended, connect.CodePermissionDenied},
		{"unrecognized", connect.CodePermissionDenied},
		{"", connect.CodePermissionDenied},
	} {
		err := authorization.Enforce(ctx, principal{id: "user-1", role: tc.role}, admin)
		if tc.want == 0 && err != nil {
			t.Fatalf("role %q: %v", tc.role, err)
		}
		if tc.want != 0 && connect.CodeOf(api.ToConnectError(ctx, err)) != tc.want {
			t.Fatalf("role %q: code %v, want %v", tc.role, connect.CodeOf(api.ToConnectError(ctx, err)), tc.want)
		}
	}
	if err := authorization.Enforce(ctx, principal{id: "user-1", role: authorization.RoleAdmin}, authorization.NewRolePolicy()); connect.CodeOf(api.ToConnectError(ctx, err)) != connect.CodePermissionDenied {
		t.Fatalf("empty policy: %v", err)
	}
	if err := authorization.Enforce(ctx, principal{id: "user-1", role: "unrecognized"}, authorization.NewRolePolicy("unrecognized")); connect.CodeOf(api.ToConnectError(ctx, err)) != connect.CodePermissionDenied {
		t.Fatalf("unknown role should deny: %v", err)
	}
}

func TestEnforceMissingIdentityAndPolicyFailure(t *testing.T) {
	ctx := context.Background()
	admin := authorization.NewRolePolicy(authorization.RoleAdmin)
	for _, actor := range []authorization.Principal{nil, principal{role: authorization.RoleAdmin}} {
		err := authorization.Enforce(ctx, actor, admin)
		if connect.CodeOf(api.ToConnectError(ctx, err)) != connect.CodeUnauthenticated {
			t.Fatalf("missing identity: %v", err)
		}
	}
	backendErr := errors.New("database unavailable")
	err := authorization.Enforce(ctx, principal{id: "user-1", role: authorization.RoleAdmin}, failingPolicy{backendErr})
	if !errors.Is(err, backendErr) || connect.CodeOf(api.ToConnectError(ctx, err)) != connect.CodeInternal {
		t.Fatalf("policy error: %v", err)
	}
	if err := authorization.Enforce(ctx, principal{id: "user-1", role: authorization.RoleAdmin}, nil); connect.CodeOf(api.ToConnectError(ctx, err)) != connect.CodeInternal {
		t.Fatalf("missing policy: %v", err)
	}
}
