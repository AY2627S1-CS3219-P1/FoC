package review

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"connectrpc.com/validate"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/api/errs"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/auth"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/auth/tokenclaims"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/pagination/v1"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/user/review/v1"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/user/review/v1/reviewv1connect"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/pagination"
	"github.com/google/uuid"
)

// handlerLogicStub records the arguments the handler passes so tests can assert
// the adapter parsed the request correctly. It only implements the Logic
// methods the handler calls, matching the consumer-defined interface rule.
type handlerLogicStub struct {
	submitted ReviewSubmission
	submitter uuid.UUID
	submitErr error

	listedFor   uuid.UUID
	forUserPage ReviewPage
	forUserErr  error

	removedID     uuid.UUID
	removedReason string
	removedBy     uuid.UUID
	removeErr     error

	removals    []ReviewRemoval
	removalsErr error
}

func (s *handlerLogicStub) SubmitReview(_ context.Context, reviewerID, revieweeID, orderID uuid.UUID, submission ReviewSubmission) error {
	s.submitter = reviewerID
	s.submitted = submission
	return s.submitErr
}

func (s *handlerLogicStub) ListReviewsForUser(_ context.Context, userID uuid.UUID, page pagination.Pagination) (ReviewPage, error) {
	s.listedFor = userID
	return s.forUserPage, s.forUserErr
}

func (s *handlerLogicStub) ListReviewFromUser(_ context.Context, userID uuid.UUID, _ pagination.Pagination) (ReviewPage, error) {
	s.listedFor = userID
	return ReviewPage{}, s.forUserErr
}

func (s *handlerLogicStub) RemoveReview(_ context.Context, id uuid.UUID, reason string, createdBy uuid.UUID) error {
	s.removedID = id
	s.removedReason = reason
	s.removedBy = createdBy
	return s.removeErr
}

func (s *handlerLogicStub) ListReviewRemovals(_ context.Context) ([]ReviewRemoval, error) {
	return s.removals, s.removalsErr
}

// reviewClient wires the handler with only the request-validation interceptor.
// The RBAC/ABAC gates the feature needs live here (ABAC in the handler, RBAC in
// the router interceptor); the token-claim RBAC interceptor is covered by the
// router tests, so the client exercises the handler's own rules.
func reviewClient(t *testing.T, logic Logic, caller auth.Caller) reviewv1connect.ReviewServiceClient {
	t.Helper()
	_, handler := reviewv1connect.NewReviewServiceHandler(
		&Handler{Logic: logic}, connect.WithInterceptors(validate.NewInterceptor()))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if caller.ID != "" {
			r = r.WithContext(auth.WithCaller(r.Context(), caller))
		}
		handler.ServeHTTP(w, r)
	}))
	t.Cleanup(server.Close)
	return reviewv1connect.NewReviewServiceClient(server.Client(), server.URL)
}

func TestSubmitReviewRPC(t *testing.T) {
	caller := uuid.New()
	submitter, target, order := uuid.New(), uuid.New(), uuid.New()

	t.Run("parses the request and derives the reviewer from the caller", func(t *testing.T) {
		stub := &handlerLogicStub{}
		client := reviewClient(t, stub, auth.Caller{ID: caller.String()})
		_, err := client.SubmitReview(context.Background(), connect.NewRequest(&reviewv1.SubmitReviewRequest{
			From:       caller.String(),
			To:         target.String(),
			OrderId:    order.String(),
			Submission: &reviewv1.ReviewSubmission{Rating: 5, Comment: "excellent"},
		}))
		if err != nil {
			t.Fatalf("submit: %v", err)
		}
		if stub.submitter != caller {
			t.Fatalf("reviewer not derived from caller: got %v want %v", stub.submitter, caller)
		}
		if stub.submitted.Rating != 5 || stub.submitted.Comment != "excellent" {
			t.Fatalf("submission not mapped: %+v", stub.submitted)
		}
	})

	t.Run("impersonation is rejected", func(t *testing.T) {
		stub := &handlerLogicStub{}
		client := reviewClient(t, stub, auth.Caller{ID: caller.String()})
		_, err := client.SubmitReview(context.Background(), connect.NewRequest(&reviewv1.SubmitReviewRequest{
			From:       submitter.String(),
			To:         target.String(),
			OrderId:    order.String(),
			Submission: &reviewv1.ReviewSubmission{Rating: 5, Comment: "x"},
		}))
		if connect.CodeOf(err) != connect.CodePermissionDenied {
			t.Fatalf("impersonation should be forbidden: %v", err)
		}
		if stub.submitted.Comment != "" {
			t.Fatal("impersonation attempt reached the service")
		}
	})

	t.Run("missing actor is unauthorized", func(t *testing.T) {
		client := reviewClient(t, &handlerLogicStub{}, auth.Caller{})
		_, err := client.SubmitReview(context.Background(), connect.NewRequest(&reviewv1.SubmitReviewRequest{
			From:       caller.String(),
			To:         target.String(),
			OrderId:    order.String(),
			Submission: &reviewv1.ReviewSubmission{Rating: 5, Comment: "x"},
		}))
		if connect.CodeOf(err) != connect.CodeUnauthenticated {
			t.Fatalf("missing actor: %v", err)
		}
	})

	t.Run("invalid ids are rejected by validation", func(t *testing.T) {
		client := reviewClient(t, &handlerLogicStub{}, auth.Caller{ID: caller.String()})
		_, err := client.SubmitReview(context.Background(), connect.NewRequest(&reviewv1.SubmitReviewRequest{
			From:       "not-a-uuid",
			To:         target.String(),
			OrderId:    order.String(),
			Submission: &reviewv1.ReviewSubmission{Rating: 5, Comment: "x"},
		}))
		if connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Fatalf("invalid reviewer: %v", err)
		}
	})

	t.Run("submission out of range is rejected by validation", func(t *testing.T) {
		client := reviewClient(t, &handlerLogicStub{}, auth.Caller{ID: caller.String()})
		_, err := client.SubmitReview(context.Background(), connect.NewRequest(&reviewv1.SubmitReviewRequest{
			From:       caller.String(),
			To:         target.String(),
			OrderId:    order.String(),
			Submission: &reviewv1.ReviewSubmission{Rating: 6, Comment: "x"},
		}))
		if connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Fatalf("rating out of range: %v", err)
		}
	})
}

func TestListReviewsForUserRPC(t *testing.T) {
	user := uuid.New()
	reviewer := uuid.New()
	page := ReviewPage{
		Reviews:  []Review{{ID: uuid.New(), ReviewerID: reviewer, RevieweeID: user, OrderID: uuid.New(), Rating: 5, Comment: "great"}},
		PageInfo: pagination.NewPageInfo(2, 20, 1),
	}

	t.Run("maps reviews and page info", func(t *testing.T) {
		stub := &handlerLogicStub{forUserPage: page}
		client := reviewClient(t, stub, auth.Caller{ID: user.String()})
		resp, err := client.ListReviewsForUser(context.Background(), connect.NewRequest(&reviewv1.ListReviewsForUserRequest{
			UserId:     user.String(),
			Pagination: &paginationv1.Pagination{Page: 2, PageSize: 20},
		}))
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		if stub.listedFor != user {
			t.Fatalf("wrong user listed: %v", stub.listedFor)
		}
		if len(resp.Msg.Reviews) != 1 || resp.Msg.Reviews[0].RevieweeId != user.String() {
			t.Fatalf("unexpected reviews: %+v", resp.Msg.Reviews)
		}
		if resp.Msg.Pagination.GetTotalItems() != 1 || resp.Msg.Pagination.GetPage() != 2 || resp.Msg.Pagination.GetPageSize() != 20 {
			t.Fatalf("unexpected pagination: %+v", resp.Msg.Pagination)
		}
	})

	t.Run("suspended caller may list their own reviews", func(t *testing.T) {
		stub := &handlerLogicStub{forUserPage: page}
		client := reviewClient(t, stub, auth.Caller{ID: user.String(), Role: string(tokenclaims.RoleSuspendedUser)})
		_, err := client.ListReviewsForUser(context.Background(), connect.NewRequest(&reviewv1.ListReviewsForUserRequest{
			UserId: user.String(),
		}))
		if err != nil {
			t.Fatalf("suspended self list: %v", err)
		}
	})

	t.Run("suspended caller may not list someone else", func(t *testing.T) {
		stub := &handlerLogicStub{}
		other := uuid.New()
		client := reviewClient(t, stub, auth.Caller{ID: user.String(), Role: string(tokenclaims.RoleSuspendedUser)})
		_, err := client.ListReviewsForUser(context.Background(), connect.NewRequest(&reviewv1.ListReviewsForUserRequest{
			UserId: other.String(),
		}))
		if connect.CodeOf(err) != connect.CodePermissionDenied {
			t.Fatalf("suspended cross-user list should be forbidden: %v", err)
		}
	})
}

func TestListMyReviewsRPC(t *testing.T) {
	caller := uuid.New()
	t.Run("lists the caller's given reviews", func(t *testing.T) {
		stub := &handlerLogicStub{forUserPage: ReviewPage{PageInfo: pagination.NewPageInfo(1, 20, 0)}}
		client := reviewClient(t, stub, auth.Caller{ID: caller.String()})
		_, err := client.ListMyReviews(context.Background(), connect.NewRequest(&reviewv1.ListMyReviewsRequest{}))
		if err != nil {
			t.Fatalf("list mine: %v", err)
		}
		if stub.listedFor != caller {
			t.Fatalf("expected the caller to be the subject: %v", stub.listedFor)
		}
	})
	t.Run("missing actor is unauthorized", func(t *testing.T) {
		client := reviewClient(t, &handlerLogicStub{}, auth.Caller{})
		if _, err := client.ListMyReviews(context.Background(), connect.NewRequest(&reviewv1.ListMyReviewsRequest{})); connect.CodeOf(err) != connect.CodeUnauthenticated {
			t.Fatalf("missing actor: %v", err)
		}
	})
}

func TestRemoveReviewRPC(t *testing.T) {
	caller := uuid.New()
	id := uuid.New()

	t.Run("records the removal as the caller", func(t *testing.T) {
		stub := &handlerLogicStub{}
		client := reviewClient(t, stub, auth.Caller{ID: caller.String()})
		_, err := client.RemoveReview(context.Background(), connect.NewRequest(&reviewv1.RemoveReviewRequest{
			Id:     id.String(),
			Reason: "offensive content",
		}))
		if err != nil {
			t.Fatalf("remove: %v", err)
		}
		if stub.removedID != id || stub.removedReason != "offensive content" || stub.removedBy != caller {
			t.Fatalf("removal not recorded correctly: id=%v reason=%q by=%v", stub.removedID, stub.removedReason, stub.removedBy)
		}
	})

	t.Run("invalid id is rejected by validation", func(t *testing.T) {
		client := reviewClient(t, &handlerLogicStub{}, auth.Caller{ID: caller.String()})
		if _, err := client.RemoveReview(context.Background(), connect.NewRequest(&reviewv1.RemoveReviewRequest{Id: "bad"})); connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Fatalf("invalid id: %v", err)
		}
	})
}

func TestListReviewRemovalsRPC(t *testing.T) {
	stub := &handlerLogicStub{removals: []ReviewRemoval{{ID: uuid.New(), Reason: "abuse"}}}
	client := reviewClient(t, stub, auth.Caller{ID: uuid.New().String(), Admin: true})
	resp, err := client.ListReviewRemovals(context.Background(), connect.NewRequest(&reviewv1.ListReviewRemovalsRequest{}))
	if err != nil {
		t.Fatalf("list removals: %v", err)
	}
	if len(resp.Msg.Removals) != 1 || resp.Msg.Removals[0].Reason != "abuse" {
		t.Fatalf("unexpected removals: %+v", resp.Msg.Removals)
	}
}

func TestReviewRPCErrorMapping(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		code connect.Code
	}{
		{"already exists", errs.NewAlreadyExistsError("review already exists"), connect.CodeAlreadyExists},
		{"not found", errs.NewNotFoundError("review not found"), connect.CodeNotFound},
		{"internal", errors.New("private postgres stack trace"), connect.CodeInternal},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stub := &handlerLogicStub{removalsErr: tc.err}
			client := reviewClient(t, stub, auth.Caller{ID: uuid.New().String(), Admin: true})
			_, err := client.ListReviewRemovals(context.Background(), connect.NewRequest(&reviewv1.ListReviewRemovalsRequest{}))
			if connect.CodeOf(err) != tc.code {
				t.Fatalf("expected %v, got %v", tc.code, err)
			}
			if tc.code == connect.CodeInternal && strings.Contains(err.Error(), "private postgres stack trace") {
				t.Fatal("internal error details leaked to the client")
			}
		})
	}
}
