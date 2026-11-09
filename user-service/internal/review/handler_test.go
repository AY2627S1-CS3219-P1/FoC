package review

import (
	"context"
	"errors"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"connectrpc.com/validate"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/api/errs"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/auth"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/auth/tokenclaims"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/pagination/v1"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/user/review/v1"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/user/review/v1/reviewv1connect"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/images"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/pagination"
	"github.com/google/uuid"
)

// handlerLogicStub records the arguments the handler passes so tests can assert
// the adapter parsed the request correctly.
type handlerLogicStub struct {
	submitted ReviewSubmission
	submitter uuid.UUID
	photoIDs  []uuid.UUID
	submitErr error

	uploadOwner uuid.UUID
	uploadType  string
	uploadSize  int64
	uploadGrant images.UploadGrant
	uploadID    uuid.UUID
	uploadErr   error

	listedFor   uuid.UUID
	forUserPage ReviewPage
	forUserErr  error

	removedID     uuid.UUID
	removedReason string
	removedBy     uuid.UUID
	removeErr     error

	removalsPage RemovalPage
	removalsErr  error
	removalsFor  pagination.Pagination

	reviewedFor      uuid.UUID
	reviewedOrderIDs []uuid.UUID
	reviewedResult   []uuid.UUID
	reviewedErr      error
	myReview         Review
	myReviewErr      error
}

func (s *handlerLogicStub) SubmitReview(
	_ context.Context,
	reviewerID, revieweeID, orderID uuid.UUID,
	submission ReviewSubmission,
	photoIDs []uuid.UUID,
) error {
	s.submitter = reviewerID
	s.submitted = submission
	s.photoIDs = photoIDs
	return s.submitErr
}

func (s *handlerLogicStub) CreateReviewPhotoUpload(
	_ context.Context,
	callerID uuid.UUID,
	contentType string,
	sizeBytes int64,
) (uuid.UUID, images.UploadGrant, error) {
	s.uploadOwner = callerID
	s.uploadType = contentType
	s.uploadSize = sizeBytes
	return s.uploadID, s.uploadGrant, s.uploadErr
}

func (s *handlerLogicStub) ListReviewsForUser(
	_ context.Context,
	userID uuid.UUID,
	page pagination.Pagination,
) (ReviewPage, error) {
	s.listedFor = userID
	return s.forUserPage, s.forUserErr
}

func (s *handlerLogicStub) ListReviewsFromUser(
	_ context.Context,
	userID uuid.UUID,
	_ pagination.Pagination,
) (ReviewPage, error) {
	s.listedFor = userID
	return ReviewPage{}, s.forUserErr
}

func (s *handlerLogicStub) ListMyReviewsReceived(
	_ context.Context,
	userID uuid.UUID,
	page pagination.Pagination,
) (ReviewPage, error) {
	s.listedFor = userID
	return s.forUserPage, s.forUserErr
}

func (s *handlerLogicStub) ListMyReviewsForOrders(
	_ context.Context,
	reviewerID uuid.UUID,
	orderIDs []uuid.UUID,
) ([]uuid.UUID, error) {
	s.reviewedFor = reviewerID
	s.reviewedOrderIDs = orderIDs
	return s.reviewedResult, s.reviewedErr
}

func (s *handlerLogicStub) GetMyReviewForOrder(
	_ context.Context,
	reviewerID, _ uuid.UUID,
) (Review, error) {
	s.reviewedFor = reviewerID
	if s.myReviewErr != nil {
		return Review{}, s.myReviewErr
	}
	return s.myReview, nil
}

func (s *handlerLogicStub) RemoveReview(_ context.Context, id uuid.UUID, reason string, createdBy uuid.UUID) error {
	s.removedID = id
	s.removedReason = reason
	s.removedBy = createdBy
	return s.removeErr
}

func (s *handlerLogicStub) ListReviewRemovals(_ context.Context, page pagination.Pagination) (RemovalPage, error) {
	s.removalsFor = page
	return s.removalsPage, s.removalsErr
}

// reviewClient mounts the handler without the RBAC interceptor (covered by
// authorization_test.go), so tests exercise the handler's own ABAC rules.
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

func TestCreateReviewPhotoUploadResponseIsNoStore(t *testing.T) {
	stub := &handlerLogicStub{
		uploadID:    uuid.New(),
		uploadGrant: images.UploadGrant{URL: "https://storage.test/upload", ExpiresAt: time.Now().Add(time.Minute)},
	}
	client := reviewClient(t, stub, auth.Caller{ID: uuid.New().String()})

	resp, err := client.CreateReviewPhotoUpload(
		context.Background(),
		connect.NewRequest(&reviewv1.CreateReviewPhotoUploadRequest{
			ContentType: "image/jpeg",
			SizeBytes:   200,
		}),
	)
	if err != nil {
		t.Fatalf("create upload: %v", err)
	}
	// The response carries a short-lived pre-signed PUT grant, so it must not
	// be cached.
	if got := resp.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", got)
	}
}

func TestListMyReviewsReceivedRPC(t *testing.T) {
	caller := uuid.New()
	page := ReviewPage{PageInfo: pagination.NewPageInfo(1, 20, 1)}
	stub := &handlerLogicStub{forUserPage: page}
	client := reviewClient(t, stub, auth.Caller{ID: caller.String()})
	resp, err := client.ListMyReviewsReceived(
		context.Background(),
		connect.NewRequest(&reviewv1.ListMyReviewsReceivedRequest{}),
	)
	if err != nil {
		t.Fatalf("list received: %v", err)
	}
	if stub.listedFor != caller {
		t.Fatalf("listed user = %v, want caller %v", stub.listedFor, caller)
	}
	if resp.Msg.Pagination == nil || resp.Msg.Pagination.TotalItems != 1 {
		t.Fatalf("pagination = %+v", resp.Msg.Pagination)
	}
}

func TestListMyReviewsForOrdersRPC(t *testing.T) {
	caller := uuid.New()
	t.Run("returns the reviewed subset and derives the reviewer from the caller", func(t *testing.T) {
		stub := &handlerLogicStub{reviewedResult: []uuid.UUID{uuid.New()}}
		client := reviewClient(t, stub, auth.Caller{ID: caller.String()})
		one, two := uuid.New(), uuid.New()
		resp, err := client.ListMyReviewsForOrders(
			context.Background(),
			connect.NewRequest(&reviewv1.ListMyReviewsForOrdersRequest{
				OrderIds: []string{one.String(), two.String()},
			}),
		)
		if err != nil {
			t.Fatalf("list reviewed orders: %v", err)
		}
		if stub.reviewedFor != caller {
			t.Fatalf("reviewer = %v, want caller %v", stub.reviewedFor, caller)
		}
		if len(stub.reviewedOrderIDs) != 2 || stub.reviewedOrderIDs[0] != one || stub.reviewedOrderIDs[1] != two {
			t.Fatalf("order ids not passed through: %v", stub.reviewedOrderIDs)
		}
		if len(resp.Msg.ReviewedOrderIds) != 1 || resp.Msg.ReviewedOrderIds[0] != stub.reviewedResult[0].String() {
			t.Fatalf("reviewed ids = %v", resp.Msg.ReviewedOrderIds)
		}
	})
	t.Run("invalid order id is rejected", func(t *testing.T) {
		client := reviewClient(t, &handlerLogicStub{}, auth.Caller{ID: caller.String()})
		_, err := client.ListMyReviewsForOrders(
			context.Background(),
			connect.NewRequest(&reviewv1.ListMyReviewsForOrdersRequest{
				OrderIds: []string{"not-a-uuid"},
			}),
		)
		if connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Fatalf("want invalid argument, got %v", err)
		}
	})
}

func TestGetMyReviewForOrderRPC(t *testing.T) {
	caller := uuid.New()
	photoURL := "https://storage.invalid/get/1"
	t.Run("returns the caller's review with photos", func(t *testing.T) {
		stub := &handlerLogicStub{myReview: Review{
			Rating: 4, Comment: "solid",
			Photos: []ReviewPhotoView{{ID: uuid.New(), URL: photoURL, ContentType: "image/png"}},
		}}
		client := reviewClient(t, stub, auth.Caller{ID: caller.String()})
		resp, err := client.GetMyReviewForOrder(
			context.Background(),
			connect.NewRequest(&reviewv1.GetMyReviewForOrderRequest{
				OrderId: uuid.New().String(),
			}),
		)
		if err != nil {
			t.Fatalf("get review: %v", err)
		}
		if resp.Msg.Review == nil || resp.Msg.Review.Rating != 4 {
			t.Fatalf("review = %+v", resp.Msg.Review)
		}
		if len(resp.Msg.Review.Photos) != 1 || resp.Msg.Review.Photos[0].Url != photoURL {
			t.Fatalf("photos = %+v", resp.Msg.Review.Photos)
		}
		if got := resp.Header().Get("Cache-Control"); got != "no-store" {
			t.Fatalf("Cache-Control = %q, want no-store", got)
		}
	})
	t.Run("unreviewed order is reported as not found", func(t *testing.T) {
		stub := &handlerLogicStub{myReviewErr: errReviewNotFound}
		client := reviewClient(t, stub, auth.Caller{ID: caller.String()})
		_, err := client.GetMyReviewForOrder(
			context.Background(),
			connect.NewRequest(&reviewv1.GetMyReviewForOrderRequest{
				OrderId: uuid.New().String(),
			}),
		)
		if connect.CodeOf(err) != connect.CodeNotFound {
			t.Fatalf("want not found, got %v", err)
		}
		if got := reasonOf(t, err); got != reasonReviewNotFound {
			t.Fatalf("reason = %q, want %q", got, reasonReviewNotFound)
		}
	})
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

	t.Run("passes ordered photo IDs through to the logic", func(t *testing.T) {
		stub := &handlerLogicStub{}
		client := reviewClient(t, stub, auth.Caller{ID: caller.String()})
		first, second := uuid.New(), uuid.New()
		_, err := client.SubmitReview(context.Background(), connect.NewRequest(&reviewv1.SubmitReviewRequest{
			From:       caller.String(),
			To:         target.String(),
			OrderId:    order.String(),
			Submission: &reviewv1.ReviewSubmission{Rating: 4, Comment: "with photos"},
			PhotoIds:   []string{first.String(), second.String()},
		}))
		if err != nil {
			t.Fatalf("submit: %v", err)
		}
		if len(stub.photoIDs) != 2 || stub.photoIDs[0] != first || stub.photoIDs[1] != second {
			t.Fatalf("photo IDs not mapped in order: %v", stub.photoIDs)
		}
	})

	t.Run("a zero or malformed photo ID is rejected instead of dropped", func(t *testing.T) {
		stub := &handlerLogicStub{}
		client := reviewClient(t, stub, auth.Caller{ID: caller.String()})
		_, err := client.SubmitReview(context.Background(), connect.NewRequest(&reviewv1.SubmitReviewRequest{
			From:       caller.String(),
			To:         target.String(),
			OrderId:    order.String(),
			Submission: &reviewv1.ReviewSubmission{Rating: 4, Comment: "bad photo id"},
			PhotoIds:   []string{uuid.Nil.String()},
		}))
		if connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Fatalf("expected invalid argument, got %v", err)
		}
		if len(stub.photoIDs) != 0 {
			t.Fatal("the logic must not run when a photo ID is invalid")
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
		Reviews: []Review{
			{ID: uuid.New(), ReviewerID: reviewer, RevieweeID: user, OrderID: uuid.New(), Rating: 5, Comment: "great"},
		},
		PageInfo: pagination.NewPageInfo(2, 20, 1),
	}

	t.Run("maps reviews and page info", func(t *testing.T) {
		stub := &handlerLogicStub{forUserPage: page}
		client := reviewClient(t, stub, auth.Caller{ID: user.String()})
		resp, err := client.ListReviewsForUser(
			context.Background(),
			connect.NewRequest(&reviewv1.ListReviewsForUserRequest{
				UserId:     user.String(),
				Pagination: &paginationv1.Pagination{Page: 2, PageSize: 20},
			}),
		)
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		if stub.listedFor != user {
			t.Fatalf("wrong user listed: %v", stub.listedFor)
		}
		if len(resp.Msg.Reviews) != 1 || resp.Msg.Reviews[0].RevieweeId != user.String() {
			t.Fatalf("unexpected reviews: %+v", resp.Msg.Reviews)
		}
		if resp.Msg.Pagination.GetTotalItems() != 1 || resp.Msg.Pagination.GetPage() != 2 ||
			resp.Msg.Pagination.GetPageSize() != 20 {
			t.Fatalf("unexpected pagination: %+v", resp.Msg.Pagination)
		}
	})

	t.Run("suspended caller may list their own reviews", func(t *testing.T) {
		stub := &handlerLogicStub{forUserPage: page}
		client := reviewClient(t, stub, auth.Caller{ID: user.String(), Role: string(tokenclaims.RoleSuspendedUser)})
		_, err := client.ListReviewsForUser(
			context.Background(),
			connect.NewRequest(&reviewv1.ListReviewsForUserRequest{
				UserId: user.String(),
			}),
		)
		if err != nil {
			t.Fatalf("suspended self list: %v", err)
		}
	})

	t.Run("suspended caller may not list someone else", func(t *testing.T) {
		stub := &handlerLogicStub{}
		other := uuid.New()
		client := reviewClient(t, stub, auth.Caller{ID: user.String(), Role: string(tokenclaims.RoleSuspendedUser)})
		_, err := client.ListReviewsForUser(
			context.Background(),
			connect.NewRequest(&reviewv1.ListReviewsForUserRequest{
				UserId: other.String(),
			}),
		)
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
		if _, err := client.ListMyReviews(
			context.Background(),
			connect.NewRequest(&reviewv1.ListMyReviewsRequest{}),
		); connect.CodeOf(
			err,
		) != connect.CodeUnauthenticated {
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
			t.Fatalf(
				"removal not recorded correctly: id=%v reason=%q by=%v",
				stub.removedID,
				stub.removedReason,
				stub.removedBy,
			)
		}
	})

	t.Run("invalid id is rejected by validation", func(t *testing.T) {
		client := reviewClient(t, &handlerLogicStub{}, auth.Caller{ID: caller.String()})
		if _, err := client.RemoveReview(
			context.Background(),
			connect.NewRequest(&reviewv1.RemoveReviewRequest{Id: "bad"}),
		); connect.CodeOf(
			err,
		) != connect.CodeInvalidArgument {
			t.Fatalf("invalid id: %v", err)
		}
	})
}

func TestListReviewRemovalsRPC(t *testing.T) {
	removalsPage := RemovalPage{
		Removals: []ReviewRemoval{{ID: uuid.New(), Reason: "abuse"}},
		PageInfo: pagination.NewPageInfo(2, 10, 3),
	}
	admin := uuid.New().String()

	t.Run("maps removals and page info", func(t *testing.T) {
		stub := &handlerLogicStub{removalsPage: removalsPage}
		client := reviewClient(t, stub, auth.Caller{ID: admin, Admin: true})
		resp, err := client.ListReviewRemovals(
			context.Background(),
			connect.NewRequest(&reviewv1.ListReviewRemovalsRequest{
				Pagination: &paginationv1.Pagination{Page: 2, PageSize: 10},
			}),
		)
		if err != nil {
			t.Fatalf("list removals: %v", err)
		}
		if len(resp.Msg.Removals) != 1 || resp.Msg.Removals[0].Reason != "abuse" {
			t.Fatalf("unexpected removals: %+v", resp.Msg.Removals)
		}
		if resp.Msg.Pagination.GetTotalItems() != 3 || resp.Msg.Pagination.GetPage() != 2 ||
			resp.Msg.Pagination.GetPageSize() != 10 {
			t.Fatalf("unexpected pagination: %+v", resp.Msg.Pagination)
		}
		if stub.removalsFor.Page() != 2 || stub.removalsFor.PageSize() != 10 {
			t.Fatalf("page not passed through: %+v", stub.removalsFor)
		}
	})

	t.Run("pagination preserves int64 values", func(t *testing.T) {
		stub := &handlerLogicStub{
			removalsPage: RemovalPage{
				PageInfo: pagination.NewPageInfo(math.MaxInt64, 1, math.MaxInt64),
			},
		}
		client := reviewClient(t, stub, auth.Caller{ID: admin, Admin: true})
		resp, err := client.ListReviewRemovals(context.Background(),
			connect.NewRequest(&reviewv1.ListReviewRemovalsRequest{
				Pagination: &paginationv1.Pagination{Page: math.MaxInt64, PageSize: 1},
			}))
		if err != nil {
			t.Fatalf("list removals: %v", err)
		}
		if stub.removalsFor.Offset() != math.MaxInt64-1 {
			t.Fatalf("unexpected offset: %d", stub.removalsFor.Offset())
		}
		info := resp.Msg.GetPagination()
		if info.GetPage() != math.MaxInt64 || info.GetTotalPages() != math.MaxInt64 ||
			info.GetTotalItems() != math.MaxInt64 || info.GetPageSize() != 1 {
			t.Fatalf("unexpected pagination: %+v", info)
		}
	})

	t.Run("pagination overflow is rejected", func(t *testing.T) {
		stub := &handlerLogicStub{}
		client := reviewClient(t, stub, auth.Caller{ID: admin, Admin: true})
		_, err := client.ListReviewRemovals(
			context.Background(),
			connect.NewRequest(&reviewv1.ListReviewRemovalsRequest{
				Pagination: &paginationv1.Pagination{Page: math.MaxInt64, PageSize: 2},
			}),
		)
		if connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Fatalf("offset overflow should be invalid argument: %v", err)
		}
	})
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
			_, err := client.ListReviewRemovals(
				context.Background(),
				connect.NewRequest(&reviewv1.ListReviewRemovalsRequest{}),
			)
			if connect.CodeOf(err) != tc.code {
				t.Fatalf("expected %v, got %v", tc.code, err)
			}
			if tc.code == connect.CodeInternal && strings.Contains(err.Error(), "private postgres stack trace") {
				t.Fatal("internal error details leaked to the client")
			}
		})
	}
}
