package shared

import (
	"context"
	"errors"

	"connectrpc.com/connect"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/api"
	domain "github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/location/shared"
)

// ReadAdminConnectError maps shared domain sentinels without changing the
// established detailed validation and unknown-error responses.
func ReadAdminConnectError(ctx context.Context, err error) error {
	switch {
	case errors.Is(err, domain.ErrNotFound):
		return connect.NewError(connect.CodeNotFound, domain.ErrNotFound)
	case errors.Is(err, domain.ErrPermissionDenied):
		return connect.NewError(connect.CodePermissionDenied, domain.ErrPermissionDenied)
	default:
		return api.ToConnectError(ctx, err)
	}
}
