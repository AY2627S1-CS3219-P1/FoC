//go:build integration

package order

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

// Q15 and the pre-upgrade persisted contract: SHA256(JSON-array(order, reason)).
// This independent wire calculation must not follow the new revision-bound hash.
func assertLegacyCancellationKey(t *testing.T, f *fixture, caller, key, orderID, reason string) {
	t.Helper()
	wire, err := json.Marshal([]string{orderID, reason})
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(wire)
	var stored string
	if err := f.pool.QueryRow(f.ctx, "SELECT request_hash FROM order_idempotency_keys WHERE caller_id=$1 AND method=$2 AND idempotency_key=$3", caller, CancelOrderMethod, key).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored != hex.EncodeToString(digest[:]) {
		t.Fatal("legacy key was rewritten or was not a pre-upgrade hash")
	}
}

func TestPostgresLegacyCancellationUpgradeReplay(t *testing.T) {
	f := setup(t)
	t.Run("terminal replay and payload integrity", func(t *testing.T) {
		courier, key := uuid.NewString(), uuid.NewString()
		o := cancellationState(t, f, Delivering, courier)
		before := NewCanceller(f.repo, nil, AssignmentOptions{})
		first, err := before.Cancel(f.ctx, Caller{ID: courier}, o.ID, "  leave  ", key)
		if err != nil || first.Status != CancellationSucceeded {
			t.Fatalf("pre-upgrade terminal cancellation: %+v %v", first, err)
		}
		assertLegacyCancellationKey(t, f, courier, key, o.ID, "leave")
		audit, facts, _ := f.counts(t, o.ID)
		after := NewCanceller(f.repo, nil, AssignmentOptions{})
		replay, err := after.CancelConfirmed(f.ctx, Caller{ID: courier}, o.ID, "leave", key, o.Revision)
		if err != nil || replay.OperationID != first.OperationID || replay.Status != CancellationSucceeded {
			t.Fatalf("upgraded terminal replay: %+v %v", replay, err)
		}
		if _, err := after.CancelConfirmed(f.ctx, Caller{ID: courier}, o.ID, "changed", key, o.Revision); !errors.Is(err, ErrAlreadyExists) {
			t.Fatalf("legacy key accepted changed reason: %v", err)
		}
		if _, err := after.CancelConfirmed(f.ctx, Caller{ID: courier}, uuid.NewString(), "leave", key, o.Revision); !errors.Is(err, ErrAlreadyExists) {
			t.Fatalf("legacy key accepted changed missing order: %v", err)
		}
		if _, err := after.CancelConfirmed(f.ctx, Caller{ID: uuid.NewString()}, o.ID, "leave", key, o.Revision); !errors.Is(err, ErrForbidden) {
			t.Fatalf("legacy key escaped caller scope: %v", err)
		}
		if a, n, _ := f.counts(t, o.ID); a != audit || n != facts {
			t.Fatal("upgraded replay or rejected payload wrote effects")
		}
		assertLegacyCancellationKey(t, f, courier, key, o.ID, "leave")
		// Corrupting a retained pointer is an integrity error, never a new intent.
		if _, err := f.pool.Exec(f.ctx, "UPDATE order_idempotency_keys SET resource_id=$1 WHERE caller_id=$2 AND method=$3 AND idempotency_key=$4", uuid.NewString(), courier, CancelOrderMethod, key); err != nil {
			t.Fatal(err)
		}
		if _, err := after.CancelConfirmed(f.ctx, Caller{ID: courier}, o.ID, "leave", key, o.Revision); !errors.Is(err, ErrIdempotencyIntegrity) {
			t.Fatalf("legacy key lost resource integrity: %v", err)
		}
	})

	t.Run("pending raised reward recovers and replays past replacement", func(t *testing.T) {
		o := assignmentOpenOrder(t, f)
		clock, now := assignmentClock(o)
		credit := newAssignmentCredit(100, o)
		assigner := NewAssigner(f.repo, credit, AssignmentOptions{Now: now})
		courier := Caller{ID: uuid.NewString()}
		offer, err := assigner.MakeOffer(f.ctx, courier, o.ID, o.RewardCredits+20, uuid.NewString())
		if err != nil {
			t.Fatal(err)
		}
		if assigned, err := assigner.AcceptOffer(f.ctx, Caller{ID: o.RequesterID}, offer.ID); err != nil || assigned.Phase != AssignmentSucceeded {
			t.Fatalf("counteroffer: %+v %v", assigned, err)
		}
		confirmed, err := f.repo.Get(f.ctx, o.ID)
		if err != nil {
			t.Fatal(err)
		}
		credit.denyRevert = true
		key := uuid.NewString()
		before := NewCanceller(f.repo, credit, AssignmentOptions{Now: now})
		first, err := before.Cancel(f.ctx, courier, o.ID, "leave", key)
		if err != nil || first.Status != CancellationPending {
			t.Fatalf("pre-upgrade pending: %+v %v", first, err)
		}
		assertLegacyCancellationKey(t, f, courier.ID, key, o.ID, "leave")
		after := NewCanceller(f.repo, credit, AssignmentOptions{Now: now})
		pending, err := after.CancelConfirmed(f.ctx, courier, o.ID, "leave", key, confirmed.Revision)
		if err != nil || pending.OperationID != first.OperationID || pending.Status != CancellationPending {
			t.Fatalf("upgraded pending replay: %+v %v", pending, err)
		}
		credit.denyRevert = false
		clock.Add(int64(time.Minute))
		if err := after.RecoverCancellationsOnce(f.ctx); err != nil {
			t.Fatal(err)
		}
		replacement := Caller{ID: uuid.NewString()}
		assigned, err := assigner.AcceptOrder(f.ctx, replacement, o.ID, uuid.NewString())
		if err != nil || assigned.Phase != AssignmentSucceeded {
			t.Fatalf("replacement: %+v %v", assigned, err)
		}
		audit, facts, _ := f.counts(t, o.ID)
		replay, err := after.CancelConfirmed(f.ctx, Caller{ID: courier.ID, Suspended: true}, o.ID, "leave", key, confirmed.Revision)
		if err != nil || replay.OperationID != first.OperationID || replay.Status != CancellationSucceeded || !replay.Order.IsAssignedTo(replacement.ID) || replay.Order.RewardCredits != o.RewardCredits {
			t.Fatalf("legacy replay affected replacement: %+v %v", replay, err)
		}
		if a, n, _ := f.counts(t, o.ID); a != audit || n != facts {
			t.Fatal("legacy lifetime replay repeated effects")
		}
		assertLegacyCancellationKey(t, f, courier.ID, key, o.ID, "leave")
	})
	t.Log("PASS upgrade: terminal and pending legacy keys replay, raised reward recovers, replacement survives, changed payload/caller and corrupt pointer fail closed")
}
