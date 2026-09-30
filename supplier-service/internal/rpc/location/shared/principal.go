package shared

import (
	"context"

	"github.com/AY2627S1-CS3219-P1/FoC/pkg/api"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/api/errs"
	location "github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/location/shared"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/pkg/middleware"
)

func CallerFromContext(ctx context.Context) (location.Caller, bool) {
	claims, ok := middleware.ClaimsFromContext[middleware.AccessClaims](ctx)
	if !ok {
		return location.Caller{}, false
	}
	admin := claims.Role == "admin" || claims.Role == "super_admin"
	return location.Caller{ID: claims.Subject, Admin: admin}, true
}

func RequireCaller(ctx context.Context) (location.Caller, error) {
	caller, ok := CallerFromContext(ctx)
	if !ok {
		return location.Caller{}, api.ToConnectError(ctx, errs.NewUnauthorizedError("unauthenticated"))
	}
	return caller, nil
}
