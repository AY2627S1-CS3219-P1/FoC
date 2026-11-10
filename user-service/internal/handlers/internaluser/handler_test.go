package internaluser

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"connectrpc.com/connect"
	"connectrpc.com/validate"
	"github.com/google/uuid"

	"github.com/AY2627S1-CS3219-P1/FoC/pkg/api/errs"
	userv1 "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/user/v1"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/user/v1/userv1connect"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/models"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/review"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/warning"
)

type usersStub struct{ users []models.User }

func (s usersStub) ListByIDs(context.Context, []uuid.UUID) ([]models.User, error) {
	return s.users, nil
}

type reviewsStub struct {
	facts review.Facts
	err   error
}

func (s reviewsStub) ReviewFacts(context.Context, uuid.UUID) (review.Facts, error) {
	return s.facts, s.err
}

type warningsStub struct {
	facts warning.Facts
	err   error
}

func (s warningsStub) Facts(context.Context, uuid.UUID) (warning.Facts, error) { return s.facts, s.err }

func client(t *testing.T, h *Handler) userv1connect.UserInternalServiceClient {
	t.Helper()
	path, handler := userv1connect.NewUserInternalServiceHandler(h, connect.WithInterceptors(validate.NewInterceptor()))
	mux := http.NewServeMux()
	mux.Handle(path, handler)
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return userv1connect.NewUserInternalServiceClient(server.Client(), server.URL)
}

func TestGetUsersNamesRolesAsTokensDo(t *testing.T) {
	suspended := models.User{ID: uuid.New(), DisplayName: "Sam", Role: models.RoleSuspended}
	admin := models.User{ID: uuid.New(), DisplayName: "Ada", Role: models.RoleAdmin}
	c := client(t, &Handler{Users: usersStub{users: []models.User{suspended, admin}}})

	resp, err := c.GetUsers(context.Background(), connect.NewRequest(&userv1.GetUsersRequest{
		Ids: []string{suspended.ID.String(), admin.ID.String(), uuid.NewString()},
	}))
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, user := range resp.Msg.GetUsers() {
		got[user.GetDisplayName()] = user.GetRole()
	}
	if got["Sam"] != "suspended_user" || got["Ada"] != "admin" || len(got) != 2 {
		t.Fatalf("users = %v", got)
	}
}

func TestGetReviewFactsReportsTheRemovingReport(t *testing.T) {
	reportID := uuid.New()
	c := client(t, &Handler{Reviews: reviewsStub{facts: review.Facts{
		ReviewerID: uuid.New(), RevieweeID: uuid.New(), OrderID: uuid.New(), Removed: true, RemovalReportID: reportID,
	}}})

	resp, err := c.GetReviewFacts(context.Background(), connect.NewRequest(&userv1.GetReviewFactsRequest{
		ReviewId: uuid.NewString(),
	}))
	if err != nil || !resp.Msg.GetRemoved() || resp.Msg.GetRemovalReportId() != reportID.String() {
		t.Fatalf("facts = %+v, %v", resp, err)
	}

	missing := client(t, &Handler{Reviews: reviewsStub{err: errs.NewNotFoundError("review not found")}})
	_, err = missing.GetReviewFacts(context.Background(), connect.NewRequest(&userv1.GetReviewFactsRequest{
		ReviewId: uuid.NewString(),
	}))
	if connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatalf("missing review = %v, want NotFound", err)
	}
}

func TestGetWarningFacts(t *testing.T) {
	c := client(t, &Handler{Warnings: warningsStub{facts: warning.Facts{
		UserID: uuid.New(), RequestID: uuid.New(), Active: true,
	}}})
	resp, err := c.GetWarningFacts(context.Background(), connect.NewRequest(&userv1.GetWarningFactsRequest{
		WarningId: uuid.NewString(),
	}))
	if err != nil || !resp.Msg.GetActive() || resp.Msg.ReportId != nil {
		t.Fatalf("facts = %+v, %v", resp, err)
	}

	for name, tc := range map[string]struct {
		err  error
		code connect.Code
	}{
		"missing": {warning.ErrNotFound, connect.CodeNotFound},
		"failure": {errors.New("down"), connect.CodeInternal},
	} {
		t.Run(name, func(t *testing.T) {
			c := client(t, &Handler{Warnings: warningsStub{err: tc.err}})
			_, err := c.GetWarningFacts(context.Background(), connect.NewRequest(&userv1.GetWarningFactsRequest{
				WarningId: uuid.NewString(),
			}))
			if connect.CodeOf(err) != tc.code {
				t.Fatalf("error = %v, want %v", err, tc.code)
			}
		})
	}
}
