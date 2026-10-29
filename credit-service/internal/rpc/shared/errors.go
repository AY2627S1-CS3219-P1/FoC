// Package shared converts between Credit's domain and its credit.v1 RPC
// contract for every Connect handler.
package shared

import (
	"context"
	"errors"
	"log/slog"

	"connectrpc.com/connect"
	"github.com/AY2627S1-CS3219-P1/FoC/credit-service/internal/account"
	"github.com/AY2627S1-CS3219-P1/FoC/credit-service/internal/reservation"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
)

// ErrorDomain is the google.rpc.ErrorInfo domain of every Credit reason.
const ErrorDomain = "credit.v1"

// errorMappings lists the domain errors a caller may see. A reason is the
// contract's ErrorInfo reason; callers branch on it, not on the message.
var errorMappings = []struct {
	err    error
	code   connect.Code
	reason string
}{
	{context.Canceled, connect.CodeCanceled, ""},
	{context.DeadlineExceeded, connect.CodeDeadlineExceeded, ""},
	{reservation.ErrInsufficientCredits, connect.CodeFailedPrecondition, "INSUFFICIENT_CREDITS"},
	{reservation.ErrIdempotencyConflict, connect.CodeAlreadyExists, "IDEMPOTENCY_CONFLICT"},
	{reservation.ErrReservationNotFound, connect.CodeNotFound, "RESERVATION_NOT_FOUND"},
	{reservation.ErrReservationReleased, connect.CodeFailedPrecondition, "RESERVATION_RELEASED"},
	{reservation.ErrReservationSettled, connect.CodeFailedPrecondition, "RESERVATION_SETTLED"},
	{reservation.ErrAdjustmentPending, connect.CodeFailedPrecondition, "ADJUSTMENT_PENDING"},
	{reservation.ErrAdjustmentReverted, connect.CodeFailedPrecondition, "ADJUSTMENT_REVERTED"},
	{account.ErrAccountNotFound, connect.CodeNotFound, "ACCOUNT_NOT_FOUND"},
	{reservation.ErrInvalidArgument, connect.CodeInvalidArgument, ""},
	{account.ErrInvalidArgument, connect.CodeInvalidArgument, ""},
	// A deadlock victim changed nothing; repeating the call is safe.
	{reservation.ErrAborted, connect.CodeAborted, ""},
}

// ConnectError exposes domain errors with their contract reason and hides
// everything else behind a logged internal error.
func ConnectError(ctx context.Context, err error) error {
	if err == nil {
		return nil
	}

	for _, m := range errorMappings {
		if !errors.Is(err, m.err) {
			continue
		}
		connectErr := connect.NewError(m.code, m.err)
		if m.reason == "" {
			return connectErr
		}
		detail, detailErr := connect.NewErrorDetail(&errdetails.ErrorInfo{Domain: ErrorDomain, Reason: m.reason})
		if detailErr != nil {
			slog.ErrorContext(ctx, "Encode Credit error detail", "error", detailErr)
			return connectErr
		}
		connectErr.AddDetail(detail)
		return connectErr
	}

	slog.ErrorContext(ctx, "Credit operation failed", "error", err)
	return connect.NewError(connect.CodeInternal, errors.New("internal error"))
}
