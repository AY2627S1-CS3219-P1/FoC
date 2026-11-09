package report

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/database/reportdb"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

type PostgresRepository struct {
	queries *reportdb.Queries
}

func NewPostgresRepository(queries *reportdb.Queries) *PostgresRepository {
	return &PostgresRepository{queries: queries}
}

var _ Repository = (*PostgresRepository)(nil)

// Lookups

func (r *PostgresRepository) UserExists(ctx context.Context, id uuid.UUID) (bool, error) {
	exists, err := r.queries.UserExists(ctx, id)
	if err != nil {
		return false, fmt.Errorf("check user exists: %w", err)
	}
	return exists, nil
}

func (r *PostgresRepository) LiveReviewAuthor(ctx context.Context, reviewID uuid.UUID) (uuid.UUID, error) {
	author, err := r.queries.LiveReviewAuthor(ctx, reviewID)
	if errors.Is(err, sql.ErrNoRows) {
		return uuid.Nil, errReviewNotFound
	}
	if err != nil {
		return uuid.Nil, fmt.Errorf("read review author: %w", err)
	}
	return author, nil
}

// Writes

func (r *PostgresRepository) InsertReport(ctx context.Context, input ReportInput) error {
	err := r.queries.InsertReport(ctx, reportdb.InsertReportParams{
		ID:          input.ID,
		ReporterID:  input.ReporterID,
		SubjectID:   input.SubjectID,
		ReviewID:    nullUUID(input.ReviewID),
		OrderID:     nullUUID(input.OrderID),
		Category:    string(input.Category),
		Description: input.Description,
	})
	return mapReportError("insert report", err)
}

func (r *PostgresRepository) LockReport(
	ctx context.Context,
	q *reportdb.Queries,
	id uuid.UUID,
) (LockedReport, error) {
	row, err := q.LockReport(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return LockedReport{}, errReportNotFound
	}
	if err != nil {
		return LockedReport{}, fmt.Errorf("lock report: %w", err)
	}

	return LockedReport{
		ReporterID: row.ReporterID,
		SubjectID:  row.SubjectID,
		ReviewID:   row.ReviewID.UUID,
		Status:     Status(row.Status),
		RequestID:  row.RequestID,
	}, nil
}

func (r *PostgresRepository) TransitionReport(
	ctx context.Context,
	q *reportdb.Queries,
	id uuid.UUID,
	from, to Status,
) error {
	rows, err := q.TransitionReport(ctx, reportdb.TransitionReportParams{
		ToStatus:   string(to),
		ID:         id,
		FromStatus: string(from),
	})
	if err != nil {
		return mapReportError("transition report", err)
	}
	if rows == 0 {
		return errReportStateChanged
	}
	return nil
}

func (r *PostgresRepository) CloseReport(
	ctx context.Context,
	q *reportdb.Queries,
	id uuid.UUID,
	closure Closure,
) error {
	rows, err := q.CloseReport(ctx, reportdb.CloseReportParams{
		Outcome:       string(closure.Outcome),
		OutcomeReason: closure.Reason,
		ClosedBy:      closure.ClosedBy,
		ID:            id,
	})
	if err != nil {
		return mapReportError("close report", err)
	}
	if rows == 0 {
		return errReportStateChanged
	}
	return nil
}

func (r *PostgresRepository) InsertResponse(ctx context.Context, q *reportdb.Queries, input ResponseInput) error {
	err := q.InsertResponse(ctx, reportdb.InsertResponseParams{
		ID:          input.ID,
		ReportID:    input.ReportID,
		ResponderID: input.ResponderID,
		Body:        input.Body,
	})
	return mapReportError("insert report response", err)
}

// Reads

func (r *PostgresRepository) GetReport(ctx context.Context, id uuid.UUID) (Report, error) {
	row, err := r.queries.GetReport(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return Report{}, errReportNotFound
	}
	if err != nil {
		return Report{}, fmt.Errorf("get report: %w", err)
	}
	return toReport(row), nil
}

func (r *PostgresRepository) ListResponses(ctx context.Context, reportID uuid.UUID) ([]Response, error) {
	rows, err := r.queries.ListResponses(ctx, reportID)
	if err != nil {
		return nil, fmt.Errorf("list report responses: %w", err)
	}

	out := make([]Response, len(rows))
	for i, row := range rows {
		out[i] = Response{
			ID:            row.ID,
			ReportID:      row.ReportID,
			ResponderID:   row.ResponderID,
			ResponderName: row.ResponderName,
			Body:          row.Body,
			CreatedAt:     row.CreatedAt,
		}
	}
	return out, nil
}

func (r *PostgresRepository) ListReportsInvolving(
	ctx context.Context,
	userID uuid.UUID,
	offset, limit int64,
) ([]Report, error) {
	rows, err := r.queries.ListReportsInvolving(ctx, reportdb.ListReportsInvolvingParams{
		UserID:     userID,
		PageOffset: offset,
		PageLimit:  limit,
	})
	if err != nil {
		return nil, fmt.Errorf("list reports involving user: %w", err)
	}

	out := make([]Report, len(rows))
	for i, row := range rows {
		out[i] = toReport(reportdb.GetReportRow(row))
	}
	return out, nil
}

func (r *PostgresRepository) CountReportsInvolving(ctx context.Context, userID uuid.UUID) (int64, error) {
	count, err := r.queries.CountReportsInvolving(ctx, userID)
	if err != nil {
		return 0, fmt.Errorf("count reports involving user: %w", err)
	}
	return count, nil
}

func (r *PostgresRepository) ListReports(ctx context.Context, filter Filter, offset, limit int64) ([]Report, error) {
	rows, err := r.queries.ListReports(ctx, reportdb.ListReportsParams{
		Status:     nullStatus(filter.Status),
		SubjectID:  nullUUID(filter.SubjectID),
		ReviewID:   nullUUID(filter.ReviewID),
		OrderID:    nullUUID(filter.OrderID),
		PageOffset: offset,
		PageLimit:  limit,
	})
	if err != nil {
		return nil, fmt.Errorf("list reports: %w", err)
	}

	out := make([]Report, len(rows))
	for i, row := range rows {
		out[i] = toReport(reportdb.GetReportRow(row))
	}
	return out, nil
}

func (r *PostgresRepository) CountReports(ctx context.Context, filter Filter) (int64, error) {
	count, err := r.queries.CountReports(ctx, reportdb.CountReportsParams{
		Status:    nullStatus(filter.Status),
		SubjectID: nullUUID(filter.SubjectID),
		ReviewID:  nullUUID(filter.ReviewID),
		OrderID:   nullUUID(filter.OrderID),
	})
	if err != nil {
		return 0, fmt.Errorf("count reports: %w", err)
	}
	return count, nil
}

// Conversion

// toReport takes GetReportRow; the list rows have identical fields and convert
// to it directly.
func toReport(row reportdb.GetReportRow) Report {
	return Report{
		ID:            row.ID,
		ReporterID:    row.ReporterID,
		ReporterName:  row.ReporterName,
		SubjectID:     row.SubjectID,
		SubjectName:   row.SubjectName,
		ReviewID:      row.ReviewID.UUID,
		OrderID:       row.OrderID.UUID,
		Category:      Category(row.Category),
		Description:   row.Description,
		Status:        Status(row.Status),
		Outcome:       Outcome(row.Outcome.String),
		OutcomeReason: row.OutcomeReason.String,
		ClosedAt:      row.ClosedAt.Time,
		ClosedBy:      row.ClosedBy.UUID,
		CloserName:    row.CloserName.String,
		CreatedAt:     row.CreatedAt,
		UpdatedAt:     row.UpdatedAt,
	}
}

func nullUUID(id uuid.UUID) uuid.NullUUID {
	return uuid.NullUUID{UUID: id, Valid: id != uuid.Nil}
}

func nullStatus(status Status) sql.NullString {
	return sql.NullString{String: string(status), Valid: status != ""}
}

// mapReportError classifies PostgreSQL constraint failures so the service can
// map them to client errors; anything else stays an internal error.
func mapReportError(action string, err error) error {
	if err == nil {
		return nil
	}

	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "23505":
			return fmt.Errorf("%s: %w", action, errReportAlreadyOpen)
		case "23514", "23503":
			return fmt.Errorf("%s: %w: %s", action, errReportInvalid, pgErr.ConstraintName)
		}
	}
	return fmt.Errorf("%s: %w", action, err)
}
