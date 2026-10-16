package shared

import (
	"context"
	"errors"
	"log/slog"

	"connectrpc.com/connect"
	app "github.com/AY2627S1-CS3219-P1/FoC/order-service/internal/order"
)

// ConnectError exposes domain errors without leaking dependency diagnostics.
func ConnectError(ctx context.Context, err error) error {
	if err == nil {
		return nil
	}
	var rejection app.CreationRejection
	if errors.As(err, &rejection) {
		return connect.NewError(connect.CodeFailedPrecondition, rejection)
	}
	for _, entry := range []struct {
		err  error
		code connect.Code
	}{
		{context.Canceled, connect.CodeCanceled},
		{context.DeadlineExceeded, connect.CodeDeadlineExceeded},
		{app.ErrNotFound, connect.CodeNotFound},
		{app.ErrForbidden, connect.CodePermissionDenied},
		{app.ErrInvalid, connect.CodeInvalidArgument},
		{app.ErrPrecondition, connect.CodeFailedPrecondition},
		{app.ErrConflict, connect.CodeAborted},
		{app.ErrAlreadyExists, connect.CodeAlreadyExists},
		{app.ErrUnavailable, connect.CodeUnavailable},
		{app.ErrRateLimited, connect.CodeResourceExhausted},
		{app.ErrImagesUnavailable, connect.CodeFailedPrecondition},
	} {
		if errors.Is(err, entry.err) {
			return connect.NewError(entry.code, entry.err)
		}
	}
	slog.ErrorContext(ctx, "Order operation failed", "error", err)
	return connect.NewError(connect.CodeInternal, errors.New("internal error"))
}
