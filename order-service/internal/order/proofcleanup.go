package order

import (
	"context"
	"log/slog"
	"time"

	db "github.com/AY2627S1-CS3219-P1/FoC/order-service/internal/database/orderdb"
	"github.com/google/uuid"
)

type ProofCleanupRepository interface {
	ProofRepository
	ProofCleanupCandidates(context.Context, string, time.Time, int32) ([]ProofUpload, error)
}

type ProofCleanupTx interface {
	ProofTx
	ScheduleProofCleanup(context.Context, string, time.Time) (bool, error)
}

type ProofDeleter interface {
	Delete(context.Context, string) error
}

type ProofCleaner struct {
	proofs     *Proofs
	repository ProofCleanupRepository
	storage    ProofDeleter
}

func NewProofCleaner(proofs *Proofs, repository ProofCleanupRepository, storage ProofDeleter) *ProofCleaner {
	return &ProofCleaner{proofs: proofs, repository: repository, storage: storage}
}

// Keep retired identities and periodically re-delete their keys: a PUT started
// before URL expiry can finish after expiry. Successful deletion is not proof
// that every in-flight request is gone. Accepted evidence is never cleaned here.
func (s *ProofCleaner) CleanOnce(ctx context.Context) error {
	rows, err := s.repository.ProofCleanupCandidates(ctx, s.proofs.uploadScope, time.Now().UTC(), 50)
	if err != nil {
		return err
	}
	for _, candidate := range rows {
		var key string
		err = s.proofs.locked(ctx, candidate.OrderID, func(o Order, base ProofTx) (*Change, error) {
			tx, ok := base.(ProofCleanupTx)
			if !ok {
				return nil, ErrUnavailable
			}
			row, err := tx.GetProofUpload(ctx, candidate.ID)
			if err != nil {
				return nil, err
			}
			if row.State == ProofAccepted || row.UploadScope != s.proofs.uploadScope {
				return nil, nil
			}
			now, err := tx.ProofServerTime(ctx)
			if err != nil {
				return nil, err
			}
			if row.ExpiresAt.After(now) {
				return nil, nil
			}
			pending, err := tx.HasPendingDelivery(ctx, o.ID)
			if err != nil {
				return nil, err
			}
			if pending {
				return nil, nil
			}
			if row.State == ProofUploading && o.Status == Delivering && o.PickedUpAt != nil &&
				now.Before(o.PickedUpAt.Add(2*o.DeliveryDuration)) {
				return nil, nil
			}
			if err = tx.AbandonProofUpload(ctx, o.ID, row.CourierID, row.UploadID, now); err != nil {
				return nil, err
			}
			claimed, err := tx.ScheduleProofCleanup(ctx, row.ID, now.Add(time.Hour))
			if err != nil {
				return nil, err
			}
			if claimed {
				key = row.Key
			}
			return nil, nil
		})
		if err != nil {
			return err
		}
		if key != "" {
			bounded, cancel := context.WithTimeout(ctx, 10*time.Second)
			err = s.storage.Delete(bounded, key)
			cancel()
			if err != nil {
				slog.Warn("retired proof cleanup will retry", "order_id", candidate.OrderID, "proof_id", candidate.ID)
			}
		}
	}
	return nil
}

func (s *ProofCleaner) Run(ctx context.Context) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		if err := s.CleanOnce(ctx); err != nil && ctx.Err() == nil {
			slog.Error("proof cleanup scan failed", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (r *PostgresRepository) ProofCleanupCandidates(
	ctx context.Context,
	scope string,
	now time.Time,
	limit int32,
) ([]ProofUpload, error) {
	rows, err := db.New(r.pool).
		ProofCleanupCandidates(ctx, db.ProofCleanupCandidatesParams{ExpiresAt: now, UploadScope: scope, Limit: limit})
	if err != nil {
		return nil, err
	}
	result := make([]ProofUpload, len(rows))
	for i, row := range rows {
		result[i] = ProofUpload{ID: row.ID.String(), OrderID: row.OrderID.String()}
	}
	return result, nil
}

func (t *postgresTx) ScheduleProofCleanup(ctx context.Context, id string, next time.Time) (bool, error) {
	if _, err := t.GetProofUpload(ctx, id); err != nil {
		return false, t.writeResult(err)
	}
	n, err := t.q.ScheduleProofCleanup(ctx, db.ScheduleProofCleanupParams{ID: uuid.MustParse(id), CleanupAfter: next})
	return n == 1, t.writeResult(err)
}
