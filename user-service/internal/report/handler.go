package report

import (
	"context"

	"connectrpc.com/connect"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/api"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/api/errs"
	paginationv1 "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/pagination/v1"
	reportv1 "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/user/report/v1"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/user/report/v1/reportv1connect"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/images"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/pagination"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/evidence"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/handlers/userdto"
	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type Logic interface {
	SubmitReport(ctx context.Context, reporterID uuid.UUID, submission Submission) (Report, error)
	ListMyReports(ctx context.Context, userID uuid.UUID, page pagination.Pagination) (Page, error)
	GetReport(ctx context.Context, callerID uuid.UUID, isAdmin bool, id uuid.UUID) (Detail, error)
	SubmitResponse(
		ctx context.Context,
		callerID, reportID uuid.UUID,
		body string,
		evidenceIDs []uuid.UUID,
	) (Report, Response, error)
	CreateEvidenceUpload(ctx context.Context, ownerID uuid.UUID, contentType string, sizeBytes int64) (
		uuid.UUID, images.UploadGrant, error)
	GetEvidenceDownload(ctx context.Context, callerID uuid.UUID, isAdmin bool, evidenceID uuid.UUID) (
		evidence.View, error)
	ListReports(ctx context.Context, filter Filter, page pagination.Pagination) (Page, error)
	BeginReview(ctx context.Context, actorID, id uuid.UUID) (Report, error)
	RequestResponse(ctx context.Context, actorID, id uuid.UUID) (Report, error)
	CloseReport(ctx context.Context, actorID, id uuid.UUID, outcome Outcome, reason string) (Report, error)
}

// Handler adapts the report proto RPCs to the report service.
type Handler struct {
	reportv1connect.UnimplementedReportServiceHandler
	Logic Logic
}

// Participants

func (h *Handler) SubmitReport(
	ctx context.Context,
	req *connect.Request[reportv1.SubmitReportRequest],
) (*connect.Response[reportv1.SubmitReportResponse], error) {
	actorID, err := userdto.ActorID(ctx)
	if err != nil {
		return nil, api.ToConnectError(ctx, err)
	}

	target, err := targetFromProto(req.Msg)
	if err != nil {
		return nil, api.ToConnectError(ctx, err)
	}
	category, ok := categoryFromProto(req.Msg.GetCategory())
	if !ok {
		return nil, api.ToConnectError(ctx, errs.NewBadRequestError("invalid report category"))
	}

	evidenceIDs, err := evidence.ParseIDs(req.Msg.GetEvidenceIds())
	if err != nil {
		return nil, api.ToConnectError(ctx, err)
	}

	report, err := h.Logic.SubmitReport(ctx, actorID, Submission{
		Target:      target,
		Category:    category,
		Description: req.Msg.GetDescription(),
		EvidenceIDs: evidenceIDs,
	})
	if err != nil {
		return nil, evidence.ToConnectError(ctx, err)
	}
	return connect.NewResponse(&reportv1.SubmitReportResponse{Report: toReportProto(report)}), nil
}

func (h *Handler) ListMyReports(
	ctx context.Context,
	req *connect.Request[reportv1.ListMyReportsRequest],
) (*connect.Response[reportv1.ListMyReportsResponse], error) {
	actorID, err := userdto.ActorID(ctx)
	if err != nil {
		return nil, api.ToConnectError(ctx, err)
	}
	page, err := newPage(req.Msg.GetPagination())
	if err != nil {
		return nil, api.ToConnectError(ctx, errs.WrapBadRequestError(err, "invalid pagination"))
	}

	result, err := h.Logic.ListMyReports(ctx, actorID, page)
	if err != nil {
		return nil, api.ToConnectError(ctx, err)
	}
	return connect.NewResponse(&reportv1.ListMyReportsResponse{
		Reports:    toReportProtos(result.Reports),
		Pagination: toProtoPageInfo(result.PageInfo),
	}), nil
}

func (h *Handler) GetReport(
	ctx context.Context,
	req *connect.Request[reportv1.GetReportRequest],
) (*connect.Response[reportv1.GetReportResponse], error) {
	caller, err := userdto.Actor(ctx)
	if err != nil {
		return nil, api.ToConnectError(ctx, err)
	}
	callerID, err := userdto.ActorID(ctx)
	if err != nil {
		return nil, api.ToConnectError(ctx, err)
	}
	id, err := parseID(req.Msg.GetId(), "invalid report ID")
	if err != nil {
		return nil, api.ToConnectError(ctx, err)
	}

	detail, err := h.Logic.GetReport(ctx, callerID, caller.Admin, id)
	if err != nil {
		return nil, api.ToConnectError(ctx, err)
	}

	responses := make([]*reportv1.ReportResponse, len(detail.Responses))
	for i, response := range detail.Responses {
		responses[i] = toResponseProto(response)
	}
	return connect.NewResponse(&reportv1.GetReportResponse{
		Report:    toReportProto(detail.Report),
		Responses: responses,
	}), nil
}

func (h *Handler) SubmitResponse(
	ctx context.Context,
	req *connect.Request[reportv1.SubmitResponseRequest],
) (*connect.Response[reportv1.SubmitResponseResponse], error) {
	actorID, err := userdto.ActorID(ctx)
	if err != nil {
		return nil, api.ToConnectError(ctx, err)
	}
	reportID, err := parseID(req.Msg.GetReportId(), "invalid report ID")
	if err != nil {
		return nil, api.ToConnectError(ctx, err)
	}

	evidenceIDs, err := evidence.ParseIDs(req.Msg.GetEvidenceIds())
	if err != nil {
		return nil, api.ToConnectError(ctx, err)
	}

	report, response, err := h.Logic.SubmitResponse(ctx, actorID, reportID, req.Msg.GetBody(), evidenceIDs)
	if err != nil {
		return nil, evidence.ToConnectError(ctx, err)
	}
	return connect.NewResponse(&reportv1.SubmitResponseResponse{
		Report:   toReportProto(report),
		Response: toResponseProto(response),
	}), nil
}

func (h *Handler) CreateEvidenceUpload(
	ctx context.Context,
	req *connect.Request[reportv1.CreateEvidenceUploadRequest],
) (*connect.Response[reportv1.CreateEvidenceUploadResponse], error) {
	actorID, err := userdto.ActorID(ctx)
	if err != nil {
		return nil, api.ToConnectError(ctx, err)
	}

	upload := req.Msg.GetUpload()
	id, grant, err := h.Logic.CreateEvidenceUpload(ctx, actorID, upload.GetContentType(), upload.GetSizeBytes())
	if err != nil {
		return nil, evidence.ToConnectError(ctx, err)
	}
	return connect.NewResponse(&reportv1.CreateEvidenceUploadResponse{Grant: evidence.ToGrantProto(id, grant)}), nil
}

func (h *Handler) GetEvidenceDownload(
	ctx context.Context,
	req *connect.Request[reportv1.GetEvidenceDownloadRequest],
) (*connect.Response[reportv1.GetEvidenceDownloadResponse], error) {
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
	resp := connect.NewResponse(&reportv1.GetEvidenceDownloadResponse{Evidence: evidence.ToProto(view)})
	// The signed URL expires within minutes; caches must not keep it.
	resp.Header().Set("Cache-Control", "no-store")
	return resp, nil
}

// Administrators

func (h *Handler) ListReports(
	ctx context.Context,
	req *connect.Request[reportv1.ListReportsRequest],
) (*connect.Response[reportv1.ListReportsResponse], error) {
	filter, err := filterFromProto(req.Msg)
	if err != nil {
		return nil, api.ToConnectError(ctx, err)
	}
	page, err := newPage(req.Msg.GetPagination())
	if err != nil {
		return nil, api.ToConnectError(ctx, errs.WrapBadRequestError(err, "invalid pagination"))
	}

	result, err := h.Logic.ListReports(ctx, filter, page)
	if err != nil {
		return nil, api.ToConnectError(ctx, err)
	}
	return connect.NewResponse(&reportv1.ListReportsResponse{
		Reports:    toReportProtos(result.Reports),
		Pagination: toProtoPageInfo(result.PageInfo),
	}), nil
}

func (h *Handler) BeginReview(
	ctx context.Context,
	req *connect.Request[reportv1.BeginReviewRequest],
) (*connect.Response[reportv1.BeginReviewResponse], error) {
	actorID, id, err := adminTarget(ctx, req.Msg.GetId())
	if err != nil {
		return nil, api.ToConnectError(ctx, err)
	}

	report, err := h.Logic.BeginReview(ctx, actorID, id)
	if err != nil {
		return nil, api.ToConnectError(ctx, err)
	}
	return connect.NewResponse(&reportv1.BeginReviewResponse{Report: toReportProto(report)}), nil
}

func (h *Handler) RequestResponse(
	ctx context.Context,
	req *connect.Request[reportv1.RequestResponseRequest],
) (*connect.Response[reportv1.RequestResponseResponse], error) {
	actorID, id, err := adminTarget(ctx, req.Msg.GetId())
	if err != nil {
		return nil, api.ToConnectError(ctx, err)
	}

	report, err := h.Logic.RequestResponse(ctx, actorID, id)
	if err != nil {
		return nil, api.ToConnectError(ctx, err)
	}
	return connect.NewResponse(&reportv1.RequestResponseResponse{Report: toReportProto(report)}), nil
}

func (h *Handler) CloseReport(
	ctx context.Context,
	req *connect.Request[reportv1.CloseReportRequest],
) (*connect.Response[reportv1.CloseReportResponse], error) {
	actorID, id, err := adminTarget(ctx, req.Msg.GetId())
	if err != nil {
		return nil, api.ToConnectError(ctx, err)
	}
	outcome, ok := outcomeFromProto(req.Msg.GetOutcome())
	if !ok {
		return nil, api.ToConnectError(ctx, errs.NewBadRequestError("invalid report outcome"))
	}

	report, err := h.Logic.CloseReport(ctx, actorID, id, outcome, req.Msg.GetReason())
	if err != nil {
		return nil, api.ToConnectError(ctx, err)
	}
	return connect.NewResponse(&reportv1.CloseReportResponse{Report: toReportProto(report)}), nil
}

// Request conversion

func adminTarget(ctx context.Context, rawID string) (uuid.UUID, uuid.UUID, error) {
	actorID, err := userdto.ActorID(ctx)
	if err != nil {
		return uuid.Nil, uuid.Nil, err
	}
	id, err := parseID(rawID, "invalid report ID")
	if err != nil {
		return uuid.Nil, uuid.Nil, err
	}
	return actorID, id, nil
}

func targetFromProto(msg *reportv1.SubmitReportRequest) (Target, error) {
	switch target := msg.GetTarget().(type) {
	case *reportv1.SubmitReportRequest_ReviewId:
		reviewID, err := parseID(target.ReviewId, "invalid review ID")
		if err != nil {
			return Target{}, err
		}
		return ReviewTarget(reviewID), nil
	case *reportv1.SubmitReportRequest_Order:
		orderID, err := parseID(target.Order.GetOrderId(), "invalid order ID")
		if err != nil {
			return Target{}, err
		}
		subjectID, err := parseID(target.Order.GetSubjectId(), "invalid subject ID")
		if err != nil {
			return Target{}, err
		}
		return OrderTarget(orderID, subjectID), nil
	default:
		return Target{}, errs.NewBadRequestError("a report must target a review or an order")
	}
}

func filterFromProto(msg *reportv1.ListReportsRequest) (Filter, error) {
	var filter Filter
	var err error

	if msg.Status != nil {
		var ok bool
		if filter.Status, ok = statusFromProto(msg.GetStatus()); !ok {
			return Filter{}, errs.NewBadRequestError("invalid report status")
		}
	}
	if msg.SubjectId != nil {
		if filter.SubjectID, err = parseID(msg.GetSubjectId(), "invalid subject ID"); err != nil {
			return Filter{}, err
		}
	}
	if msg.ReviewId != nil {
		if filter.ReviewID, err = parseID(msg.GetReviewId(), "invalid review ID"); err != nil {
			return Filter{}, err
		}
	}
	if msg.OrderId != nil {
		if filter.OrderID, err = parseID(msg.GetOrderId(), "invalid order ID"); err != nil {
			return Filter{}, err
		}
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

var categories = map[reportv1.ReportCategory]Category{
	reportv1.ReportCategory_REPORT_CATEGORY_HARASSMENT:            CategoryHarassment,
	reportv1.ReportCategory_REPORT_CATEGORY_FRAUD:                 CategoryFraud,
	reportv1.ReportCategory_REPORT_CATEGORY_NO_SHOW:               CategoryNoShow,
	reportv1.ReportCategory_REPORT_CATEGORY_INAPPROPRIATE_CONTENT: CategoryInappropriateContent,
	reportv1.ReportCategory_REPORT_CATEGORY_OTHER:                 CategoryOther,
}

var statuses = map[reportv1.ReportStatus]Status{
	reportv1.ReportStatus_REPORT_STATUS_OPEN:              StatusOpen,
	reportv1.ReportStatus_REPORT_STATUS_UNDER_REVIEW:      StatusUnderReview,
	reportv1.ReportStatus_REPORT_STATUS_AWAITING_RESPONSE: StatusAwaitingResponse,
	reportv1.ReportStatus_REPORT_STATUS_CLOSED:            StatusClosed,
}

var outcomes = map[reportv1.ReportOutcome]Outcome{
	reportv1.ReportOutcome_REPORT_OUTCOME_NO_ACTION:      OutcomeNoAction,
	reportv1.ReportOutcome_REPORT_OUTCOME_WARNING:        OutcomeWarning,
	reportv1.ReportOutcome_REPORT_OUTCOME_SUSPENSION:     OutcomeSuspension,
	reportv1.ReportOutcome_REPORT_OUTCOME_REVIEW_REMOVAL: OutcomeReviewRemoval,
}

func categoryFromProto(category reportv1.ReportCategory) (Category, bool) {
	value, ok := categories[category]
	return value, ok
}

func statusFromProto(status reportv1.ReportStatus) (Status, bool) {
	value, ok := statuses[status]
	return value, ok
}

func outcomeFromProto(outcome reportv1.ReportOutcome) (Outcome, bool) {
	value, ok := outcomes[outcome]
	return value, ok
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

func toReportProtos(reports []Report) []*reportv1.Report {
	out := make([]*reportv1.Report, len(reports))
	for i, report := range reports {
		out[i] = toReportProto(report)
	}
	return out
}

func toReportProto(report Report) *reportv1.Report {
	out := &reportv1.Report{
		Id:            report.ID.String(),
		ReporterId:    report.ReporterID.String(),
		ReporterName:  report.ReporterName,
		SubjectId:     report.SubjectID.String(),
		SubjectName:   report.SubjectName,
		Category:      toProtoEnum(categories, report.Category),
		Description:   report.Description,
		Status:        toProtoEnum(statuses, report.Status),
		Outcome:       toProtoEnum(outcomes, report.Outcome),
		OutcomeReason: report.OutcomeReason,
		CloserName:    report.CloserName,
		CreatedAt:     timestamppb.New(report.CreatedAt),
		UpdatedAt:     timestamppb.New(report.UpdatedAt),
		Evidence:      evidence.ToProtos(report.Evidence),
	}
	if report.ReviewID != uuid.Nil {
		out.Target = &reportv1.Report_ReviewId{ReviewId: report.ReviewID.String()}
	} else {
		out.Target = &reportv1.Report_OrderId{OrderId: report.OrderID.String()}
	}
	if !report.ClosedAt.IsZero() {
		out.ClosedAt = timestamppb.New(report.ClosedAt)
	}
	if report.ClosedBy != uuid.Nil {
		out.ClosedBy = report.ClosedBy.String()
	}
	return out
}

func toResponseProto(response Response) *reportv1.ReportResponse {
	return &reportv1.ReportResponse{
		Id:            response.ID.String(),
		ReportId:      response.ReportID.String(),
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
