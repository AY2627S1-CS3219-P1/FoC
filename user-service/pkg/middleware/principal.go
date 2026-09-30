package middleware

import (
	"context"

	"github.com/AY2627S1-CS3219-P1/FoC/pkg/authorization"
)

func withAccessClaims(ctx context.Context, claims AccessClaims) context.Context {
	ctx = context.WithValue(ctx, claimsKey[AccessClaims]{}, claims)
	return authorization.WithPrincipal(ctx, claims)
}
