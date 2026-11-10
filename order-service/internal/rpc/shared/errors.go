package shared

import (
	"context"
	"errors"
	"log/slog"

	"connectrpc.com/connect"
	app "github.com/AY2627S1-CS3219-P1/FoC/order-service/internal/order"
	commonv1 "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/common/v1"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
)

// Photoless confirmations get their own reason so clients can render the
// curated message instead of the generic precondition guidance.
const (
	reasonProofRequired  = "PROOF_REQUIRED"
	proofRequiredMessage = "Add a delivery proof photo before confirming. If the upload failed, retry the upload or use the delivery-outage option."
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
	if errors.Is(err, app.ErrProofRequired) {
		return proofRequiredError(ctx)
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
		{app.ErrProofImageTooLarge, connect.CodeInvalidArgument},
		{app.ErrPrecondition, connect.CodeFailedPrecondition},
		{app.ErrConflict, connect.CodeAborted},
		{app.ErrAlreadyExists, connect.CodeAlreadyExists},
		{app.ErrUnavailable, connect.CodeUnavailable},
		{app.ErrRateLimited, connect.CodeResourceExhausted},
	} {
		if errors.Is(err, entry.err) {
			return connect.NewError(entry.code, entry.err)
		}
	}
	slog.ErrorContext(ctx, "Order operation failed", "error", err)
	return connect.NewError(connect.CodeInternal, errors.New("internal error"))
}

// proofRequiredError raises a rejected photoless confirmation as
// FailedPrecondition with the PROOF_REQUIRED reason both in message and in
// the common.v1.ErrorDetail clients decode, keeping the cause in logs.
func proofRequiredError(ctx context.Context) error {
	connectErr := connect.NewError(connect.CodeFailedPrecondition, errors.New(proofRequiredMessage))
	if detail, detailErr := connect.NewErrorDetail(
		&errdetails.ErrorInfo{Domain: "order.v1", Reason: reasonProofRequired},
	); detailErr == nil {
		connectErr.AddDetail(detail)
	} else {
		slog.ErrorContext(ctx, "encode order error info", "error", detailErr)
	}
	if detail, detailErr := connect.NewErrorDetail(&commonv1.ErrorDetail{
		Reason:  reasonProofRequired,
		Message: proofRequiredMessage,
	}); detailErr == nil {
		connectErr.AddDetail(detail)
	} else {
		slog.ErrorContext(ctx, "encode order error detail", "error", detailErr)
	}
	return connectErr
}
