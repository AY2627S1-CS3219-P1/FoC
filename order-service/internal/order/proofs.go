package order

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	"github.com/AY2627S1-CS3219-P1/FoC/order-service/internal/images"
	"github.com/google/uuid"
)

const maxProofOutageAge = 30 * time.Second

// Proofs owns delivery evidence and confirmation. It never calls storage, image
// decoders or case services while an order transaction is open.
type Proofs struct {
	repository        ProofRepository
	grants            ProofGrantIssuer
	objects           ProofObjects
	validator         ProofImageValidator
	outages           ProofOutageAuthority
	uploadScope       string
	verificationSlots chan struct{}
	now               func() time.Time
	newID             func() string
}

func NewProofs(repository ProofRepository, grants ProofGrantIssuer, objects ProofObjects, validator ProofImageValidator, options ProofOptions) *Proofs {
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.NewID == nil {
		options.NewID = uuid.NewString
	}

	return &Proofs{
		repository:        repository,
		grants:            grants,
		objects:           objects,
		validator:         validator,
		outages:           options.Outages,
		uploadScope:       options.UploadScope,
		verificationSlots: make(chan struct{}, 2),
		now:               options.Now,
		newID:             options.NewID,
	}
}

// CreateUpload persists one identity before signing. Failed signing is retried
// against that identity. Grant generation and assignment are checked again after
// signing, so cancellation or expiry cannot be hidden by a slow signer.
func (s *Proofs) CreateUpload(ctx context.Context, caller Caller, in ProofUploadInput) (ProofUpload, images.UploadGrant, error) {
	var err error
	caller, in.OrderID, err = proofCaller(caller, in.OrderID)
	if err != nil {
		return ProofUpload{}, images.UploadGrant{}, err
	}
	id, err := parseID(in.UploadID)
	if err != nil || !validImageType(in.ContentType) || in.SizeBytes < 1 || in.SizeBytes > MaxImageBytes {
		return ProofUpload{}, images.UploadGrant{}, ErrInvalid
	}
	in.UploadID = id.String()
	if s.grants == nil {
		return ProofUpload{}, images.UploadGrant{}, ErrUnavailable
	}

	var upload ProofUpload
	err = s.locked(ctx, in.OrderID, func(o Order, tx ProofTx) (*Change, error) {
		now := s.timestamp()
		if err := canUploadProof(o, caller, now); err != nil {
			return nil, err
		}
		stored, err := tx.FindProofUpload(ctx, o.ID, caller.ID, in.UploadID)
		if err != nil && !errors.Is(err, ErrNotFound) {
			return nil, err
		}
		if errors.Is(err, ErrNotFound) {
			count, err := tx.CountStagedProofs(ctx, o.ID, caller.ID)
			if err != nil {
				return nil, err
			}
			if count >= MaxStagedProofs {
				return nil, fmt.Errorf("proof staging limit reached; retry an existing upload or wait for orphan cleanup: %w", ErrPrecondition)
			}
			proofID := s.newID()
			stored = ProofUpload{
				ID: proofID, OrderID: o.ID, CourierID: caller.ID, UploadID: in.UploadID,
				Key: "order/proof/" + proofID, ContentType: in.ContentType, SizeBytes: in.SizeBytes,
				CreatedAt: now, State: ProofUploading,
			}
		} else if stored.State != ProofUploading {
			return nil, ErrPrecondition
		} else if stored.ContentType != in.ContentType || stored.SizeBytes != in.SizeBytes {
			return nil, ErrAlreadyExists
		}
		if stored.Generation == 0 || !stored.ExpiresAt.After(now) {
			stored.Generation++
			stored.GrantedAt = now
			stored.ExpiresAt = now.Add(ImageUploadLifetime)
			if err := tx.SaveProofUpload(ctx, stored); err != nil {
				return nil, err
			}
		}
		upload = stored
		return nil, nil
	})
	if err != nil {
		return ProofUpload{}, images.UploadGrant{}, err
	}

	grant, err := s.grants.CreateProofGrant(ctx, upload)
	if err != nil || grant.URL == "" || grant.Method != "PUT" || !grant.ExpiresAt.Equal(upload.ExpiresAt) {
		return ProofUpload{}, images.UploadGrant{}, ErrUnavailable
	}
	err = s.locked(ctx, in.OrderID, func(o Order, tx ProofTx) (*Change, error) {
		if err := canUploadProof(o, caller, s.timestamp()); err != nil {
			return nil, err
		}
		stored, err := tx.GetProofUpload(ctx, upload.ID)
		if err != nil {
			return nil, err
		}
		if !proofUploadMatches(stored, upload) || !grant.ExpiresAt.After(s.timestamp()) {
			return nil, ErrConflict
		}
		return nil, nil
	})
	if err != nil {
		return ProofUpload{}, images.UploadGrant{}, err
	}
	return upload, grant, nil
}

// AbandonUpload retires a client upload identity, even if an earlier admission
// response was lost. The durable fence prevents late admissions and attachment.
// It does not delete bytes or revoke an outstanding immutable PUT grant.
func (s *Proofs) AbandonUpload(ctx context.Context, caller Caller, orderID, uploadID string) error {
	var err error
	caller, orderID, err = proofCaller(caller, orderID)
	if err != nil {
		return err
	}
	id, err := parseID(uploadID)
	if err != nil {
		return ErrInvalid
	}
	return s.locked(ctx, orderID, func(o Order, tx ProofTx) (*Change, error) {
		if !o.IsAssignedTo(caller.ID) {
			return nil, ErrNotFound
		}
		row, err := tx.FindProofUpload(ctx, orderID, caller.ID, id.String())
		if err != nil && !errors.Is(err, ErrNotFound) {
			return nil, err
		}
		if err == nil && row.State == ProofAccepted {
			return nil, ErrPrecondition
		}
		return nil, tx.AbandonProofUpload(ctx, orderID, caller.ID, id.String(), s.timestamp())
	})
}

// VerifyUpload resolves a successful, failed or lost PUT response. A 412 from
// an immutable PUT is not itself failure: inspect and decode the existing object.
// This does not confirm delivery or change its deadline.
func (s *Proofs) VerifyUpload(ctx context.Context, caller Caller, orderID, proofID string) error {
	var err error
	caller, orderID, err = proofCaller(caller, orderID)
	if err != nil {
		return err
	}
	id, err := parseID(proofID)
	if err != nil {
		return err
	}

	var upload ProofUpload
	err = s.locked(ctx, orderID, func(o Order, tx ProofTx) (*Change, error) {
		if err := canUploadProof(o, caller, s.timestamp()); err != nil {
			return nil, err
		}
		row, err := tx.GetProofUpload(ctx, id.String())
		if err != nil {
			return nil, err
		}
		if row.State != ProofUploading || !proofBelongsTo(row, o.ID, caller.ID) {
			return nil, ErrNotFound
		}
		upload = row
		return nil, nil
	})
	if err != nil {
		return err
	}
	if err := s.verifyObject(ctx, upload); err != nil {
		return err
	}
	return s.locked(ctx, orderID, func(o Order, tx ProofTx) (*Change, error) {
		if err := canUploadProof(o, caller, s.timestamp()); err != nil {
			return nil, err
		}
		if err := s.recheckProofs(ctx, tx, o, caller, []ProofUpload{upload}); err != nil {
			return nil, err
		}
		if verified, ok := tx.(interface {
			MarkProofVerified(context.Context, string) error
		}); ok {
			return nil, verified.MarkProofVerified(ctx, upload.ID)
		}
		return nil, nil
	})
}

// ConfirmDelivery is the immediate application operation. The production RPC
// uses ProofReceipts to admit a durable receipt before invoking this finalizer.
func (s *Proofs) ConfirmDelivery(ctx context.Context, caller Caller, in ConfirmDeliveryInput) (Order, error) {
	return s.confirmReceipt(ctx, caller, in, nil)
}

func (s *Proofs) confirmReceipt(ctx context.Context, caller Caller, in ConfirmDeliveryInput, receipt *DeliveryReceipt) (Order, error) {
	var err error
	caller, in.OrderID, err = proofCaller(caller, in.OrderID)
	if err != nil {
		return Order{}, err
	}
	in.ProofIDs, err = normalizedProofIDs(in.ProofIDs)
	if err != nil || (len(in.ProofIDs) == 0) != in.WithoutProof {
		return Order{}, ErrInvalid
	}

	var result Order
	var uploads []ProofUpload
	var repeat bool
	err = s.locked(ctx, in.OrderID, func(o Order, tx ProofTx) (*Change, error) {
		var err error
		at, err := s.receiptTime(ctx, tx, caller, in, receipt)
		if err != nil {
			return nil, err
		}
		repeat, err = s.confirmationState(ctx, tx, o, caller, in, at)
		if err != nil {
			return nil, err
		}
		if repeat {
			result = o
			return nil, nil
		}
		for _, id := range in.ProofIDs {
			row, err := tx.GetProofUpload(ctx, id)
			if err != nil {
				if errors.Is(err, ErrNotFound) {
					return nil, ErrPrecondition
				}
				return nil, err
			}
			if row.State != ProofUploading || !proofBelongsTo(row, o.ID, caller.ID) {
				return nil, ErrPrecondition
			}
			uploads = append(uploads, row)
		}
		return nil, nil
	})
	if err != nil || repeat {
		return result, err
	}

	var outage ProofOutage
	if in.WithoutProof {
		if receipt != nil && receipt.Outage != nil {
			outage = *receipt.Outage
		} else {
			if s.outages == nil {
				return Order{}, ErrPrecondition
			}
			outage, err = s.outages.ConfirmProofOutage(ctx)
		}
		if err != nil {
			return Order{}, ErrUnavailable
		}
		at := s.timestamp()
		if receipt != nil {
			at = receipt.ReceivedAt
		}
		if !s.isCurrentProofOutage(outage, at) {
			return Order{}, ErrPrecondition
		}
	} else {
		for _, upload := range uploads {
			if err := s.verifyObject(ctx, upload); err != nil {
				return Order{}, err
			}
		}
	}

	err = s.locked(ctx, in.OrderID, func(o Order, tx ProofTx) (*Change, error) {
		now, err := s.receiptTime(ctx, tx, caller, in, receipt)
		if err != nil {
			return nil, err
		}
		processedAt := s.timestamp()
		if receipt != nil {
			processedAt, err = tx.(ReceiptTx).ProofServerTime(ctx)
			if err != nil {
				return nil, err
			}
		}
		repeat, err := s.confirmationState(ctx, tx, o, caller, in, now)
		if err != nil {
			return nil, err
		}
		if repeat {
			result = o
			return nil, nil
		}
		if in.WithoutProof && receipt == nil {
			if !s.isCurrentProofOutage(outage, now) {
				return nil, ErrPrecondition
			}
			current, err := tx.IsProofOutageCurrent(ctx, outage)
			if err != nil {
				return nil, err
			}
			if !current {
				return nil, ErrPrecondition
			}
		}
		if err := s.recheckProofs(ctx, tx, o, caller, uploads); err != nil {
			return nil, err
		}
		actor := Actor{Kind: User, ID: caller.ID}
		next, _, err := Transition(o, Deliver, actor)
		if err != nil {
			return nil, err
		}
		after := cloneOrder(o)
		after.Status = next
		after.DeliveredAt = &now
		after.UnattendedDelivery = in.Unattended
		after.Revision++
		after.UpdatedAt = processedAt
		confirmation := ProofConfirmation{
			OrderID: o.ID, CourierID: caller.ID, ProofIDs: slices.Clone(in.ProofIDs),
			Unattended: in.Unattended, OutageReason: outage.Reason, ConfirmedAt: now,
		}
		if in.WithoutProof {
			confirmation.Outage = &outage
		}
		details, err := json.Marshal(confirmation)
		if err != nil {
			return nil, err
		}
		change, err := NewChange(&o, after, actor, Deliver, details, s.newID)
		if err != nil {
			return nil, err
		}
		if err := tx.SaveProofConfirmation(ctx, confirmation, uploads); err != nil {
			return nil, err
		}
		result = after
		return &change, nil
	})
	if err != nil {
		return Order{}, err
	}
	return result, nil
}

// receiptTime never accepts a client clock. Re-read the immutable durable
// record under the parent lock before using its earlier effective timestamp.
func (s *Proofs) receiptTime(ctx context.Context, tx ProofTx, c Caller, in ConfirmDeliveryInput, expected *DeliveryReceipt) (time.Time, error) {
	if expected == nil {
		return s.timestamp(), nil
	}
	receipts, ok := tx.(ReceiptTx)
	if !ok {
		return time.Time{}, ErrUnavailable
	}
	stored, err := receipts.GetDeliveryReceipt(ctx, expected.ID)
	if err != nil {
		return time.Time{}, err
	}
	if (stored.State != "pending" && stored.State != "accepted") || !stored.matches(c, in) || !stored.ReceivedAt.Equal(expected.ReceivedAt) {
		return time.Time{}, ErrPrecondition
	}
	return stored.ReceivedAt, nil
}

// confirmationState recognizes only a durable, matching accepted input, before
// checking a first confirmation's status or deadline. Completed alone is not
// evidence that this particular confirmation succeeded.
func (s *Proofs) confirmationState(ctx context.Context, tx ProofTx, o Order, caller Caller, in ConfirmDeliveryInput, now time.Time) (bool, error) {
	if !o.IsAssignedTo(caller.ID) {
		return false, ErrNotFound
	}
	accepted, err := tx.GetProofConfirmation(ctx, o.ID)
	if err == nil {
		if !strings.EqualFold(accepted.CourierID, caller.ID) || accepted.Unattended != in.Unattended || !slices.Equal(accepted.ProofIDs, in.ProofIDs) || (accepted.OutageReason != "") != in.WithoutProof {
			return false, ErrPrecondition
		}
		if o.Status != Delivered && o.Status != Completed {
			return false, ErrPrecondition
		}
		return true, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return false, err
	}
	return false, canUploadProof(o, caller, now)
}

func (s *Proofs) recheckProofs(ctx context.Context, tx ProofTx, o Order, caller Caller, rows []ProofUpload) error {
	for _, expected := range rows {
		stored, err := tx.GetProofUpload(ctx, expected.ID)
		if err != nil {
			return err
		}
		if stored.State != ProofUploading || !proofBelongsTo(stored, o.ID, caller.ID) || stored.ID != expected.ID || stored.Key != expected.Key || stored.ContentType != expected.ContentType || stored.SizeBytes != expected.SizeBytes {
			return ErrPrecondition
		}
	}
	return nil
}

func (s *Proofs) verifyObject(ctx context.Context, upload ProofUpload) error {
	// Admit before fetching or allocating object bytes. Do not retain an
	// unbounded queue of 10 MB bodies waiting for the native decoder.
	select {
	case s.verificationSlots <- struct{}{}:
		defer func() { <-s.verificationSlots }()
	default:
		return ErrUnavailable
	}
	if s.objects == nil || s.validator == nil {
		return ErrUnavailable
	}
	info, err := s.objects.Inspect(ctx, upload.Key)
	if errors.Is(err, images.ErrObjectNotFound) {
		return ErrPrecondition
	}
	if err != nil {
		return ErrUnavailable
	}
	if !info.Exists || info.Size != upload.SizeBytes || info.Size < 1 || info.Size > MaxImageBytes || info.ContentType != upload.ContentType {
		return ErrPrecondition
	}
	body, err := s.objects.ReadProof(ctx, upload.Key, MaxImageBytes)
	if errors.Is(err, images.ErrObjectNotFound) {
		return ErrPrecondition
	}
	if err != nil {
		return ErrUnavailable
	}
	defer func() { _ = body.Close() }()
	data, err := io.ReadAll(io.LimitReader(body, MaxImageBytes+1))
	if err != nil {
		return ErrUnavailable
	}
	if int64(len(data)) != upload.SizeBytes {
		return ErrPrecondition
	}
	if err := s.validator.Validate(ctx, upload.ContentType, data); err != nil {
		return err
	}
	return nil
}

// ForOrder authorizes participants before signing downloads. Nonparticipant
// admins fail closed until a revocation-safe case authority is implemented.
// A cached boolean case lookup is not sufficient authorization.
func (s *Proofs) ForOrder(ctx context.Context, caller Caller, orderID string) (DeliveryProofView, error) {
	var err error
	caller, orderID, err = proofCaller(caller, orderID)
	if err != nil {
		return DeliveryProofView{}, err
	}
	var confirmation ProofConfirmation
	var rows []ProofUpload
	err = s.locked(ctx, orderID, func(o Order, tx ProofTx) (*Change, error) {
		if !o.HasParticipant(caller.ID) {
			return nil, ErrNotFound
		}
		stored, err := tx.GetProofConfirmation(ctx, orderID)
		if errors.Is(err, ErrNotFound) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		confirmation = stored
		for _, id := range stored.ProofIDs {
			row, err := tx.GetProofUpload(ctx, id)
			if err != nil {
				return nil, err
			}
			if row.State != ProofAccepted || !proofBelongsTo(row, orderID, stored.CourierID) {
				return nil, ErrPrecondition
			}
			rows = append(rows, row)
		}
		return nil, nil
	})
	if err != nil {
		return DeliveryProofView{}, err
	}

	out := DeliveryProofView{Proofs: []ProofView{}, OutageReason: confirmation.OutageReason}
	for _, row := range rows {
		if s.objects == nil {
			return DeliveryProofView{}, ErrUnavailable
		}
		grant, err := s.objects.CreateDownload(ctx, row.Key)
		if err != nil {
			return DeliveryProofView{}, ErrUnavailable
		}
		out.Proofs = append(out.Proofs, ProofView{ID: row.ID, DownloadURL: grant.URL, ExpiresAt: grant.ExpiresAt})
	}
	return out, nil
}

func proofCaller(caller Caller, orderID string) (Caller, string, error) {
	id, err := parseID(caller.ID)
	if err != nil {
		return caller, "", ErrForbidden
	}
	order, err := parseID(orderID)
	if err != nil {
		return caller, "", ErrInvalid
	}
	caller.ID = id.String()
	return caller, order.String(), nil
}

func normalizedProofIDs(ids []string) ([]string, error) {
	if len(ids) > 5 {
		return nil, ErrInvalid
	}
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		parsed, err := parseID(id)
		if err != nil {
			return nil, err
		}
		out = append(out, parsed.String())
	}
	slices.Sort(out)
	for i := 1; i < len(out); i++ {
		if out[i] == out[i-1] {
			return nil, ErrInvalid
		}
	}
	return out, nil
}

func canUploadProof(o Order, caller Caller, now time.Time) error {
	if !o.IsAssignedTo(caller.ID) {
		return ErrNotFound
	}
	if o.Status != Delivering || o.PickedUpAt == nil || o.DeliveryDuration <= 0 || !now.Before(o.PickedUpAt.Add(2*o.DeliveryDuration)) {
		return ErrPrecondition
	}
	return nil
}

// proofUploadMatches compares persisted values, not time zone or monotonic
// clock representation, while rejecting any changed identity or grant field.
func proofUploadMatches(stored, expected ProofUpload) bool {
	return stored.State == expected.State && stored.ID == expected.ID &&
		stored.OrderID == expected.OrderID && stored.CourierID == expected.CourierID &&
		stored.UploadID == expected.UploadID && stored.Key == expected.Key &&
		stored.ContentType == expected.ContentType && stored.SizeBytes == expected.SizeBytes &&
		stored.Generation == expected.Generation && stored.SigningEpoch == expected.SigningEpoch && stored.GrantedAt.Equal(expected.GrantedAt) &&
		stored.ExpiresAt.Equal(expected.ExpiresAt) && stored.CreatedAt.Equal(expected.CreatedAt)
}

func proofBelongsTo(row ProofUpload, orderID, courierID string) bool {
	return row.OrderID == orderID && strings.EqualFold(row.CourierID, courierID)
}

func (s *Proofs) isCurrentProofOutage(outage ProofOutage, now time.Time) bool {
	return outage.Reason == "storage_service_unavailable" &&
		len(outage.EvidenceID) > 0 && len(outage.EvidenceID) <= 200 && outage.Generation > 0 &&
		s.uploadScope != "" && outage.UploadScope == s.uploadScope &&
		!outage.ObservedAt.After(now) && now.Sub(outage.ObservedAt) <= maxProofOutageAge &&
		outage.ValidUntil.After(now) && !outage.ValidUntil.After(outage.ObservedAt.Add(maxProofOutageAge))
}

func (s *Proofs) timestamp() time.Time { return s.now().UTC().Truncate(time.Microsecond) }

// borrowedProofRepository lets the existing Mutator own lock -> fresh relations
// -> build -> Apply inside the proof repository's ONE transaction. It does not
// start a nested transaction or retry a callback.
type borrowedProofRepository struct{ tx ProofTx }

func (r borrowedProofRepository) Within(_ context.Context, fn func(Tx) error) error { return fn(r.tx) }

func (s *Proofs) locked(ctx context.Context, id string, build func(Order, ProofTx) (*Change, error)) error {
	return s.repository.WithinProofs(ctx, func(tx ProofTx) error {
		return NewMutator(borrowedProofRepository{tx}).Mutate(ctx, id, func(o Order, _ []Offer) (*Change, error) {
			return build(o, tx)
		})
	})
}
