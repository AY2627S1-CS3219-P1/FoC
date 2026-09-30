package bootstrap

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/jwt"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/models"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/store"
)

var createdID = uuid.New()

type fakeStore struct{ got *models.User }

func (f *fakeStore) Bootstrap(_ context.Context, u *models.User) (store.BootstrapOutcome, error) {
	f.got = u
	u.ID = createdID
	return store.BootstrapCreated, nil
}

func TestRunNormalizesInput(t *testing.T) {
	fake := &fakeStore{}
	user, outcome, err := (&Bootstrapper{Store: fake}).Run(context.Background(), Config{Email: " Admin@Example.com "})
	if err != nil || outcome != store.BootstrapCreated {
		t.Fatalf("run: %v, %v", outcome, err)
	}
	if user.ID != createdID || fake.got.Email != "admin@example.com" || fake.got.DisplayName != DefaultDisplayName {
		t.Fatalf("user: %+v", fake.got)
	}

	if _, _, err := (&Bootstrapper{Store: fake}).Run(context.Background(), Config{Email: "a@b.com", DisplayName: "  Ops  "}); err != nil || fake.got.DisplayName != "Ops" {
		t.Fatalf("display name: %q, %v", fake.got.DisplayName, err)
	}
}

func TestRunRejectsInvalidInput(t *testing.T) {
	b := &Bootstrapper{Store: &fakeStore{}}
	if _, _, err := b.Run(context.Background(), Config{Email: "Name <a@b.com>"}); !errors.Is(err, jwt.ErrInvalidEmail) {
		t.Fatalf("invalid email: %v", err)
	}
	if _, _, err := b.Run(context.Background(), Config{Email: "a@b.com", DisplayName: strings.Repeat("x", 101)}); !errors.Is(err, jwt.ErrInvalidProfile) {
		t.Fatalf("long name: %v", err)
	}
}
