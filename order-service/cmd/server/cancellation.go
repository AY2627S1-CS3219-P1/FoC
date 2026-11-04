package main

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/AY2627S1-CS3219-P1/FoC/order-service/internal/clients"
	"github.com/AY2627S1-CS3219-P1/FoC/order-service/internal/order"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/credit/v1/creditv1connect"
)

type cancellationStartupRepository interface {
	order.CancellationRepository
	HasPendingCancellations(context.Context) (bool, error)
}

// Outstanding financial intent must remain recoverable even when admissions or
// unrelated creation dependencies are unavailable. Reachability is not a local
// startup check, but recovery cannot silently lose its URL or workload identity.
func newCanceller(
	ctx context.Context,
	repository cancellationStartupRepository,
	c config,
) (*order.Canceller, bool, error) {
	pending, err := repository.HasPendingCancellations(ctx)
	if err != nil {
		return nil, false, err
	}
	if pending && c.CreditServiceURL == "" {
		return nil, false, errors.New(
			"outstanding cancellation workflows require Credit URL for recovery",
		)
	}
	var credit order.CancellationCredit
	if c.CreditServiceURL != "" {
		client := &http.Client{Timeout: 2 * time.Second, CheckRedirect: clients.NoRedirect}
		credit = &clients.Credit{
			Client: creditv1connect.NewCreditReservationServiceClient(client, c.CreditServiceURL),
		}
	}
	return order.NewCanceller(repository, credit, order.AssignmentOptions{}), credit != nil, nil
}
