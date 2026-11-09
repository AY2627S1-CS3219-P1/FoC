package shared_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"connectrpc.com/connect"
	"github.com/AY2627S1-CS3219-P1/FoC/credit-service/internal/account"
	"github.com/AY2627S1-CS3219-P1/FoC/credit-service/internal/reservation"
	"github.com/AY2627S1-CS3219-P1/FoC/credit-service/internal/rpc/shared"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
)

func TestConnectErrorCarriesContractReasons(t *testing.T) {
	tests := []struct {
		err    error
		code   connect.Code
		reason string
	}{
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
		{fmt.Errorf("commit: %w", reservation.ErrAborted), connect.CodeAborted, ""},
		{context.DeadlineExceeded, connect.CodeDeadlineExceeded, ""},
		{errors.New("connection refused"), connect.CodeInternal, ""},
	}
	for _, tt := range tests {
		t.Run(tt.err.Error(), func(t *testing.T) {
			err := shared.ConnectError(context.Background(), tt.err)

			var connectErr *connect.Error
			if !errors.As(err, &connectErr) || connectErr.Code() != tt.code {
				t.Fatalf("code: %v, want %v", err, tt.code)
			}
			if got := reason(t, connectErr); got != tt.reason {
				t.Fatalf("reason %q, want %q", got, tt.reason)
			}
		})
	}
}

func TestConnectErrorHidesUnexpectedErrors(t *testing.T) {
	err := shared.ConnectError(context.Background(), errors.New("password=secret"))
	if connect.CodeOf(err) != connect.CodeInternal || errors.Unwrap(err).Error() != "internal error" {
		t.Fatalf("leaked: %v", err)
	}
}

func reason(t *testing.T, err *connect.Error) string {
	t.Helper()
	for _, detail := range err.Details() {
		value, decodeErr := detail.Value()
		if decodeErr != nil {
			t.Fatal(decodeErr)
		}
		if info, ok := value.(*errdetails.ErrorInfo); ok {
			if info.Domain != shared.ErrorDomain {
				t.Fatalf("domain %q", info.Domain)
			}
			return info.Reason
		}
	}
	return ""
}
