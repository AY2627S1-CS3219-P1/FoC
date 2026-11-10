package outbox

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/AY2627S1-CS3219-P1/FoC/moderation-service/internal/appeal"
	"github.com/AY2627S1-CS3219-P1/FoC/moderation-service/internal/database/appealdb"
	"github.com/AY2627S1-CS3219-P1/FoC/moderation-service/internal/database/outboxdb"
	"github.com/AY2627S1-CS3219-P1/FoC/moderation-service/internal/database/reportdb"
	"github.com/AY2627S1-CS3219-P1/FoC/moderation-service/internal/evidence"
	"github.com/AY2627S1-CS3219-P1/FoC/moderation-service/internal/report"
)

// ReportTransaction runs each report operation in one database transaction, so
// report rows, evidence and recorded outcomes commit together.
func ReportTransaction(db *sql.DB) report.WithTransaction {
	return func(ctx context.Context, operation func(report.Tx) error) error {
		return inTransaction(ctx, db, func(tx *sql.Tx) error {
			return operation(report.Tx{
				Queries:      reportdb.New(tx),
				Consequences: ForReport(outboxdb.New(tx)),
				Evidence:     evidence.NewAttacher(tx),
			})
		})
	}
}

// AppealTransaction runs each appeal operation in one database transaction,
// so an appeal's close and the outcomes it overturns commit together.
func AppealTransaction(db *sql.DB) appeal.WithTransaction {
	return func(ctx context.Context, operation func(appeal.Tx) error) error {
		return inTransaction(ctx, db, func(tx *sql.Tx) error {
			return operation(appeal.Tx{
				Queries:      appealdb.New(tx),
				Consequences: ForAppeal(outboxdb.New(tx)),
				Evidence:     evidence.NewAttacher(tx),
			})
		})
	}
}

func inTransaction(ctx context.Context, db *sql.DB, operation func(*sql.Tx) error) (err error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer func() {
		if err != nil {
			err = errors.Join(err, ignoreDone(tx.Rollback()))
		}
	}()

	if err := operation(tx); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}
	return nil
}

func ignoreDone(err error) error {
	if errors.Is(err, sql.ErrTxDone) {
		return nil
	}
	return err
}
