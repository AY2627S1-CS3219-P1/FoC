package appeal

import (
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"connectrpc.com/connect"
	"connectrpc.com/validate"
	"github.com/AY2627S1-CS3219-P1/FoC/moderation-service/internal/evidence"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/auth"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/auth/tokenclaims"
	appealv1 "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/moderation/appeal/v1"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/moderation/appeal/v1/appealv1connect"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/images"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/pagination"
	"github.com/google/uuid"
)

// handlerLogicStub records what the handler passes to the service.
type handlerLogicStub struct {
	submittedBy uuid.UUID
	submission  Submission

	gotCaller uuid.UUID
	gotAdmin  bool

	participant uuid.UUID
	resolutions []Resolution

	responseEvidence []uuid.UUID
	uploadedBy       uuid.UUID

	appeal Appeal
}

func (s *handlerLogicStub) SubmitAppeal(
	_ context.Context,
	appellantID uuid.UUID,
	submission Submission,
) (Appeal, error) {
	s.submittedBy, s.submission = appellantID, submission
	return s.appeal, nil
}

func (s *handlerLogicStub) ListMyAppeals(context.Context, uuid.UUID, pagination.Pagination) (Page, error) {
	return Page{Appeals: []Appeal{s.appeal}}, nil
}

func (s *handlerLogicStub) GetAppeal(_ context.Context, callerID uuid.UUID, isAdmin bool, _ uuid.UUID) (Detail, error) {
	s.gotCaller, s.gotAdmin = callerID, isAdmin
	return Detail{Appeal: s.appeal}, nil
}

func (s *handlerLogicStub) SubmitResponse(
	_ context.Context,
	_, _ uuid.UUID,
	_ string,
	evidenceIDs []uuid.UUID,
) (Appeal, Response, error) {
	s.responseEvidence = evidenceIDs
	return s.appeal, Response{}, nil
}

func (s *handlerLogicStub) CreateEvidenceUpload(
	_ context.Context,
	ownerID uuid.UUID,
	_ string,
	_ int64,
) (uuid.UUID, images.UploadGrant, error) {
	s.uploadedBy = ownerID
	return uuid.New(), images.UploadGrant{}, nil
}

func (s *handlerLogicStub) GetEvidenceDownload(
	_ context.Context,
	callerID uuid.UUID,
	isAdmin bool,
	evidenceID uuid.UUID,
) (evidence.View, error) {
	s.gotCaller, s.gotAdmin = callerID, isAdmin
	return evidence.View{ID: evidenceID}, nil
}

func (s *handlerLogicStub) ListAppeals(context.Context, Filter, pagination.Pagination) (Page, error) {
	return Page{Appeals: []Appeal{s.appeal}}, nil
}

func (s *handlerLogicStub) BeginReview(context.Context, uuid.UUID, uuid.UUID) (Appeal, error) {
	return s.appeal, nil
}

func (s *handlerLogicStub) RequestResponse(_ context.Context, _, _, participantID uuid.UUID) (Appeal, error) {
	s.participant = participantID
	return s.appeal, nil
}

func (s *handlerLogicStub) CloseAppeal(_ context.Context, _, _ uuid.UUID, resolutions []Resolution) (Appeal, error) {
	s.resolutions = resolutions
	return s.appeal, nil
}

// appealClient mounts the handler with request validation, without the role
// interceptor that authorization_test.go covers.
func appealClient(t *testing.T, logic Logic, caller auth.Caller) appealv1connect.AppealServiceClient {
	t.Helper()

	_, handler := appealv1connect.NewAppealServiceHandler(
		&Handler{Logic: logic}, connect.WithInterceptors(validate.NewInterceptor()))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r = r.WithContext(auth.WithCaller(r.Context(), caller))
		handler.ServeHTTP(w, r)
	}))
	t.Cleanup(server.Close)
	return appealv1connect.NewAppealServiceClient(server.Client(), server.URL)
}

func TestSubmitAppealRPC(t *testing.T) {
	callerID := uuid.New()
	caller := auth.Caller{ID: callerID.String(), Role: string(tokenclaims.RoleSuspendedUser)}

	t.Run("maps every target kind", func(t *testing.T) {
		stub := &handlerLogicStub{appeal: Appeal{ID: uuid.New()}}
		key, requestID := uuid.New(), uuid.New()
		report, warning, removed, retained := uuid.New(), uuid.New(), uuid.New(), uuid.New()
		requestIDText := requestID.String()

		_, err := appealClient(t, stub, caller).SubmitAppeal(context.Background(),
			connect.NewRequest(&appealv1.SubmitAppealRequest{
				IdempotencyKey: key.String(),
				RequestId:      &requestIDText,
				Reason:         "unfair",
				Decisions: []*appealv1.AppealedDecision{
					{Target: &appealv1.AppealedDecision_ReportId{ReportId: report.String()}},
					{Target: &appealv1.AppealedDecision_WarningId{WarningId: warning.String()}},
					{Target: &appealv1.AppealedDecision_RemovedReviewId{RemovedReviewId: removed.String()}},
					{Target: &appealv1.AppealedDecision_RetainedReviewId{RetainedReviewId: retained.String()}},
				},
			}))
		if err != nil {
			t.Fatal(err)
		}

		want := []Target{
			{Kind: KindReportOutcome, TargetID: report},
			{Kind: KindWarning, TargetID: warning},
			{Kind: KindReviewRemoval, TargetID: removed},
			{Kind: KindReviewRetention, TargetID: retained},
		}
		if stub.submittedBy != callerID || stub.submission.IdempotencyKey != key ||
			stub.submission.RequestID != requestID || !slices.Equal(stub.submission.Targets, want) {
			t.Fatalf("submission = %+v by %s", stub.submission, stub.submittedBy)
		}
	})

	t.Run("credit adjustments are unimplemented", func(t *testing.T) {
		stub := &handlerLogicStub{}
		_, err := appealClient(t, stub, caller).SubmitAppeal(context.Background(),
			connect.NewRequest(&appealv1.SubmitAppealRequest{
				IdempotencyKey: uuid.NewString(),
				Reason:         "unfair",
				Decisions: []*appealv1.AppealedDecision{
					{Target: &appealv1.AppealedDecision_CreditEntryId{CreditEntryId: uuid.NewString()}},
				},
			}))
		if connect.CodeOf(err) != connect.CodeUnimplemented {
			t.Fatalf("code = %v, want unimplemented", connect.CodeOf(err))
		}
	})

	for _, tc := range []struct {
		name string
		req  *appealv1.SubmitAppealRequest
	}{
		{"no decisions", &appealv1.SubmitAppealRequest{IdempotencyKey: uuid.NewString(), Reason: "x"}},
		{"missing key", &appealv1.SubmitAppealRequest{
			Reason: "x",
			Decisions: []*appealv1.AppealedDecision{
				{Target: &appealv1.AppealedDecision_WarningId{WarningId: uuid.NewString()}},
			},
		}},
		{"decision without a target", &appealv1.SubmitAppealRequest{
			IdempotencyKey: uuid.NewString(),
			Reason:         "x",
			Decisions:      []*appealv1.AppealedDecision{{}},
		}},
	} {
		t.Run("rejects "+tc.name, func(t *testing.T) {
			stub := &handlerLogicStub{}
			_, err := appealClient(t, stub, caller).SubmitAppeal(context.Background(), connect.NewRequest(tc.req))
			if connect.CodeOf(err) != connect.CodeInvalidArgument {
				t.Fatalf("code = %v, want invalid argument", connect.CodeOf(err))
			}
			if stub.submittedBy != uuid.Nil {
				t.Fatal("invalid request reached the service")
			}
		})
	}
}

func TestAdminAppealRPCs(t *testing.T) {
	adminID := uuid.New()
	admin := auth.Caller{ID: adminID.String(), Role: string(tokenclaims.RoleAdmin), Admin: true}
	decisionID, participantID := uuid.New(), uuid.New()
	stub := &handlerLogicStub{appeal: Appeal{
		ID:     uuid.New(),
		Status: StatusClosed,
		Decisions: []Decision{{
			ID:            decisionID,
			Target:        Target{Kind: KindWarning, TargetID: uuid.New()},
			Outcome:       OutcomeOverturned,
			OutcomeReason: "overturned",
		}},
	}}
	client := appealClient(t, stub, admin)
	ctx := context.Background()

	if _, err := client.RequestResponse(ctx, connect.NewRequest(&appealv1.RequestResponseRequest{
		Id: uuid.NewString(), ParticipantId: participantID.String(),
	})); err != nil || stub.participant != participantID {
		t.Fatalf("request response: participant=%v err=%v", stub.participant, err)
	}

	resp, err := client.CloseAppeal(ctx, connect.NewRequest(&appealv1.CloseAppealRequest{
		Id: uuid.NewString(),
		Resolutions: []*appealv1.DecisionResolution{{
			DecisionId: decisionID.String(),
			Outcome:    appealv1.DecisionOutcome_DECISION_OUTCOME_OVERTURNED,
			Reason:     "overturned",
		}},
	}))
	if err != nil {
		t.Fatal(err)
	}
	want := []Resolution{{DecisionID: decisionID, Outcome: OutcomeOverturned, Reason: "overturned"}}
	if !slices.Equal(stub.resolutions, want) {
		t.Fatalf("resolutions = %+v", stub.resolutions)
	}
	decision := resp.Msg.GetAppeal().GetDecisions()[0]
	if resp.Msg.GetAppeal().GetStatus() != appealv1.AppealStatus_APPEAL_STATUS_CLOSED ||
		decision.GetOutcome() != appealv1.DecisionOutcome_DECISION_OUTCOME_OVERTURNED ||
		decision.GetWarningId() == "" {
		t.Fatalf("closed appeal = %+v", resp.Msg.GetAppeal())
	}

	if _, err := client.GetAppeal(
		ctx,
		connect.NewRequest(&appealv1.GetAppealRequest{Id: uuid.NewString()}),
	); err != nil {
		t.Fatal(err)
	}
	if stub.gotCaller != adminID || !stub.gotAdmin {
		t.Fatalf("caller = %s, admin = %v", stub.gotCaller, stub.gotAdmin)
	}

	_, err = client.CloseAppeal(ctx, connect.NewRequest(&appealv1.CloseAppealRequest{
		Id: uuid.NewString(),
		Resolutions: []*appealv1.DecisionResolution{{
			DecisionId: decisionID.String(),
			Reason:     "missing outcome",
		}},
	}))
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("code = %v, want invalid argument", connect.CodeOf(err))
	}
}
