package warning

import (
	"context"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/auth"
	paginationv1 "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/pagination/v1"
	userv1 "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/user/v1"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/pagination"
	"github.com/google/uuid"
)

type handlerLogicStub struct {
	listUserID uuid.UUID
	listPage   pagination.Pagination
	listResult WarningPage
	removeID   uuid.UUID
	removeText *string
	appealID   *uuid.UUID
	actorID    uuid.UUID
}

func (s *handlerLogicStub) ListWarnings(
	_ context.Context,
	userID uuid.UUID,
	page pagination.Pagination,
) (WarningPage, error) {
	s.listUserID = userID
	s.listPage = page
	return s.listResult, nil
}

func (s *handlerLogicStub) MarkWarningAsRemoved(
	_ context.Context,
	warningID uuid.UUID,
	removedReason *string,
	appealID *uuid.UUID,
	actorID uuid.UUID,
) error {
	s.removeID = warningID
	s.removeText = removedReason
	s.appealID = appealID
	s.actorID = actorID
	return nil
}

func TestWarningListHandlersReturnPagination(t *testing.T) {
	actorID, targetID, warningID := uuid.New(), uuid.New(), uuid.New()
	createdAt := time.Date(2026, time.November, 1, 12, 0, 0, 0, time.UTC)
	logic := &handlerLogicStub{listResult: WarningPage{
		Warnings: []Warning{{
			ID: warningID, RequestID: uuid.New(), Reason: "test warning", Status: StatusActive, CreatedAt: createdAt,
		}},
		PageInfo: pagination.NewPageInfo(2, 10, 21),
	}}
	handler := &Handler{Logic: logic}
	ctx := auth.WithCaller(context.Background(), auth.Caller{ID: actorID.String()})

	myResponse, err := handler.ListMyWarnings(ctx, connect.NewRequest(&userv1.ListMyWarningsRequest{
		Pagination: &paginationv1.Pagination{Page: 2, PageSize: 10},
	}))
	if err != nil {
		t.Fatal(err)
	}
	if logic.listUserID != actorID || logic.listPage.Offset() != 10 || logic.listPage.PageSize() != 10 ||
		myResponse.Msg.GetPagination().GetTotalItems() != 21 || myResponse.Msg.GetPagination().GetTotalPages() != 3 ||
		len(myResponse.Msg.GetWarnings()) != 1 || myResponse.Msg.GetWarnings()[0].GetId() != warningID.String() {
		t.Fatalf("my warnings response=%+v user=%s page=%+v", myResponse.Msg, logic.listUserID, logic.listPage)
	}

	userResponse, err := handler.ListWarningsForUser(ctx, connect.NewRequest(&userv1.ListWarningsForUserRequest{
		UserId: targetID.String(), Pagination: &paginationv1.Pagination{Page: 2, PageSize: 10},
	}))
	if err != nil {
		t.Fatal(err)
	}
	if logic.listUserID != targetID || userResponse.Msg.GetPagination().GetPage() != 2 {
		t.Fatalf("user warning response=%+v user=%s", userResponse.Msg, logic.listUserID)
	}
}

func TestMarkWarningAsRemovedHandlerPassesOptionalMetadataAndActor(t *testing.T) {
	actorID, warningID, appealID := uuid.New(), uuid.New(), uuid.New()
	reason, appealText := "appeal overturned", appealID.String()
	logic := &handlerLogicStub{}
	handler := &Handler{Logic: logic}
	ctx := auth.WithCaller(context.Background(), auth.Caller{ID: actorID.String()})

	_, err := handler.MarkWarningAsRemoved(ctx, connect.NewRequest(&userv1.MarkWarningAsRemovedRequest{
		Id: warningID.String(), RemovedReason: &reason, AppealId: &appealText,
	}))
	if err != nil {
		t.Fatal(err)
	}
	if logic.removeID != warningID || logic.actorID != actorID || logic.removeText == nil ||
		*logic.removeText != reason || logic.appealID == nil || *logic.appealID != appealID {
		t.Fatalf("remove args: warning=%s actor=%s reason=%v appeal=%v", logic.removeID, logic.actorID,
			logic.removeText, logic.appealID)
	}
}
