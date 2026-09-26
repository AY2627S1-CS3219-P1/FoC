package user

import (
	"context"
	"errors"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"user-service/internal/apperr"
	"user-service/internal/clock"
	"user-service/internal/models"
)

// fakeRepo is an in-memory Repository.
type fakeRepo struct {
	mu    sync.Mutex
	users map[uuid.UUID]User
	favs  map[[2]uuid.UUID]time.Time
}

func newFakeRepo(users ...User) *fakeRepo {
	f := &fakeRepo{users: map[uuid.UUID]User{}, favs: map[[2]uuid.UUID]time.Time{}}
	for _, u := range users {
		f.users[u.ID] = u
	}
	return f
}

func (f *fakeRepo) GetByID(_ context.Context, id uuid.UUID) (*User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	u, ok := f.users[id]
	if !ok {
		return nil, ErrNotFound
	}
	return &u, nil
}

func (f *fakeRepo) List(_ context.Context, p ListParams) ([]User, int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var all []User
	for _, u := range f.users {
		if p.Role == nil || u.Role == *p.Role {
			all = append(all, u)
		}
	}
	sort.Slice(all, func(i, j int) bool { return all[i].CreatedAt.After(all[j].CreatedAt) })
	total := int64(len(all))
	if p.Offset >= len(all) {
		return nil, total, nil
	}
	return all[p.Offset:min(p.Offset+p.Limit, len(all))], total, nil
}

func (f *fakeRepo) Update(_ context.Context, u *User) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.users[u.ID]; !ok {
		return ErrNotFound
	}
	f.users[u.ID] = *u
	return nil
}

func (f *fakeRepo) Delete(_ context.Context, id uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.users[id]; !ok {
		return ErrNotFound
	}
	delete(f.users, id)
	return nil
}

func (f *fakeRepo) ListFavourites(_ context.Context, userID uuid.UUID) ([]models.FavouriteSupplier, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []models.FavouriteSupplier
	for k, t := range f.favs {
		if k[0] == userID {
			out = append(out, models.FavouriteSupplier{UserID: k[0], SupplierID: k[1], CreatedAt: t})
		}
	}
	return out, nil
}

func (f *fakeRepo) AddFavourite(_ context.Context, userID, supplierID uuid.UUID, now time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.favs[[2]uuid.UUID{userID, supplierID}]; !ok {
		f.favs[[2]uuid.UUID{userID, supplierID}] = now
	}
	return nil
}

func (f *fakeRepo) RemoveFavourite(_ context.Context, userID, supplierID uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.favs, [2]uuid.UUID{userID, supplierID})
	return nil
}

type noSuppliers struct{}

func (noSuppliers) SupplierExists(context.Context, uuid.UUID) (bool, error) { return false, nil }

func ptr(s string) *string { return &s }

func seed() (*Service, *fakeRepo, User) {
	u := User{ID: uuid.New(), Email: "a@u.nus.edu", DisplayName: "A", TelegramHandle: ptr("alice_tg"), Role: models.RoleUser}
	repo := newFakeRepo(u)
	return NewService(repo, AllowAllSuppliers{}, clock.Real{}), repo, u
}

func fieldErr(t *testing.T, err error, field string) {
	t.Helper()
	var ae *apperr.Error
	if !errors.As(err, &ae) || ae.Fields[field] == "" {
		t.Fatalf("want field error on %q, got %v", field, err)
	}
}

func TestUpdate_NormalizesPartial(t *testing.T) {
	svc, _, u := seed()
	got, err := svc.Update(context.Background(), u.ID, UpdateInput{
		DisplayName: ptr("  Alice "), PhoneNumber: ptr("+65 9123-4567"), TelegramHandle: ptr("@new_handle"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.DisplayName != "Alice" || *got.PhoneNumber != "+6591234567" || *got.TelegramHandle != "new_handle" {
		t.Fatalf("not normalized: %+v", got)
	}
}

func TestUpdate_Validation(t *testing.T) {
	svc, repo, u := seed()
	ctx := context.Background()
	cases := map[string]struct {
		in    UpdateInput
		field string
	}{
		"blank name":         {UpdateInput{DisplayName: ptr("  ")}, "display_name"},
		"long description":   {UpdateInput{Description: ptr(string(make([]rune, 501)))}, "description"},
		"bad telegram":       {UpdateInput{TelegramHandle: ptr("ab")}, "telegram_handle"},
		"bad phone":          {UpdateInput{PhoneNumber: ptr("12ab")}, "phone_number"},
		"clear last contact": {UpdateInput{TelegramHandle: ptr("")}, "telegram_handle"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := svc.Update(ctx, u.ID, tc.in)
			fieldErr(t, err, tc.field)
			if stored, _ := repo.GetByID(ctx, u.ID); stored.DisplayName != "A" || stored.TelegramHandle == nil {
				t.Fatal("invalid update leaked into store")
			}
		})
	}
}

func TestUpdate_NotFound(t *testing.T) {
	svc, _, _ := seed()
	if _, err := svc.Update(context.Background(), uuid.New(), UpdateInput{}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want not found, got %v", err)
	}
}

func TestList_RoleFilterAndClamp(t *testing.T) {
	svc, _, _ := seed()
	bad := models.RoleName("root")
	_, _, err := svc.List(context.Background(), ListParams{Role: &bad})
	fieldErr(t, err, "role")

	if p := (ListParams{Limit: 0, Offset: -5}).Normalize(); p.Limit != DefaultPageSize || p.Offset != 0 {
		t.Errorf("got %+v", p)
	}
	if p := (ListParams{Limit: 10_000}).Normalize(); p.Limit != MaxPageSize {
		t.Errorf("got %+v", p)
	}
}

func TestFavourites(t *testing.T) {
	svc, _, u := seed()
	ctx := context.Background()
	sup := uuid.New()
	for i := 0; i < 2; i++ {
		if err := svc.AddFavourite(ctx, u.ID, sup); err != nil {
			t.Fatal(err)
		}
	}
	if favs, _ := svc.Favourites(ctx, u.ID); len(favs) != 1 {
		t.Fatalf("favs = %d", len(favs))
	}
	_ = svc.RemoveFavourite(ctx, u.ID, sup)
	if favs, _ := svc.Favourites(ctx, u.ID); len(favs) != 0 {
		t.Fatalf("favs after remove = %d", len(favs))
	}

	strict := NewService(newFakeRepo(u), noSuppliers{}, clock.Real{})
	if err := strict.AddFavourite(ctx, u.ID, sup); !errors.Is(err, ErrSupplierNotFound) {
		t.Fatalf("unknown supplier: %v", err)
	}
}
