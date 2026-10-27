package order

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"strings"
	"time"
)

type DeliveryReceipt struct {
	ID, OrderID, CourierID   string
	ProofIDs                 []string
	Unattended, WithoutProof bool
	ReceivedAt               time.Time
	ProcessedAt              *time.Time
	State                    string
	Outage                   *ProofOutage
	AttemptCount             int64
}

func (r DeliveryReceipt) Input() ConfirmDeliveryInput {
	return ConfirmDeliveryInput{
		OrderID:      r.OrderID,
		ProofIDs:     slices.Clone(r.ProofIDs),
		Unattended:   r.Unattended,
		WithoutProof: r.WithoutProof,
	}
}

func (r DeliveryReceipt) matches(c Caller, in ConfirmDeliveryInput) bool {
	return strings.EqualFold(r.CourierID, c.ID) && r.OrderID == in.OrderID && slices.Equal(r.ProofIDs, in.ProofIDs) &&
		r.Unattended == in.Unattended &&
		r.WithoutProof == in.WithoutProof
}

// ReceiptTx borrows the proof/aggregate transaction. All status changes follow
// the parent lock. The database clock is sampled after acquiring that lock.
type ReceiptTx interface {
	ProofTx
	ProofServerTime(context.Context) (time.Time, error)
	GetPendingDeliveryReceipt(context.Context, string) (DeliveryReceipt, error)
	GetDeliveryReceipt(context.Context, string) (DeliveryReceipt, error)
	InsertDeliveryReceipt(context.Context, DeliveryReceipt) error
	FinishDeliveryReceipt(context.Context, string, string, time.Time) error
}

type ReceiptRepository interface {
	ProofRepository
	DeliveryReceipt(context.Context, string) (DeliveryReceipt, error)
	DueProofReceipts(context.Context, int32) ([]string, error)
	RetryProofReceipt(context.Context, DeliveryReceipt) error
	AlertProofReceipt(context.Context, string) (bool, error)
}

// ProofReceipts is the production confirmation entry point. Admission commits
// before remote validation; recovery never grants extra time to upload evidence.
type ProofReceipts struct {
	proofs     *Proofs
	repository ReceiptRepository
}

func NewProofReceipts(proofs *Proofs, repository ReceiptRepository) *ProofReceipts {
	return &ProofReceipts{proofs: proofs, repository: repository}
}

func (s *ProofReceipts) ConfirmDelivery(ctx context.Context, c Caller, in ConfirmDeliveryInput) (Order, error) {
	var err error
	c, in.OrderID, err = proofCaller(c, in.OrderID)
	if err != nil {
		return Order{}, err
	}
	in.ProofIDs, err = normalizedProofIDs(in.ProofIDs)
	if err != nil || (len(in.ProofIDs) == 0) != in.WithoutProof {
		return Order{}, ErrInvalid
	}
	// A zero-photo exception must already have authoritative evidence when
	// admitted. Merely experiencing a failed photo request is not an outage.
	var outage *ProofOutage
	var outageErr error
	if in.WithoutProof && s.proofs.outages != nil {
		value, e := s.proofs.outages.ConfirmProofOutage(ctx)
		if e == nil {
			outage = &value
		} else {
			outageErr = e
		}
	}
	var receipt DeliveryReceipt
	var accepted *Order
	err = s.proofs.locked(ctx, in.OrderID, func(o Order, base ProofTx) (*Change, error) {
		if !o.IsAssignedTo(c.ID) {
			return nil, ErrNotFound
		}
		tx, ok := base.(ReceiptTx)
		if !ok {
			return nil, ErrUnavailable
		}
		// Accepted replay is evaluated before deadline, current status and outage.
		existing, e := tx.GetProofConfirmation(ctx, o.ID)
		if e == nil {
			if !o.IsAssignedTo(c.ID) || existing.CourierID != c.ID || existing.Unattended != in.Unattended ||
				!slices.Equal(existing.ProofIDs, in.ProofIDs) ||
				(existing.OutageReason != "") != in.WithoutProof {
				return nil, ErrPrecondition
			}
			accepted = &o
			return nil, nil
		}
		if !errors.Is(e, ErrNotFound) {
			return nil, e
		}
		pending, e := tx.GetPendingDeliveryReceipt(ctx, o.ID)
		if e == nil {
			if !pending.matches(c, in) {
				return nil, ErrPrecondition
			}
			receipt = pending
			return nil, nil
		}
		if !errors.Is(e, ErrNotFound) {
			return nil, e
		}
		now, e := tx.ProofServerTime(ctx)
		if e != nil {
			return nil, e
		}
		if e = canUploadProof(o, c, now); e != nil {
			return nil, e
		}
		for _, id := range in.ProofIDs {
			row, e := tx.GetProofUpload(ctx, id)
			if e != nil {
				if errors.Is(e, ErrNotFound) {
					return nil, ErrPrecondition
				}
				return nil, e
			}
			if !proofBelongsTo(row, o.ID, c.ID) || row.UploadScope != s.proofs.uploadScope ||
				row.State != ProofUploading ||
				row.VerifiedAt == nil ||
				row.VerifiedAt.After(now) {
				return nil, ErrPrecondition
			}
		}
		if in.WithoutProof {
			if outageErr != nil && !errors.Is(outageErr, ErrPrecondition) {
				return nil, ErrUnavailable
			}
			if outage == nil || !s.proofs.isCurrentProofOutage(*outage, now) {
				return nil, ErrPrecondition
			}
			current, e := tx.IsProofOutageCurrent(ctx, *outage)
			if e != nil {
				return nil, e
			}
			if !current {
				return nil, ErrPrecondition
			}
		}
		receipt = DeliveryReceipt{
			ID:           s.proofs.newID(),
			OrderID:      o.ID,
			CourierID:    c.ID,
			ProofIDs:     slices.Clone(in.ProofIDs),
			Unattended:   in.Unattended,
			WithoutProof: in.WithoutProof,
			ReceivedAt:   now,
			State:        "pending",
			Outage:       outage,
		}
		return nil, tx.InsertDeliveryReceipt(ctx, receipt)
	})
	if err != nil {
		return Order{}, err
	}
	if accepted != nil {
		return *accepted, nil
	}
	return s.process(ctx, receipt)
}

func (s *ProofReceipts) process(ctx context.Context, r DeliveryReceipt) (Order, error) {
	result, err := s.proofs.confirmReceipt(ctx, Caller{ID: r.CourierID}, r.Input(), &r)
	if err == nil {
		return result, nil
	}
	if errors.Is(err, ErrPrecondition) || errors.Is(err, ErrNotFound) || errors.Is(err, ErrInvalid) ||
		errors.Is(err, ErrForbidden) {
		finishErr := s.proofs.locked(ctx, r.OrderID, func(o Order, base ProofTx) (*Change, error) {
			tx, ok := base.(ReceiptTx)
			if !ok {
				return nil, ErrUnavailable
			}
			current, e := tx.GetDeliveryReceipt(ctx, r.ID)
			if e != nil {
				return nil, e
			}
			if current.State != "pending" {
				return nil, nil
			}
			state := "rejected"
			if o.Status != Delivering || !o.IsAssignedTo(r.CourierID) {
				state = "superseded"
			}
			now, e := tx.ProofServerTime(ctx)
			if e != nil {
				return nil, e
			}
			return nil, tx.FinishDeliveryReceipt(ctx, r.ID, state, now)
		})
		if finishErr != nil {
			return Order{}, finishErr
		}
	} else {
		if e := s.repository.RetryProofReceipt(ctx, r); e != nil {
			return Order{}, e
		}
		alert, e := s.repository.AlertProofReceipt(ctx, r.ID)
		if e != nil {
			return Order{}, e
		}
		if alert {
			slog.Error(
				"delivery proof validation remains pending",
				"order_id",
				r.OrderID,
				"receipt_id",
				r.ID,
				"received_at",
				r.ReceivedAt,
			)
		}
	}
	return Order{}, err
}

func (s *ProofReceipts) RecoverOnce(ctx context.Context) error {
	ids, err := s.repository.DueProofReceipts(ctx, 20)
	if err != nil {
		return err
	}
	for _, id := range ids {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		r, e := s.repository.DeliveryReceipt(ctx, id)
		if e != nil {
			return e
		}
		if r.State != "pending" {
			continue
		}
		if _, e = s.process(
			ctx,
			r,
		); e != nil && !errors.Is(e, ErrPrecondition) && !errors.Is(e, ErrNotFound) &&
			!errors.Is(e, ErrUnavailable) {
			slog.Error("delivery proof recovery failed", "receipt_id", id, "error", e)
		}
	}
	return nil
}

func (s *ProofReceipts) Run(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		if err := s.RecoverOnce(ctx); err != nil && ctx.Err() == nil {
			slog.Error("delivery proof recovery scan failed", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
