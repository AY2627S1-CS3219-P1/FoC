package profile

import (
	"context"
	"errors"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/auth"
	userv1 "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/user/v1"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/models"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/service"
	"github.com/google/uuid"
)

type fakeLogic struct {
	status service.AccountDeletionStatus
	err    error
	gotID  uuid.UUID
}

func (f *fakeLogic) GetMyProfile(context.Context, uuid.UUID) (models.User, error) {
	return models.User{}, nil
}

func (f *fakeLogic) UpdateMyProfile(context.Context, uuid.UUID, service.ProfileInput) (models.User, error) {
	return models.User{}, nil
}

func (f *fakeLogic) GetMyAccountDeletionStatus(
	_ context.Context,
	actorID uuid.UUID,
) (service.AccountDeletionStatus, error) {
	f.gotID = actorID
	return f.status, f.err
}

func TestGetMyAccountDeletionStatusUsesCallerIdentity(t *testing.T) {
	actorID := uuid.New()
	requested := time.Date(2026, 11, 8, 8, 0, 0, 0, time.UTC)
	checked := requested.Add(time.Hour)
	logic := &fakeLogic{status: service.AccountDeletionStatus{
		IsDeleting: true, RequestedAt: &requested, LastCheckedAt: &checked,
		WaitingReasons: []string{"order_outstanding"},
	}}
	handler := &Handler{Logic: logic}
	ctx := auth.WithCaller(context.Background(), auth.Caller{ID: actorID.String()})

	response, err := handler.GetMyAccountDeletionStatus(
		ctx,
		connect.NewRequest(&userv1.GetMyAccountDeletionStatusRequest{}),
	)
	if err != nil {
		t.Fatal(err)
	}
	if logic.gotID != actorID {
		t.Fatalf("handler queried %v, want caller %v", logic.gotID, actorID)
	}
	if !response.Msg.IsDeleting || len(response.Msg.WaitingReasons) != 1 ||
		response.Msg.WaitingReasons[0] != "order_outstanding" {
		t.Fatalf("status response = %+v", response.Msg)
	}
	if !response.Msg.RequestedAt.AsTime().Equal(requested) || !response.Msg.LastCheckedAt.AsTime().Equal(checked) {
		t.Fatalf("timestamps = %v, %v", response.Msg.RequestedAt, response.Msg.LastCheckedAt)
	}
	if response.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("status response is cacheable")
	}
}

func TestGetMyAccountDeletionStatusRejectsMissingCaller(t *testing.T) {
	handler := &Handler{Logic: &fakeLogic{}}
	_, err := handler.GetMyAccountDeletionStatus(
		context.Background(),
		connect.NewRequest(&userv1.GetMyAccountDeletionStatusRequest{}),
	)
	var connectErr *connect.Error
	if !errors.As(err, &connectErr) || connectErr.Code() != connect.CodeUnauthenticated {
		t.Fatalf("missing caller error = %v", err)
	}
}
