package users

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	"github.com/AY2627S1-CS3219-P1/FoC/pkg/auth/tokenclaims"
	userv1 "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/user/v1"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/user/v1/userv1connect"
)

type stubUserService struct {
	userv1connect.UnimplementedUserInternalServiceHandler

	known     map[string]*userv1.InternalUser
	batches   int
	reviewErr error
}

func (s *stubUserService) GetUsers(
	_ context.Context,
	req *connect.Request[userv1.GetUsersRequest],
) (*connect.Response[userv1.GetUsersResponse], error) {
	s.batches++
	out := &userv1.GetUsersResponse{}
	for _, id := range req.Msg.GetIds() {
		if user, ok := s.known[id]; ok {
			out.Users = append(out.Users, user)
		}
	}
	return connect.NewResponse(out), nil
}

func (s *stubUserService) GetReviewFacts(
	_ context.Context,
	req *connect.Request[userv1.GetReviewFactsRequest],
) (*connect.Response[userv1.GetReviewFactsResponse], error) {
	if s.reviewErr != nil {
		return nil, s.reviewErr
	}
	reportID := uuid.NewString()
	return connect.NewResponse(&userv1.GetReviewFactsResponse{
		ReviewerId: uuid.NewString(), RevieweeId: uuid.NewString(), OrderId: uuid.NewString(),
		Removed: true, RemovalReportId: &reportID,
	}), nil
}

func newClient(t *testing.T, stub *stubUserService) *Client {
	t.Helper()
	path, handler := userv1connect.NewUserInternalServiceHandler(stub)
	mux := http.NewServeMux()
	mux.Handle(path, handler)
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return NewClient(userv1connect.NewUserInternalServiceClient(server.Client(), server.URL))
}

func TestPeopleBatchesAndSkipsUnknownUsers(t *testing.T) {
	known := map[string]*userv1.InternalUser{}
	var ids []uuid.UUID
	for range maxBatch + 5 {
		id := uuid.New()
		ids = append(ids, id)
		known[id.String()] = &userv1.InternalUser{Id: id.String(), DisplayName: "U", Role: "suspended_user"}
	}
	unknown := uuid.New()
	stub := &stubUserService{known: known}

	people, err := newClient(t, stub).People(context.Background(), append(ids, unknown, ids[0], uuid.Nil))
	if err != nil {
		t.Fatal(err)
	}
	if len(people) != maxBatch+5 || stub.batches != 2 {
		t.Fatalf("people = %d in %d batches", len(people), stub.batches)
	}
	if _, ok := people[unknown]; ok || !people[ids[0]].IsSuspended() ||
		people[ids[0]].Role != tokenclaims.RoleSuspendedUser {
		t.Fatalf("person = %+v", people[ids[0]])
	}
}

func TestReviewFactsDistinguishesMissingFromUnavailable(t *testing.T) {
	facts, err := newClient(t, &stubUserService{}).ReviewFacts(context.Background(), uuid.New())
	if err != nil || !facts.Removed || facts.RemovalReportID == uuid.Nil {
		t.Fatalf("facts = %+v, %v", facts, err)
	}

	missing := &stubUserService{reviewErr: connect.NewError(connect.CodeNotFound, errors.New("review not found"))}
	if _, err := newClient(t, missing).ReviewFacts(context.Background(), uuid.New()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing review = %v, want ErrNotFound", err)
	}

	down := &stubUserService{reviewErr: connect.NewError(connect.CodeInternal, errors.New("boom"))}
	_, err = newClient(t, down).ReviewFacts(context.Background(), uuid.New())
	if !errors.Is(err, ErrUnavailable) || connect.CodeOf(ExternalError(err, "x")) != connect.CodeUnavailable {
		t.Fatalf("failure = %v, want ErrUnavailable", err)
	}
}

type failingSource struct{}

func (failingSource) People(context.Context, []uuid.UUID) (map[uuid.UUID]Person, error) {
	return nil, ErrUnavailable
}

func TestNamesFallsBackToNoneWhenUserServiceIsDown(t *testing.T) {
	if names := Names(context.Background(), failingSource{}, []uuid.UUID{uuid.New()}); len(names) != 0 {
		t.Fatalf("names = %v, want none", names)
	}
}
