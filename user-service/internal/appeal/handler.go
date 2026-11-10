package appeal

import (
	"context"

	"connectrpc.com/connect"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/api"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/api/errs"
	paginationv1 "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/pagination/v1"
	appealv1 "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/user/appeal/v1"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/user/appeal/v1/appealv1connect"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/images"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/pagination"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/evidence"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/handlers/userdto"
	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type Logic interface {
	SubmitAppeal(ctx context.Context, appellantID uuid.UUID, submission Submission) (Appeal, error)
	ListMyAppeals(ctx context.Context, userID uuid.UUID, page pagination.Pagination) (Page, error)
	GetAppeal(ctx context.Context, callerID uuid.UUID, isAdmin bool, id uuid.UUID) (Detail, error)
	SubmitResponse(
		ctx context.Context,
		callerID, appealID uuid.UUID,
		body string,
		evidenceIDs []uuid.UUID,
	) (Appeal, Response, error)
	CreateEvidenceUpload(ctx context.Context, ownerID uuid.UUID, contentType string, sizeBytes int64) (
		uuid.UUID, images.UploadGrant, error)
	GetEvidenceDownload(ctx context.Context, callerID uuid.UUID, isAdmin bool, evidenceID uuid.UUID) (
		evidence.View, error)
	ListAppeals(ctx context.Context, filter Filter, page pagination.Pagination) (Page, error)
	BeginReview(ctx context.Context, actorID, id uuid.UUID) (Appeal, error)
	RequestResponse(ctx context.Context, actorID, id, participantID uuid.UUID) (Appeal, error)
	CloseAppeal(ctx context.Context, actorID, id uuid.UUID, resolutions []Resolution) (Appeal, error)
}

// Handler adapts the appeal proto RPCs to the appeal service.
type Handler struct {
	appealv1connect.UnimplementedAppealServiceHandler
	Logic Logic
}

// Participants

func (h *Handler) SubmitAppeal(
	ctx context.Context,
	req *connect.Request[appealv1.SubmitAppealRequest],
) (*connect.Response[appealv1.SubmitAppealResponse], error) {
	actorID, err := userdto.ActorID(ctx)
	if err != nil {
		return nil, api.ToConnectError(ctx, err)
	}

	submission, err := submissionFromProto(req.Msg)
	if err != nil {
		return nil, err
	}

	appeal, err := h.Logic.SubmitAppeal(ctx, actorID, submission)
	if err != nil {
		return nil, evidence.ToConnectError(ctx, err)
	}
	return connect.NewResponse(&appealv1.SubmitAppealResponse{Appeal: toAppealProto(appeal)}), nil
}

func (h *Handler) ListMyAppeals(
	ctx context.Context,
	req *connect.Request[appealv1.ListMyAppealsRequest],
) (*connect.Response[appealv1.ListMyAppealsResponse], error) {
	actorID, err := userdto.ActorID(ctx)
	if err != nil {
		return nil, api.ToConnectError(ctx, err)
	}
	page, err := newPage(req.Msg.GetPagination())
	if err != nil {
		return nil, api.ToConnectError(ctx, errs.WrapBadRequestError(err, "invalid pagination"))
	}

	result, err := h.Logic.ListMyAppeals(ctx, actorID, page)
	if err != nil {
		return nil, api.ToConnectError(ctx, err)
	}
	return connect.NewResponse(&appealv1.ListMyAppealsResponse{
		Appeals:    toAppealProtos(result.Appeals),
		Pagination: toProtoPageInfo(result.PageInfo),
	}), nil
}

func (h *Handler) GetAppeal(
	ctx context.Context,
	req *connect.Request[appealv1.GetAppealRequest],
) (*connect.Response[appealv1.GetAppealResponse], error) {
	caller, err := userdto.Actor(ctx)
	if err != nil {
		return nil, api.ToConnectError(ctx, err)
	}
	callerID, err := userdto.ActorID(ctx)
	if err != nil {
		return nil, api.ToConnectError(ctx, err)
	}
	id, err := parseID(req.Msg.GetId(), "invalid appeal ID")
	if err != nil {
		return nil, api.ToConnectError(ctx, err)
	}

	detail, err := h.Logic.GetAppeal(ctx, callerID, caller.Admin, id)
	if err != nil {
		return nil, api.ToConnectError(ctx, err)
	}

	responses := make([]*appealv1.AppealResponse, len(detail.Responses))
	for i, response := range detail.Responses {
		responses[i] = toResponseProto(response)
	}
	return connect.NewResponse(&appealv1.GetAppealResponse{
		Appeal:    toAppealProto(detail.Appeal),
		Responses: responses,
	}), nil
}

func (h *Handler) SubmitResponse(
	ctx context.Context,
	req *connect.Request[appealv1.SubmitResponseRequest],
) (*connect.Response[appealv1.SubmitResponseResponse], error) {
	actorID, err := userdto.ActorID(ctx)
	if err != nil {
		return nil, api.ToConnectError(ctx, err)
	}
	appealID, err := parseID(req.Msg.GetAppealId(), "invalid appeal ID")
	if err != nil {
		return nil, api.ToConnectError(ctx, err)
	}

	evidenceIDs, err := evidence.ParseIDs(req.Msg.GetEvidenceIds())
	if err != nil {
		return nil, api.ToConnectError(ctx, err)
	}

	appeal, response, err := h.Logic.SubmitResponse(ctx, actorID, appealID, req.Msg.GetBody(), evidenceIDs)
	if err != nil {
		return nil, evidence.ToConnectError(ctx, err)
	}
	return connect.NewResponse(&appealv1.SubmitResponseResponse{
		Appeal:   toAppealProto(appeal),
		Response: toResponseProto(response),
	}), nil
}

func (h *Handler) CreateEvidenceUpload(
	ctx context.Context,
	req *connect.Request[appealv1.CreateEvidenceUploadRequest],
) (*connect.Response[appealv1.CreateEvidenceUploadResponse], error) {
	actorID, err := userdto.ActorID(ctx)
	if err != nil {
		return nil, api.ToConnectError(ctx, err)
	}

	upload := req.Msg.GetUpload()
	id, grant, err := h.Logic.CreateEvidenceUpload(ctx, actorID, upload.GetContentType(), upload.GetSizeBytes())
	if err != nil {
		return nil, evidence.ToConnectError(ctx, err)
	}
	return connect.NewResponse(&appealv1.CreateEvidenceUploadResponse{Grant: evidence.ToGrantProto(id, grant)}), nil
}

func (h *Handler) GetEvidenceDownload(
	ctx context.Context,
	req *connect.Request[appealv1.GetEvidenceDownloadRequest],
) (*connect.Response[appealv1.GetEvidenceDownloadResponse], error) {
	caller, err := userdto.Actor(ctx)
	if err != nil {
		return nil, api.ToConnectError(ctx, err)
	}
	callerID, err := userdto.ActorID(ctx)
	if err != nil {
		return nil, api.ToConnectError(ctx, err)
	}
	evidenceID, err := parseID(req.Msg.GetEvidenceId(), "invalid evidence ID")
	if err != nil {
		return nil, api.ToConnectError(ctx, err)
	}

	view, err := h.Logic.GetEvidenceDownload(ctx, callerID, caller.Admin, evidenceID)
	if err != nil {
		return nil, evidence.ToConnectError(ctx, err)
	}
	resp := connect.NewResponse(&appealv1.GetEvidenceDownloadResponse{Evidence: evidence.ToProto(view)})
	// The signed URL expires within minutes; caches must not keep it.
	resp.Header().Set("Cache-Control", "no-store")
	return resp, nil
}

// Administrators

func (h *Handler) ListAppeals(
	ctx context.Context,
	req *connect.Request[appealv1.ListAppealsRequest],
) (*connect.Response[appealv1.ListAppealsResponse], error) {
	filter, err := filterFromProto(req.Msg)
	if err != nil {
		return nil, api.ToConnectError(ctx, err)
	}
	page, err := newPage(req.Msg.GetPagination())
	if err != nil {
		return nil, api.ToConnectError(ctx, errs.WrapBadRequestError(err, "invalid pagination"))
	}

	result, err := h.Logic.ListAppeals(ctx, filter, page)
	if err != nil {
		return nil, api.ToConnectError(ctx, err)
	}
	return connect.NewResponse(&appealv1.ListAppealsResponse{
		Appeals:    toAppealProtos(result.Appeals),
		Pagination: toProtoPageInfo(result.PageInfo),
	}), nil
}

func (h *Handler) BeginReview(
	ctx context.Context,
	req *connect.Request[appealv1.BeginReviewRequest],
) (*connect.Response[appealv1.BeginReviewResponse], error) {
	actorID, id, err := adminTarget(ctx, req.Msg.GetId())
	if err != nil {
		return nil, api.ToConnectError(ctx, err)
	}

	appeal, err := h.Logic.BeginReview(ctx, actorID, id)
	if err != nil {
		return nil, api.ToConnectError(ctx, err)
	}
	return connect.NewResponse(&appealv1.BeginReviewResponse{Appeal: toAppealProto(appeal)}), nil
}

func (h *Handler) RequestResponse(
	ctx context.Context,
	req *connect.Request[appealv1.RequestResponseRequest],
) (*connect.Response[appealv1.RequestResponseResponse], error) {
	actorID, id, err := adminTarget(ctx, req.Msg.GetId())
	if err != nil {
		return nil, api.ToConnectError(ctx, err)
	}
	participantID, err := parseID(req.Msg.GetParticipantId(), "invalid participant ID")
	if err != nil {
		return nil, api.ToConnectError(ctx, err)
	}

	appeal, err := h.Logic.RequestResponse(ctx, actorID, id, participantID)
	if err != nil {
		return nil, api.ToConnectError(ctx, err)
	}
	return connect.NewResponse(&appealv1.RequestResponseResponse{Appeal: toAppealProto(appeal)}), nil
}

func (h *Handler) CloseAppeal(
	ctx context.Context,
	req *connect.Request[appealv1.CloseAppealRequest],
) (*connect.Response[appealv1.CloseAppealResponse], error) {
	actorID, id, err := adminTarget(ctx, req.Msg.GetId())
	if err != nil {
		return nil, api.ToConnectError(ctx, err)
	}
	resolutions := make([]Resolution, len(req.Msg.GetResolutions()))
	for i, resolution := range req.Msg.GetResolutions() {
		decisionID, err := parseID(resolution.GetDecisionId(), "invalid decision ID")
		if err != nil {
			return nil, api.ToConnectError(ctx, err)
		}
		outcome, ok := outcomes[resolution.GetOutcome()]
		if !ok {
			return nil, api.ToConnectError(ctx, errs.NewBadRequestError("invalid decision outcome"))
		}
		resolutions[i] = Resolution{DecisionID: decisionID, Outcome: outcome, Reason: resolution.GetReason()}
	}

	appeal, err := h.Logic.CloseAppeal(ctx, actorID, id, resolutions)
	if err != nil {
		return nil, api.ToConnectError(ctx, err)
	}
	return connect.NewResponse(&appealv1.CloseAppealResponse{Appeal: toAppealProto(appeal)}), nil
}

// Request conversion

// submissionFromProto returns Connect errors directly, so an appeal of a credit
// adjustment reports unimplemented rather than a bad request.
func submissionFromProto(msg *appealv1.SubmitAppealRequest) (Submission, error) {
	key, err := parseID(msg.GetIdempotencyKey(), "invalid idempotency key")
	if err != nil {
		return Submission{}, connect.NewError(connect.CodeInvalidArgument, err)
	}
	submission := Submission{IdempotencyKey: key, Reason: msg.GetReason()}
	if submission.EvidenceIDs, err = evidence.ParseIDs(msg.GetEvidenceIds()); err != nil {
		return Submission{}, connect.NewError(connect.CodeInvalidArgument, err)
	}
	if msg.RequestId != nil {
		if submission.RequestID, err = parseID(msg.GetRequestId(), "invalid request ID"); err != nil {
			return Submission{}, connect.NewError(connect.CodeInvalidArgument, err)
		}
	}

	for _, decision := range msg.GetDecisions() {
		var kind Kind
		var raw string
		switch target := decision.GetTarget().(type) {
		case *appealv1.AppealedDecision_ReportId:
			kind, raw = KindReportOutcome, target.ReportId
		case *appealv1.AppealedDecision_WarningId:
			kind, raw = KindWarning, target.WarningId
		case *appealv1.AppealedDecision_RemovedReviewId:
			kind, raw = KindReviewRemoval, target.RemovedReviewId
		case *appealv1.AppealedDecision_RetainedReviewId:
			kind, raw = KindReviewRetention, target.RetainedReviewId
		case *appealv1.AppealedDecision_CreditEntryId:
			return Submission{}, connect.NewError(connect.CodeUnimplemented,
				errs.NewBadRequestError("credit adjustments cannot be appealed yet"))
		default:
			return Submission{}, connect.NewError(connect.CodeInvalidArgument,
				errs.NewBadRequestError("every appealed decision needs a target"))
		}
		id, err := parseID(raw, "invalid decision target")
		if err != nil {
			return Submission{}, connect.NewError(connect.CodeInvalidArgument, err)
		}
		submission.Targets = append(submission.Targets, Target{Kind: kind, TargetID: id})
	}
	return submission, nil
}

func adminTarget(ctx context.Context, rawID string) (uuid.UUID, uuid.UUID, error) {
	actorID, err := userdto.ActorID(ctx)
	if err != nil {
		return uuid.Nil, uuid.Nil, err
	}
	id, err := parseID(rawID, "invalid appeal ID")
	if err != nil {
		return uuid.Nil, uuid.Nil, err
	}
	return actorID, id, nil
}

func filterFromProto(msg *appealv1.ListAppealsRequest) (Filter, error) {
	var filter Filter
	if msg.Status != nil {
		status, ok := statuses[msg.GetStatus()]
		if !ok {
			return Filter{}, errs.NewBadRequestError("invalid appeal status")
		}
		filter.Status = status
	}
	if msg.AppellantId != nil {
		id, err := parseID(msg.GetAppellantId(), "invalid appellant ID")
		if err != nil {
			return Filter{}, err
		}
		filter.AppellantID = id
	}
	return filter, nil
}

func parseID(value, message string) (uuid.UUID, error) {
	id, err := uuid.Parse(value)
	if err != nil || id == uuid.Nil {
		return uuid.Nil, errs.NewBadRequestError(message)
	}
	return id, nil
}

// newPage normalizes the optional proto pagination into the shared model.
func newPage(p *paginationv1.Pagination) (pagination.Pagination, error) {
	if p == nil {
		return pagination.New(0, 0)
	}
	return pagination.New(p.GetPage(), p.GetPageSize())
}

// Enum mapping

var statuses = map[appealv1.AppealStatus]Status{
	appealv1.AppealStatus_APPEAL_STATUS_OPEN:              StatusOpen,
	appealv1.AppealStatus_APPEAL_STATUS_UNDER_REVIEW:      StatusUnderReview,
	appealv1.AppealStatus_APPEAL_STATUS_AWAITING_RESPONSE: StatusAwaitingResponse,
	appealv1.AppealStatus_APPEAL_STATUS_CLOSED:            StatusClosed,
}

var outcomes = map[appealv1.DecisionOutcome]Outcome{
	appealv1.DecisionOutcome_DECISION_OUTCOME_UPHELD:     OutcomeUpheld,
	appealv1.DecisionOutcome_DECISION_OUTCOME_OVERTURNED: OutcomeOverturned,
}

// toProtoEnum returns the protobuf key for a domain value, or the zero
// (unspecified) enum when none matches.
func toProtoEnum[P comparable, D comparable](values map[P]D, value D) P {
	for key, candidate := range values {
		if candidate == value {
			return key
		}
	}
	var unspecified P
	return unspecified
}

// Response conversion

func toAppealProtos(appeals []Appeal) []*appealv1.Appeal {
	out := make([]*appealv1.Appeal, len(appeals))
	for i, appeal := range appeals {
		out[i] = toAppealProto(appeal)
	}
	return out
}

func toAppealProto(appeal Appeal) *appealv1.Appeal {
	out := &appealv1.Appeal{
		Id:            appeal.ID.String(),
		AppellantId:   appeal.AppellantID.String(),
		AppellantName: appeal.AppellantName,
		Reason:        appeal.Reason,
		Status:        toProtoEnum(statuses, appeal.Status),
		CloserName:    appeal.CloserName,
		CreatedAt:     timestamppb.New(appeal.CreatedAt),
		UpdatedAt:     timestamppb.New(appeal.UpdatedAt),
		Evidence:      evidence.ToProtos(appeal.Evidence),
	}
	if appeal.RequestID != uuid.Nil {
		requestID := appeal.RequestID.String()
		out.RequestId = &requestID
	}
	if appeal.AwaitedUserID != uuid.Nil {
		out.AwaitedUserId = appeal.AwaitedUserID.String()
	}
	if !appeal.ClosedAt.IsZero() {
		out.ClosedAt = timestamppb.New(appeal.ClosedAt)
	}
	if appeal.ClosedBy != uuid.Nil {
		out.ClosedBy = appeal.ClosedBy.String()
	}
	for _, decision := range appeal.Decisions {
		out.Decisions = append(out.Decisions, toDecisionProto(decision))
	}
	return out
}

func toDecisionProto(decision Decision) *appealv1.Decision {
	out := &appealv1.Decision{
		Id:              decision.ID.String(),
		CounterpartName: decision.CounterpartName,
		Outcome:         toProtoEnum(outcomes, decision.Outcome),
		OutcomeReason:   decision.OutcomeReason,
	}
	if decision.CounterpartID != uuid.Nil {
		out.CounterpartId = decision.CounterpartID.String()
	}

	target := decision.Target.TargetID.String()
	switch decision.Target.Kind {
	case KindReportOutcome:
		out.Target = &appealv1.Decision_ReportId{ReportId: target}
	case KindWarning:
		out.Target = &appealv1.Decision_WarningId{WarningId: target}
	case KindReviewRemoval:
		out.Target = &appealv1.Decision_RemovedReviewId{RemovedReviewId: target}
	case KindReviewRetention:
		out.Target = &appealv1.Decision_RetainedReviewId{RetainedReviewId: target}
	case KindCreditAdjustment:
		out.Target = &appealv1.Decision_CreditEntryId{CreditEntryId: target}
	}
	return out
}

func toResponseProto(response Response) *appealv1.AppealResponse {
	return &appealv1.AppealResponse{
		Id:            response.ID.String(),
		AppealId:      response.AppealID.String(),
		ResponderId:   response.ResponderID.String(),
		ResponderName: response.ResponderName,
		Body:          response.Body,
		CreatedAt:     timestamppb.New(response.CreatedAt),
		Evidence:      evidence.ToProtos(response.Evidence),
	}
}

func toProtoPageInfo(info pagination.PageInfo) *paginationv1.PageInfo {
	return &paginationv1.PageInfo{
		Page:       info.Page(),
		PageSize:   info.PageSize(),
		TotalItems: info.TotalItems(),
		TotalPages: info.TotalPages(),
	}
}
