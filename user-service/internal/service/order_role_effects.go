package service

import (
	"context"

	"connectrpc.com/connect"
	pb "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/order/v1"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/models"
)

type OrderRoleClient interface {
	ApplyAccountSuspension(context.Context, *connect.Request[pb.ApplyAccountSuspensionRequest]) (*connect.Response[pb.ApplyAccountSuspensionResponse], error)
}

// OrderRoleEffects consumes a client constructed with the authenticated #29
// transport. It owns no credential, reads no admin bearer and has no fallback
// client. The composition root deliberately leaves this absent in production
// until the separate trusted-caller dependency is available.
type OrderRoleEffects struct{ Client OrderRoleClient }

func (s *OrderRoleEffects) ApplyRoleDecision(ctx context.Context, d models.RoleDispatch) error {
	if s.Client == nil {
		return ErrRoleEffectsUnconfigured
	}
	response, err := s.Client.ApplyAccountSuspension(ctx, connect.NewRequest(&pb.ApplyAccountSuspensionRequest{
		DecisionId: d.ID.String(),
		UserId:     d.UserID.String(),
		AdminId:    d.AdminID.String(),
		Revision:   d.Revision,
		Suspended:  d.ToRole == models.RoleSuspended,
		Reason:     d.Reason,
	}))
	if err != nil {
		return err
	}
	if response == nil || response.Msg == nil || response.Msg.DecisionId != d.ID.String() {
		return ErrRoleEffectsUnavailable
	}
	return nil
}
