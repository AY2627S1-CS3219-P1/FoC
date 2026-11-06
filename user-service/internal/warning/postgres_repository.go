package warning

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/database/warningdb"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
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
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23503" {
			// The warned courier has no users row; retrying cannot succeed.
			return false, fmt.Errorf("insert account warning: %w", ErrUnknownCourier)
		}
		return false, fmt.Errorf("insert account warning: %w", err)
	}
	if rows > 0 {
		return true, nil
	}
	if warning.SourceEventID == nil {
		return false, errors.New("account warning insert affected no rows")
	}
	return false, nil
}

func (p *PostgresRepository) ListByUser(
	ctx context.Context,
	userID uuid.UUID,
	offset, limit int64,
) ([]Warning, error) {
	rows, err := p.queries.ListAccountWarningsByUser(ctx, warningdb.ListAccountWarningsByUserParams{
		UserID: userID, PageOffset: offset, PageLimit: limit,
	})
	if err != nil {
		return nil, fmt.Errorf("list account warnings: %w", err)
	}
	warnings := make([]Warning, len(rows))
	for i, row := range rows {
		warnings[i] = warningFromRow(row)
	}
	return warnings, nil
}

func (p *PostgresRepository) ListForAdmin(
	ctx context.Context,
	userID uuid.UUID,
	offset, limit int64,
) ([]AdminWarning, error) {
	rows, err := p.queries.ListAccountWarningsForAdmin(ctx, warningdb.ListAccountWarningsForAdminParams{
		UserID: userID, PageOffset: offset, PageLimit: limit,
	})
	if err != nil {
		return nil, fmt.Errorf("list account warnings for admin: %w", err)
	}
	warnings := make([]AdminWarning, len(rows))
	for i, row := range rows {
		warnings[i] = adminWarningFromRow(row)
	}
	return warnings, nil
}

func (p *PostgresRepository) CountByUser(ctx context.Context, userID uuid.UUID) (int64, error) {
	count, err := p.queries.CountAccountWarningsByUser(ctx, userID)
	if err != nil {
		return 0, fmt.Errorf("count account warnings: %w", err)
	}
	return count, nil
}

func (p *PostgresRepository) Remove(
	ctx context.Context,
	id uuid.UUID,
	removedAt time.Time,
	reason *string,
	appealID *uuid.UUID,
	actorID *uuid.UUID,
) (Warning, error) {
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

// adminWarningFromRow maps the admin query's row onto the read projection.
// The row carries every table column plus the joined updater name, so the
// common columns feed the shared table mapping and only the name is added.
func adminWarningFromRow(row warningdb.ListAccountWarningsForAdminRow) AdminWarning {
	admin := AdminWarning{Warning: warningFromRow(warningdb.AccountWarning{
		ID: row.ID, UserID: row.UserID, RequestID: row.RequestID, ReportID: row.ReportID,
		Reason: row.Reason, Status: row.Status, SourceEventID: row.SourceEventID,
		RemovedAt: row.RemovedAt, RemovedReason: row.RemovedReason, AppealID: row.AppealID,
		CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt, DeletedAt: row.DeletedAt,
		CreatedBy: row.CreatedBy, UpdatedBy: row.UpdatedBy,
	})}
	if row.UpdatedByName.Valid {
		admin.UpdatedByName = stringPointer(row.UpdatedByName.String)
	}
	return admin
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
