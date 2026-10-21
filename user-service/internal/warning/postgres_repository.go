package warning

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/database/warningdb"
	"github.com/google/uuid"
)

type PostgresRepository struct {
	queries *warningdb.Queries
}

func NewPostgresRepository(queries *warningdb.Queries) *PostgresRepository {
	return &PostgresRepository{queries: queries}
}

var _ Repository = (*PostgresRepository)(nil)

func (p *PostgresRepository) Create(ctx context.Context, warning Warning) (bool, error) {
	var eventID uuid.NullUUID
	if warning.SourceEventID != nil {
		eventID = uuid.NullUUID{UUID: *warning.SourceEventID, Valid: true}
	}
	rows, err := p.queries.CreateAccountWarning(ctx, warningdb.CreateAccountWarningParams{
		UserID: warning.UserID, RequestID: warning.RequestID, ReportID: nullableUUID(warning.ReportID),
		Reason: warning.Reason, Status: string(warning.Status), SourceEventID: eventID,
		CreatedBy: nullableUUID(warning.CreatedBy), UpdatedBy: nullableUUID(warning.UpdatedBy),
		CreatedAt: sql.NullTime{Time: warning.CreatedAt, Valid: !warning.CreatedAt.IsZero()},
	})
	if err != nil {
		return false, fmt.Errorf("insert account warning: %w", err)
	}
	if rows > 0 {
		return true, nil
	}
	if warning.SourceEventID == nil {
		return false, errors.New("account warning insert affected no rows")
	}
	if _, err := p.GetByEventID(ctx, *warning.SourceEventID); err != nil {
		return false, fmt.Errorf("load warning after duplicate event: %w", err)
	}
	return false, nil
}

func (p *PostgresRepository) GetByEventID(ctx context.Context, eventID uuid.UUID) (Warning, error) {
	row, err := p.queries.GetAccountWarningByEventID(ctx, uuid.NullUUID{UUID: eventID, Valid: true})
	if err != nil {
		return Warning{}, mapNotFound(err, "get warning by event ID")
	}
	return warningFromRow(row), nil
}

func (p *PostgresRepository) GetByID(ctx context.Context, id uuid.UUID) (Warning, error) {
	row, err := p.queries.GetAccountWarningByID(ctx, id)
	if err != nil {
		return Warning{}, mapNotFound(err, "get warning")
	}
	return warningFromRow(row), nil
}

func (p *PostgresRepository) ListByUser(ctx context.Context, userID uuid.UUID) ([]Warning, error) {
	rows, err := p.queries.ListAccountWarningsByUser(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("list account warnings: %w", err)
	}
	warnings := make([]Warning, len(rows))
	for i, row := range rows {
		warnings[i] = warningFromRow(row)
	}
	return warnings, nil
}

func (p *PostgresRepository) Remove(ctx context.Context, id uuid.UUID, removedAt time.Time, reason *string, appealID *uuid.UUID, actorID *uuid.UUID) (Warning, error) {
	row, err := p.queries.RemoveAccountWarning(ctx, warningdb.RemoveAccountWarningParams{
		ID: id, RemovedAt: sql.NullTime{Time: removedAt, Valid: true},
		RemovedReason: nullableString(reason), AppealID: nullableUUID(appealID), UpdatedBy: nullableUUID(actorID),
	})
	if err != nil {
		return Warning{}, mapNotFound(err, "remove warning")
	}
	return warningFromRow(row), nil
}

func warningFromRow(row warningdb.AccountWarning) Warning {
	warning := Warning{
		ID: row.ID, UserID: row.UserID, RequestID: row.RequestID, Reason: row.Reason,
		Status: Status(row.Status), CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
	}
	if row.ReportID.Valid {
		warning.ReportID = uuidPointer(row.ReportID.UUID)
	}
	if row.SourceEventID.Valid {
		warning.SourceEventID = uuidPointer(row.SourceEventID.UUID)
	}
	if row.RemovedAt.Valid {
		warning.RemovedAt = timePointer(row.RemovedAt.Time)
	}
	if row.RemovedReason.Valid {
		warning.RemovedReason = stringPointer(row.RemovedReason.String)
	}
	if row.AppealID.Valid {
		warning.AppealID = uuidPointer(row.AppealID.UUID)
	}
	if row.DeletedAt.Valid {
		warning.DeletedAt = timePointer(row.DeletedAt.Time)
	}
	if row.CreatedBy.Valid {
		warning.CreatedBy = uuidPointer(row.CreatedBy.UUID)
	}
	if row.UpdatedBy.Valid {
		warning.UpdatedBy = uuidPointer(row.UpdatedBy.UUID)
	}
	return warning
}

func mapNotFound(err error, operation string) error {
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	return fmt.Errorf("%s: %w", operation, err)
}

func nullableUUID(value *uuid.UUID) uuid.NullUUID {
	if value == nil {
		return uuid.NullUUID{}
	}
	return uuid.NullUUID{UUID: *value, Valid: true}
}

func nullableString(value *string) sql.NullString {
	if value == nil {
		return sql.NullString{}
	}
	return sql.NullString{String: *value, Valid: true}
}

func uuidPointer(value uuid.UUID) *uuid.UUID { return &value }
func timePointer(value time.Time) *time.Time { return &value }
func stringPointer(value string) *string     { return &value }
