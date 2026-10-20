package order

import (
	"context"
	"testing"
	"time"
)

type rotatingProofIssuer struct {
	*proofDependencies
	epoch  string
	expiry time.Time
}

func (s *rotatingProofIssuer) ProofSigningEpoch(context.Context) (string, time.Time, error) {
	return s.epoch, s.expiry, nil
}

func TestProofCredentialRotationRefreshesGrantNotProof(t *testing.T) {
	proofs, repo, dependencies, now := proofScenario()
	issuer := &rotatingProofIssuer{proofDependencies: dependencies, epoch: "first"}
	proofs.grants = issuer
	input := proofUploadInput(dependencies)
	first, _, err := proofs.CreateUpload(t.Context(), Caller{ID: proofCourier}, input)
	if err != nil {
		t.Fatal(err)
	}
	issuer.epoch = "rotated"
	issuer.expiry = now.Add(2 * time.Minute)
	refreshed, _, err := proofs.CreateUpload(t.Context(), Caller{ID: proofCourier}, input)
	if err != nil || refreshed.ID != first.ID || refreshed.Generation != first.Generation+1 || len(repo.uploads) != 1 || !refreshed.ExpiresAt.Equal(issuer.expiry) {
		t.Fatalf("rotation lost identity or temporary-credential lifetime: %+v %v", refreshed, err)
	}
}
