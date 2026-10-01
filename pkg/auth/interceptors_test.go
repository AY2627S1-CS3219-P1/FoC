package auth_test

import (
	"context"
	"testing"

	"connectrpc.com/connect"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/auth"
)

func TestCallerInterceptors(t *testing.T) {
	for _, tc := range []struct {
		name        string
		interceptor connect.Interceptor
		caller      *auth.Caller
		want        connect.Code
		called      bool
	}{
		{"missing caller", auth.RequireCaller(), nil, connect.CodeUnauthenticated, false},
		{"missing admin", auth.RequireAdmin(), nil, connect.CodeUnauthenticated, false},
		{"blank caller", auth.RequireCaller(), &auth.Caller{Admin: true}, connect.CodeUnauthenticated, false},
		{"ordinary caller", auth.RequireCaller(), &auth.Caller{ID: "user"}, 0, true},
		{"ordinary admin request", auth.RequireAdmin(), &auth.Caller{ID: "user"}, connect.CodePermissionDenied, false},
		{"administrator", auth.RequireAdmin(), &auth.Caller{ID: "admin", Admin: true}, 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			if tc.caller != nil {
				ctx = auth.WithCaller(ctx, *tc.caller)
			}
			called := false
			next := tc.interceptor.WrapUnary(func(context.Context, connect.AnyRequest) (connect.AnyResponse, error) {
				called = true
				return nil, nil
			})
			_, err := next(ctx, nil)
			if (tc.want == 0 && err != nil) || (tc.want != 0 && connect.CodeOf(err) != tc.want) || called != tc.called {
				t.Fatalf("code=%v called=%v, want code=%v called=%v", connect.CodeOf(err), called, tc.want, tc.called)
			}
		})
	}
}
