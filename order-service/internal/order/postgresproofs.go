package order

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"time"

	db "github.com/AY2627S1-CS3219-P1/FoC/order-service/internal/database/orderdb"
	"github.com/google/uuid"
)

func (r *PostgresRepository) WithinProofs(ctx context.Context, fn func(ProofTx) error) error {
	return r.within(ctx, func(tx *postgresTx) error { return fn(tx) })
}

func (t *postgresTx) proofLocked(orderID string) error {
	if _, ok := t.locked[orderID]; !ok {
		return ErrPrecondition
	}
	return nil
}

func (t *postgresTx) ProofServerTime(ctx context.Context) (time.Time, error) {
	if len(t.locked) == 0 {
		return time.Time{}, ErrPrecondition
	}
	return t.q.ProofServerTime(ctx)
}

func (t *postgresTx) HasPendingDelivery(ctx context.Context, orderID string) (bool, error) {
	if err := t.proofLocked(orderID); err != nil {
		return false, err
	}
	return t.q.HasPendingDelivery(ctx, uuid.MustParse(orderID))
}

func (t *postgresTx) FindProofUpload(ctx context.Context, orderID, courierID, uploadID string) (ProofUpload, error) {
	if err := t.proofLocked(orderID); err != nil {
		return ProofUpload{}, err
	}
	fenced, err := t.q.ProofIdentityFenced(ctx, db.ProofIdentityFencedParams{OrderID: uuid.MustParse(orderID), CourierID: uuid.MustParse(courierID), UploadID: uuid.MustParse(uploadID)})
	if err != nil {
		return ProofUpload{}, err
	}
	if fenced {
		return ProofUpload{OrderID: orderID, CourierID: courierID, UploadID: uploadID, State: ProofDeleting}, nil
	}
	row, err := t.q.FindProofUpload(ctx, db.FindProofUploadParams{OrderID: uuid.MustParse(orderID), CourierID: uuid.MustParse(courierID), UploadID: uuid.MustParse(uploadID)})
	if err != nil {
		return ProofUpload{}, persistenceError(err)
	}
	return fromProofUpload(row), nil
}

func (t *postgresTx) CountStagedProofs(ctx context.Context, orderID, courierID string) (int64, error) {
	if err := t.proofLocked(orderID); err != nil {
		return 0, err
	}
	return t.q.CountStagedProofs(ctx, db.CountStagedProofsParams{OrderID: uuid.MustParse(orderID), CourierID: uuid.MustParse(courierID)})
}

func (t *postgresTx) GetProofUpload(ctx context.Context, id string) (ProofUpload, error) {
	parsed, err := parseID(id)
	if err != nil {
		return ProofUpload{}, err
	}
	row, err := t.q.GetProofUpload(ctx, parsed)
	if err != nil {
		return ProofUpload{}, persistenceError(err)
	}
	if err := t.proofLocked(row.OrderID.String()); err != nil {
		return ProofUpload{}, err
	}
	return fromProofUpload(row), nil
}

func (t *postgresTx) SaveProofUpload(ctx context.Context, row ProofUpload) error {
	if err := t.proofLocked(row.OrderID); err != nil {
		return t.writeResult(err)
	}
	fenced, err := t.q.ProofIdentityFenced(ctx, db.ProofIdentityFencedParams{OrderID: uuid.MustParse(row.OrderID), CourierID: uuid.MustParse(row.CourierID), UploadID: uuid.MustParse(row.UploadID)})
	if err != nil {
		return t.writeResult(err)
	}
	if fenced {
		return t.writeResult(ErrPrecondition)
	}
	n, err := t.q.SaveProofUpload(ctx, db.SaveProofUploadParams{ID: uuid.MustParse(row.ID), OrderID: uuid.MustParse(row.OrderID), CourierID: uuid.MustParse(row.CourierID), UploadID: uuid.MustParse(row.UploadID), ObjectKey: row.Key, ContentType: row.ContentType, SizeBytes: row.SizeBytes, State: string(row.State), Generation: row.Generation, SigningEpoch: row.SigningEpoch, GrantedAt: row.GrantedAt, ExpiresAt: row.ExpiresAt, CreatedAt: row.CreatedAt})
	return t.proofWrite(n, err)
}

func (t *postgresTx) proofWrite(n int64, err error) error {
	if err == nil && n != 1 {
		err = ErrConflict
	}
	return t.writeResult(err)
}

func (t *postgresTx) MarkProofVerified(ctx context.Context, id string) error {
	row, err := t.GetProofUpload(ctx, id)
	if err != nil {
		return t.writeResult(err)
	}
	if row.State != ProofUploading {
		return t.writeResult(ErrPrecondition)
	}
	n, err := t.q.VerifyProofUpload(ctx, uuid.MustParse(id))
	return t.proofWrite(n, err)
}

func (t *postgresTx) AbandonProofUpload(ctx context.Context, orderID, courierID, uploadID string, at time.Time) error {
	if err := t.proofLocked(orderID); err != nil {
		return t.writeResult(err)
	}
	row, err := t.FindProofUpload(ctx, orderID, courierID, uploadID)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return t.writeResult(err)
	}
	if err == nil && row.ID != "" {
		reserved, e := t.q.ProofReservedByReceipt(ctx, db.ProofReservedByReceiptParams{OrderID: uuid.MustParse(orderID), Column2: uuid.MustParse(row.ID)})
		if e != nil {
			return t.writeResult(e)
		}
		if row.State == ProofAccepted || reserved {
			return t.writeResult(ErrPrecondition)
		}
	}
	if err := t.q.FenceProofIdentity(ctx, db.FenceProofIdentityParams{OrderID: uuid.MustParse(orderID), CourierID: uuid.MustParse(courierID), UploadID: uuid.MustParse(uploadID), RetiredAt: at}); err != nil {
		return t.writeResult(err)
	}
	_, err = t.q.RetireProofUpload(ctx, db.RetireProofUploadParams{OrderID: uuid.MustParse(orderID), CourierID: uuid.MustParse(courierID), UploadID: uuid.MustParse(uploadID)})
	return t.writeResult(err)
}

func (t *postgresTx) GetProofConfirmation(ctx context.Context, orderID string) (ProofConfirmation, error) {
	if err := t.proofLocked(orderID); err != nil {
		return ProofConfirmation{}, err
	}
	payload, err := t.q.GetProofConfirmation(ctx, uuid.MustParse(orderID))
	if err != nil {
		return ProofConfirmation{}, persistenceError(err)
	}
	var result ProofConfirmation
	err = json.Unmarshal(payload, &result)
	return result, err
}

func (t *postgresTx) SaveProofConfirmation(ctx context.Context, c ProofConfirmation, uploads []ProofUpload) error {
	if err := t.proofLocked(c.OrderID); err != nil {
		return t.writeResult(err)
	}
	receipt, err := t.GetPendingDeliveryReceipt(ctx, c.OrderID)
	if err != nil {
		return t.writeResult(err)
	}
	if receipt.CourierID != c.CourierID || !receipt.ReceivedAt.Equal(c.ConfirmedAt) || !slices.Equal(receipt.ProofIDs, c.ProofIDs) || receipt.Unattended != c.Unattended || receipt.WithoutProof != (c.OutageReason != "") {
		return t.writeResult(ErrPrecondition)
	}
	if len(uploads) != len(c.ProofIDs) {
		return t.writeResult(ErrPrecondition)
	}
	for _, expected := range uploads {
		stored, err := t.GetProofUpload(ctx, expected.ID)
		if err != nil {
			return t.writeResult(err)
		}
		if !proofUploadMatches(stored, expected) || stored.State != ProofUploading || !proofBelongsTo(stored, c.OrderID, c.CourierID) || stored.VerifiedAt == nil || stored.VerifiedAt.After(receipt.ReceivedAt) {
			return t.writeResult(ErrPrecondition)
		}
		n, err := t.q.AcceptProofUpload(ctx, uuid.MustParse(stored.ID))
		if err = t.proofWrite(n, err); err != nil {
			return err
		}
	}
	payload, err := json.Marshal(c)
	if err != nil {
		return t.writeResult(err)
	}
	if err = t.q.SaveProofConfirmation(ctx, db.SaveProofConfirmationParams{OrderID: uuid.MustParse(c.OrderID), Payload: payload}); err != nil {
		return t.writeResult(err)
	}
	now, err := t.ProofServerTime(ctx)
	if err != nil {
		return t.writeResult(err)
	}
	return t.FinishDeliveryReceipt(ctx, receipt.ID, "accepted", now)
}

func fromProofUpload(row db.OrderProofUpload) ProofUpload {
	return ProofUpload{ID: row.ID.String(), OrderID: row.OrderID.String(), CourierID: row.CourierID.String(), UploadID: row.UploadID.String(), Key: row.ObjectKey, ContentType: row.ContentType, SizeBytes: row.SizeBytes, State: ProofState(row.State), Generation: row.Generation, SigningEpoch: row.SigningEpoch, GrantedAt: row.GrantedAt, ExpiresAt: row.ExpiresAt, CreatedAt: row.CreatedAt, VerifiedAt: row.VerifiedAt}
}

func fromDeliveryReceipt(row db.OrderDeliveryReceipt) (DeliveryReceipt, error) {
	result := DeliveryReceipt{ID: row.ID.String(), OrderID: row.OrderID.String(), CourierID: row.CourierID.String(), ProofIDs: []string{}, Unattended: row.Unattended, WithoutProof: row.WithoutProof, ReceivedAt: row.ReceivedAt, ProcessedAt: row.ProcessedAt, State: row.State, AttemptCount: row.AttemptCount}
	for _, id := range row.ProofIds {
		result.ProofIDs = append(result.ProofIDs, id.String())
	}
	if len(row.Outage) > 0 && string(row.Outage) != "null" {
		if err := json.Unmarshal(row.Outage, &result.Outage); err != nil {
			return DeliveryReceipt{}, err
		}
	}
	return result, nil
}

func (t *postgresTx) GetPendingDeliveryReceipt(ctx context.Context, orderID string) (DeliveryReceipt, error) {
	if err := t.proofLocked(orderID); err != nil {
		return DeliveryReceipt{}, err
	}
	row, err := t.q.GetPendingDeliveryReceipt(ctx, uuid.MustParse(orderID))
	if err != nil {
		return DeliveryReceipt{}, persistenceError(err)
	}
	return fromDeliveryReceipt(row)
}

func (t *postgresTx) GetDeliveryReceipt(ctx context.Context, id string) (DeliveryReceipt, error) {
	row, err := t.q.GetDeliveryReceipt(ctx, uuid.MustParse(id))
	if err != nil {
		return DeliveryReceipt{}, persistenceError(err)
	}
	if err := t.proofLocked(row.OrderID.String()); err != nil {
		return DeliveryReceipt{}, err
	}
	return fromDeliveryReceipt(row)
}

func (t *postgresTx) InsertDeliveryReceipt(ctx context.Context, r DeliveryReceipt) error {
	if err := t.proofLocked(r.OrderID); err != nil {
		return t.writeResult(err)
	}
	ids := make([]uuid.UUID, 0, len(r.ProofIDs))
	for _, id := range r.ProofIDs {
		ids = append(ids, uuid.MustParse(id))
	}
	outage, err := json.Marshal(r.Outage)
	if err != nil {
		return t.writeResult(err)
	}
	return t.writeResult(t.q.InsertDeliveryReceipt(ctx, db.InsertDeliveryReceiptParams{ID: uuid.MustParse(r.ID), OrderID: uuid.MustParse(r.OrderID), CourierID: uuid.MustParse(r.CourierID), ProofIds: ids, Unattended: r.Unattended, WithoutProof: r.WithoutProof, ReceivedAt: r.ReceivedAt, Outage: outage}))
}

func (t *postgresTx) FinishDeliveryReceipt(ctx context.Context, id, state string, at time.Time) error {
	if _, err := t.GetDeliveryReceipt(ctx, id); err != nil {
		return t.writeResult(err)
	}
	n, err := t.q.FinishDeliveryReceipt(ctx, db.FinishDeliveryReceiptParams{ID: uuid.MustParse(id), State: state, ProcessedAt: &at})
	return t.proofWrite(n, err)
}

func (r *PostgresRepository) DeliveryReceipt(ctx context.Context, id string) (DeliveryReceipt, error) {
	parsed, err := parseID(id)
	if err != nil {
		return DeliveryReceipt{}, err
	}
	row, err := db.New(r.pool).GetDeliveryReceipt(ctx, parsed)
	if err != nil {
		return DeliveryReceipt{}, persistenceError(err)
	}
	return fromDeliveryReceipt(row)
}

func (r *PostgresRepository) DueProofReceipts(ctx context.Context, now time.Time, limit int32) ([]string, error) {
	rows, err := db.New(r.pool).DueProofReceipts(ctx, db.DueProofReceiptsParams{NextAttemptAt: now, Limit: limit})
	if err != nil {
		return nil, err
	}
	ids := make([]string, len(rows))
	for i, id := range rows {
		ids[i] = id.String()
	}
	return ids, nil
}

func (r *PostgresRepository) RetryProofReceipt(ctx context.Context, receipt DeliveryReceipt, next time.Time) error {
	return db.New(r.pool).RetryProofReceipt(ctx, db.RetryProofReceiptParams{ID: uuid.MustParse(receipt.ID), NextAttemptAt: next})
}

func (r *PostgresRepository) AlertProofReceipt(ctx context.Context, id string, now time.Time) (bool, error) {
	n, err := db.New(r.pool).AlertProofReceipt(ctx, db.AlertProofReceiptParams{ID: uuid.MustParse(id), AlertedAt: &now, AlertedAt_2: &[]time.Time{now.Add(-time.Hour)}[0]})
	return n == 1, err
}

func (t *postgresTx) IsProofOutageCurrent(ctx context.Context, outage ProofOutage) (bool, error) {
	if len(t.locked) == 0 {
		return false, ErrPrecondition
	}
	row, err := t.q.LockProofOutage(ctx, outage.UploadScope)
	if errors.Is(persistenceError(err), ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return row.Active && row.EvidenceID.String() == outage.EvidenceID && row.Generation == outage.Generation && row.ObservedAt.Equal(outage.ObservedAt) && row.ValidUntil.Equal(outage.ValidUntil) && row.Reason == outage.Reason, nil
}

var _ ProofRepository = (*PostgresRepository)(nil)
var _ ReceiptRepository = (*PostgresRepository)(nil)
var _ ReceiptTx = (*postgresTx)(nil)
