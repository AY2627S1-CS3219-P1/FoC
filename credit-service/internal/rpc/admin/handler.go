// Package admin serves credit.v1.CreditAdminService to administrators.
package admin

import (
	"context"

	"connectrpc.com/connect"
	"github.com/AY2627S1-CS3219-P1/FoC/credit-service/internal/account"
	"github.com/AY2627S1-CS3219-P1/FoC/credit-service/internal/rpc/shared"
	creditv1 "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/credit/v1"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/credit/v1/creditv1connect"
)

// Totals reads credit totals across all accounts.
type Totals interface {
	Totals(ctx context.Context) (account.Totals, error)
}

// Server relies on the router's admin interceptor for authorization.
type Server struct {
	creditv1connect.UnimplementedCreditAdminServiceHandler
	Totals Totals
}

func (s *Server) GetCreditTotals(
	ctx context.Context,
	_ *connect.Request[creditv1.GetCreditTotalsRequest],
) (*connect.Response[creditv1.GetCreditTotalsResponse], error) {
	t, err := s.Totals.Totals(ctx)
	if err != nil {
		return nil, shared.ConnectError(ctx, err)
	}

	return connect.NewResponse(&creditv1.GetCreditTotalsResponse{
		Reserved:    t.Reserved,
		Returned:    t.Returned,
		Transferred: t.Transferred,
	}), nil
}
