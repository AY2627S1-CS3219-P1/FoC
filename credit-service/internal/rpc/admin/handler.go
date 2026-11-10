// Package admin serves credit.v1.CreditAdminService to administrators.
package admin

import (
	"context"
	"time"

	"connectrpc.com/connect"
	"github.com/AY2627S1-CS3219-P1/FoC/credit-service/internal/account"
	"github.com/AY2627S1-CS3219-P1/FoC/credit-service/internal/rpc/shared"
	creditv1 "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/credit/v1"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/credit/v1/creditv1connect"
	"google.golang.org/genproto/googleapis/type/date"
)

// Totals reads credit totals across all accounts.
type Totals interface {
	Totals(ctx context.Context) (account.Totals, error)
}

// Volumes reads daily payments to couriers.
type Volumes interface {
	DailyVolume(ctx context.Context, start, end account.Date) ([]account.DailyVolume, error)
}

// Server relies on the router's admin interceptor for authorization.
type Server struct {
	creditv1connect.UnimplementedCreditAdminServiceHandler
	Totals  Totals
	Volumes Volumes
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

func (s *Server) GetDailyTransactionVolume(
	ctx context.Context,
	req *connect.Request[creditv1.GetDailyTransactionVolumeRequest],
) (*connect.Response[creditv1.GetDailyTransactionVolumeResponse], error) {
	start, end := accountDate(req.Msg.GetStartDate()), accountDate(req.Msg.GetEndDate())
	volumes, err := s.Volumes.DailyVolume(ctx, start, end)
	if err != nil {
		return nil, shared.ConnectError(ctx, err)
	}

	days := make([]*creditv1.DailyTransactionVolume, len(volumes))
	for i, v := range volumes {
		days[i] = &creditv1.DailyTransactionVolume{
			Date: &date.Date{
				Year:  int32(v.Date.Year),
				Month: int32(v.Date.Month),
				Day:   int32(v.Date.Day),
			},
			CreditsTransferred: v.Credits,
			PaymentCount:       v.Payments,
		}
	}
	return connect.NewResponse(&creditv1.GetDailyTransactionVolumeResponse{Days: days}), nil
}

// accountDate converts a protobuf calendar date. Validity is checked by the domain.
func accountDate(d *date.Date) account.Date {
	return account.Date{Year: int(d.GetYear()), Month: time.Month(d.GetMonth()), Day: int(d.GetDay())}
}
