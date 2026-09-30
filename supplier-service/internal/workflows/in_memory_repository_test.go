package workflows_test

import (
	"context"
	"encoding/json"
	"sort"
	"sync"
	"time"

	w "github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/workflows"
	"github.com/google/uuid"
)

const (
	locationID = "8f383cca-1609-4326-8440-f75d943dc924"
	buildingID = "9dbda827-5a46-4089-9209-a2bde43f18a0"
	categoryID = "63dc656d-4e7b-43ad-a0ba-325b44132766"
	key        = "1e3f8ee1-1362-4e73-bd7f-2c4b61600b29"
)

var admin = w.Caller{ID: "admin", Role: "admin"}
var owner = w.Caller{ID: "owner", Role: "user"}

func ptr[T any](v T) *T { return &v }

// In memory test repository
type TestRepository struct {
	mu           sync.Mutex
	locations    map[string]w.Location
	disablements map[string]w.Disablement
	requests     map[string]w.AdditionRequest
	keys         map[w.IdempotencyScope]w.IdempotencyRecord
	failCreate   bool
}

func newTestRepository() *TestRepository {
	return &TestRepository{locations: map[string]w.Location{}, disablements: map[string]w.Disablement{}, requests: map[string]w.AdditionRequest{}, keys: map[w.IdempotencyScope]w.IdempotencyRecord{}}
}
func copyValue[T any](v T) T {
	b, _ := json.Marshal(v)
	var out T
	_ = json.Unmarshal(b, &out)
	return out
}
func (r *TestRepository) Within(ctx context.Context, f func(w.Tx) error) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if e := ctx.Err(); e != nil {
		return e
	}
	locations, disablements, requests := copyValue(r.locations), copyValue(r.disablements), copyValue(r.requests)
	keys := map[w.IdempotencyScope]w.IdempotencyRecord{}
	for k, v := range r.keys {
		keys[k] = v
	}
	if e := f(r); e != nil {
		r.locations = locations
		r.disablements = disablements
		r.requests = requests
		r.keys = keys
		return e
	}
	return nil
}
func (r *TestRepository) Location(_ context.Context, id string) (w.Location, error) {
	v, ok := r.locations[id]
	if !ok {
		return v, w.ErrNotFound
	}
	return copyValue(v), nil
}
func (r *TestRepository) Disablement(_ context.Context, id string) (w.Disablement, error) {
	v, ok := r.disablements[id]
	if !ok {
		return v, w.ErrNotFound
	}
	return copyValue(v), nil
}
func (r *TestRepository) SaveDisablement(_ context.Context, d w.Disablement, expected int64) error {
	if expected > 0 && r.disablements[d.ID].Revision != expected {
		return w.ErrAborted
	}
	r.disablements[d.ID] = copyValue(d)
	return nil
}
func (r *TestRepository) Overlaps(_ context.Context, d w.Disablement) (bool, error) {
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
func (r *TestRepository) ListDisablements(_ context.Context, id string, state w.DisablementState, now time.Time, p w.Page) ([]w.Disablement, int64, error) {
	items := []w.Disablement{}
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
func slicePage[T any](items []T, p w.Page) []T {
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
func (r *TestRepository) Request(_ context.Context, id string) (w.AdditionRequest, error) {
	v, ok := r.requests[id]
	if !ok {
		return v, w.ErrNotFound
	}
	return copyValue(v), nil
}
func (r *TestRepository) SaveRequest(_ context.Context, v w.AdditionRequest, expected int64) error {
	if expected > 0 {
		old := r.requests[v.ID]
		if old.Status != w.Pending {
			return w.ErrFailedPrecondition
		}
		if old.Revision != expected {
			return w.ErrAborted
		}
	}
	r.requests[v.ID] = copyValue(v)
	return nil
}
func (r *TestRepository) ListRequests(_ context.Context, c w.Caller, status w.RequestStatus, p w.Page) ([]w.AdditionRequest, int64, error) {
	items := []w.AdditionRequest{}
	for _, v := range r.requests {
		if (c.IsAdmin() || v.SubmittedBy == c.ID || v.Status == w.Approved) && (status == "" || v.Status == status) {
			items = append(items, copyValue(v))
		}
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].CreatedAt.Equal(items[j].CreatedAt) {
			return items[i].ID < items[j].ID
		}
		return items[i].CreatedAt.After(items[j].CreatedAt)
	})
	return slicePage(items, p), int64(len(items)), nil
}
func (r *TestRepository) ValidateReferences(_ context.Context, p w.Proposal) error {
	if p.BuildingID != buildingID {
		return w.ErrFailedPrecondition
	}
	for _, id := range p.CategoryIDs {
		if id != categoryID {
			return w.ErrFailedPrecondition
		}
	}
	return nil
}
func (r *TestRepository) CreateLocation(_ context.Context, p w.Proposal, now time.Time) (w.Location, error) {
	if r.failCreate {
		return w.Location{}, w.DependencyError("create Location", context.DeadlineExceeded)
	}
	v := w.Location{ID: uuid.NewString(), Proposal: copyValue(p), Revision: 1, CreatedAt: now, UpdatedAt: now}
	r.locations[v.ID] = v
	return copyValue(v), nil
}
func (r *TestRepository) Idempotency(_ context.Context, s w.IdempotencyScope, now time.Time) (*w.IdempotencyRecord, error) {
	for k, v := range r.keys {
		if !now.Before(v.ExpiresAt) {
			delete(r.keys, k)
		}
	}
	if v, ok := r.keys[s]; ok {
		return &v, nil
	}
	return nil, nil
}
func (r *TestRepository) SaveIdempotency(_ context.Context, s w.IdempotencyScope, v w.IdempotencyRecord) error {
	r.keys[s] = v
	return nil
}
