package bootstrap

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/jwt"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/models"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/store"
)

var createdID = uuid.New()

// fakeStore simulates store.Admin.Bootstrap: it reports the outcome, the
// sessions its transaction revoked, and records the clock it was given.
type fakeStore struct {
	got     *models.User
	outcome store.BootstrapOutcome
	revoked []uuid.UUID
	now     time.Time
}

func (f *fakeStore) Bootstrap(
	_ context.Context, u *models.User, now time.Time,
) (store.BootstrapOutcome, []uuid.UUID, error) {
	f.got = u
	u.ID = createdID
	f.now = now
	return f.outcome, f.revoked, nil
}

// fakeRevocations records sessions written to the revocation dictionary.
type fakeRevocations struct {
	revoked []string
	err     error
}

func (f *fakeRevocations) Revoke(_ context.Context, sessionIDs ...string) error {
	if f.err != nil {
		return f.err
	}
	f.revoked = append(f.revoked, sessionIDs...)
	return nil
}

func TestRunNormalizesInput(t *testing.T) {
	fake := &fakeStore{outcome: store.BootstrapCreated}
	user, outcome, err := (&Bootstrapper{Store: fake}).Run(context.Background(), Config{Email: " Admin@Example.com "})
	if err != nil || outcome != store.BootstrapCreated {
		t.Fatalf("run: %v, %v", outcome, err)
	}
	if user.ID != createdID || fake.got.Email != "admin@example.com" || fake.got.DisplayName != DefaultDisplayName {
		t.Fatalf("user: %+v", user)
	}

	if _, _, err := (&Bootstrapper{Store: fake}).Run(
		context.Background(),
		Config{Email: "a@b.com", DisplayName: "  Ops  "},
	); err != nil ||
		fake.got.DisplayName != "Ops" {
		t.Fatalf("display name: %q, %v", fake.got.DisplayName, err)
	}
}

func TestRunRejectsInvalidInput(t *testing.T) {
	b := &Bootstrapper{Store: &fakeStore{}}
	if _, _, err := b.Run(context.Background(), Config{Email: "Name <a@b.com>"}); !errors.Is(err, jwt.ErrInvalidEmail) {
		t.Fatalf("invalid email: %v", err)
	}
	if _, _, err := b.Run(
		context.Background(),
		Config{Email: "a@b.com", DisplayName: strings.Repeat("x", 101)},
	); !errors.Is(
		err,
		jwt.ErrInvalidProfile,
	) {
		t.Fatalf("long name: %v", err)
	}
}

func TestRunRecordsPromotedSessions(t *testing.T) {
	laptop, phone := uuid.New(), uuid.New()
	revocations := &fakeRevocations{}
	fake := &fakeStore{outcome: store.BootstrapPromoted, revoked: []uuid.UUID{laptop, phone}}
	clock := time.Date(2026, 11, 5, 0, 0, 0, 0, time.UTC)
	b := &Bootstrapper{Store: fake, Revocations: revocations, Now: func() time.Time { return clock }}

	if _, outcome, err := b.Run(context.Background(), Config{Email: "promoted@example.com"}); err != nil ||
		outcome != store.BootstrapPromoted {
		t.Fatalf("run: %v, %v", outcome, err)
	}
	// The store revoked inside its transaction; the bootstrapper recorded the
	// dictionary entries afterwards with its own clock.
	if !fake.now.Equal(clock) {
		t.Fatalf("store received clock %v, want %v", fake.now, clock)
	}
	if len(revocations.revoked) != 2 ||
		revocations.revoked[0] != laptop.String() || revocations.revoked[1] != phone.String() {
		t.Fatalf("recorded %v, want [%s %s]", revocations.revoked, laptop, phone)
	}
}

func TestRunRecordsNothingWithoutPromotion(t *testing.T) {
	revocations := &fakeRevocations{}
	for _, tc := range []struct {
		name    string
		outcome store.BootstrapOutcome
	}{
		{"created", store.BootstrapCreated},
		{"unchanged", store.BootstrapUnchanged},
		{"already done", store.BootstrapAlreadyDone},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := &Bootstrapper{Store: &fakeStore{outcome: tc.outcome}, Revocations: revocations}
			if _, _, err := b.Run(context.Background(), Config{Email: "user@example.com"}); err != nil {
				t.Fatal(err)
			}
			if len(revocations.revoked) != 0 {
				t.Fatalf("recorded %v without a promotion", revocations.revoked)
			}
		})
	}
}

func TestRunPanicsWithoutRevocationsOnPromotion(t *testing.T) {
	b := &Bootstrapper{Store: &fakeStore{outcome: store.BootstrapPromoted, revoked: []uuid.UUID{uuid.New()}}}
	defer func() {
		if recover() == nil {
			t.Fatal("expected nil revocations writer to panic")
		}
	}()
	_, _, _ = b.Run(context.Background(), Config{Email: "promoted@example.com"})
}
