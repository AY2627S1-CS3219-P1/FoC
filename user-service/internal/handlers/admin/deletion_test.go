package admin

import (
	"context"
	"testing"

	"connectrpc.com/connect"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/api/errs"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/auth"
	userv1 "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/user/v1"
	"github.com/google/uuid"
)

type deletionLogicFunc func(context.Context, uuid.UUID, uuid.UUID) error

func (f deletionLogicFunc) DeleteUser(ctx context.Context, actor, target uuid.UUID) error {
	return f(ctx, actor, target)
}

func TestDeleteUserUsesVerifiedActorAndReturnsFirstBlocker(t *testing.T) {
	actor, target := uuid.New(), uuid.New()
	ctx := auth.WithCaller(context.Background(), auth.Caller{ID: actor.String(), Role: "admin", Admin: true})
	called := false
	h := &Handler{Deletion: deletionLogicFunc(func(_ context.Context, gotActor, gotTarget uuid.UUID) error {
		called = true
		if gotActor != actor || gotTarget != target {
			t.Fatal("wrong deletion identities")
		}
		return errs.NewFailedPreconditionError("Order Service: Account has an active delivery.")
	})}
	_, err := h.DeleteUser(ctx, connect.NewRequest(&userv1.DeleteUserRequest{UserId: target.String()}))
	if !called || connect.CodeOf(err) != connect.CodeFailedPrecondition || err == nil {
		t.Fatalf("lost the deletion blocker: called=%v error=%v", called, err)
	}
	h.Deletion = deletionLogicFunc(func(context.Context, uuid.UUID, uuid.UUID) error { return nil })
	response, err := h.DeleteUser(ctx, connect.NewRequest(&userv1.DeleteUserRequest{UserId: target.String()}))
	if err != nil || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("successful deletion response: %+v %v", response, err)
	}
}

func TestDeleteUserRejectsMissingActorAndMalformedTarget(t *testing.T) {
	h := &Handler{Deletion: deletionLogicFunc(func(context.Context, uuid.UUID, uuid.UUID) error {
		t.Fatal("invalid request reached deletion logic")
		return nil
	})}
	_, err := h.DeleteUser(context.Background(), connect.NewRequest(&userv1.DeleteUserRequest{UserId: uuid.NewString()}))
	if connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("missing actor: %v", err)
	}
	ctx := auth.WithCaller(context.Background(), auth.Caller{ID: uuid.NewString(), Role: "admin", Admin: true})
	_, err = h.DeleteUser(ctx, connect.NewRequest(&userv1.DeleteUserRequest{UserId: "invalid"}))
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("invalid target: %v", err)
	}
}
