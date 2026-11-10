package service

import (
	"context"

	"connectrpc.com/connect"
	pb "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/order/v1"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/models"
)

type OrderRoleClient interface {
	ApplyAccountSuspension(
		context.Context,
		*connect.Request[pb.ApplyAccountSuspensionRequest],
	) (*connect.Response[pb.ApplyAccountSuspensionResponse], error)
}

// OrderRoleEffects uses the private Order origin. Istio authenticates User's
// workload and authorizes only User at the suspension method boundary. No admin
// bearer or credential is persisted or forwarded, including during recovery.
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
