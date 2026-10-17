package review

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"connectrpc.com/connect"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/api"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/auth"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/auth/tokenclaims"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/user/review/v1"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/user/review/v1/reviewv1connect"
)

// TestAuthorizeCaller exercises the token-claim RBAC switch, the piece that maps
// a caller's role to a procedure. It covers the suspended-submit gate and the
// admin-only removal/audit gates, plus the procedures that carry no gate.
func TestAuthorizeCaller(t *testing.T) {
	for _, tc := range []struct {
		name        string
		caller      auth.Caller
		procedure   string
		wantAllowed bool
	}{
		// SubmitReview is open to every caller except suspended participants.
		{"suspended cannot submit", auth.Caller{ID: "a", Role: string(tokenclaims.RoleSuspendedUser)},
			reviewv1connect.ReviewServiceSubmitReviewProcedure, false},
		{"user may submit", auth.Caller{ID: "a", Role: string(tokenclaims.RoleUser)},
			reviewv1connect.ReviewServiceSubmitReviewProcedure, true},
		{"admin may submit", auth.Caller{ID: "a", Role: string(tokenclaims.RoleAdmin), Admin: true},
			reviewv1connect.ReviewServiceSubmitReviewProcedure, true},
		{"super admin may submit", auth.Caller{ID: "a", Role: string(tokenclaims.RoleSuperAdmin), Admin: true},
			reviewv1connect.ReviewServiceSubmitReviewProcedure, true},

		// Listing reviews received or given carries no gate.
		{"list received open to suspended", auth.Caller{ID: "a", Role: string(tokenclaims.RoleSuspendedUser)},
			reviewv1connect.ReviewServiceListReviewsForUserProcedure, true},
		{"list received open to user", auth.Caller{ID: "a", Role: string(tokenclaims.RoleUser)},
			reviewv1connect.ReviewServiceListReviewsForUserProcedure, true},
		{"list mine open to user", auth.Caller{ID: "a", Role: string(tokenclaims.RoleUser)},
			reviewv1connect.ReviewServiceListMyReviewsProcedure, true},

		// Removals and the removal audit are admin-only.
		{"user cannot remove", auth.Caller{ID: "a", Role: string(tokenclaims.RoleUser)},
			reviewv1connect.ReviewServiceRemoveReviewProcedure, false},
		{"suspended cannot remove", auth.Caller{ID: "a", Role: string(tokenclaims.RoleSuspendedUser)},
			reviewv1connect.ReviewServiceRemoveReviewProcedure, false},
		{"admin may remove", auth.Caller{ID: "a", Role: string(tokenclaims.RoleAdmin), Admin: true},
			reviewv1connect.ReviewServiceRemoveReviewProcedure, true},
		{"super admin may remove", auth.Caller{ID: "a", Role: string(tokenclaims.RoleSuperAdmin), Admin: true},
			reviewv1connect.ReviewServiceRemoveReviewProcedure, true},
		{"user cannot list removals", auth.Caller{ID: "a", Role: string(tokenclaims.RoleUser)},
			reviewv1connect.ReviewServiceListReviewRemovalsProcedure, false},
		{"admin may list removals", auth.Caller{ID: "a", Role: string(tokenclaims.RoleAdmin), Admin: true},
			reviewv1connect.ReviewServiceListReviewRemovalsProcedure, true},
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
				t.Fatalf("expected forbidden (%d), got %v", http.StatusForbidden, err)
			}
		})
	}
}

// TestAuthorizeInterceptor verifies the interceptor's own responsibilities: it
// rejects an unauthenticated request and delegates to the next interceptor for
// an authenticated one. The role-based RBAC it applies is covered by
// TestAuthorizeCaller, since a hand-built connect request carries no procedure.
func TestAuthorizeInterceptor(t *testing.T) {
	next := connect.UnaryFunc(func(_ context.Context, _ connect.AnyRequest) (connect.AnyResponse, error) {
		return nil, nil
	})
	wrapped := AuthorizeInterceptor().WrapUnary(next)
	req := connect.NewRequest(&reviewv1.SubmitReviewRequest{})

	t.Run("denies when the caller is missing", func(t *testing.T) {
		_, err := wrapped(context.Background(), req)
		if connect.CodeOf(err) != connect.CodeUnauthenticated {
			t.Fatalf("expected unauthorized without a caller, got %v", err)
		}
	})

	t.Run("delegates for an authenticated caller", func(t *testing.T) {
		ctx := auth.WithCaller(context.Background(), auth.Caller{ID: "user", Role: string(tokenclaims.RoleUser)})
		if _, err := wrapped(ctx, req); err != nil {
			t.Fatalf("expected success, got %v", err)
		}
	})
}
