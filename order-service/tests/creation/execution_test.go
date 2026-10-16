//go:build integration

package creation_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/AY2627S1-CS3219-P1/FoC/order-service/internal/clients"
	app "github.com/AY2627S1-CS3219-P1/FoC/order-service/internal/order"
	creditpb "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/credit/v1"
	orderpb "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/order/v1"
	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type scenario struct {
	*fixture
	clock     *clock
	remotes   *remotes
	locations *clients.Supplier
	credit    *clients.Credit
	creator   *app.Creator
}

func newScenario(t *testing.T, f *fixture) *scenario {
	t.Helper()
	f.reset(t)
	clock := newClock()
	remotes := newRemotes(t)
	locations, credit := remotes.adapters(t)
	creator := app.NewCreator(f.repo, locations, credit, app.CreationOptions{AdmissionsEnabled: true, Now: clock.Now})
	return &scenario{fixture: f, clock: clock, remotes: remotes, locations: locations, credit: credit, creator: creator}
}

func (s *scenario) restart() *app.Creator {
	// Restart with admissions disabled and no Supplier dependency. Persisted work
	// must recover without a user token or re-fetching mutable location data.
	return app.NewCreator(s.repo, nil, s.credit, app.CreationOptions{Now: s.clock.Now})
}

func TestCreationExecution(t *testing.T) {
	f := databaseFixture(t)
	t.Run("opens once and replays after expiry while admissions are disabled", func(t *testing.T) {
		s := newScenario(t, f)
		c := mount(t, f, s.creator)
		in := input(s.clock.Now())
		first, err := create(c, f.ctx, in)
		if err != nil {
			t.Fatal(err)
		}
		if first.Status != orderpb.OrderStatus_ORDER_STATUS_AWAITING_COURIER || first.Revision != 2 {
			t.Fatalf("order=%v", first)
		}
		s.clock.Advance(2 * time.Hour)
		s.remotes.mu.Lock()
		s.remotes.locationFailure = true
		s.remotes.mu.Unlock()
		retry := proto.Clone(in).(*orderpb.CreateOrderRequest)
		retry.ItemDescription = " \u2003Book "
		retry.PickupLocationId = strings.ToUpper(retry.PickupLocationId)
		replay, err := create(mount(t, f, s.restart()), f.ctx, retry)
		if err != nil || !proto.Equal(first, replay) {
			t.Fatalf("replay=%v err=%v", replay, err)
		}
		changedImages := proto.Clone(in).(*orderpb.CreateOrderRequest)
		changedImages.ItemImageIds = []string{uuid.NewString()}
		if _, err := create(c, f.ctx, changedImages); connect.CodeOf(err) != connect.CodeAlreadyExists {
			t.Fatalf("changed images bypassed retry conflict: %v", err)
		}
		retry.RewardCredits++
		if _, err := create(c, f.ctx, retry); connect.CodeOf(err) != connect.CodeAlreadyExists {
			t.Fatalf("changed retry=%v", err)
		}
		if f.count(t, "orders") != 1 || f.count(t, "order_audit") != 2 || f.count(t, "order_outbox") != 2 {
			t.Fatal("duplicate persisted change")
		}
		s.remotes.mu.Lock()
		defer s.remotes.mu.Unlock()
		if s.remotes.holds != 1 || s.remotes.reserveCalls != 1 || s.remotes.locationCalls != 2 {
			t.Fatalf("remote calls=%+v", s.remotes)
		}
	})
	t.Run("insufficient credits is sticky and has no release or outcome", func(t *testing.T) {
		s := newScenario(t, f)
		s.remotes.refuse = true
		c := mount(t, f, s.creator)
		in := input(s.clock.Now())
		if _, err := create(c, f.ctx, in); connect.CodeOf(err) != connect.CodeFailedPrecondition || strings.Contains(err.Error(), "private") {
			t.Fatalf("refusal=%v", err)
		}
		s.remotes.mu.Lock()
		s.remotes.refuse = false
		s.remotes.mu.Unlock()
		if _, err := create(c, f.ctx, in); connect.CodeOf(err) != connect.CodeFailedPrecondition {
			t.Fatalf("replay=%v", err)
		}
		if get(t, c, f.onlyOrder(t)).Status != orderpb.OrderStatus_ORDER_STATUS_REJECTED {
			t.Fatal("refused order opened")
		}
		var outcomes int
		if err := f.pool.QueryRow(f.ctx, "SELECT count(*) FROM order_outbox WHERE kind='order.outcome'").Scan(&outcomes); err != nil {
			t.Fatal(err)
		}
		s.remotes.mu.Lock()
		defer s.remotes.mu.Unlock()
		if s.remotes.reserveCalls != 1 || s.remotes.releaseCalls != 0 || outcomes != 0 {
			t.Fatal("refusal moved money or emitted outcome")
		}
	})
	t.Run("unknown Reserve and not found stay pending until confirmed release", func(t *testing.T) {
		s := newScenario(t, f)
		s.remotes.unknownReserve = true
		c := mount(t, f, s.creator)
		in := input(s.clock.Now())
		pending, err := create(c, f.ctx, in)
		if err != nil || pending.Status != orderpb.OrderStatus_ORDER_STATUS_PENDING_RESERVATION {
			t.Fatalf("pending=%v err=%v", pending, err)
		}
		s.clock.Advance(2 * time.Hour)
		s.remotes.mu.Lock()
		s.remotes.unknownRelease = true
		s.remotes.mu.Unlock()
		if err := s.restart().RecoverCreationsOnce(f.ctx); err != nil {
			t.Fatal(err)
		}
		w, err := f.repo.Creation(f.ctx, pending.Id)
		if err != nil || w.Phase != app.AbortPending {
			t.Fatalf("workflow=%+v err=%v", w, err)
		}
		if get(t, c, pending.Id).Status != orderpb.OrderStatus_ORDER_STATUS_PENDING_RESERVATION {
			t.Fatal("rejected before confirming release")
		}
		s.remotes.mu.Lock()
		s.remotes.unknownRelease = false
		s.remotes.unknownReserve = false
		s.remotes.mu.Unlock()
		s.clock.Advance(31 * time.Second)
		if err := s.restart().RecoverCreationsOnce(f.ctx); err != nil {
			t.Fatal(err)
		}
		if _, err := create(c, f.ctx, in); connect.CodeOf(err) != connect.CodeFailedPrecondition {
			t.Fatalf("expired replay=%v", err)
		}
		if get(t, c, pending.Id).Status != orderpb.OrderStatus_ORDER_STATUS_REJECTED {
			t.Fatal("expiry did not reject")
		}
		// A delayed first Reserve is fenced by the remote tombstone. Order's resolved
		// workflow likewise cannot reopen if any old task is delivered again.
		_ = s.credit.Reserve(f.ctx, app.ReservationIntent{OrderID: pending.Id, RequesterID: requester, Amount: 10})
		if err := s.creator.ProcessCreation(f.ctx, pending.Id); err != nil {
			t.Fatal(err)
		}
		view, err := s.credit.GetReservation(f.ctx, pending.Id)
		if err != nil || view.State != app.ReservationIsReleased || view.ReservedAmount != 0 {
			t.Fatalf("current reservation=%+v err=%v", view, err)
		}
	})
	t.Run("expiry during Reserve releases before rejecting", func(t *testing.T) {
		s := newScenario(t, f)
		in := input(s.clock.Now())
		in.ExpiresAt = timestamppb.New(s.clock.Now().Add(time.Second))
		s.remotes.onReserve = func() { s.clock.Advance(2 * time.Second) }
		c := mount(t, f, s.creator)
		if _, err := create(c, f.ctx, in); connect.CodeOf(err) != connect.CodeFailedPrecondition {
			t.Fatalf("expiry=%v", err)
		}
		id := f.onlyOrder(t)
		w, err := f.repo.Creation(f.ctx, id)
		if err != nil || w.Rejection != app.CreationExpired || get(t, c, id).Status != orderpb.OrderStatus_ORDER_STATUS_REJECTED {
			t.Fatalf("workflow=%+v err=%v", w, err)
		}
		s.remotes.mu.Lock()
		defer s.remotes.mu.Unlock()
		if s.remotes.holds != 1 || s.remotes.releaseCalls != 1 || s.remotes.reservations[id].ReservedAmount != 0 {
			t.Fatal("expired creation retained hold")
		}
	})
	t.Run("historical Reserve success is not proof of current hold", func(t *testing.T) {
		s := newScenario(t, f)
		s.remotes.unknownGet = true
		c := mount(t, f, s.creator)
		pending, err := create(c, f.ctx, input(s.clock.Now()))
		if err != nil {
			t.Fatal(err)
		}
		if err := s.credit.ReleaseExpired(f.ctx, pending.Id); err != nil {
			t.Fatal(err)
		}
		s.remotes.mu.Lock()
		s.remotes.unknownGet = false
		s.remotes.mu.Unlock()
		s.clock.Advance(2 * time.Second)
		if err := s.restart().RecoverCreationsOnce(f.ctx); err != nil {
			t.Fatal(err)
		}
		w, err := f.repo.Creation(f.ctx, pending.Id)
		if err != nil || w.Rejection != app.ReservationReleased || get(t, c, pending.Id).Status != orderpb.OrderStatus_ORDER_STATUS_REJECTED {
			t.Fatalf("workflow=%+v err=%v", w, err)
		}
	})
	for _, tc := range []struct {
		name   string
		expire bool
	}{{"late response after lease takeover", false}, {"late success after abort cannot reopen", true}} {
		t.Run(tc.name, func(t *testing.T) {
			s := newScenario(t, f)
			in := input(s.clock.Now())
			if tc.expire {
				in.ExpiresAt = timestamppb.New(s.clock.Now().Add(5 * time.Second))
			}
			entered, release := make(chan struct{}), make(chan struct{})
			var once atomic.Bool
			s.remotes.onGet = func(v *creditpb.Reservation) {
				if v.Status == creditpb.ReservationStatus_RESERVATION_STATUS_HELD && once.CompareAndSwap(false, true) {
					close(entered)
					<-release
				}
			}
			c := mount(t, f, s.creator)
			finished := make(chan error, 1)
			go func() { _, err := create(c, f.ctx, in); finished <- err }()
			select {
			case <-entered:
			case <-time.After(5 * time.Second):
				t.Fatal("financial observation did not start")
			}
			id := f.onlyOrder(t)
			s.clock.Advance(16 * time.Second)
			err := s.restart().RecoverCreationsOnce(f.ctx)
			close(release)
			if err != nil {
				t.Fatal(err)
			}
			select {
			case err := <-finished:
				if tc.expire && connect.CodeOf(err) != connect.CodeFailedPrecondition {
					t.Fatalf("old response=%v", err)
				}
				if !tc.expire && err != nil {
					t.Fatal(err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("old response did not finish")
			}
			order := get(t, c, id)
			want := orderpb.OrderStatus_ORDER_STATUS_AWAITING_COURIER
			if tc.expire {
				want = orderpb.OrderStatus_ORDER_STATUS_REJECTED
			}
			if order.Status != want || order.Revision != 2 || f.count(t, "order_audit") != 2 {
				t.Fatalf("stale response changed order: %v", order)
			}
		})
	}
	t.Run("concurrent duplicate RPCs share one creation", func(t *testing.T) {
		s := newScenario(t, f)
		c := mount(t, f, s.creator)
		in := input(s.clock.Now())
		type result struct {
			id  string
			err error
		}
		results := make(chan result, 50)
		var group sync.WaitGroup
		for range 50 {
			group.Go(func() {
				o, err := create(c, f.ctx, proto.Clone(in).(*orderpb.CreateOrderRequest))
				id := ""
				if o != nil {
					id = o.Id
				}
				results <- result{id, err}
			})
		}
		group.Wait()
		close(results)
		id := f.onlyOrder(t)
		for result := range results {
			if result.err != nil || result.id != id {
				t.Fatalf("duplicate=%+v expected=%s", result, id)
			}
		}
		if f.count(t, "order_creation_workflows") != 1 || f.count(t, "order_creation_keys") != 1 || f.count(t, "order_audit") != 2 {
			t.Fatal("duplicate workflow or revision")
		}
		s.remotes.mu.Lock()
		defer s.remotes.mu.Unlock()
		if s.remotes.holds != 1 {
			t.Fatal("duplicate financial effect")
		}
	})
	t.Run("concurrent new requests respect requester rate limit", func(t *testing.T) {
		s := newScenario(t, f)
		c := mount(t, f, s.creator)
		results := make(chan error, 50)
		var group sync.WaitGroup
		for range 50 {
			group.Go(func() { _, err := create(c, f.ctx, input(s.clock.Now())); results <- err })
		}
		group.Wait()
		close(results)
		accepted, limited := 0, 0
		for err := range results {
			if err == nil {
				accepted++
			} else if connect.CodeOf(err) == connect.CodeResourceExhausted {
				limited++
			} else {
				t.Fatal(err)
			}
		}
		if accepted != 10 || limited != 40 || f.count(t, "orders") != 10 {
			t.Fatalf("accepted=%d limited=%d", accepted, limited)
		}
		s.clock.Advance(61 * time.Second)
		if _, err := create(c, f.ctx, input(s.clock.Now())); err != nil {
			t.Fatalf("limit did not reset: %v", err)
		}
	})
	t.Run("unsupported images invalid expiry and archived locations do not admit", func(t *testing.T) {
		s := newScenario(t, f)
		c := mount(t, f, s.creator)
		in := input(s.clock.Now())
		in.ItemImageIds = []string{uuid.NewString()}
		if _, err := create(c, f.ctx, in); connect.CodeOf(err) != connect.CodeFailedPrecondition {
			t.Fatal(err)
		}
		in = input(s.clock.Now())
		in.ExpiresAt = timestamppb.New(s.clock.Now())
		if _, err := create(c, f.ctx, in); connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Fatal(err)
		}
		s.remotes.mu.Lock()
		s.remotes.archived = true
		s.remotes.mu.Unlock()
		if _, err := create(c, f.ctx, input(s.clock.Now())); connect.CodeOf(err) != connect.CodeFailedPrecondition {
			t.Fatal(err)
		}
		if f.count(t, "orders") != 0 || f.count(t, "order_creation_keys") != 0 {
			t.Fatal("invalid request admitted")
		}
	})
	t.Run("unresolved key survives beyond 24h and resolved key can retire", func(t *testing.T) {
		s := newScenario(t, f)
		s.remotes.unknownReserve = true
		c := mount(t, f, s.creator)
		in := input(s.clock.Now())
		in.ExpiresAt = timestamppb.New(s.clock.Now().Add(48 * time.Hour))
		pending, err := create(c, f.ctx, in)
		if err != nil {
			t.Fatal(err)
		}
		s.clock.Advance(25 * time.Hour)
		changed := proto.Clone(in).(*orderpb.CreateOrderRequest)
		changed.ItemDescription = "Changed"
		if _, err := create(c, f.ctx, changed); connect.CodeOf(err) != connect.CodeAlreadyExists {
			t.Fatalf("unresolved key retired: %v", err)
		}
		s.remotes.mu.Lock()
		s.remotes.unknownReserve = false
		s.remotes.mu.Unlock()
		if err := s.restart().RecoverCreationsOnce(f.ctx); err != nil {
			t.Fatal(err)
		}
		fresh := input(s.clock.Now())
		fresh.IdempotencyKey = in.IdempotencyKey
		opened, err := create(c, f.ctx, fresh)
		if err != nil || opened.Id == pending.Id {
			t.Fatalf("resolved key reuse=%v err=%v", opened, err)
		}
		if f.count(t, "order_creation_workflows") != 2 || f.count(t, "order_creation_keys") != 1 {
			t.Fatal("workflow history or mapping retention incorrect")
		}
	})
	t.Run("caller cancellation after admission remains recoverable", func(t *testing.T) {
		s := newScenario(t, f)
		ctx, cancel := context.WithCancel(clients.WithUserBearer(f.ctx, "Bearer user-token"))
		defer cancel()
		var once atomic.Bool
		s.remotes.onGet = func(*creditpb.Reservation) {
			if once.CompareAndSwap(false, true) {
				cancel()
			}
		}
		in := app.CreateInput{Key: uuid.NewString(), PickupID: pickupID, DeliveryID: deliveryID, ItemDescription: "Book", RewardCredits: 10, DeliveryDuration: 10 * time.Minute, ExpiresAt: s.clock.Now().Add(time.Hour)}
		if _, err := s.creator.Create(ctx, app.Caller{ID: requester}, in); !errors.Is(err, context.Canceled) {
			t.Fatalf("cancellation=%v", err)
		}
		s.clock.Advance(16 * time.Second)
		if err := s.restart().RecoverCreationsOnce(f.ctx); err != nil {
			t.Fatal(err)
		}
		w, err := f.repo.Creation(f.ctx, f.onlyOrder(t))
		if err != nil || w.Phase != app.CreationOpened {
			t.Fatalf("recovery=%+v err=%v", w, err)
		}
		if strings.Contains(string(w.Canonical), "user-token") || strings.Contains(string(w.Canonical), "service-credit-token") {
			t.Fatal("credential persisted")
		}
	})
}

type blockedLocations struct {
	entered, release chan struct{}
}

func (b blockedLocations) GetLocation(ctx context.Context, _ string) (app.SelectableLocation, error) {
	close(b.entered)
	select {
	case <-b.release:
		return app.SelectableLocation{}, app.ErrUnavailable
	case <-ctx.Done():
		return app.SelectableLocation{}, ctx.Err()
	}
}

func TestCreationPreflightRaces(t *testing.T) {
	f := databaseFixture(t)
	for _, changed := range []bool{false, true} {
		name := "identical request joins admitted workflow"
		if changed {
			name = "changed request conflicts with admitted workflow"
		}
		t.Run(name, func(t *testing.T) {
			s := newScenario(t, f)
			locations := blockedLocations{entered: make(chan struct{}), release: make(chan struct{})}
			slow := app.NewCreator(f.repo, locations, s.credit, app.CreationOptions{AdmissionsEnabled: true, Now: s.clock.Now})
			slowClient := mount(t, f, slow)
			fastClient := mount(t, f, s.creator)
			in := input(s.clock.Now())
			type result struct {
				order *orderpb.Order
				err   error
			}
			finished := make(chan result, 1)
			go func() {
				o, err := create(slowClient, f.ctx, proto.Clone(in).(*orderpb.CreateOrderRequest))
				finished <- result{o, err}
			}()
			select {
			case <-locations.entered:
			case <-time.After(5 * time.Second):
				t.Fatal("preflight did not start")
			}
			fast := proto.Clone(in).(*orderpb.CreateOrderRequest)
			if changed {
				fast.ItemDescription = "Different accepted payload"
			}
			original, err := create(fastClient, f.ctx, fast)
			close(locations.release)
			if err != nil {
				t.Fatal(err)
			}
			select {
			case response := <-finished:
				if changed {
					if connect.CodeOf(response.err) != connect.CodeAlreadyExists {
						t.Fatalf("changed race=%v", response.err)
					}
				} else if response.err != nil || !proto.Equal(response.order, original) {
					t.Fatalf("same race=%v err=%v", response.order, response.err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("preflight did not finish")
			}
			if f.count(t, "orders") != 1 {
				t.Fatal("preflight race admitted duplicates")
			}
		})
	}
	t.Run("same key has independent caller scopes", func(t *testing.T) {
		s := newScenario(t, f)
		key := uuid.NewString()
		ctx := clients.WithUserBearer(f.ctx, "Bearer user-token")
		in := app.CreateInput{Key: key, PickupID: pickupID, DeliveryID: deliveryID, ItemDescription: "Book", RewardCredits: 10, DeliveryDuration: 10 * time.Minute, ExpiresAt: s.clock.Now().Add(time.Hour)}
		first, err := s.creator.Create(ctx, app.Caller{ID: requester}, in)
		if err != nil {
			t.Fatal(err)
		}
		second, err := s.creator.Create(ctx, app.Caller{ID: uuid.NewString()}, in)
		if err != nil || first.ID == second.ID || f.count(t, "order_creation_keys") != 2 {
			t.Fatalf("caller scope second=%+v err=%v", second, err)
		}
	})
}
