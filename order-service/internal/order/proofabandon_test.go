package order

import (
	"errors"
	"testing"
)

// Explicit photo removal must permit a replacement, without abandoning the
// bounded active-admission rule or reviving a retired upload identity.
func TestProofRemovalReleasesAdmissionWithoutRevivingIdentity(t *testing.T) {
	s, r, d, _ := proofScenario()
	for range MaxStagedProofs + 1 {
		in := proofUploadInput(d)
		row, _, err := s.CreateUpload(t.Context(), Caller{ID: proofCourier}, in)
		if err != nil {
			t.Fatal(err)
		}
		for range 2 {
			if err := s.AbandonUpload(t.Context(), Caller{ID: proofCourier}, proofOrderID, in.UploadID); err != nil {
				t.Fatal(err)
			}
		}
		if r.uploads[row.ID].State != ProofDeleting {
			t.Fatal("removed upload was not fenced")
		}
		if _, _, err := s.CreateUpload(t.Context(), Caller{ID: proofCourier}, in); !errors.Is(err, ErrPrecondition) {
			t.Fatalf("revived retired identity: %v", err)
		}
	}
	if len(r.changes) != 0 {
		t.Fatal("photo removal changed lifecycle")
	}
}

func TestProofAbandonmentFencesUnknownAndInFlightAdmissions(t *testing.T) {
	for _, admitted := range []bool{false, true} {
		t.Run(map[bool]string{false: "before admission", true: "during signing"}[admitted], func(t *testing.T) {
			s, r, d, _ := proofScenario()
			in := proofUploadInput(d)
			retire := func() {
				if err := s.AbandonUpload(t.Context(), Caller{ID: proofCourier}, proofOrderID, in.UploadID); err != nil {
					t.Fatal(err)
				}
			}
			if admitted {
				d.onGrant = retire
			} else {
				retire()
			}
			_, _, err := s.CreateUpload(t.Context(), Caller{ID: proofCourier}, in)
			if !errors.Is(err, ErrPrecondition) && !errors.Is(err, ErrConflict) {
				t.Fatalf("late admission returned a usable grant: %v", err)
			}
			for _, row := range r.uploads {
				if row.State != ProofDeleting {
					t.Fatal("retirement fence lost")
				}
			}
		})
	}
}

func TestProofActiveQuotaStillAppliesAndAbandonmentProtectsAcceptedProofs(t *testing.T) {
	s, _, d, _ := proofScenario()
	var first ProofUpload
	for range MaxStagedProofs {
		first = uploadedProof(t, s, d)
	}
	if _, _, err := s.CreateUpload(t.Context(), Caller{ID: proofCourier}, proofUploadInput(d)); !errors.Is(err, ErrPrecondition) {
		t.Fatal(err)
	}
	if err := s.AbandonUpload(t.Context(), Caller{ID: proofOther}, proofOrderID, first.UploadID); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	if err := s.AbandonUpload(t.Context(), Caller{ID: proofCourier}, proofOrderID, first.UploadID); err != nil {
		t.Fatal(err)
	}
	uploadedProof(t, s, d)
	accepted, r, objects, _ := proofScenario()
	row := uploadedProof(t, accepted, objects)
	if _, err := accepted.ConfirmDelivery(t.Context(), Caller{ID: proofCourier}, proofConfirmationInput(row)); err != nil {
		t.Fatal(err)
	}
	if err := accepted.AbandonUpload(t.Context(), Caller{ID: proofCourier}, proofOrderID, row.UploadID); !errors.Is(err, ErrPrecondition) {
		t.Fatal(err)
	}
	if r.uploads[row.ID].State != ProofAccepted || len(r.changes) != 1 {
		t.Fatal("accepted evidence altered")
	}
}

// A client nonce may equal a server proof ID without naming that proof's
// admission identity. Its fence must not corrupt accepted delivery evidence.
func TestProofAbandonmentDoesNotAliasServerProofIDs(t *testing.T) {
	s, r, d, _ := proofScenario()
	row := uploadedProof(t, s, d)
	in := proofConfirmationInput(row)
	if _, err := s.ConfirmDelivery(t.Context(), Caller{ID: proofCourier}, in); err != nil {
		t.Fatal(err)
	}
	if err := s.AbandonUpload(t.Context(), Caller{ID: proofCourier}, proofOrderID, row.ID); err != nil {
		t.Fatal(err)
	}
	if len(r.uploads) != 1 || r.uploads[row.ID].State != ProofAccepted || r.uploads[row.ID].UploadID != row.UploadID {
		t.Fatal("client nonce overwrote accepted server proof")
	}
	view, err := s.ForOrder(t.Context(), Caller{ID: proofRequester}, proofOrderID)
	if err != nil || len(view.Proofs) != 1 {
		t.Fatalf("accepted evidence no longer readable: %v", err)
	}
	if _, err := s.ConfirmDelivery(t.Context(), Caller{ID: proofCourier}, in); err != nil {
		t.Fatal(err)
	}
	if err := s.AbandonUpload(t.Context(), Caller{ID: proofCourier}, proofOrderID, row.UploadID); !errors.Is(err, ErrPrecondition) {
		t.Fatal(err)
	}
}

func TestProofAbandonmentFenceRollsBackWithTransaction(t *testing.T) {
	s, r, d, _ := proofScenario()
	row := uploadedProof(t, s, d)
	aborted := errors.New("transaction aborted")
	err := r.WithinProofs(t.Context(), func(tx ProofTx) error {
		if err := tx.AbandonProofUpload(t.Context(), proofOrderID, proofCourier, row.UploadID, row.CreatedAt); err != nil {
			return err
		}
		return aborted
	})
	if !errors.Is(err, aborted) || len(r.retired) != 0 || r.uploads[row.ID].State != ProofUploading {
		t.Fatal("aborted retirement left a fence or altered evidence")
	}
}
