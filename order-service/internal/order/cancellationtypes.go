package order

import (
	"context"
	"time"
)

const CancelOrderMethod = "/order.v1.OrderService/CancelOrder"
const BeginWithdrawal Action = "begin_withdrawal"

type CancellationStatus string

const (
	CancellationPending    CancellationStatus = "pending"
	CancellationSucceeded  CancellationStatus = "succeeded"
	CancellationSuperseded CancellationStatus = "superseded"
)

// Cancellation retains immutable assignment identity independently of later assignments.
type Cancellation struct {
	ID, OrderID, CallerID, Reason                   string
	Status                                          CancellationStatus
	AssignmentID, CourierID                         string
	AssignedAt                                      *time.Time
	OriginalAmount                                  int64
	Generation                                      int64
	Attempts                                        int32
	LeaseUntil, NextAttemptAt, CreatedAt, UpdatedAt time.Time
}

type CancellationResult struct {
	Order       Order
	OperationID string
	Status      CancellationStatus
}

type CancellationCredit interface {
	Revert(context.Context, string, string) error
	AssignmentReservation(context.Context, string) (AssignmentReservation, error)
}

type CancellationTx interface {
	AssignmentTx
	FindCancellation(context.Context, string) (Cancellation, error)
	InsertCancellation(context.Context, Cancellation) error
	SaveCancellation(context.Context, Cancellation, int64) error
	CurrentSuccessfulAssignment(context.Context, string) (*AssignmentAttempt, error)
}

type CancellationRepository interface {
	WithinCancellation(context.Context, func(CancellationTx) error) error
	CancellationOrderID(context.Context, string) (string, error)
	DueCancellations(context.Context, time.Time, int32) ([]string, error)
}
