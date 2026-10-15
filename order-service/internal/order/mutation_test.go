package order

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

type txFake struct {
	events          *[]string
	order           Order
	offers          []Offer
	lockErr         error
	relationshipErr error
	applyErr        error
	applied         int
	change          Change
}

func (tx *txFake) LockOrder(_ context.Context, id string) (Order, error) {
	*tx.events = append(*tx.events, "lock:"+id)
	return tx.order, tx.lockErr
}
func (tx *txFake) Relationships(_ context.Context, id string) ([]Offer, error) {
	*tx.events = append(*tx.events, "relationships:"+id)
	return tx.offers, tx.relationshipErr
}
func (tx *txFake) Apply(_ context.Context, change Change) error {
	*tx.events = append(*tx.events, "apply")
	tx.applied++
	tx.change = change
	return tx.applyErr
}

type repoFake struct {
	tx        *txFake
	commits   int
	rollbacks int
}

func (r *repoFake) Within(_ context.Context, fn func(Tx) error) error {
	err := fn(r.tx)
	if err != nil {
		r.rollbacks++
		return err
	}
	r.commits++
	return nil
}

func TestMutateLocksLoadsRelationshipsBuildsAndAppliesInOrder(t *testing.T) {
	events := []string{}
	current := Order{ID: "o1", Status: AwaitingCourier, Revision: 4}
	offer := Offer{ID: "offer", OrderID: "o1", Status: OfferOpen}
	tx := &txFake{events: &events, order: current, offers: []Offer{offer}}
	repo := &repoFake{tx: tx}
	wantChange := Change{ExpectedRevision: 4, Order: Order{ID: "o1", Status: AwaitingPickup, Revision: 5}, Action: Assign}
	err := NewMutator(repo).Mutate(context.Background(), "o1", func(got Order, offers []Offer) (*Change, error) {
		events = append(events, "build")
		if got != current || !reflect.DeepEqual(offers, []Offer{offer}) {
			t.Fatalf("build received (%+v, %+v)", got, offers)
		}
		return &wantChange, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(events, []string{"lock:o1", "relationships:o1", "build", "apply"}) {
		t.Fatalf("operation sequence = %v", events)
	}
	if repo.commits != 1 || repo.rollbacks != 0 || tx.applied != 1 || !reflect.DeepEqual(tx.change, wantChange) {
		t.Fatalf("repo=%+v applied=%d change=%+v", repo, tx.applied, tx.change)
	}
}

// NFR-04/NFR-04.1 (../../REQUIREMENTS.md#nfr-04): recognized local repeats produce no
// aggregate, audit or outbox writes; relay/downstream retries are outside this test.
func TestMutateRecognizedRepeatCommitsWithoutWrites(t *testing.T) {
	events := []string{}
	repo := &repoFake{tx: &txFake{events: &events, order: Order{ID: "o1", Revision: 3}}}
	err := NewMutator(repo).Mutate(context.Background(), "o1", func(Order, []Offer) (*Change, error) {
		events = append(events, "build")
		return nil, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(events, []string{"lock:o1", "relationships:o1", "build"}) || repo.commits != 1 || repo.rollbacks != 0 || repo.tx.applied != 0 {
		t.Fatalf("repeat had writes/rollback: events=%v repo=%+v apply=%d", events, repo, repo.tx.applied)
	}
}

func TestMutateRollsBackAnyFailure(t *testing.T) {
	failure := errors.New("injected failure")
	cases := []struct {
		name  string
		setup func(*txFake)
		build func() func(Order, []Offer) (*Change, error)
	}{
		{name: "lock", setup: func(tx *txFake) { tx.lockErr = failure }, build: func() func(Order, []Offer) (*Change, error) {
			return func(Order, []Offer) (*Change, error) { return nil, nil }
		}},
		{name: "relationships", setup: func(tx *txFake) { tx.relationshipErr = failure }, build: func() func(Order, []Offer) (*Change, error) {
			return func(Order, []Offer) (*Change, error) { return nil, nil }
		}},
		{name: "build", setup: func(*txFake) {}, build: func() func(Order, []Offer) (*Change, error) {
			return func(Order, []Offer) (*Change, error) { return nil, failure }
		}},
		{name: "apply", setup: func(tx *txFake) { tx.applyErr = failure }, build: func() func(Order, []Offer) (*Change, error) {
			return func(o Order, _ []Offer) (*Change, error) {
				return &Change{ExpectedRevision: o.Revision, Order: Order{ID: o.ID, Revision: o.Revision + 1}}, nil
			}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			events := []string{}
			tx := &txFake{events: &events, order: Order{ID: "o1", Revision: 1}}
			tc.setup(tx)
			repo := &repoFake{tx: tx}
			err := NewMutator(repo).Mutate(context.Background(), "o1", tc.build())
			if !errors.Is(err, failure) || repo.rollbacks != 1 || repo.commits != 0 {
				t.Fatalf("Mutate=(%v), commits=%d rollbacks=%d", err, repo.commits, repo.rollbacks)
			}
		})
	}
}

func TestMutateRejectsStaleRevisionBeforeApply(t *testing.T) {
	events := []string{}
	repo := &repoFake{tx: &txFake{events: &events, order: Order{ID: "o1", Revision: 7}}}
	err := NewMutator(repo).Mutate(context.Background(), "o1", func(Order, []Offer) (*Change, error) {
		return &Change{ExpectedRevision: 6, Order: Order{ID: "o1", Revision: 7}}, nil
	})
	if !errors.Is(err, ErrConflict) || repo.rollbacks != 1 || repo.tx.applied != 0 {
		t.Fatalf("stale mutation=(%v), rollback=%d apply=%d", err, repo.rollbacks, repo.tx.applied)
	}
}
