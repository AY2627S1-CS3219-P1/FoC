package additionrequest_test

import (
	"context"
	"encoding/json"
	"sort"
	"sync"
	"time"

	"github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/idempotency"
	additionrequest "github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/location/additionrequest"

	"github.com/google/uuid"
)

const (
	locationID = "8f383cca-1609-4326-8440-f75d943dc924"
	buildingID = "9dbda827-5a46-4089-9209-a2bde43f18a0"
	categoryID = "63dc656d-4e7b-43ad-a0ba-325b44132766"
	key        = "1e3f8ee1-1362-4e73-bd7f-2c4b61600b29"
)

var admin = additionrequest.Caller{ID: "admin", Role: "admin"}
var owner = additionrequest.Caller{ID: "owner", Role: "user"}

func ptr[T any](v T) *T { return &v }

// In memory test repository
type TestRepository struct {
	mu                    sync.Mutex
	locations             map[string]additionrequest.Location
	requests              map[string]additionrequest.AdditionRequest
	keys                  map[idempotency.Scope]idempotency.Record
	onRetryLock           func()
	failRetrySave         bool
	failCreate            bool
	failSaveRequest       bool
	locationsAtFailedSave int
}

func newTestRepository() *TestRepository {
	return &TestRepository{locations: map[string]additionrequest.Location{}, requests: map[string]additionrequest.AdditionRequest{}, keys: map[idempotency.Scope]idempotency.Record{}}
}
func copyValue[T any](v T) T {
	b, _ := json.Marshal(v)
	var out T
	_ = json.Unmarshal(b, &out)
	return out
}

func (r *TestRepository) Within(ctx context.Context, f func(additionrequest.Tx) error) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if e := ctx.Err(); e != nil {
		return e
	}
	locations, requests := copyValue(r.locations), copyValue(r.requests)
	keys := map[idempotency.Scope]idempotency.Record{}
	for k, v := range r.keys {
		keys[k] = v
	}
	if e := f(r); e != nil {
		r.locations = locations
		r.requests = requests
		r.keys = keys
		return e
	}
	return nil
}

func (r *TestRepository) Location(_ context.Context, id string) (additionrequest.Location, error) {
	v, ok := r.locations[id]
	if !ok {
		return v, additionrequest.ErrNotFound
	}
	return copyValue(v), nil
}

func (r *TestRepository) Request(_ context.Context, id string) (additionrequest.AdditionRequest, error) {
	v, ok := r.requests[id]
	if !ok {
		return v, additionrequest.ErrNotFound
	}
	return copyValue(v), nil
}

func (r *TestRepository) SaveRequest(_ context.Context, v additionrequest.AdditionRequest, expected int64) error {
	if r.failSaveRequest {
		r.locationsAtFailedSave = len(r.locations)
		return additionrequest.DependencyError("save request", context.DeadlineExceeded)
	}
	if expected > 0 {
		old := r.requests[v.ID]
		if old.Status != additionrequest.Pending {
			return additionrequest.ErrFailedPrecondition
		}
		if old.Revision != expected {
			return additionrequest.ErrAborted
		}
	}
	r.requests[v.ID] = copyValue(v)
	return nil
}

func (r *TestRepository) ListRequests(_ context.Context, c additionrequest.Caller, status additionrequest.RequestStatus, p additionrequest.Page) ([]additionrequest.AdditionRequest, int64, error) {
	items := []additionrequest.AdditionRequest{}
	for _, v := range r.requests {
		if (c.IsAdmin() || v.SubmittedBy == c.ID || v.Status == additionrequest.Approved) && (status == "" || v.Status == status) {
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

func (r *TestRepository) ValidateReferences(_ context.Context, p additionrequest.Proposal) error {
	if p.BuildingID != buildingID {
		return additionrequest.ErrFailedPrecondition
	}
	for _, id := range p.CategoryIDs {
		if id != categoryID {
			return additionrequest.ErrFailedPrecondition
		}
	}
	return nil
}

func (r *TestRepository) CreateLocation(_ context.Context, p additionrequest.Proposal, now time.Time) (additionrequest.Location, error) {
	if r.failCreate {
		return additionrequest.Location{}, additionrequest.DependencyError("create Location", context.DeadlineExceeded)
	}
	v := additionrequest.Location{ID: uuid.NewString(), Proposal: copyValue(p), Revision: 1, CreatedAt: now, UpdatedAt: now}
	r.locations[v.ID] = v
	return copyValue(v), nil
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
		return additionrequest.ErrFailedPrecondition
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

func slicePage[T any](items []T, p additionrequest.Page) []T {
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
