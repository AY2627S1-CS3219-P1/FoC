package disablement_test

import (
	"context"
	"encoding/json"
	"sort"
	"sync"
	"time"

	"github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/idempotency"

	disablement "github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/location/disablement"
)

const (
	locationID = "8f383cca-1609-4326-8440-f75d943dc924"
	buildingID = "9dbda827-5a46-4089-9209-a2bde43f18a0"
	categoryID = "63dc656d-4e7b-43ad-a0ba-325b44132766"
	key        = "1e3f8ee1-1362-4e73-bd7f-2c4b61600b29"
)

var admin = disablement.Caller{ID: "admin", Role: "admin"}

func ptr[T any](v T) *T { return &v }

// In memory test repository
type TestRepository struct {
	mu            sync.Mutex
	locations     map[string]disablement.Location
	disablements  map[string]disablement.Disablement
	keys          map[idempotency.Scope]idempotency.Record
	onRetryLock   func()
	failRetrySave bool
}

func newTestRepository() *TestRepository {
	return &TestRepository{locations: map[string]disablement.Location{}, disablements: map[string]disablement.Disablement{}, keys: map[idempotency.Scope]idempotency.Record{}}
}
func copyValue[T any](v T) T {
	b, _ := json.Marshal(v)
	var out T
	_ = json.Unmarshal(b, &out)
	return out
}

func (r *TestRepository) Within(ctx context.Context, f func(disablement.Tx) error) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if e := ctx.Err(); e != nil {
		return e
	}
	locations, disablements := copyValue(r.locations), copyValue(r.disablements)
	keys := map[idempotency.Scope]idempotency.Record{}
	for k, v := range r.keys {
		keys[k] = v
	}
	if e := f(r); e != nil {
		r.locations = locations
		r.disablements = disablements
		r.keys = keys
		return e
	}
	return nil
}

func (r *TestRepository) Location(_ context.Context, id string) (disablement.Location, error) {
	v, ok := r.locations[id]
	if !ok {
		return v, disablement.ErrNotFound
	}
	return copyValue(v), nil
}

func (r *TestRepository) Disablement(_ context.Context, id string) (disablement.Disablement, error) {
	v, ok := r.disablements[id]
	if !ok {
		return v, disablement.ErrNotFound
	}
	return copyValue(v), nil
}

func (r *TestRepository) SaveDisablement(_ context.Context, d disablement.Disablement, expected int64) error {
	if expected > 0 && r.disablements[d.ID].Revision != expected {
		return disablement.ErrAborted
	}
	r.disablements[d.ID] = copyValue(d)
	return nil
}

func (r *TestRepository) Overlaps(_ context.Context, d disablement.Disablement) (bool, error) {
	for _, v := range r.disablements {
		if v.ID == d.ID || v.LocationID != d.LocationID || v.CancelledAt != nil {
			continue
		}
		end := v.EndsAt
		if v.EndedAt != nil && (end == nil || v.EndedAt.Before(*end)) {
			end = v.EndedAt
		}
		if (end == nil || d.StartsAt.Before(*end)) && (d.EndsAt == nil || v.StartsAt.Before(*d.EndsAt)) {
			return true, nil
		}
	}
	return false, nil
}

func (r *TestRepository) ListDisablements(_ context.Context, id string, state disablement.DisablementState, now time.Time, p disablement.Page) ([]disablement.Disablement, int64, error) {
	items := []disablement.Disablement{}
	for _, v := range r.disablements {
		if v.LocationID == id && (state == "" || v.State(now) == state) {
			items = append(items, copyValue(v))
		}
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].StartsAt.Equal(items[j].StartsAt) {
			return items[i].ID < items[j].ID
		}
		return items[i].StartsAt.After(items[j].StartsAt)
	})
	return slicePage(items, p), int64(len(items)), nil
}
func slicePage[T any](items []T, p disablement.Page) []T {
	start := int64(p.Number-1) * int64(p.Size)
	if start >= int64(len(items)) {
		return []T{}
	}
	end := start + int64(p.Size)
	if end > int64(len(items)) {
		end = int64(len(items))
	}
	return items[start:end]
}

func (r *TestRepository) Find(_ context.Context, s idempotency.Scope, now time.Time) (*idempotency.Record, error) {
	if v, ok := r.keys[s]; ok && !now.Before(v.ExpiresAt) {
		delete(r.keys, s)
	}
	if v, ok := r.keys[s]; ok {
		return &v, nil
	}
	return nil, nil
}

func (r *TestRepository) Save(ctx context.Context, s idempotency.Scope, v idempotency.Record) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if r.failRetrySave {
		return disablement.ErrFailedPrecondition
	}
	r.keys[s] = v
	return nil
}

func (r *TestRepository) Idempotency() idempotency.Store { return r }

func (r *TestRepository) Lock(ctx context.Context, _ idempotency.Scope) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if r.onRetryLock != nil {
		r.onRetryLock()
	}
	return nil
}
