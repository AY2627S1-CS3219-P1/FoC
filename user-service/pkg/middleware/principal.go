package middleware

import (
	"context"

	"github.com/AY2627S1-CS3219-P1/FoC/pkg/api/errs"
	"github.com/google/uuid"
)

// AccessPrincipalFromContext resolves the verified access claims for a request.
func AccessPrincipalFromContext(ctx context.Context) (AccessClaims, error) {
	claims, ok := ClaimsFromContext[AccessClaims](ctx)
	if !ok {
		return AccessClaims{}, errs.NewUnauthorizedError("invalid or missing access token")
	}
	actorID, err := uuid.Parse(claims.Subject)
	if err != nil || actorID == uuid.Nil {
		return AccessClaims{}, errs.NewUnauthorizedError("invalid or missing access token")
	}
	return claims, nil
}
