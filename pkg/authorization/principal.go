package authorization

import (
	"context"

	"github.com/AY2627S1-CS3219-P1/FoC/pkg/api/errs"
)

type principalKey struct{}

// WithPrincipal attaches a verified caller at the authentication boundary.
func WithPrincipal(ctx context.Context, principal Principal) context.Context {
	return context.WithValue(ctx, principalKey{}, principal)
}

// PrincipalFromContext returns the caller attached by authentication middleware.
func PrincipalFromContext(ctx context.Context) (Principal, error) {
	principal, ok := ctx.Value(principalKey{}).(Principal)
	if !ok || principal == nil {
		return nil, errs.NewUnauthorizedError("invalid or missing access token")
	}
	return principal, nil
}
