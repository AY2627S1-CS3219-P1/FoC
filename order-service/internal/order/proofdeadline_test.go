package order

import (
	"testing"
	"time"
)

// Q9 requires the saved confirmation time itself to precede the deadline.
func TestProofConfirmationUsesTheCheckedTimestamp(t *testing.T) {
	s, r, d, _ := proofScenario()
	row := uploadedProof(t, s, d)
	deadline := r.order.PickedUpAt.Add(2 * r.order.DeliveryDuration)
	tick := 0
	s.now = func() time.Time { tick++; return deadline.Add(time.Duration(tick-3) * time.Microsecond) }
	result, err := s.ConfirmDelivery(t.Context(), Caller{ID: proofCourier}, proofConfirmationInput(row))
	if err != nil {
		t.Fatal(err)
	}
	if result.DeliveredAt == nil || !result.DeliveredAt.Before(deadline) {
		t.Fatal("confirmation recorded at or after deadline")
	}
}
