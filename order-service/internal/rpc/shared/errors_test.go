package shared

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"connectrpc.com/connect"
	app "github.com/AY2627S1-CS3219-P1/FoC/order-service/internal/order"
	commonv1 "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/common/v1"
)

// Confirming delivery without the required proof stays a definitive 400
// rejection, but the client-readable message and reason must reach callers.
func TestConnectErrorMapsProofRequiredWithClientMessage(t *testing.T) {
	for _, cause := range []error{
		app.ErrProofRequired,
		fmt.Errorf("admission refused: %w", app.ErrProofRequired),
		fmt.Errorf("wrapped: %w", fmt.Errorf("inner: %w", app.ErrProofRequired)),
	} {
		err := ConnectError(context.Background(), cause)
		var connectErr *connect.Error
		if !errors.As(err, &connectErr) || connectErr.Code() != connect.CodeFailedPrecondition ||
			connectErr.Message() != proofRequiredMessage {
			t.Fatalf("mapping: %v", err)
		}
		var detail *commonv1.ErrorDetail
		for _, encoded := range connectErr.Details() {
			if value, valueErr := encoded.Value(); valueErr == nil {
				if decoded, ok := value.(*commonv1.ErrorDetail); ok {
					detail = decoded
				}
			}
		}
		if detail == nil {
			t.Fatal("no common.v1.ErrorDetail attached")
		}
		if detail.Reason != reasonProofRequired || detail.Message != proofRequiredMessage {
			t.Fatalf("detail: %+v", detail)
		}
	}
	if err := ConnectError(context.Background(), nil); err != nil {
		t.Fatalf("nil error mapped: %v", err)
	}
}
