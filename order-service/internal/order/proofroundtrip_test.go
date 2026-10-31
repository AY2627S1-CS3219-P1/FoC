package order

import (
	"errors"
	"testing"
	"time"
)

// Persisted timestamps describe instants, not an in-memory time zone. Stable
// upload admission and accepted evidence must survive a database round-trip.
func TestProofUploadAndConfirmationSurviveTimestampRoundTrip(t *testing.T) {
	s, r, d, _ := proofScenario()
	r.readLocation = time.FixedZone("database-session", 8*60*60)
	in := proofUploadInput(d)
	row, first, err := s.CreateUpload(t.Context(), Caller{ID: proofCourier}, in)
	if err != nil {
		t.Fatal(err)
	}
	repeated, second, err := s.CreateUpload(t.Context(), Caller{ID: proofCourier}, in)
	if err != nil || repeated.ID != row.ID || first.URL != second.URL {
		t.Fatalf("timestamp representation broke upload retry: %v", err)
	}
	if err := s.VerifyUpload(t.Context(), Caller{ID: proofCourier}, proofOrderID, row.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ConfirmDelivery(t.Context(), Caller{ID: proofCourier}, proofConfirmationInput(row)); err != nil {
		t.Fatal(err)
	}
	if len(r.changes) != 1 || r.uploads[row.ID].State != ProofAccepted {
		t.Fatal("timestamp representation broke accepted evidence")
	}
}

// Equivalent representation must be accepted without weakening the immutable
// identity/generation check after remote signing.
func TestProofUploadRecheckRejectsChangedPersistedValues(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*ProofUpload)
	}{
		{"state", func(r *ProofUpload) { r.State = ProofDeleting }},
		{"proof ID", func(r *ProofUpload) { r.ID = proofOther }},
		{"order ID", func(r *ProofUpload) { r.OrderID = proofOther }},
		{"courier ID", func(r *ProofUpload) { r.CourierID = proofOther }},
		{"upload ID", func(r *ProofUpload) { r.UploadID = proofOther }},
		{"key", func(r *ProofUpload) { r.Key += "-changed" }},
		{"type", func(r *ProofUpload) { r.ContentType = "image/jpeg" }},
		{"size", func(r *ProofUpload) { r.SizeBytes++ }},
		{"generation", func(r *ProofUpload) { r.Generation++ }},
		{"granted instant", func(r *ProofUpload) { r.GrantedAt = r.GrantedAt.Add(time.Second) }},
		{"expiry instant", func(r *ProofUpload) { r.ExpiresAt = r.ExpiresAt.Add(time.Second) }},
		{"created instant", func(r *ProofUpload) { r.CreatedAt = r.CreatedAt.Add(time.Second) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, r, d, _ := proofScenario()
			d.onGrant = func() {
				for id, row := range r.uploads {
					tc.change(&row)
					r.uploads[id] = row
				}
			}
			if _, _, err := s.CreateUpload(
				t.Context(),
				Caller{ID: proofCourier},
				proofUploadInput(d),
			); !errors.Is(
				err,
				ErrConflict,
			) {
				t.Fatalf("changed upload accepted: %v", err)
			}
		})
	}
}
