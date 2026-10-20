package order

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func creationInput() CreateInput {
	return CreateInput{Key: uuid.NewString(), PickupID: uuid.NewString(), DeliveryID: uuid.NewString(), ItemDescription: "Book", RewardCredits: 10, DeliveryDuration: 10 * time.Minute, ExpiresAt: time.Date(2026, 10, 20, 1, 2, 3, 456, time.UTC)}
}

func TestCreationCanonicalPayload(t *testing.T) {
	original := creationInput()
	_, _, hash, err := canonicalCreation(original)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		edit func(*CreateInput)
		same bool
	}{
		{"different retry key", func(v *CreateInput) { v.Key = uuid.NewString() }, true},
		{"UUID representation", func(v *CreateInput) { v.PickupID = strings.ToUpper(v.PickupID) }, true},
		{"trimmed text", func(v *CreateInput) { v.ItemDescription = " \u2003Book\u2003 " }, true},
		{"timestamp timezone", func(v *CreateInput) { v.ExpiresAt = v.ExpiresAt.In(time.FixedZone("SGT", 8*3600)) }, true},
		{"optional presence", func(v *CreateInput) { empty := ""; v.Instructions = &empty }, false},
		{"timestamp nanoseconds", func(v *CreateInput) { v.ExpiresAt = v.ExpiresAt.Add(time.Nanosecond) }, false},
		{"image IDs affect the payload", func(v *CreateInput) { v.ImageIDs = []string{uuid.NewString()} }, false},
		{"different reward", func(v *CreateInput) { v.RewardCredits++ }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input := original
			tc.edit(&input)
			_, _, got, err := canonicalCreation(input)
			if err != nil || (got == hash) != tc.same {
				t.Fatalf("hash equality=%v want=%v err=%v", got == hash, tc.same, err)
			}
		})
	}
}

func TestCreationInputEligibility(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*CreateInput)
		want error
	}{
		{"nil UUID", func(v *CreateInput) { v.Key = uuid.Nil.String() }, ErrInvalid},
		{"same locations", func(v *CreateInput) { v.DeliveryID = v.PickupID }, ErrInvalid},
		{"zero reward", func(v *CreateInput) { v.RewardCredits = 0 }, ErrInvalid},
		{"blank description", func(v *CreateInput) { v.ItemDescription = " \u2003 " }, ErrInvalid},
		{"duration too short", func(v *CreateInput) { v.DeliveryDuration = time.Minute }, ErrInvalid},
		{"out of range timestamp", func(v *CreateInput) { v.ExpiresAt = time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC) }, ErrInvalid},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input := creationInput()
			tc.edit(&input)
			_, _, _, err := canonicalCreation(input)
			if !errors.Is(err, tc.want) {
				t.Fatalf("err=%v want=%v", err, tc.want)
			}
		})
	}
	// A past deadline remains structurally valid until the operation has looked up
	// idempotency; this is essential to retained retry semantics.
	input := creationInput()
	input.ExpiresAt = time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
	if _, _, _, err := canonicalCreation(input); err != nil {
		t.Fatal(err)
	}
}

type blockingRecoveryRepository struct {
	CreationRepository
	started chan struct{}
}

func (r blockingRecoveryRepository) DueCreations(ctx context.Context, _ time.Time, _ int32) ([]string, error) {
	close(r.started)
	<-ctx.Done()
	return nil, ctx.Err()
}

func TestCreationRecoveryShutdownCancelsScan(t *testing.T) {
	repository := blockingRecoveryRepository{started: make(chan struct{})}
	creator := NewCreator(repository, nil, nil, CreationOptions{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { defer close(done); creator.RecoverCreations(ctx) }()
	select {
	case <-repository.started:
	case <-time.After(time.Second):
		t.Fatal("recovery did not scan immediately")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("recovery ignored service shutdown")
	}
}
