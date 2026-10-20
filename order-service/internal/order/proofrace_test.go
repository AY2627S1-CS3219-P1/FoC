package order

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/AY2627S1-CS3219-P1/FoC/order-service/internal/images"
)

// Q1 requires one proof and one unexpired grant generation for concurrent
// identical upload admissions. This tests the application/transaction seam, not
// a concrete S3 signing implementation or PostgreSQL uniqueness constraint.
func TestProofConcurrentUploadAdmissionsKeepOneIdentity(t *testing.T) {
	s, r, d, _ := proofScenario()
	in := proofUploadInput(d)
	// Avoid a fake's global in-transaction assertion racing other goroutines:
	// another transaction may legitimately run during this caller's signing.
	s.grants = concurrentProofIssuer{}
	var wg sync.WaitGroup
	type result struct {
		row   ProofUpload
		grant images.UploadGrant
		err   error
	}
	results := make(chan result, 16)
	for range 16 {
		wg.Go(func() {
			row, grant, err := s.CreateUpload(t.Context(), Caller{ID: proofCourier}, in)
			results <- result{row, grant, err}
		})
	}
	wg.Wait()
	close(results)
	var first result
	for got := range results {
		if got.err != nil {
			t.Fatal(got.err)
		}
		if first.row.ID == "" {
			first = got
		}
		if got.row.ID != first.row.ID || got.grant.URL != first.grant.URL {
			t.Fatal("duplicate identity or grant")
		}
	}
	if len(r.uploads) != 1 || len(r.changes) != 0 {
		t.Fatal("upload admission changed lifecycle or duplicated proof")
	}
}

type concurrentProofIssuer struct{}

func (concurrentProofIssuer) CreateProofGrant(_ context.Context, row ProofUpload) (images.UploadGrant, error) {
	return images.UploadGrant{URL: "https://storage.invalid/" + row.ID, Method: "PUT", ExpiresAt: row.ExpiresAt}, nil
}

// Q8/Q9/Q11 allow a server-confirmed, current and scoped outage only. The
// exception cannot bypass the same deadline or a recorded recovery.
func TestProofOutageRejectsWrongScopeRecoveryAndDeadline(t *testing.T) {
	for _, kind := range []string{"wrong scope", "recovered", "deadline", "missing evidence", "unbounded validity"} {
		t.Run(kind, func(t *testing.T) {
			s, r, d, now := proofScenario()
			d.outage = ProofOutage{Reason: "storage_service_unavailable", EvidenceID: "evidence-1", UploadScope: "proof-primary", Generation: 1, ObservedAt: *now, ValidUntil: now.Add(30 * time.Second)}
			r.outageCurrent = true
			switch kind {
			case "wrong scope":
				d.outage.UploadScope = "another-bucket"
			case "recovered":
				r.outageCurrent = false
			case "deadline":
				*now = r.order.PickedUpAt.Add(2 * r.order.DeliveryDuration)
			case "missing evidence":
				d.outage.EvidenceID = ""
			case "unbounded validity":
				d.outage.ValidUntil = now.Add(time.Hour)
			}
			if _, err := s.ConfirmDelivery(t.Context(), Caller{ID: proofCourier}, ConfirmDeliveryInput{OrderID: proofOrderID, WithoutProof: true, Unattended: true}); !errors.Is(err, ErrPrecondition) {
				t.Fatal(err)
			}
			if r.order.Status != Delivering || r.confirmation != nil || len(r.changes) != 0 {
				t.Fatal("invalid outage evidence changed delivery")
			}
		})
	}
}

// The review's reproduced resource hazard is allocating each full object while
// waiting for decoder capacity. Saturation must reject before storage reading.
func TestProofSaturationRejectsBeforeReadingObject(t *testing.T) {
	s, r, d, _ := proofScenario()
	row := uploadedProof(t, s, d)
	for range cap(s.verificationSlots) {
		s.verificationSlots <- struct{}{}
	}
	defer func() {
		for len(s.verificationSlots) > 0 {
			<-s.verificationSlots
		}
	}()
	if _, err := s.ConfirmDelivery(t.Context(), Caller{ID: proofCourier}, proofConfirmationInput(row)); !errors.Is(err, ErrUnavailable) {
		t.Fatal(err)
	}
	if d.inspections != 0 || r.confirmation != nil || r.order.Status != Delivering {
		t.Fatal("saturated verification read storage or changed delivery")
	}
}
