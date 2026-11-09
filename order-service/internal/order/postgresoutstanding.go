package order

import (
	"context"
	"fmt"

	db "github.com/AY2627S1-CS3219-P1/FoC/order-service/internal/database/orderdb"
)

// OutstandingWork reads every drain predicate in one statement, so the order,
// offers, workflows and admission records are always observed on one snapshot.
func (r *PostgresRepository) OutstandingWork(ctx context.Context, userID string) (OutstandingWork, error) {
	uid, err := parseID(userID)
	if err != nil {
		return OutstandingWork{}, err
	}

	row, err := db.New(r.pool).GetUserOutstandingWork(ctx, uid)
	if err != nil {
		return OutstandingWork{}, fmt.Errorf("outstanding work %s: %w", uid, err)
	}

	var out OutstandingWork
	if row.NonterminalOrder {
		out.Reasons = append(out.Reasons, OutstandingNonterminalOrder)
	}
	if row.OpenOffer {
		out.Reasons = append(out.Reasons, OutstandingOpenOffer)
	}
	if row.PendingAdmission {
		out.Reasons = append(out.Reasons, OutstandingPendingAdmission)
	}
	if row.PendingWorkflow {
		out.Reasons = append(out.Reasons, OutstandingPendingWorkflow)
	}
	return out, nil
}

var _ DrainReader = (*PostgresRepository)(nil)

