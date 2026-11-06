package warning

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"connectrpc.com/connect"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/api"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/auth"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/auth/tokenclaims"
	userv1 "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/user/v1"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/user/v1/userv1connect"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/handlers/userdto"
)

func TestAuthorizeCaller(t *testing.T) {
	for _, tc := range []struct {
		name        string
		caller      userdto.Caller
		procedure   string
		wantAllowed bool
	}{
		{
			name:      "user cannot list warnings for another user",
			caller:    userdto.Caller{Caller: auth.Caller{ID: "user", Role: string(tokenclaims.RoleUser)}},
			procedure: userv1connect.WarningServiceListWarningsForUserProcedure,
		},
		{
			name:      "user cannot remove a warning",
			caller:    userdto.Caller{Caller: auth.Caller{ID: "user", Role: string(tokenclaims.RoleUser)}},
			procedure: userv1connect.WarningServiceMarkWarningAsRemovedProcedure,
		},
		{
			name:        "admin may list warnings for a user",
			caller:      userdto.Caller{Caller: auth.Caller{ID: "admin", Role: string(tokenclaims.RoleAdmin), Admin: true}},
			procedure:   userv1connect.WarningServiceListWarningsForUserProcedure,
			wantAllowed: true,
		},
		{
			name:        "admin may remove a warning",
			caller:      userdto.Caller{Caller: auth.Caller{ID: "admin", Role: string(tokenclaims.RoleAdmin), Admin: true}},
			procedure:   userv1connect.WarningServiceMarkWarningAsRemovedProcedure,
			wantAllowed: true,
		},
		{
			name:        "super admin may remove a warning",
			caller:      userdto.Caller{Caller: auth.Caller{ID: "super-admin", Role: string(tokenclaims.RoleSuperAdmin), Admin: true}},
			procedure:   userv1connect.WarningServiceMarkWarningAsRemovedProcedure,
			wantAllowed: true,
		},
		{
			name:        "own warning list does not require admin role",
			caller:      userdto.Caller{Caller: auth.Caller{ID: "user", Role: string(tokenclaims.RoleUser)}},
			procedure:   userv1connect.WarningServiceListMyWarningsProcedure,
			wantAllowed: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := authorizeCaller(tc.caller, tc.procedure)
			if tc.wantAllowed {
				if err != nil {
					t.Fatalf("expected allowed, got %v", err)
				}
				return
			}

			var external api.ExternalError
			if !errors.As(err, &external) || external.Code() != http.StatusForbidden {
				t.Fatalf("expected forbidden, got %v", err)
			}
		})
	}
}

func TestAuthorizeInterceptorRequiresAuthenticatedCaller(t *testing.T) {
	next := connect.UnaryFunc(func(_ context.Context, _ connect.AnyRequest) (connect.AnyResponse, error) {
		return nil, nil
	})
	wrapped := AuthorizeInterceptor().WrapUnary(next)
	req := connect.NewRequest(&userv1.ListMyWarningsRequest{})
	if _, err := wrapped(context.Background(), req); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("missing caller: got %v, want unauthenticated", err)
	}

	ctx := auth.WithCaller(context.Background(), auth.Caller{ID: "user", Role: string(tokenclaims.RoleUser)})
	if _, err := wrapped(ctx, req); err != nil {
		t.Fatalf("authenticated request rejected: %v", err)
	}
}
