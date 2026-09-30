package location

import (
	"context"
	"errors"
	"math"
	"sync"
	"testing"
	"time"

	"github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/idempotency"
)

const (
	buildingID  = "10000000-0000-4000-8000-000000000000"
	categoryID  = "20000000-0000-4000-8000-000000000000"
	category2ID = "30000000-0000-4000-8000-000000000000"
	locationID  = "40000000-0000-4000-8000-000000000000"
	requestKey  = "50000000-0000-4000-8000-000000000000"
)

var mutationNow = time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
var mutationAdmin = Caller{ID: "admin-user", Admin: true}

type mutationFake struct {
	mu                                     sync.Mutex
	location                               Location
	records                                map[idempotency.Scope]idempotency.Record
	creates, updates, archives, references int
	failSave                               bool
}

func (f *mutationFake) Within(_ context.Context, fn func(MutationTx) error) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	clone := mutationFake{location: f.location, creates: f.creates, updates: f.updates,
		archives: f.archives, references: f.references, failSave: f.failSave}
	clone.records = make(map[idempotency.Scope]idempotency.Record, len(f.records))
	for k, v := range f.records {
		clone.records[k] = v
	}
	if err := fn(&clone); err != nil {
		return err
	}
	f.location, f.records = clone.location, clone.records
	f.creates, f.updates, f.archives, f.references = clone.creates, clone.updates, clone.archives, clone.references
	return nil
}
func (f *mutationFake) Lock(context.Context, idempotency.Scope) error { return nil }
func (f *mutationFake) Find(_ context.Context, scope idempotency.Scope, now time.Time) (*idempotency.Record, error) {
	r, ok := f.records[scope]
	if !ok || !r.ExpiresAt.After(now) {
		return nil, nil
	}
	return &r, nil
}
func (f *mutationFake) Save(_ context.Context, scope idempotency.Scope, record idempotency.Record) error {
	if f.failSave {
		return errors.New("save failed")
	}
	f.records[scope] = record
	return nil
}
func (f *mutationFake) GetForUpdate(_ context.Context, id string) (Location, error) {
	if f.location.ID != id {
		return Location{}, ErrNotFound
	}
	return f.location, nil
}
func (f *mutationFake) ValidateReferences(_ context.Context, in Input) error {
	f.references++
	if in.BuildingID != buildingID {
		return ErrFailedPrecondition
	}
	for _, id := range in.CategoryIDs {
		if id != categoryID && id != category2ID {
			return ErrFailedPrecondition
		}
	}
	return nil
}
func (f *mutationFake) Create(_ context.Context, in Input, now time.Time) (Location, error) {
	f.creates++
	f.location = fakeLocation(in, now)
	return f.location, nil
}
func (f *mutationFake) Update(_ context.Context, id string, in Input, expected int64, now time.Time) (Location, error) {
	if f.location.ID != id {
		return Location{}, ErrNotFound
	}
	if f.location.Revision != expected {
		return Location{}, ErrAborted
	}
	f.updates++
	archived, created := f.location.ArchivedAt, f.location.CreatedAt
	f.location = fakeLocation(in, now)
	f.location.Revision = expected + 1
	f.location.ArchivedAt, f.location.CreatedAt = archived, created
	return f.location, nil
}
func (f *mutationFake) SetArchived(_ context.Context, id string, at *time.Time, expected int64, now time.Time) (Location, error) {
	if f.location.ID != id {
		return Location{}, ErrNotFound
	}
	if f.location.Revision != expected {
		return Location{}, ErrAborted
	}
	f.archives++
	f.location.ArchivedAt = at
	f.location.Revision++
	f.location.UpdatedAt = now
	return f.location, nil
}

func fakeLocation(in Input, now time.Time) Location {
	categories := make([]Category, len(in.CategoryIDs))
	for i, id := range in.CategoryIDs {
		categories[i] = Category{ID: id}
	}
	return Location{ID: locationID, Name: in.Name, IsSupplier: in.IsSupplier,
		Building: Building{ID: in.BuildingID}, Categories: categories,
		Floor: in.Floor, Coordinates: *in.Coordinates, OpensAt: in.OpensAt,
		ClosesAt: in.ClosesAt, Contact: in.Contact, Details: in.Details,
		Revision: 1, CreatedAt: now, UpdatedAt: now}
}

func goodInput() Input {
	return Input{Name: " Shop ", IsSupplier: true, CategoryIDs: []string{categoryID},
		BuildingID: buildingID, Coordinates: &Coordinates{Latitude: 1.3, Longitude: 103.8}}
}

func TestMutationCreateIdempotencyAndRollback(t *testing.T) {
	ctx := context.Background()
	f := &mutationFake{records: map[idempotency.Scope]idempotency.Record{}}
	s := NewMutationService(f, func() time.Time { return mutationNow })
	in := goodInput()
	in.Floor = strPtr("  2  ")
	in.Contact = strPtr("   ")
	first, err := s.Create(ctx, mutationAdmin, CreateRequest{Key: requestKey, Input: in})
	if err != nil {
		t.Fatal(err)
	}
	if first.Name != "Shop" || first.Floor == nil || *first.Floor != "2" || first.Contact != nil || f.creates != 1 {
		t.Fatalf("first = %+v; creates = %d", first, f.creates)
	}
	replay, err := s.Create(ctx, mutationAdmin, CreateRequest{Key: requestKey, Input: in})
	if err != nil || replay.ID != first.ID || f.creates != 1 || f.references != 1 {
		t.Fatalf("replay = %+v, %v; creates=%d references=%d", replay, err, f.creates, f.references)
	}
	changed := in
	changed.Name = "Other"
	if _, err := s.Create(ctx, mutationAdmin, CreateRequest{Key: requestKey, Input: changed}); !errors.Is(err, ErrAlreadyExists) {
		t.Fatalf("conflict = %v", err)
	}
	badRef := goodInput()
	badRef.BuildingID = "60000000-0000-4000-8000-000000000000"
	if _, err := s.Create(ctx, mutationAdmin, CreateRequest{Key: "70000000-0000-4000-8000-000000000000", Input: badRef}); !errors.Is(err, ErrFailedPrecondition) {
		t.Fatalf("missing reference = %v", err)
	}
	f.failSave = true
	if _, err := s.Create(ctx, mutationAdmin, CreateRequest{Key: "80000000-0000-4000-8000-000000000000", Input: goodInput()}); err == nil {
		t.Fatal("expected record save failure")
	}
	if f.creates != 1 || len(f.records) != 1 {
		t.Fatalf("failed transaction committed: creates=%d records=%d", f.creates, len(f.records))
	}
}

func TestMutationCreateConcurrentRetry(t *testing.T) {
	f := &mutationFake{records: map[idempotency.Scope]idempotency.Record{}}
	s := NewMutationService(f, func() time.Time { return mutationNow })
	var wg sync.WaitGroup
	results := make(chan error, 16)
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := s.Create(context.Background(), mutationAdmin, CreateRequest{Key: requestKey, Input: goodInput()})
			results <- err
		}()
	}
	wg.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Fatal(err)
		}
	}
	if f.creates != 1 {
		t.Fatalf("creates = %d, want 1", f.creates)
	}
}

func TestMutationCreateEmptyCategoriesHaveOneHash(t *testing.T) {
	f := &mutationFake{}
	s := NewMutationService(f, func() time.Time { return mutationNow })
	in := goodInput()
	in.IsSupplier, in.CategoryIDs = false, nil
	first, err := s.Create(context.Background(), mutationAdmin, CreateRequest{Key: requestKey, Input: in})
	if err != nil {
		t.Fatal(err)
	}
	in.CategoryIDs = []string{}
	retry, err := s.Create(context.Background(), mutationAdmin, CreateRequest{Key: requestKey, Input: in})
	if err != nil || retry.ID != first.ID || f.creates != 1 {
		t.Fatalf("empty category replay = %+v, %v; creates=%d", retry, err, f.creates)
	}
}

func TestMutationAuthorizationAndInputValidation(t *testing.T) {
	f := &mutationFake{}
	s := NewMutationService(f, func() time.Time { return mutationNow })
	for _, tc := range []struct {
		caller Caller
		want   error
	}{{Caller{}, ErrUnauthenticated}, {Caller{Admin: true}, ErrUnauthenticated}, {Caller{ID: "user"}, ErrPermissionDenied}} {
		if _, err := s.Create(context.Background(), tc.caller, CreateRequest{Key: requestKey, Input: goodInput()}); !errors.Is(err, tc.want) {
			t.Fatalf("caller %+v: %v", tc.caller, err)
		}
	}
	checks := []struct {
		name   string
		change func(*Input)
		want   error
	}{
		{"blank name", func(in *Input) { in.Name = " \n " }, ErrInvalidArgument},
		{"long name", func(in *Input) { in.Name = stringOf('a', 201) }, ErrInvalidArgument},
		{"missing coordinates", func(in *Input) { in.Coordinates = nil }, ErrInvalidArgument},
		{"nonfinite coordinates", func(in *Input) { in.Coordinates.Latitude = math.NaN() }, ErrInvalidArgument},
		{"out of range coordinates", func(in *Input) { in.Coordinates.Longitude = 181 }, ErrInvalidArgument},
		{"bad building id", func(in *Input) { in.BuildingID = "bad" }, ErrInvalidArgument},
		{"duplicate categories", func(in *Input) { in.CategoryIDs = append(in.CategoryIDs, categoryID) }, ErrInvalidArgument},
		{"ordinary with categories", func(in *Input) { in.IsSupplier = false }, ErrFailedPrecondition},
		{"supplier without categories", func(in *Input) { in.CategoryIDs = nil }, ErrFailedPrecondition},
		{"unpaired hours", func(in *Input) { in.OpensAt = &Clock{Hour: 9} }, ErrInvalidArgument},
		{"equal hours", func(in *Input) { in.OpensAt = &Clock{Hour: 9}; in.ClosesAt = &Clock{Hour: 9} }, ErrInvalidArgument},
		{"bad hours", func(in *Input) { in.OpensAt = &Clock{Hour: 24}; in.ClosesAt = &Clock{Hour: 1} }, ErrInvalidArgument},
		{"long floor", func(in *Input) { in.Floor = strPtr(stringOf('f', 51)) }, ErrInvalidArgument},
		{"long contact", func(in *Input) { in.Contact = strPtr(stringOf('c', 501)) }, ErrInvalidArgument},
		{"long details", func(in *Input) { in.Details = stringOf('d', 2001) }, ErrInvalidArgument},
	}
	for _, tc := range checks {
		t.Run(tc.name, func(t *testing.T) {
			in := goodInput()
			tc.change(&in)
			_, err := s.Create(context.Background(), mutationAdmin, CreateRequest{Key: requestKey, Input: in})
			if !errors.Is(err, tc.want) {
				t.Fatalf("got %v want %v", err, tc.want)
			}
		})
	}
	if f.creates != 0 {
		t.Fatalf("invalid inputs created %d rows", f.creates)
	}
	valid := goodInput()
	valid.OpensAt = &Clock{Hour: 23, Minute: 59}
	valid.ClosesAt = &Clock{Hour: 0}
	valid.Name = "  " + stringOf('n', 200) + "  "
	if _, err := s.Create(context.Background(), mutationAdmin, CreateRequest{Key: requestKey, Input: valid}); err != nil {
		t.Fatalf("valid boundary input: %v", err)
	}
}

func TestMutationUpdateMasksAndRevision(t *testing.T) {
	f := &mutationFake{location: fakeLocation(goodInput(), mutationNow), records: map[idempotency.Scope]idempotency.Record{}}
	s := NewMutationService(f, func() time.Time { return mutationNow.Add(time.Hour) })
	for _, paths := range [][]string{nil, {"name", "name"}, {"id"}, {"opens_at"}, {"closes_at"}} {
		_, err := s.Update(context.Background(), mutationAdmin, UpdateRequest{ID: locationID, ExpectedRevision: 1, Paths: paths, Input: goodInput()})
		if !errors.Is(err, ErrInvalidArgument) {
			t.Fatalf("paths %v: %v", paths, err)
		}
	}
	if _, err := s.Update(context.Background(), mutationAdmin, UpdateRequest{ID: locationID, ExpectedRevision: 2, Paths: []string{"name"}, Input: Input{Name: "X"}}); !errors.Is(err, ErrAborted) {
		t.Fatalf("stale revision: %v", err)
	}
	if _, err := s.Update(context.Background(), mutationAdmin, UpdateRequest{ID: locationID, ExpectedRevision: 0, Paths: []string{"name"}, Input: Input{Name: "X"}}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("invalid revision: %v", err)
	}
	got, err := s.Update(context.Background(), mutationAdmin, UpdateRequest{ID: locationID, ExpectedRevision: 1, Paths: []string{"name", "contact", "floor", "opens_at", "closes_at"}, Input: Input{Name: "  New ", Contact: strPtr(" "), Floor: nil}})
	if err != nil || got.Name != "New" || got.Contact != nil || got.Floor != nil || got.Revision != 2 || got.IsSupplier != true || len(got.Categories) != 1 {
		t.Fatalf("masked update: %+v, %v", got, err)
	}
	if _, err := s.Update(context.Background(), mutationAdmin, UpdateRequest{ID: locationID, ExpectedRevision: 2, Paths: []string{"is_supplier"}, Input: Input{IsSupplier: false}}); !errors.Is(err, ErrFailedPrecondition) {
		t.Fatalf("classification invariant: %v", err)
	}
	if f.updates != 1 {
		t.Fatalf("updates = %d", f.updates)
	}
	got, err = s.Update(context.Background(), mutationAdmin, UpdateRequest{ID: locationID, ExpectedRevision: 2, Paths: []string{"is_supplier", "category_ids"}, Input: Input{IsSupplier: false}})
	if err != nil || got.IsSupplier || len(got.Categories) != 0 {
		t.Fatalf("atomic classification update: %+v %v", got, err)
	}
}

func TestMutationArchiveIdempotentAndEditable(t *testing.T) {
	f := &mutationFake{location: fakeLocation(goodInput(), mutationNow)}
	s := NewMutationService(f, func() time.Time { return mutationNow.Add(time.Hour) })
	first, err := s.Archive(context.Background(), mutationAdmin, locationID)
	if err != nil || first.ArchivedAt == nil || first.Revision != 2 {
		t.Fatalf("archive: %+v %v", first, err)
	}
	again, err := s.Archive(context.Background(), mutationAdmin, locationID)
	if err != nil || again.Revision != 2 || f.archives != 1 || !again.UpdatedAt.Equal(first.UpdatedAt) {
		t.Fatalf("repeat archive: %+v %v count=%d", again, err, f.archives)
	}
	updated, err := s.Update(context.Background(), mutationAdmin, UpdateRequest{ID: locationID, ExpectedRevision: 2, Paths: []string{"details"}, Input: Input{Details: "  Archived details "}})
	if err != nil || updated.ArchivedAt == nil || updated.Details != "Archived details" {
		t.Fatalf("edit archived: %+v %v", updated, err)
	}
	unarchived, err := s.Unarchive(context.Background(), mutationAdmin, locationID)
	if err != nil || unarchived.ArchivedAt != nil || unarchived.Revision != 4 {
		t.Fatalf("unarchive: %+v %v", unarchived, err)
	}
	again, err = s.Unarchive(context.Background(), mutationAdmin, locationID)
	if err != nil || again.Revision != 4 || f.archives != 2 {
		t.Fatalf("repeat unarchive: %+v %v count=%d", again, err, f.archives)
	}
}

func strPtr(s string) *string { return &s }
func stringOf(r rune, n int) string {
	b := make([]rune, n)
	for i := range b {
		b[i] = r
	}
	return string(b)
}
