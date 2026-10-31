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

type assignmentStartupRepository interface {
	order.AssignmentRepository
	HasPendingAssignments(context.Context) (bool, error)
}

// Outstanding financial intent must remain recoverable even when admissions or
// unrelated creation dependencies are unavailable. Reachability is not a local
// startup check, but recovery cannot silently lose its URL or workload identity.
func newAssigner(ctx context.Context, repository assignmentStartupRepository, c config) (*order.Assigner, bool, error) {
	pending, err := repository.HasPendingAssignments(ctx)
	if err != nil {
		return nil, false, err
	}
	if pending && c.CreditServiceURL == "" {
		return nil, false, errors.New("outstanding assignment workflows require Credit URL and token file for recovery")
	}
	var credit order.AssignmentCredit
	if c.CreditServiceURL != "" {
		if _, err := clients.ReadServiceToken(c.CreditTokenFile); err != nil {
			return nil, false, errors.New(
				"CREDIT_WORKLOAD_TOKEN_FILE must contain a readable nonempty bearer credential",
			)
		}
		client := &http.Client{Timeout: 2 * time.Second, CheckRedirect: clients.NoRedirect}
		credit = &clients.Credit{
			Client: creditv1connect.NewCreditReservationServiceClient(client, c.CreditServiceURL),
			Tokens: clients.FileTokens{Path: c.CreditTokenFile},
		}
	}
	return order.NewAssigner(repository, credit, order.AssignmentOptions{}), credit != nil, nil
}
