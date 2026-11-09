package report

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"connectrpc.com/connect"
	"connectrpc.com/validate"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/auth"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/auth/tokenclaims"
	reportv1 "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/user/report/v1"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/user/report/v1/reportv1connect"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/pagination"
	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"
)

// handlerLogicStub records what the handler passes to the service.
type handlerLogicStub struct {
	submittedBy uuid.UUID
	submission  Submission

	gotCaller uuid.UUID
	gotAdmin  bool

	filter Filter

	respondedBy   uuid.UUID
	responseBody  string
	transitioned  string
	closedBy      uuid.UUID
	closedOutcome Outcome
	closedReason  string

	report   Report
	response Response
}

func (s *handlerLogicStub) SubmitReport(
	_ context.Context,
	reporterID uuid.UUID,
	submission Submission,
) (Report, error) {
	s.submittedBy, s.submission = reporterID, submission
	return s.report, nil
}

func (s *handlerLogicStub) ListMyReports(context.Context, uuid.UUID, pagination.Pagination) (Page, error) {
	return Page{Reports: []Report{s.report}}, nil
}

func (s *handlerLogicStub) GetReport(_ context.Context, callerID uuid.UUID, isAdmin bool, _ uuid.UUID) (Detail, error) {
	s.gotCaller, s.gotAdmin = callerID, isAdmin
	return Detail{Report: s.report, Responses: []Response{s.response}}, nil
}

func (s *handlerLogicStub) SubmitResponse(
	_ context.Context,
	callerID, _ uuid.UUID,
	body string,
) (Report, Response, error) {
	s.respondedBy, s.responseBody = callerID, body
	return s.report, s.response, nil
}

func (s *handlerLogicStub) ListReports(_ context.Context, filter Filter, _ pagination.Pagination) (Page, error) {
	s.filter = filter
	return Page{Reports: []Report{s.report}}, nil
}

func (s *handlerLogicStub) BeginReview(context.Context, uuid.UUID, uuid.UUID) (Report, error) {
	s.transitioned = "begin"
	return s.report, nil
}

func (s *handlerLogicStub) RequestResponse(context.Context, uuid.UUID, uuid.UUID) (Report, error) {
	s.transitioned = "request"
	return s.report, nil
}

func (s *handlerLogicStub) CloseReport(
	_ context.Context,
	actorID, _ uuid.UUID,
	outcome Outcome,
	reason string,
) (Report, error) {
	s.closedBy, s.closedOutcome, s.closedReason = actorID, outcome, reason
	return s.report, nil
}

// reportClient mounts the handler with request validation, without the role
// interceptor that authorization_test.go covers.
func reportClient(t *testing.T, logic Logic, caller auth.Caller) reportv1connect.ReportServiceClient {
	t.Helper()

	_, handler := reportv1connect.NewReportServiceHandler(
		&Handler{Logic: logic}, connect.WithInterceptors(validate.NewInterceptor()))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r = r.WithContext(auth.WithCaller(r.Context(), caller))
		handler.ServeHTTP(w, r)
	}))
	t.Cleanup(server.Close)
	return reportv1connect.NewReportServiceClient(server.Client(), server.URL)
}

func adminCaller(id uuid.UUID) auth.Caller {
	return auth.Caller{ID: id.String(), Role: string(tokenclaims.RoleAdmin), Admin: true}
}

func TestSubmitReportRPC(t *testing.T) {
	callerID := uuid.New()
	caller := auth.Caller{ID: callerID.String(), Role: string(tokenclaims.RoleUser)}

	t.Run("passes a review target", func(t *testing.T) {
		stub := &handlerLogicStub{report: Report{ID: uuid.New(), ReviewID: uuid.New()}}
		reviewID := uuid.New()
		_, err := reportClient(t, stub, caller).SubmitReport(context.Background(),
			connect.NewRequest(&reportv1.SubmitReportRequest{
				Target:      &reportv1.SubmitReportRequest_ReviewId{ReviewId: reviewID.String()},
				Category:    reportv1.ReportCategory_REPORT_CATEGORY_HARASSMENT,
				Description: "abusive review",
			}))
		if err != nil {
			t.Fatal(err)
		}
		if stub.submittedBy != callerID || stub.submission.Target != ReviewTarget(reviewID) ||
			stub.submission.Category != CategoryHarassment {
			t.Fatalf("submission = %+v by %s", stub.submission, stub.submittedBy)
		}
	})

	t.Run("passes an order target", func(t *testing.T) {
		stub := &handlerLogicStub{report: Report{ID: uuid.New(), OrderID: uuid.New()}}
		orderID, subjectID := uuid.New(), uuid.New()
		_, err := reportClient(t, stub, caller).SubmitReport(context.Background(),
			connect.NewRequest(&reportv1.SubmitReportRequest{
				Target: &reportv1.SubmitReportRequest_Order{Order: &reportv1.OrderTarget{
					OrderId:   orderID.String(),
					SubjectId: subjectID.String(),
				}},
				Category:    reportv1.ReportCategory_REPORT_CATEGORY_NO_SHOW,
				Description: "never arrived",
			}))
		if err != nil {
			t.Fatal(err)
		}
		if stub.submission.Target != OrderTarget(orderID, subjectID) {
			t.Fatalf("target = %+v", stub.submission.Target)
		}
	})

	for _, tc := range []struct {
		name string
		req  *reportv1.SubmitReportRequest
	}{
		{"missing target", &reportv1.SubmitReportRequest{
			Category:    reportv1.ReportCategory_REPORT_CATEGORY_OTHER,
			Description: "x",
		}},
		{"unspecified category", &reportv1.SubmitReportRequest{
			Target:      &reportv1.SubmitReportRequest_ReviewId{ReviewId: uuid.NewString()},
			Description: "x",
		}},
		{"malformed review id", &reportv1.SubmitReportRequest{
			Target:      &reportv1.SubmitReportRequest_ReviewId{ReviewId: "not-a-uuid"},
			Category:    reportv1.ReportCategory_REPORT_CATEGORY_OTHER,
			Description: "x",
		}},
	} {
		t.Run("rejects "+tc.name, func(t *testing.T) {
			stub := &handlerLogicStub{}
			_, err := reportClient(t, stub, caller).SubmitReport(context.Background(), connect.NewRequest(tc.req))
			if connect.CodeOf(err) != connect.CodeInvalidArgument {
				t.Fatalf("code = %v, want invalid argument", connect.CodeOf(err))
			}
			if stub.submittedBy != uuid.Nil {
				t.Fatal("invalid request reached the service")
			}
		})
	}
}

func TestGetReportRPCIncludesResponses(t *testing.T) {
	callerID := uuid.New()
	stub := &handlerLogicStub{
		report:   Report{ID: uuid.New(), OrderID: uuid.New(), Status: StatusAwaitingResponse},
		response: Response{ID: uuid.New(), Body: "my side"},
	}

	resp, err := reportClient(t, stub, adminCaller(callerID)).GetReport(context.Background(),
		connect.NewRequest(&reportv1.GetReportRequest{Id: uuid.NewString()}))
	if err != nil {
		t.Fatal(err)
	}
	if stub.gotCaller != callerID || !stub.gotAdmin {
		t.Fatalf("caller = %s, admin = %v", stub.gotCaller, stub.gotAdmin)
	}
	if resp.Msg.GetReport().GetStatus() != reportv1.ReportStatus_REPORT_STATUS_AWAITING_RESPONSE ||
		len(resp.Msg.GetResponses()) != 1 || resp.Msg.GetResponses()[0].GetBody() != "my side" {
		t.Fatalf("response = %+v", resp.Msg)
	}
}

func TestSubmitResponseRPC(t *testing.T) {
	callerID := uuid.New()
	caller := auth.Caller{ID: callerID.String(), Role: string(tokenclaims.RoleSuspendedUser)}
	stub := &handlerLogicStub{
		report:   Report{ID: uuid.New(), OrderID: uuid.New()},
		response: Response{ID: uuid.New(), Body: "my side"},
	}

	resp, err := reportClient(t, stub, caller).SubmitResponse(context.Background(),
		connect.NewRequest(&reportv1.SubmitResponseRequest{ReportId: uuid.NewString(), Body: "my side"}))
	if err != nil {
		t.Fatal(err)
	}
	if stub.respondedBy != callerID || stub.responseBody != "my side" || resp.Msg.GetResponse().GetBody() != "my side" {
		t.Fatalf("responded by %s with %q", stub.respondedBy, stub.responseBody)
	}

	_, err = reportClient(t, stub, caller).SubmitResponse(context.Background(),
		connect.NewRequest(&reportv1.SubmitResponseRequest{ReportId: uuid.NewString()}))
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("empty body code = %v", connect.CodeOf(err))
	}
}

func TestListReportsRPCParsesFilters(t *testing.T) {
	stub := &handlerLogicStub{report: Report{ID: uuid.New(), ReviewID: uuid.New()}}
	subjectID, orderID := uuid.New(), uuid.New()

	resp, err := reportClient(t, stub, adminCaller(uuid.New())).ListReports(context.Background(),
		connect.NewRequest(&reportv1.ListReportsRequest{
			Status:    reportv1.ReportStatus_REPORT_STATUS_UNDER_REVIEW.Enum(),
			SubjectId: proto.String(subjectID.String()),
			OrderId:   proto.String(orderID.String()),
		}))
	if err != nil {
		t.Fatal(err)
	}
	want := Filter{Status: StatusUnderReview, SubjectID: subjectID, OrderID: orderID}
	if stub.filter != want {
		t.Fatalf("filter = %+v, want %+v", stub.filter, want)
	}
	if len(resp.Msg.GetReports()) != 1 || resp.Msg.GetReports()[0].GetReviewId() == "" {
		t.Fatalf("reports = %+v", resp.Msg.GetReports())
	}
}

func TestAdminTransitionRPCs(t *testing.T) {
	stub := &handlerLogicStub{report: Report{ID: uuid.New(), OrderID: uuid.New()}}
	client := reportClient(t, stub, adminCaller(uuid.New()))

	if _, err := client.BeginReview(
		context.Background(),
		connect.NewRequest(
			&reportv1.BeginReviewRequest{Id: uuid.NewString()},
		),
	); err != nil || stub.transitioned != "begin" {
		t.Fatalf("begin review: %v (%s)", err, stub.transitioned)
	}
	if _, err := client.RequestResponse(context.Background(),
		connect.NewRequest(&reportv1.RequestResponseRequest{Id: uuid.NewString()})); err != nil ||
		stub.transitioned != "request" {
		t.Fatalf("request response: %v (%s)", err, stub.transitioned)
	}
}

func TestCloseReportRPC(t *testing.T) {
	actorID := uuid.New()
	closedAt := time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)
	stub := &handlerLogicStub{report: Report{
		ID:            uuid.New(),
		OrderID:       uuid.New(),
		Status:        StatusClosed,
		Outcome:       OutcomeWarning,
		OutcomeReason: "rude messages",
		ClosedAt:      closedAt,
		ClosedBy:      actorID,
	}}
	client := reportClient(t, stub, adminCaller(actorID))

	resp, err := client.CloseReport(context.Background(), connect.NewRequest(&reportv1.CloseReportRequest{
		Id:      uuid.NewString(),
		Outcome: reportv1.ReportOutcome_REPORT_OUTCOME_WARNING,
		Reason:  "rude messages",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if stub.closedBy != actorID || stub.closedOutcome != OutcomeWarning || stub.closedReason != "rude messages" {
		t.Fatalf("close args = %s %s %q", stub.closedBy, stub.closedOutcome, stub.closedReason)
	}

	got := resp.Msg.GetReport()
	if got.GetStatus() != reportv1.ReportStatus_REPORT_STATUS_CLOSED ||
		got.GetOutcome() != reportv1.ReportOutcome_REPORT_OUTCOME_WARNING ||
		got.GetOutcomeReason() != "rude messages" || !got.GetClosedAt().AsTime().Equal(closedAt) ||
		got.GetClosedBy() != actorID.String() {
		t.Fatalf("report = %+v", got)
	}

	_, err = client.CloseReport(context.Background(),
		connect.NewRequest(&reportv1.CloseReportRequest{Id: uuid.NewString(), Reason: "x"}))
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("unspecified outcome code = %v", connect.CodeOf(err))
	}
}
