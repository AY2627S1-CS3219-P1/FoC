package accountdeletion

import (
	"testing"
	"time"

	events "github.com/AY2627S1-CS3219-P1/FoC/pkg/kafkagen/users/events/v1"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/database/deletiondb"
	"github.com/google/uuid"
)

func validDeletedFact() deletiondb.ClaimCommittedFactRow {
	return deletiondb.ClaimCommittedFactRow{
		EventID:           uuid.New(),
		UserID:            uuid.New(),
		DeletionRequestID: uuid.New(),
		DeletedAt:         time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
		PayloadVersion:    1,
		LeaseToken:        uuid.NullUUID{UUID: uuid.New(), Valid: true},
	}
}

func TestEncodeDeleted(t *testing.T) {
	t.Run("valid fact encodes to a UserDeleted message", func(t *testing.T) {
		fact := validDeletedFact()
		msg, err := encodeDeleted(fact)
		if err != nil {
			t.Fatalf("encodeDeleted: %v", err)
		}
		deleted, ok := msg.(*events.UserDeleted)
		if !ok {
			t.Fatalf("expected *events.UserDeleted, got %T", msg)
		}
		if deleted.EventId != fact.EventID.String() {
			t.Errorf("EventId = %q, want %q", deleted.EventId, fact.EventID)
		}
		if deleted.UserId != fact.UserID.String() {
			t.Errorf("UserId = %q, want %q", deleted.UserId, fact.UserID)
		}
		if deleted.DeletionRequestId != fact.DeletionRequestID.String() {
			t.Errorf("DeletionRequestId = %q, want %q", deleted.DeletionRequestId, fact.DeletionRequestID)
		}
		if deleted.DeletedAt == nil || !deleted.DeletedAt.AsTime().Equal(fact.DeletedAt) {
			t.Errorf("DeletedAt = %v, want %v", deleted.DeletedAt, fact.DeletedAt)
		}
	})

	cases := []struct {
		name string
		mut  func(deletiondb.ClaimCommittedFactRow) deletiondb.ClaimCommittedFactRow
	}{
		{"nil event id", func(f deletiondb.ClaimCommittedFactRow) deletiondb.ClaimCommittedFactRow {
			f.EventID = uuid.Nil
			return f
		}},
		{"nil user id", func(f deletiondb.ClaimCommittedFactRow) deletiondb.ClaimCommittedFactRow {
			f.UserID = uuid.Nil
			return f
		}},
		{"nil request id", func(f deletiondb.ClaimCommittedFactRow) deletiondb.ClaimCommittedFactRow {
			f.DeletionRequestID = uuid.Nil
			return f
		}},
		{"zero deleted at", func(f deletiondb.ClaimCommittedFactRow) deletiondb.ClaimCommittedFactRow {
			f.DeletedAt = time.Time{}
			return f
		}},
		{"wrong payload version", func(f deletiondb.ClaimCommittedFactRow) deletiondb.ClaimCommittedFactRow {
			f.PayloadVersion = 2
			return f
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := encodeDeleted(tc.mut(validDeletedFact())); err == nil {
				t.Fatal("expected error for invalid fact, got nil")
			}
		})
	}
}
