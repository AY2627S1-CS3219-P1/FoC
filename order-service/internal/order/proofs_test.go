package order

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"reflect"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/AY2627S1-CS3219-P1/FoC/order-service/internal/images"
	"github.com/google/uuid"
)

// These cases derive from approved Q1–Q11 in docs/plans/delivery-proof.md and
// R1.7, NFR-06.3 and NFR-18, not from implementation-derived expected values.
const (
	proofOrderID   = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	proofCourier   = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	proofRequester = "cccccccc-cccc-4ccc-8ccc-cccccccccccc"
	proofOther     = "dddddddd-dddd-4ddd-8ddd-dddddddddddd"
)

type proofUploadIdentity struct {
	orderID, courierID, uploadID string
}

type proofMemory struct {
	mu                sync.Mutex
	order             Order
	uploads           map[string]ProofUpload
	retired           map[proofUploadIdentity]time.Time
	readLocation      *time.Location
	confirmation      *ProofConfirmation
	changes           []Change
	inside            bool
	applyErr, saveErr error
	outageCurrent     bool
}

func (r *proofMemory) WithinProofs(_ context.Context, fn func(ProofTx) error) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	before := r.order
	uploads := maps.Clone(r.uploads)
	retired := maps.Clone(r.retired)
	confirmation := r.confirmation
	changes := slices.Clone(r.changes)
	r.inside = true
	defer func() { r.inside = false }()
	if err := fn(r); err != nil {
		r.order, r.uploads, r.retired, r.confirmation, r.changes = before, uploads, retired, confirmation, changes
		return err
	}
	return nil
}

func (r *proofMemory) LockOrder(_ context.Context, id string) (Order, error) {
	if r.order.ID != id {
		return Order{}, ErrNotFound
	}
	return cloneOrder(r.order), nil
}
func (r *proofMemory) Relationships(context.Context, string) ([]Offer, error) { return nil, nil }
func (r *proofMemory) Apply(_ context.Context, c Change) error {
	if r.applyErr != nil {
		return r.applyErr
	}
	if err := validateChange(&r.order, c); err != nil {
		return err
	}
	r.order = cloneOrder(c.Order)
	r.changes = append(r.changes, c)
	return nil
}
func (r *proofMemory) FindProofUpload(_ context.Context, orderID, courierID, uploadID string) (ProofUpload, error) {
	if at, ok := r.retired[proofUploadIdentity{orderID, courierID, uploadID}]; ok {
		return ProofUpload{State: ProofDeleting, OrderID: orderID, CourierID: courierID, UploadID: uploadID, CreatedAt: at}, nil
	}
	for _, row := range r.uploads {
		if row.OrderID == orderID && row.CourierID == courierID && row.UploadID == uploadID {
			return row, nil
		}
	}
	return ProofUpload{}, ErrNotFound
}
func (r *proofMemory) CountStagedProofs(_ context.Context, orderID, courierID string) (int64, error) {
	var count int64
	for _, row := range r.uploads {
		if row.OrderID == orderID && row.CourierID == courierID && row.State == ProofUploading {
			count++
		}
	}
	return count, nil
}

func (r *proofMemory) GetProofUpload(_ context.Context, id string) (ProofUpload, error) {
	row, ok := r.uploads[id]
	if !ok {
		return ProofUpload{}, ErrNotFound
	}
	if r.readLocation != nil {
		row.GrantedAt = row.GrantedAt.In(r.readLocation)
		row.ExpiresAt = row.ExpiresAt.In(r.readLocation)
		row.CreatedAt = row.CreatedAt.In(r.readLocation)
	}
	return row, nil
}
func (r *proofMemory) SaveProofUpload(_ context.Context, row ProofUpload) error {
	if r.saveErr != nil {
		return r.saveErr
	}
	r.uploads[row.ID] = row
	return nil
}
func (r *proofMemory) GetProofConfirmation(_ context.Context, id string) (ProofConfirmation, error) {
	if r.confirmation == nil || r.confirmation.OrderID != id {
		return ProofConfirmation{}, ErrNotFound
	}
	out := *r.confirmation
	out.ProofIDs = slices.Clone(out.ProofIDs)
	return out, nil
}
func (r *proofMemory) IsProofOutageCurrent(context.Context, ProofOutage) (bool, error) {
	return r.outageCurrent, nil
}

func (r *proofMemory) SaveProofConfirmation(_ context.Context, c ProofConfirmation, rows []ProofUpload) error {
	if r.saveErr != nil {
		return r.saveErr
	}
	if r.confirmation != nil {
		return ErrConflict
	}
	for _, row := range rows {
		if stored, ok := r.uploads[row.ID]; !ok || !proofUploadMatches(stored, row) {
			return ErrConflict
		}
	}
	for _, row := range rows {
		row.State = ProofAccepted
		r.uploads[row.ID] = row
	}
	c.ProofIDs = slices.Clone(c.ProofIDs)
	r.confirmation = &c
	return nil
}

type proofDependencies struct {
	repo                          *proofMemory
	now                           *time.Time
	body                          []byte
	missing, invalid, unavailable bool
	downloads, inspections        int
	onInspect, onGrant            func()
	outage                        ProofOutage
	outageErr                     error
}

func (d *proofDependencies) outside() {
	if d.repo.inside {
		panic("remote work inside order transaction")
	}
}
func (d *proofDependencies) CreateProofGrant(_ context.Context, row ProofUpload) (images.UploadGrant, error) {
	d.outside()
	if d.onGrant != nil {
		d.onGrant()
	}
	if d.unavailable {
		return images.UploadGrant{}, errors.New("signing failure")
	}
	return images.UploadGrant{URL: fmt.Sprintf("https://storage.invalid/%s?generation=%d", row.Key, row.Generation), Method: "PUT", ExpiresAt: row.ExpiresAt, Headers: map[string]string{"If-None-Match": "*"}}, nil
}
func (d *proofDependencies) Inspect(context.Context, string) (images.ObjectInfo, error) {
	d.outside()
	d.inspections++
	if d.onInspect != nil {
		d.onInspect()
	}
	if d.unavailable {
		return images.ObjectInfo{}, errors.New("storage unavailable")
	}
	if d.missing {
		return images.ObjectInfo{}, images.ErrObjectNotFound
	}
	return images.ObjectInfo{Exists: true, Size: int64(len(d.body)), ContentType: "image/png"}, nil
}
func (d *proofDependencies) ReadProof(context.Context, string, int64) (io.ReadCloser, error) {
	d.outside()
	return io.NopCloser(bytes.NewReader(d.body)), nil
}
func (d *proofDependencies) CreateDownload(context.Context, string) (images.DownloadGrant, error) {
	d.outside()
	d.downloads++
	return images.DownloadGrant{URL: "https://storage.invalid/read", ExpiresAt: d.now.Add(5 * time.Minute)}, nil
}
func (d *proofDependencies) Validate(context.Context, string, []byte) error {
	d.outside()
	if d.invalid {
		return ErrPrecondition
	}
	return nil
}
func (d *proofDependencies) ConfirmProofOutage(context.Context) (ProofOutage, error) {
	d.outside()
	return d.outage, d.outageErr
}

func proofScenario() (*Proofs, *proofMemory, *proofDependencies, *time.Time) {
	now := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	picked := now.Add(-10 * time.Minute)
	courier := proofCourier
	r := &proofMemory{
		order: Order{ID: proofOrderID, RequesterID: proofRequester, CourierID: &courier, Status: Delivering,
			PickedUpAt: &picked, DeliveryDuration: 30 * time.Minute, Revision: 4, CreatedAt: picked, UpdatedAt: picked},
		uploads: map[string]ProofUpload{},
	}
	d := &proofDependencies{repo: r, now: &now, body: []byte("decoded by the injected validator")}
	s := NewProofs(r, d, d, d, ProofOptions{Now: func() time.Time { return now }, NewID: uuid.NewString, Outages: d, UploadScope: "proof-primary"})
	return s, r, d, &now
}
func proofUploadInput(d *proofDependencies) ProofUploadInput {
	return ProofUploadInput{OrderID: proofOrderID, UploadID: uuid.NewString(), ContentType: "image/png", SizeBytes: int64(len(d.body))}
}
func uploadedProof(t *testing.T, s *Proofs, d *proofDependencies) ProofUpload {
	t.Helper()
	row, _, err := s.CreateUpload(context.Background(), Caller{ID: proofCourier}, proofUploadInput(d))
	if err != nil {
		t.Fatal(err)
	}
	return row
}
func proofConfirmationInput(row ProofUpload) ConfirmDeliveryInput {
	return ConfirmDeliveryInput{OrderID: proofOrderID, ProofIDs: []string{row.ID}}
}

func TestProofUploadIdentityAndExpiredGrant(t *testing.T) {
	s, r, d, now := proofScenario()
	in := proofUploadInput(d)
	caller := Caller{ID: proofCourier}
	row, first, err := s.CreateUpload(t.Context(), caller, in)
	if err != nil {
		t.Fatal(err)
	}
	*now = now.Add(time.Minute)
	again, second, err := s.CreateUpload(t.Context(), caller, in)
	if err != nil || again.ID != row.ID || !reflect.DeepEqual(first, second) || len(r.uploads) != 1 {
		t.Fatalf("unexpired repeat: %v %+v", err, r.uploads)
	}
	*now = first.ExpiresAt
	refreshed, third, err := s.CreateUpload(t.Context(), caller, in)
	if err != nil || refreshed.ID != row.ID || third.URL == first.URL || !third.ExpiresAt.After(first.ExpiresAt) || len(r.uploads) != 1 {
		t.Fatalf("expired repeat: %v %+v", err, refreshed)
	}
	in.SizeBytes++
	if _, _, err := s.CreateUpload(t.Context(), caller, in); !errors.Is(err, ErrAlreadyExists) {
		t.Fatalf("changed retry: %v", err)
	}
}

func TestProofSigningFailureRetainsStableIdentity(t *testing.T) {
	s, r, d, _ := proofScenario()
	in := proofUploadInput(d)
	d.unavailable = true
	if _, _, err := s.CreateUpload(t.Context(), Caller{ID: proofCourier}, in); !errors.Is(err, ErrUnavailable) {
		t.Fatal(err)
	}
	d.unavailable = false
	row, _, err := s.CreateUpload(t.Context(), Caller{ID: proofCourier}, in)
	if err != nil || len(r.uploads) != 1 || r.uploads[row.ID].ID != row.ID {
		t.Fatalf("retry: %v rows=%v", err, r.uploads)
	}
}

func TestProofUploadRechecksAssignmentAfterSigning(t *testing.T) {
	s, _, d, _ := proofScenario()
	d.onGrant = func() { d.repo.order.Status = Cancelled }
	if _, _, err := s.CreateUpload(t.Context(), Caller{ID: proofCourier}, proofUploadInput(d)); !errors.Is(err, ErrPrecondition) {
		t.Fatal(err)
	}
}

func TestProofUploadAuthorizationAndByteLimits(t *testing.T) {
	for _, tc := range []struct {
		name   string
		caller Caller
		size   int64
		media  string
		want   error
	}{
		{"anonymous", Caller{}, 1, "image/png", ErrForbidden},
		{"requester", Caller{ID: proofRequester}, 1, "image/png", ErrNotFound},
		{"admin", Caller{ID: proofOther, Admin: true}, 1, "image/png", ErrNotFound},
		{"empty", Caller{ID: proofCourier}, 0, "image/png", ErrInvalid},
		{"oversized", Caller{ID: proofCourier}, 10000001, "image/png", ErrInvalid},
		{"unsupported", Caller{ID: proofCourier}, 1, "text/plain", ErrInvalid},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, _, d, _ := proofScenario()
			in := proofUploadInput(d)
			in.SizeBytes = tc.size
			in.ContentType = tc.media
			if _, _, err := s.CreateUpload(t.Context(), tc.caller, in); !errors.Is(err, tc.want) {
				t.Fatalf("got %v want %v", err, tc.want)
			}
		})
	}
}

func TestProofConfirmationRequiresUploadedDecodedObjects(t *testing.T) {
	for _, kind := range []string{"missing", "invalid", "unavailable", "wrong size", "wrong order"} {
		t.Run(kind, func(t *testing.T) {
			s, r, d, _ := proofScenario()
			row := uploadedProof(t, s, d)
			want := ErrPrecondition
			switch kind {
			case "missing":
				d.missing = true
			case "invalid":
				d.invalid = true
			case "unavailable":
				d.unavailable = true
				want = ErrUnavailable
			case "wrong size":
				d.body = append(d.body, 'x')
			case "wrong order":
				row.OrderID = proofOther
				r.uploads[row.ID] = row
			}
			if _, err := s.ConfirmDelivery(t.Context(), Caller{ID: proofCourier}, proofConfirmationInput(row)); !errors.Is(err, want) {
				t.Fatalf("got %v want %v", err, want)
			}
			if r.order.Status != Delivering || len(r.changes) != 0 || r.confirmation != nil {
				t.Fatal("failed verification changed delivery")
			}
		})
	}
}

func TestProofFiveUploadsConfirmOnceAndRetryAfterReceipt(t *testing.T) {
	s, r, d, _ := proofScenario()
	in := ConfirmDeliveryInput{OrderID: proofOrderID, Unattended: true}
	for range 5 {
		in.ProofIDs = append(in.ProofIDs, uploadedProof(t, s, d).ID)
	}
	first, err := s.ConfirmDelivery(t.Context(), Caller{ID: proofCourier}, in)
	if err != nil || first.Status != Delivered || !first.UnattendedDelivery || len(r.changes) != 1 {
		t.Fatalf("confirm: %v %+v", err, first)
	}
	if len(r.changes[0].Facts) != 1 || r.changes[0].Facts[0].Kind != StatusChanged || len(r.confirmation.ProofIDs) != 5 {
		t.Fatal("incorrect audit/outbox/attachment")
	}
	calls := d.inspections
	slices.Reverse(in.ProofIDs)
	if _, err = s.ConfirmDelivery(t.Context(), Caller{ID: proofCourier}, in); err != nil {
		t.Fatal(err)
	}
	// Simulate the separately owned receipt operation after this confirmation.
	r.order.Status = Completed
	if result, err := s.ConfirmDelivery(t.Context(), Caller{ID: proofCourier}, in); err != nil || result.Status != Completed || len(r.changes) != 1 || d.inspections != calls {
		t.Fatalf("completed retry: %v %+v", err, result)
	}
	in.Unattended = false
	if _, err = s.ConfirmDelivery(t.Context(), Caller{ID: proofCourier}, in); !errors.Is(err, ErrPrecondition) {
		t.Fatal(err)
	}
}

func TestProofConcurrentConfirmationsProduceOneChange(t *testing.T) {
	s, r, d, _ := proofScenario()
	in := proofConfirmationInput(uploadedProof(t, s, d))
	// The dependency counter is intentionally removed from this concurrency
	// case; immutable object reads are safe and counted in sequential tests.
	var remoteMu sync.Mutex
	s.objects = &lockedProofObjects{d: d, mu: &remoteMu}
	s.validator = proofAcceptingValidator{}
	var wg sync.WaitGroup
	errs := make(chan error, 12)
	for range 12 {
		wg.Go(func() { _, err := s.ConfirmDelivery(t.Context(), Caller{ID: proofCourier}, in); errs <- err })
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if errors.Is(err, ErrUnavailable) {
			if _, retryErr := s.ConfirmDelivery(t.Context(), Caller{ID: proofCourier}, in); retryErr != nil {
				t.Fatal(retryErr)
			}
		} else if err != nil {
			t.Fatal(err)
		}
	}
	if len(r.changes) != 1 || r.order.Revision != 5 {
		t.Fatalf("duplicate changes: %d revision=%d", len(r.changes), r.order.Revision)
	}
}

type proofAcceptingValidator struct{}

func (proofAcceptingValidator) Validate(context.Context, string, []byte) error { return nil }

type lockedProofObjects struct {
	d  *proofDependencies
	mu *sync.Mutex
}

func (s *lockedProofObjects) Inspect(context.Context, string) (images.ObjectInfo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return images.ObjectInfo{Exists: true, Size: int64(len(s.d.body)), ContentType: "image/png"}, nil
}
func (s *lockedProofObjects) ReadProof(context.Context, string, int64) (io.ReadCloser, error) {
	return io.NopCloser(bytes.NewReader(s.d.body)), nil
}
func (s *lockedProofObjects) CreateDownload(context.Context, string) (images.DownloadGrant, error) {
	return images.DownloadGrant{}, nil
}

func TestProofDeadlineRecheckedAfterObjectRead(t *testing.T) {
	for _, kind := range []string{"exact deadline", "cancelled", "timed out", "changed proof", "cleanup fenced"} {
		t.Run(kind, func(t *testing.T) {
			s, r, d, now := proofScenario()
			row := uploadedProof(t, s, d)
			d.onInspect = func() {
				switch kind {
				case "exact deadline":
					*now = r.order.PickedUpAt.Add(2 * r.order.DeliveryDuration)
				case "cancelled":
					r.order.Status = Cancelled
				case "timed out":
					r.order.Status = Failed
				case "cleanup fenced":
					changed := r.uploads[row.ID]
					changed.State = ProofDeleting
					r.uploads[row.ID] = changed
				case "changed proof":
					changed := r.uploads[row.ID]
					changed.Key += "-changed"
					r.uploads[row.ID] = changed
				}
			}
			if _, err := s.ConfirmDelivery(t.Context(), Caller{ID: proofCourier}, proofConfirmationInput(row)); !errors.Is(err, ErrPrecondition) {
				t.Fatal(err)
			}
			if r.confirmation != nil || len(r.changes) != 0 {
				t.Fatal("late or stale confirmation committed")
			}
		})
	}
}

func TestProofConfirmationRollsBackAttachmentOnApplyFailure(t *testing.T) {
	s, r, d, _ := proofScenario()
	row := uploadedProof(t, s, d)
	r.applyErr = errors.New("failed aggregate write")
	if _, err := s.ConfirmDelivery(t.Context(), Caller{ID: proofCourier}, proofConfirmationInput(row)); err == nil {
		t.Fatal("expected failure")
	}
	if r.confirmation != nil || r.order.Status != Delivering || len(r.changes) != 0 {
		t.Fatal("partial confirmation committed")
	}
}

func TestProofOutageExceptionRequiresFreshServerAuthority(t *testing.T) {
	for _, tc := range []struct {
		name       string
		age        time.Duration
		reason     string
		authority  bool
		unattended bool
		want       error
	}{
		{"no authority", 0, "", false, false, ErrPrecondition},
		{"unconfirmed", 0, "", true, false, ErrPrecondition},
		{"stale", time.Minute, "storage_service_unavailable", true, false, ErrPrecondition},
		{"future", -time.Second, "storage_service_unavailable", true, false, ErrPrecondition},
		{"handover", 0, "storage_service_unavailable", true, false, nil},
		{"unattended", 0, "storage_service_unavailable", true, true, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, r, d, now := proofScenario()
			d.outage = ProofOutage{Reason: tc.reason, ObservedAt: now.Add(-tc.age), ValidUntil: now.Add(-tc.age).Add(30 * time.Second), UploadScope: "proof-primary", EvidenceID: "server-outage-1", Generation: 1}
			r.outageCurrent = true
			if !tc.authority {
				s.outages = nil
			}
			in := ConfirmDeliveryInput{OrderID: proofOrderID, WithoutProof: true, Unattended: tc.unattended}
			_, err := s.ConfirmDelivery(t.Context(), Caller{ID: proofCourier}, in)
			if !errors.Is(err, tc.want) {
				t.Fatalf("got %v want %v", err, tc.want)
			}
			if tc.want == nil {
				if r.order.Status != Delivered || r.confirmation.OutageReason != tc.reason || r.order.UnattendedDelivery != tc.unattended {
					t.Fatal("exception was not recorded")
				}
			} else if r.order.Status != Delivering {
				t.Fatal("unconfirmed outage bypass")
			}
		})
	}
}

// Q10 explicitly denies nonparticipant admin access while the authoritative,
// revocation-safe case authorization dependency is unavailable.
func TestProofReadAuthorizationPrecedesSignedDownloads(t *testing.T) {
	for _, tc := range []struct {
		name    string
		caller  Caller
		allowed bool
	}{
		{"requester", Caller{ID: proofRequester}, true},
		{"courier", Caller{ID: proofCourier}, true},
		{"former courier", Caller{ID: proofOther}, false},
		{"nonparticipant admin", Caller{ID: proofOther, Admin: true}, false},
		{"participant admin", Caller{ID: proofRequester, Admin: true}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, _, d, _ := proofScenario()
			row := uploadedProof(t, s, d)
			if _, err := s.ConfirmDelivery(t.Context(), Caller{ID: proofCourier}, proofConfirmationInput(row)); err != nil {
				t.Fatal(err)
			}
			view, err := s.ForOrder(t.Context(), tc.caller, proofOrderID)
			if tc.allowed {
				if err != nil || len(view.Proofs) != 1 || d.downloads != 1 {
					t.Fatalf("authorized: %v %+v", err, view)
				}
			} else if !errors.Is(err, ErrNotFound) || d.downloads != 0 {
				t.Fatalf("unauthorized signed proof: %v downloads=%d", err, d.downloads)
			}
		})
	}
}

func TestProofVerifyResolvesLostUploadResponseWithoutDelivery(t *testing.T) {
	s, r, d, _ := proofScenario()
	row := uploadedProof(t, s, d)
	if err := s.VerifyUpload(t.Context(), Caller{ID: proofCourier}, proofOrderID, row.ID); err != nil {
		t.Fatal(err)
	}
	if r.order.Status != Delivering || r.confirmation != nil || len(r.changes) != 0 {
		t.Fatal("upload verification confirmed delivery")
	}
	d.missing = true
	if err := s.VerifyUpload(t.Context(), Caller{ID: proofCourier}, proofOrderID, row.ID); !errors.Is(err, ErrPrecondition) {
		t.Fatal(err)
	}
}

func (r *proofMemory) AbandonProofUpload(ctx context.Context, orderID, courierID, uploadID string, at time.Time) error {
	identity := proofUploadIdentity{orderID, courierID, uploadID}
	if _, ok := r.retired[identity]; ok {
		return nil
	}
	row, err := r.FindProofUpload(ctx, orderID, courierID, uploadID)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return err
	}
	if err == nil {
		if row.State == ProofAccepted {
			return ErrPrecondition
		}
		row.State = ProofDeleting
		if err := r.SaveProofUpload(ctx, row); err != nil {
			return err
		}
	}
	if r.retired == nil {
		r.retired = make(map[proofUploadIdentity]time.Time)
	}
	r.retired[identity] = at
	return nil
}
