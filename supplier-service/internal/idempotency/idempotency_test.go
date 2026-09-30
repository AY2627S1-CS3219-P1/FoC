package idempotency

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

const testKey = "123e4567-e89b-12d3-a456-426614174000"

type fakeStore struct {
	records                   map[Scope]Record
	locked                    *Scope
	events                    []string
	lockErr, findErr, saveErr error
	onLock                    func()
}

func (s *fakeStore) Lock(_ context.Context, scope Scope) error {
	s.events = append(s.events, "lock")
	if s.onLock != nil {
		s.onLock()
	}
	s.locked = &scope
	return s.lockErr
}

func (s *fakeStore) Find(_ context.Context, scope Scope, now time.Time) (*Record, error) {
	s.events = append(s.events, "find")
	if s.locked == nil || *s.locked != scope {
		panic("Find without scoped lock")
	}
	if s.findErr != nil {
		return nil, s.findErr
	}
	v, ok := s.records[scope]
	if !ok {
		return nil, nil
	}
	if !v.ExpiresAt.After(now) {
		delete(s.records, scope)
		return nil, nil
	}
	return &v, nil
}

func (s *fakeStore) Save(_ context.Context, scope Scope, record Record) error {
	s.events = append(s.events, "save")
	if s.locked == nil || *s.locked != scope {
		panic("Save without scoped lock")
	}
	if s.saveErr != nil {
		return s.saveErr
	}
	if s.records == nil {
		s.records = make(map[Scope]Record)
	}
	s.records[scope] = record
	return nil
}

func TestRunCreateReplayConflictAndScope(t *testing.T) {
	now := time.Date(2026, 9, 28, 13, 2, 0, 123, time.FixedZone("SGT", 8*3600))
	r := New(func() time.Time { return now })
	s := &fakeStore{}
	base := Scope{Caller: "alice", Method: "Create", Key: testKey}
	creates := 0
	create := func(got time.Time) (string, error) {
		creates++
		s.events = append(s.events, "create")
		if !got.Equal(now) || got.Location() != time.UTC {
			t.Fatalf("create time = %v (%v)", got, got.Location())
		}
		return "resource-1", nil
	}
	id, err := r.Run(context.Background(), s, base, "hash-1", create)
	if err != nil || id != "resource-1" {
		t.Fatalf("create = %q, %v", id, err)
	}
	if got := s.records[base]; got.ExpiresAt != now.UTC().Add(24*time.Hour) || got.Hash != "hash-1" || got.ResourceID != id {
		t.Fatalf("record = %+v", got)
	}
	if got := strings.Join(s.events, ","); got != "lock,find,create,save" {
		t.Fatalf("order = %s", got)
	}
	id, err = r.Run(context.Background(), s, base, "hash-1", create)
	if err != nil || id != "resource-1" || creates != 1 {
		t.Fatalf("replay = %q, %v, creates %d", id, err, creates)
	}
	_, err = r.Run(context.Background(), s, base, "hash-2", create)
	if !errors.Is(err, ErrConflict) || creates != 1 {
		t.Fatalf("conflict = %v, creates %d", err, creates)
	}
	for _, scope := range []Scope{{Caller: "bob", Method: base.Method, Key: base.Key}, {Caller: base.Caller, Method: "Update", Key: base.Key}, {Caller: base.Caller, Method: base.Method, Key: "123e4567-e89b-12d3-a456-426614174001"}} {
		_, err := r.Run(context.Background(), s, scope, "hash-2", create)
		if err != nil {
			t.Fatalf("independent scope %+v: %v", scope, err)
		}
	}
	if creates != 4 {
		t.Fatalf("creates = %d", creates)
	}
}

func TestRunExpiryAtExactBoundaryAndScopedDeletion(t *testing.T) {
	now := time.Date(2026, 9, 28, 0, 0, 0, 0, time.UTC)
	r := New(func() time.Time { return now })
	scope := Scope{Caller: "a", Method: "m", Key: testKey}
	other := Scope{Caller: "b", Method: "m", Key: testKey}
	s := &fakeStore{records: map[Scope]Record{scope: {Hash: "old", ResourceID: "old-id", ExpiresAt: now.Add(time.Hour)}, other: {Hash: "other", ResourceID: "other-id", ExpiresAt: now.Add(-time.Hour)}}}
	create := func(time.Time) (string, error) { return "new-id", nil }
	if id, err := r.Run(context.Background(), s, scope, "old", create); err != nil || id != "old-id" {
		t.Fatalf("before expiry = %q, %v", id, err)
	}
	now = now.Add(time.Hour)
	if id, err := r.Run(context.Background(), s, scope, "new", create); err != nil || id != "new-id" {
		t.Fatalf("at expiry = %q, %v", id, err)
	}
	if _, ok := s.records[other]; !ok {
		t.Fatal("unrelated expired scope removed")
	}
}

func TestRunCanonicalizesKeyBeforeLock(t *testing.T) {
	now := time.Date(2026, 9, 28, 0, 0, 0, 0, time.UTC)
	r := New(func() time.Time { return now })
	s := &fakeStore{}
	for _, bad := range []string{"", "not-a-uuid", "  " + testKey, testKey + "x"} {
		_, err := r.Run(context.Background(), s, Scope{Key: bad}, "h", func(time.Time) (string, error) { t.Fatal("created with invalid key"); return "", nil })
		if !errors.Is(err, ErrInvalidKey) || len(s.events) != 0 {
			t.Fatalf("key %q: %v, events %v", bad, err, s.events)
		}
	}
	upper := "123E4567-E89B-12D3-A456-426614174000"
	_, err := r.Run(context.Background(), s, Scope{Caller: "c", Method: "m", Key: upper}, "h", func(time.Time) (string, error) { return "id", nil })
	if err != nil || s.locked.Key != testKey {
		t.Fatalf("canonical key = %+v, %v", s.locked, err)
	}
}

func TestRunSamplesClockAfterLock(t *testing.T) {
	clockCalls := 0
	s := &fakeStore{onLock: func() {
		if clockCalls != 0 {
			t.Fatal("clock sampled before lock")
		}
	}}
	now := time.Date(2026, 9, 28, 0, 0, 0, 0, time.UTC)
	r := New(func() time.Time { clockCalls++; return now })
	_, err := r.Run(context.Background(), s, Scope{Key: testKey}, "h", func(got time.Time) (string, error) {
		if got != now {
			t.Fatalf("callback time = %v", got)
		}
		return "id", nil
	})
	if err != nil || clockCalls != 1 {
		t.Fatalf("run = %v, clock calls %d", err, clockCalls)
	}
}

func TestRunPropagatesFailures(t *testing.T) {
	boom := errors.New("boom")
	for _, stage := range []string{"lock", "find", "create", "save"} {
		t.Run(stage, func(t *testing.T) {
			s := &fakeStore{}
			switch stage {
			case "lock":
				s.lockErr = boom
			case "find":
				s.findErr = boom
			case "save":
				s.saveErr = boom
			}
			created := false
			_, err := New(time.Now).Run(context.Background(), s, Scope{Key: testKey}, "h", func(time.Time) (string, error) {
				created = true
				if stage == "create" {
					return "", boom
				}
				return "id", nil
			})
			if !errors.Is(err, boom) {
				t.Fatalf("error = %v", err)
			}
			if (stage == "lock" || stage == "find") && created {
				t.Fatal("created after lookup failure")
			}
			if _, ok := s.records[Scope{Key: testKey}]; ok {
				t.Fatal("saved after failure")
			}
		})
	}
}

func TestHashCompatibilityAndScopeJSON(t *testing.T) {
	for _, tc := range []struct {
		value any
		want  string
	}{
		{nil, "74234e98afe7498fb5daf1f36ac2d78acc339464f950703b8c019892f982b90b"},
		{struct{ Categories []string }{Categories: nil}, "90db2e2772e528a4063da3196afb5aa511e241804fc145f153d175acd7156f7b"},
		{struct{ Categories []string }{Categories: []string{}}, "869961b27a01087b2bf448b23a6dc8ab1350ea4e84ad8720b4c1887a8789d9ca"},
	} {
		got, err := Hash(tc.value)
		if err != nil || got != tc.want {
			t.Fatalf("Hash(%#v) = %q, %v; want %q", tc.value, got, err, tc.want)
		}
	}
	_, err := Hash(make(chan int))
	if err == nil {
		t.Fatal("expected JSON marshal error")
	}
	b, err := json.Marshal(Scope{Caller: "a", Method: "m", Key: testKey})
	if err != nil || string(b) != `{"Caller":"a","Method":"m","Key":"123e4567-e89b-12d3-a456-426614174000"}` {
		t.Fatalf("scope JSON = %s, %v", b, err)
	}
}
