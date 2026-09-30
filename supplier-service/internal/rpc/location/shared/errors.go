package shared

import (
	"context"
	"errors"
	"log/slog"

	"connectrpc.com/connect"
	app "github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/location/lifecycle"
)

func ConnectError(ctx context.Context, err error) error {
	if err == nil {
		return nil
	}
	// Preserve generated unimplemented errors.
	if connect.CodeOf(err) == connect.CodeUnimplemented {
		return connect.NewError(connect.CodeUnimplemented, errors.New("method not implemented"))
	}
	if errors.Is(err, context.Canceled) {
		return connect.NewError(connect.CodeCanceled, context.Canceled)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return connect.NewError(connect.CodeDeadlineExceeded, context.DeadlineExceeded)
	}
	code := connect.CodeInternal
	message := "internal error"
	for _, entry := range []struct {
		err  error
		code connect.Code
	}{{app.ErrInvalidArgument, connect.CodeInvalidArgument}, {app.ErrUnauthenticated, connect.CodeUnauthenticated}, {app.ErrPermissionDenied, connect.CodePermissionDenied}, {app.ErrNotFound, connect.CodeNotFound}, {app.ErrFailedPrecondition, connect.CodeFailedPrecondition}, {app.ErrAlreadyExists, connect.CodeAlreadyExists}, {app.ErrAborted, connect.CodeAborted}} {
		if errors.Is(err, entry.err) {
			code = entry.code
			message = entry.err.Error()
			break
		}
	}
	if code == connect.CodeInternal {
		slog.ErrorContext(ctx, "Supplier workflow failed", "error", err)
	}
	return connect.NewError(code, errors.New(message))
}
