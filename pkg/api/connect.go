package api

import (
	"context"
	"errors"
	"log/slog"

	"connectrpc.com/connect"
)

// ToConnectError converts a client-facing ExternalError to a Connect error.
// Unknown errors are logged and returned without exposing their details.
func ToConnectError(ctx context.Context, err error) error {
	if err == nil {
		err = errors.New("nil error passed to ToConnectError")
	}

	var extErr ExternalError
	if errors.As(err, &extErr) {
		slog.ErrorContext(ctx, "Error occurred", "error", extErr.ErrorTrace(), "code", extErr.Code())
		code := extErr.GetConnectCode()
		if code == connect.CodeInternal {
			return connect.NewError(code, errors.New(MsgInternalError))
		}
		return connect.NewError(code, extErr)
	}

	slog.ErrorContext(ctx, "Error occurred", "error", err)
	return connect.NewError(connect.CodeInternal, errors.New(MsgInternalError))
}
