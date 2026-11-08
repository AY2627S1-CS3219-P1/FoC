package accountdeletion

import (
	"slices"
	"testing"

	creditv1 "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/credit/v1"
	orderv1 "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/order/v1"
)

// TestOrderReasonCodes pins the order drain's waiting-reason codes to the stable
// strings the status API and frontend render, dropping the unspecified zero.
func TestOrderReasonCodes(t *testing.T) {
	got := reasonCodes([]orderv1.OutstandingWorkReason{
		orderv1.OutstandingWorkReason_OUTSTANDING_WORK_REASON_NONTERMINAL_ORDER,
		orderv1.OutstandingWorkReason_OUTSTANDING_WORK_REASON_OPEN_OFFER,
		orderv1.OutstandingWorkReason_OUTSTANDING_WORK_REASON_PENDING_WORKFLOW,
		orderv1.OutstandingWorkReason_OUTSTANDING_WORK_REASON_PENDING_ADMISSION,
		orderv1.OutstandingWorkReason_OUTSTANDING_WORK_REASON_UNSPECIFIED,
	}, orderReasonCode)
	want := []string{"order_outstanding", "offer_open", "pending_workflow", "pending_admission"}
	if !slices.Equal(got, want) {
		t.Fatalf("order reason codes = %v, want %v", got, want)
	}
}

// TestCreditReasonCodes pins the credit drain's codes the same way.
func TestCreditReasonCodes(t *testing.T) {
	got := reasonCodes([]creditv1.OutstandingWorkReason{
		creditv1.OutstandingWorkReason_OUTSTANDING_WORK_REASON_RESERVED_BALANCE,
		creditv1.OutstandingWorkReason_OUTSTANDING_WORK_REASON_ACTIVE_RESERVATION,
		creditv1.OutstandingWorkReason_OUTSTANDING_WORK_REASON_UNSETTLED_ASSIGNMENT,
		creditv1.OutstandingWorkReason_OUTSTANDING_WORK_REASON_PENDING_FINANCIAL_WORK,
		creditv1.OutstandingWorkReason_OUTSTANDING_WORK_REASON_UNSPECIFIED,
	}, creditReasonCode)
	want := []string{"credit_reserved_balance", "credit_reservation", "credit_unsettled_assignment",
		"credit_pending_financial_work"}
	if !slices.Equal(got, want) {
		t.Fatalf("credit reason codes = %v, want %v", got, want)
	}
}
