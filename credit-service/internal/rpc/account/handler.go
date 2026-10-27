// Package account serves credit.v1.CreditAccountService to signed-in users.
package account

import (
	"context"

	"connectrpc.com/connect"
	"github.com/AY2627S1-CS3219-P1/FoC/credit-service/internal/account"
	"github.com/AY2627S1-CS3219-P1/FoC/credit-service/internal/rpc/shared"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/api"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/api/errs"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/auth"
	creditv1 "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/credit/v1"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/credit/v1/creditv1connect"
)

// Accounts lists the account reads the handler calls.
type Accounts interface {
	GetAccount(ctx context.Context, userID string) (account.Account, error)
	ListLedgerEntries(ctx context.Context, userID string, p account.Page) (account.LedgerPage, error)
}

// Server reads only the caller's own account; requests never name a user.
type Server struct {
	creditv1connect.UnimplementedCreditAccountServiceHandler
	Accounts Accounts
}

func (s *Server) GetMyAccount(ctx context.Context, _ *connect.Request[creditv1.GetMyAccountRequest]) (*connect.Response[creditv1.GetMyAccountResponse], error) {
	caller, ok := auth.CallerFromContext(ctx)
	if !ok {
		return nil, api.ToConnectError(ctx, errs.NewUnauthorizedError("unauthenticated"))
	}

	a, err := s.Accounts.GetAccount(ctx, caller.ID)
	if err != nil {
		return nil, shared.ConnectError(ctx, err)
	}
	return connect.NewResponse(&creditv1.GetMyAccountResponse{Account: shared.Account(a)}), nil
}

func (s *Server) ListMyLedgerEntries(ctx context.Context, req *connect.Request[creditv1.ListMyLedgerEntriesRequest]) (*connect.Response[creditv1.ListMyLedgerEntriesResponse], error) {
	caller, ok := auth.CallerFromContext(ctx)
	if !ok {
		return nil, api.ToConnectError(ctx, errs.NewUnauthorizedError("unauthenticated"))
	}

	page := account.Page{Number: req.Msg.GetPage(), Size: req.Msg.GetPageSize()}
	p, err := s.Accounts.ListLedgerEntries(ctx, caller.ID, page)
	if err != nil {
		return nil, shared.ConnectError(ctx, err)
	}
	return connect.NewResponse(shared.LedgerPage(p)), nil
}
