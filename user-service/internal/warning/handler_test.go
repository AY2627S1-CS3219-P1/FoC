package warning

import (
	"context"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/api/errs"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/auth"
	paginationv1 "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/pagination/v1"
	warningv1 "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/user/warning/v1"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/pagination"
	"github.com/google/uuid"
)

type handlerLogicStub struct {
	listUserID  uuid.UUID
	listPage    pagination.Pagination
	listResult  WarningPage
	adminResult AdminWarningPage
	removeID    uuid.UUID
	removeText  *string
	appealID    *uuid.UUID
	actorID     uuid.UUID
	removeErr   error
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

func (s *handlerLogicStub) ListWarningsForAdmin(
	_ context.Context,
	userID uuid.UUID,
	page pagination.Pagination,
) (AdminWarningPage, error) {
	s.listUserID = userID
	s.listPage = page
	return s.adminResult, nil
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
	return s.removeErr
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

	myResponse, err := handler.ListMyWarnings(ctx, connect.NewRequest(&warningv1.ListMyWarningsRequest{
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
	if got := myResponse.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("user list Cache-Control = %q, want no-store", got)
	}

	adminHandler := &AdminHandler{Logic: logic}
	sourceEventID, updaterID := uuid.New(), uuid.New()
	updaterName := "Admin"
	updatedAt := time.Date(2026, time.November, 2, 9, 0, 0, 0, time.UTC)
	removedAt := time.Date(2026, time.November, 3, 8, 0, 0, 0, time.UTC)
	removedReason, requestID := "appeal overturned", uuid.New()
	logic.adminResult = AdminWarningPage{
		Warnings: []AdminWarning{{
			Warning: Warning{
				ID: warningID, RequestID: requestID, Reason: "test warning", Status: StatusRemoved,
				SourceEventID: &sourceEventID, UpdatedBy: &updaterID,
				CreatedAt: createdAt, UpdatedAt: updatedAt,
				RemovedAt: &removedAt, RemovedReason: &removedReason,
			},
			UpdatedByName: &updaterName,
		}},
		PageInfo: pagination.NewPageInfo(2, 10, 21),
	}
	userResponse, err := adminHandler.ListWarningsForUser(ctx, connect.NewRequest(&warningv1.ListWarningsForUserRequest{
		UserId: targetID.String(), Pagination: &paginationv1.Pagination{Page: 2, PageSize: 10},
	}))
	if err != nil {
		t.Fatal(err)
	}
	if got := userResponse.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("admin list Cache-Control = %q, want no-store", got)
	}
	adminWarning := userResponse.Msg.GetWarnings()[0]
	if logic.listUserID != targetID || userResponse.Msg.GetPagination().GetPage() != 2 ||
		adminWarning.Id != warningID.String() ||
		adminWarning.RequestId != requestID.String() ||
		adminWarning.Reason != "test warning" ||
		adminWarning.Status != warningv1.WarningStatus_WARNING_STATUS_REMOVED ||
		!adminWarning.CreatedAt.AsTime().Equal(createdAt) ||
		!adminWarning.UpdatedAt.AsTime().Equal(updatedAt) ||
		!adminWarning.RemovedAt.AsTime().Equal(removedAt) ||
		adminWarning.RemovedReason == nil || *adminWarning.RemovedReason != removedReason ||
		adminWarning.SourceEventId == nil || *adminWarning.SourceEventId != sourceEventID.String() ||
		adminWarning.UpdatedBy == nil || *adminWarning.UpdatedBy != updaterID.String() ||
		adminWarning.UpdatedByName == nil || *adminWarning.UpdatedByName != updaterName {
		t.Fatalf("admin warning response=%+v user=%s", userResponse.Msg, logic.listUserID)
	}
}

func TestAdminHandlerOmitsAbsentAuditFields(t *testing.T) {
	actorID := uuid.New()
	logic := &handlerLogicStub{adminResult: AdminWarningPage{
		// A system-created courier warning: no removal, no updater, no event.
		Warnings: []AdminWarning{{Warning: Warning{
			ID: uuid.New(), RequestID: uuid.New(), Reason: "test warning",
			Status: StatusActive, CreatedAt: time.Now().UTC(),
		}}},
		PageInfo: pagination.NewPageInfo(1, 20, 1),
	}}
	adminHandler := &AdminHandler{Logic: logic}
	ctx := auth.WithCaller(context.Background(), auth.Caller{ID: actorID.String()})

	response, err := adminHandler.ListWarningsForUser(ctx, connect.NewRequest(&warningv1.ListWarningsForUserRequest{
		UserId: actorID.String(),
	}))
	if err != nil {
		t.Fatal(err)
	}
	item := response.Msg.GetWarnings()[0]
	// Pointer fields distinguish present from absent; Get* accessors cannot.
	if item.SourceEventId != nil || item.UpdatedBy != nil || item.UpdatedByName != nil ||
		item.RemovedAt != nil || item.RemovedReason != nil {
		t.Fatalf("absent audit fields must be unset on the wire: %+v", item)
	}
}

func TestMarkWarningAsRemovedHandlerMapsNotFound(t *testing.T) {
	actorID, warningID := uuid.New(), uuid.New()
	// The stub stands in for the service, which maps the repository's plain
	// ErrNotFound into the shared external NotFound error before the handler.
	adminHandler := &AdminHandler{Logic: &handlerLogicStub{
		removeErr: errs.WrapNotFoundError(ErrNotFound, "warning not found"),
	}}
	ctx := auth.WithCaller(context.Background(), auth.Caller{ID: actorID.String()})

	_, err := adminHandler.MarkWarningAsRemoved(ctx, connect.NewRequest(&warningv1.MarkWarningAsRemovedRequest{
		Id: warningID.String(),
	}))
	if connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatalf("not-found removal error = %v, want CodeNotFound", err)
	}
}

func TestMarkWarningAsRemovedHandlerPassesOptionalMetadataAndActor(t *testing.T) {
	actorID, warningID, appealID := uuid.New(), uuid.New(), uuid.New()
	reason, appealText := "appeal overturned", appealID.String()
	logic := &handlerLogicStub{}
	adminHandler := &AdminHandler{Logic: logic}
	ctx := auth.WithCaller(context.Background(), auth.Caller{ID: actorID.String()})

	_, err := adminHandler.MarkWarningAsRemoved(ctx, connect.NewRequest(&warningv1.MarkWarningAsRemovedRequest{
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
