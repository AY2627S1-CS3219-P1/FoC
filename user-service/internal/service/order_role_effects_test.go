package service

import (
	"context"
	"testing"

	"connectrpc.com/connect"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/auth"
	pb "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/order/v1"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/models"
	"github.com/google/uuid"
)

type roleClientFunc func(context.Context, *connect.Request[pb.ApplyAccountSuspensionRequest]) (*connect.Response[pb.ApplyAccountSuspensionResponse], error)

func (f roleClientFunc) ApplyAccountSuspension(ctx context.Context, req *connect.Request[pb.ApplyAccountSuspensionRequest]) (*connect.Response[pb.ApplyAccountSuspensionResponse], error) {
	return f(ctx, req)
}

func TestOrderRoleEffectsRetainsDecisionAndNeverForwardsAdminBearer(t *testing.T) {
	d := models.RoleDispatch{ID: uuid.New(), UserID: uuid.New(), AdminID: uuid.New(), Revision: 7, ToRole: models.RoleSuspended, Reason: "explicit reason"}
	calls := 0
	effects := &OrderRoleEffects{Client: roleClientFunc(func(_ context.Context, req *connect.Request[pb.ApplyAccountSuspensionRequest]) (*connect.Response[pb.ApplyAccountSuspensionResponse], error) {
		calls++
		m := req.Msg
		if m.DecisionId != d.ID.String() || m.UserId != d.UserID.String() || m.AdminId != d.AdminID.String() || m.Revision != 7 || !m.Suspended || m.Reason != d.Reason || req.Header().Get("Authorization") != "" {
			t.Fatalf("changed decision or leaked human credential: %v", m)
		}
		if calls == 1 {
			return nil, connect.NewError(connect.CodeUnavailable, nil)
		}
		if calls == 2 {
			return connect.NewResponse(&pb.ApplyAccountSuspensionResponse{DecisionId: uuid.NewString()}), nil
		}
		return connect.NewResponse(&pb.ApplyAccountSuspensionResponse{DecisionId: d.ID.String()}), nil
	})}
	ctx := auth.WithUserBearer(context.Background(), "Bearer expired-admin-session")
	if err := effects.ApplyRoleDecision(ctx, d); err == nil {
		t.Fatal("lost reply accepted")
	}
	if err := effects.ApplyRoleDecision(ctx, d); err == nil {
		t.Fatal("wrong decision acknowledgement accepted")
	}
	if err := effects.ApplyRoleDecision(ctx, d); err != nil {
		t.Fatal(err)
	}
	if err := (&OrderRoleEffects{}).ApplyRoleDecision(ctx, d); err != ErrRoleEffectsUnconfigured {
		t.Fatal("nil client enabled dispatch")
	}
}
