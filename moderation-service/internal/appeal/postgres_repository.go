package appeal

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/AY2627S1-CS3219-P1/FoC/moderation-service/internal/database/appealdb"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

type PostgresRepository struct {
	queries *appealdb.Queries
}

func NewPostgresRepository(queries *appealdb.Queries) *PostgresRepository {
	return &PostgresRepository{queries: queries}
}

var _ Repository = (*PostgresRepository)(nil)

// Submission

func (r *PostgresRepository) AppealByKey(
	ctx context.Context,
	q *appealdb.Queries,
	appellantID, key uuid.UUID,
) (ExistingAppeal, error) {
	row, err := q.AppealByIdempotencyKey(ctx, appealdb.AppealByIdempotencyKeyParams{
		AppellantID:    appellantID,
		IdempotencyKey: key,
	})
	if errors.Is(err, sql.ErrNoRows) {
		return ExistingAppeal{}, errAppealNotFound
	}
	if err != nil {
		return ExistingAppeal{}, fmt.Errorf("find appeal by idempotency key: %w", err)
	}
	return ExistingAppeal{ID: row.ID, RequestHash: row.RequestHash}, nil
}

func (r *PostgresRepository) InsertAppeal(ctx context.Context, q *appealdb.Queries, input AppealInput) (bool, error) {
	rows, err := q.InsertAppeal(ctx, appealdb.InsertAppealParams{
		ID:             input.ID,
		AppellantID:    input.AppellantID,
		RequestID:      nullUUID(input.RequestID),
		Reason:         input.Reason,
		IdempotencyKey: input.IdempotencyKey,
		RequestHash:    input.RequestHash,
	})
	if err != nil {
		return false, mapAppealError("insert appeal", err)
	}
	return rows == 1, nil
}

func (r *PostgresRepository) InsertDecision(ctx context.Context, q *appealdb.Queries, input DecisionInput) error {
	params := appealdb.InsertDecisionParams{
		ID:            input.ID,
		AppealID:      input.AppealID,
		Kind:          string(input.Target.Kind),
		CounterpartID: nullUUID(input.CounterpartID),
	}
	target := nullUUID(input.Target.TargetID)
	switch input.Target.Kind {
	case KindReportOutcome:
		params.ReportID = target
	case KindWarning:
		params.WarningID = target
	default:
		params.ReviewID = target
	}
	return mapAppealError("insert appeal decision", q.InsertDecision(ctx, params))
}

func (r *PostgresRepository) ReportFacts(ctx context.Context, q *appealdb.Queries, id uuid.UUID) (ReportFacts, error) {
	row, err := q.ReportForAppeal(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return ReportFacts{}, errTargetNotFound
	}
	if err != nil {
		return ReportFacts{}, fmt.Errorf("read appealed report: %w", err)
	}
	return ReportFacts{
		ReporterID: row.ReporterID,
		SubjectID:  row.SubjectID,
		ReviewID:   row.ReviewID.UUID,
		Status:     row.Status,
		Outcome:    ReportOutcome(row.Outcome.String),
		RequestID:  row.RequestID,
	}, nil
}

func (r *PostgresRepository) ReviewRetainedByReport(
	ctx context.Context,
	q *appealdb.Queries,
	reviewID, reporterID uuid.UUID,
) (bool, error) {
	retained, err := q.ReviewRetainedByReport(ctx, appealdb.ReviewRetainedByReportParams{
		ReviewID:   reviewID,
		ReporterID: reporterID,
	})
	if err != nil {
		return false, fmt.Errorf("read kept review: %w", err)
	}
	return retained, nil
}

// Lifecycle

func (r *PostgresRepository) LockAppeal(ctx context.Context, q *appealdb.Queries, id uuid.UUID) (LockedAppeal, error) {
	row, err := q.LockAppeal(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return LockedAppeal{}, errAppealNotFound
	}
	if err != nil {
		return LockedAppeal{}, fmt.Errorf("lock appeal: %w", err)
	}
	return LockedAppeal{
		AppellantID:   row.AppellantID,
		Status:        Status(row.Status),
		AwaitedUserID: row.AwaitedUserID.UUID,
	}, nil
}

func (r *PostgresRepository) LockedDecisions(
	ctx context.Context,
	q *appealdb.Queries,
	appealID uuid.UUID,
) ([]LockedDecision, error) {
	rows, err := q.LockedDecisions(ctx, appealID)
	if err != nil {
		return nil, fmt.Errorf("read appeal decisions: %w", err)
	}

	out := make([]LockedDecision, len(rows))
	for i, row := range rows {
		out[i] = LockedDecision{
			ID:              row.ID,
			Target:          targetOf(row.Kind, row.ReportID, row.WarningID, row.ReviewID),
			CounterpartID:   row.CounterpartID.UUID,
			ReportOutcome:   ReportOutcome(row.ReportOutcome.String),
			ReportSubjectID: row.ReportSubjectID.UUID,
			ReportReviewID:  row.ReportReviewID.UUID,
		}
	}
	return out, nil
}

func (r *PostgresRepository) TransitionAppeal(
	ctx context.Context,
	q *appealdb.Queries,
	id uuid.UUID,
	from, to Status,
	awaited uuid.UUID,
) error {
	rows, err := q.TransitionAppeal(ctx, appealdb.TransitionAppealParams{
		ToStatus:      string(to),
		AwaitedUserID: nullUUID(awaited),
		ID:            id,
		FromStatus:    string(from),
	})
	if err != nil {
		return mapAppealError("transition appeal", err)
	}
	if rows == 0 {
		return errAppealStateChanged
	}
	return nil
}

func (r *PostgresRepository) CloseAppeal(ctx context.Context, q *appealdb.Queries, id, closedBy uuid.UUID) error {
	rows, err := q.CloseAppeal(ctx, appealdb.CloseAppealParams{ClosedBy: closedBy, ID: id})
	if err != nil {
		return mapAppealError("close appeal", err)
	}
	if rows == 0 {
		return errAppealStateChanged
	}
	return nil
}

func (r *PostgresRepository) ResolveDecision(
	ctx context.Context,
	q *appealdb.Queries,
	appealID uuid.UUID,
	resolution Resolution,
) error {
	rows, err := q.ResolveDecision(ctx, appealdb.ResolveDecisionParams{
		Outcome:       string(resolution.Outcome),
		OutcomeReason: resolution.Reason,
		ID:            resolution.DecisionID,
		AppealID:      appealID,
	})
	if err != nil {
		return mapAppealError("resolve appeal decision", err)
	}
	if rows == 0 {
		return errAppealStateChanged
	}
	return nil
}

func (r *PostgresRepository) InsertResponse(ctx context.Context, q *appealdb.Queries, input ResponseInput) error {
	err := q.InsertAppealResponse(ctx, appealdb.InsertAppealResponseParams{
		ID:          input.ID,
		AppealID:    input.AppealID,
		ResponderID: input.ResponderID,
		Body:        input.Body,
	})
	return mapAppealError("insert appeal response", err)
}

// Reads

func (r *PostgresRepository) GetAppeal(ctx context.Context, id uuid.UUID) (Appeal, error) {
	row, err := r.queries.GetAppeal(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return Appeal{}, errAppealNotFound
	}
	if err != nil {
		return Appeal{}, fmt.Errorf("get appeal: %w", err)
	}

	appeals, err := r.withDecisions(ctx, []appealdb.GetAppealRow{row})
	if err != nil {
		return Appeal{}, err
	}
	return appeals[0], nil
}

func (r *PostgresRepository) ListResponses(ctx context.Context, appealID uuid.UUID) ([]Response, error) {
	rows, err := r.queries.ListAppealResponses(ctx, appealID)
	if err != nil {
		return nil, fmt.Errorf("list appeal responses: %w", err)
	}

	out := make([]Response, len(rows))
	for i, row := range rows {
		out[i] = Response{
			ID:          row.ID,
			AppealID:    row.AppealID,
			ResponderID: row.ResponderID,
			Body:        row.Body,
			CreatedAt:   row.CreatedAt,
		}
	}
	return out, nil
}

func (r *PostgresRepository) ListAppealsInvolving(
	ctx context.Context,
	userID uuid.UUID,
	offset, limit int64,
) ([]Appeal, error) {
	rows, err := r.queries.ListAppealsInvolving(ctx, appealdb.ListAppealsInvolvingParams{
		UserID:     userID,
		PageOffset: offset,
		PageLimit:  limit,
	})
	if err != nil {
		return nil, fmt.Errorf("list appeals involving user: %w", err)
	}

	converted := make([]appealdb.GetAppealRow, len(rows))
	for i, row := range rows {
		converted[i] = appealdb.GetAppealRow(row)
	}
	return r.withDecisions(ctx, converted)
}

func (r *PostgresRepository) CountAppealsInvolving(ctx context.Context, userID uuid.UUID) (int64, error) {
	count, err := r.queries.CountAppealsInvolving(ctx, userID)
	if err != nil {
		return 0, fmt.Errorf("count appeals involving user: %w", err)
	}
	return count, nil
}

func (r *PostgresRepository) ListAppeals(ctx context.Context, filter Filter, offset, limit int64) ([]Appeal, error) {
	rows, err := r.queries.ListAppeals(ctx, appealdb.ListAppealsParams{
		Status:      nullStatus(filter.Status),
		AppellantID: nullUUID(filter.AppellantID),
		PageOffset:  offset,
		PageLimit:   limit,
	})
	if err != nil {
		return nil, fmt.Errorf("list appeals: %w", err)
	}

	converted := make([]appealdb.GetAppealRow, len(rows))
	for i, row := range rows {
		converted[i] = appealdb.GetAppealRow(row)
	}
	return r.withDecisions(ctx, converted)
}

func (r *PostgresRepository) CountAppeals(ctx context.Context, filter Filter) (int64, error) {
	count, err := r.queries.CountAppeals(ctx, appealdb.CountAppealsParams{
		Status:      nullStatus(filter.Status),
		AppellantID: nullUUID(filter.AppellantID),
	})
	if err != nil {
		return 0, fmt.Errorf("count appeals: %w", err)
	}
	return count, nil
}

// withDecisions converts appeal rows and attaches their decisions with one
// query for the whole page.
func (r *PostgresRepository) withDecisions(ctx context.Context, rows []appealdb.GetAppealRow) ([]Appeal, error) {
	appeals := make([]Appeal, len(rows))
	ids := make([]uuid.UUID, len(rows))
	index := make(map[uuid.UUID]int, len(rows))
	for i, row := range rows {
		appeals[i] = toAppeal(row)
		ids[i] = row.ID
		index[row.ID] = i
	}
	if len(rows) == 0 {
		return appeals, nil
	}

	decisions, err := r.queries.ListDecisions(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("list appeal decisions: %w", err)
	}
	for _, row := range decisions {
		i := index[row.AppealID]
		appeals[i].Decisions = append(appeals[i].Decisions, Decision{
			ID:            row.ID,
			AppealID:      row.AppealID,
			Target:        targetOf(row.Kind, row.ReportID, row.WarningID, row.ReviewID),
			CounterpartID: row.CounterpartID.UUID,
			Outcome:       Outcome(row.Outcome.String),
			OutcomeReason: row.OutcomeReason.String,
		})
	}
	return appeals, nil
}

// Conversion

func toAppeal(row appealdb.GetAppealRow) Appeal {
	return Appeal{
		ID:            row.ID,
		AppellantID:   row.AppellantID,
		RequestID:     row.RequestID.UUID,
		Reason:        row.Reason,
		Status:        Status(row.Status),
		AwaitedUserID: row.AwaitedUserID.UUID,
		ClosedAt:      row.ClosedAt.Time,
		ClosedBy:      row.ClosedBy.UUID,
		CreatedAt:     row.CreatedAt,
		UpdatedAt:     row.UpdatedAt,
	}
}

func targetOf(kind string, reportID, warningID, reviewID uuid.NullUUID) Target {
	target := Target{Kind: Kind(kind)}
	switch target.Kind {
	case KindReportOutcome:
		target.TargetID = reportID.UUID
	case KindWarning:
		target.TargetID = warningID.UUID
	default:
		target.TargetID = reviewID.UUID
	}
	return target
}

func nullUUID(id uuid.UUID) uuid.NullUUID {
	return uuid.NullUUID{UUID: id, Valid: id != uuid.Nil}
}

func nullStatus(status Status) sql.NullString {
	return sql.NullString{String: string(status), Valid: status != ""}
}

// mapAppealError classifies PostgreSQL constraint failures. The only unique
// violation an appeal write can hit is the active-decision index, because the
// idempotency key conflict is absorbed by ON CONFLICT.
func mapAppealError(action string, err error) error {
	if err == nil {
		return nil
	}

	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "23505":
			return fmt.Errorf("%s: %w", action, errDecisionAlreadyAppeal)
		case "23514", "23503":
			return fmt.Errorf("%s: %w: %s", action, errAppealInvalid, pgErr.ConstraintName)
		}
	}
	return fmt.Errorf("%s: %w", action, err)
}
