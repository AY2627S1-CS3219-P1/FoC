//go:build integration

package order_test

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"
	app "github.com/AY2627S1-CS3219-P1/FoC/order-service/internal/order"
	"github.com/AY2627S1-CS3219-P1/FoC/order-service/internal/router"
	rpcorder "github.com/AY2627S1-CS3219-P1/FoC/order-service/internal/rpc/order"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/auth"
	pb "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/order/v1"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/order/v1/orderv1connect"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
)

// These are scripted Credit contract responses, not a ledger implementation.
// Mutations and observation are separate so a lost observation can leave a
// durable pending attempt after the script has applied its financial intent.
type mountedAcceptanceCredit struct {
	mu                sync.Mutex
	views             map[string]app.AssignmentReservation
	applied, reverted map[string]bool
	available         int64
	loseObservation   bool
}

func (c *mountedAcceptanceCredit) Adjust(_ context.Context, in app.AdjustmentIntent) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.reverted[in.OfferID] {
		return app.ErrUnavailable
	}
	if c.applied[in.OfferID] {
		return nil
	}
	v := c.views[in.OrderID]
	increase := in.Amount - v.ReservedAmount
	if increase > c.available {
		return app.ErrCreditInsufficient
	}
	c.available -= increase
	v.State, v.OfferID, v.CourierID, v.ReservedAmount = "assigned", in.OfferID, in.CourierID, in.Amount
	c.views[in.OrderID] = v
	c.applied[in.OfferID] = true
	return nil
}

func (c *mountedAcceptanceCredit) Revert(_ context.Context, orderID, attemptID string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.reverted[attemptID] = true
	v := c.views[orderID]
	if v.State == "assigned" && v.OfferID == attemptID {
		c.available += v.ReservedAmount - v.OriginalAmount
		v.State, v.OfferID, v.CourierID, v.ReservedAmount = "held", "", "", v.OriginalAmount
		c.views[orderID] = v
	}
	return nil
}

func (c *mountedAcceptanceCredit) AssignmentReservation(
	_ context.Context,
	orderID string,
) (app.AssignmentReservation, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.loseObservation {
		c.loseObservation = false
		return app.AssignmentReservation{}, app.ErrUnavailable
	}
	return c.views[orderID], nil
}

// Drain a real response only after the mounted handler has completed, then
// simulate loss at the client transport. The next send uses the original key.
type loseFirstAcceptance struct {
	base http.RoundTripper
	lost atomic.Bool
}

func (r *loseFirstAcceptance) RoundTrip(req *http.Request) (*http.Response, error) {
	response, err := r.base.RoundTrip(req)
	if err == nil && req.URL.Path == orderv1connect.OrderServiceAcceptOrderProcedure &&
		r.lost.CompareAndSwap(false, true) {
		_, _ = io.Copy(io.Discard, response.Body)
		_ = response.Body.Close()
		return nil, errors.New("scripted response loss after acceptance handler completed")
	}
	return response, err
}

func acceptancePostgres(t *testing.T, ctx context.Context) *pgxpool.Pool {
	t.Helper()
	// Always allocate a new database, never honor a shared TEST_DATABASE_URL.
	container, err := postgres.Run(
		ctx,
		"postgres:18",
		postgres.WithDatabase("acceptance_rpc"),
		postgres.WithUsername("order"),
		postgres.WithPassword("order"),
		postgres.BasicWaitStrategies(),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := container.Terminate(context.Background()); err != nil {
			t.Error(err)
		}
	})
	url, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	var version int
	if err = pool.QueryRow(ctx, "SELECT current_setting('server_version_num')::integer").
		Scan(&version); err != nil || version < 180000 ||
		version >= 190000 {
		t.Fatalf("requires isolated PostgreSQL18: %d %v", version, err)
	}
	db, err := sql.Open("pgx", url)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	provider, err := goose.NewProvider(goose.DialectPostgres, db, os.DirFS("../../../database/schema"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = provider.Up(ctx); err != nil {
		t.Fatal(err)
	}
	return pool
}

func seedAcceptanceOrder(
	t *testing.T,
	ctx context.Context,
	repo *app.PostgresRepository,
	requester string,
	now time.Time,
) app.Order {
	t.Helper()
	secret := "Call private room 02-15"
	o := app.Order{
		ID:          uuid.NewString(),
		RequesterID: requester,
		Status:      app.PendingReservation,
		Pickup:      app.LocationSnapshot{LocationID: uuid.NewString(), Name: "Cafe", BuildingName: "COM2"},
		Delivery: app.LocationSnapshot{
			LocationID:   uuid.NewString(),
			Name:         "Private room",
			BuildingName: "COM3",
		},
		DeliveryInstructions: &secret,
		ItemDescription:      "Two meals",
		RewardCredits:        20,
		ReservedCredits:      20,
		DeliveryDuration:     30 * time.Minute,
		ExpiresAt:            now.Add(time.Hour),
		Revision:             1,
		CreatedAt:            now,
		UpdatedAt:            now,
	}
	change, err := app.NewChange(nil, o, app.Actor{Kind: app.User, ID: requester}, app.Create, nil, uuid.NewString)
	if err != nil {
		t.Fatal(err)
	}
	if err = repo.Within(ctx, func(tx app.Tx) error { return tx.Apply(ctx, change) }); err != nil {
		t.Fatal(err)
	}
	var after app.Order
	err = app.NewMutator(repo).Mutate(ctx, o.ID, func(before app.Order, _ []app.Offer) (*app.Change, error) {
		after = before
		after.Status, after.Revision, after.UpdatedAt = app.AwaitingCourier, before.Revision+1, now.Add(time.Second)
		change, err := app.NewChange(
			&before,
			after,
			app.Actor{Kind: app.System},
			app.ConfirmReservation,
			nil,
			uuid.NewString,
		)
		return &change, err
	})
	if err != nil {
		t.Fatal(err)
	}
	return after
}

func acceptanceRequest[T any](message *T, caller, role string) *connect.Request[T] {
	request := connect.NewRequest(message)
	request.Header().Set("X-Test-User", caller)
	request.Header().Set("X-Test-Role", role)
	return request
}

func acceptanceRejection(t *testing.T, err error) *pb.AssignmentAttemptResult {
	t.Helper()
	var failure *connect.Error
	if !errors.As(err, &failure) || failure.Code() != connect.CodeFailedPrecondition {
		t.Fatalf("expected typed failed_precondition: %v", err)
	}
	for _, detail := range failure.Details() {
		value, decodeErr := detail.Value()
		if decodeErr != nil {
			t.Fatal(decodeErr)
		}
		if attempt, ok := value.(*pb.AssignmentAttemptResult); ok &&
			attempt.Status == pb.AssignmentAttemptStatus_ASSIGNMENT_ATTEMPT_STATUS_REJECTED {
			return attempt
		}
	}
	t.Fatal("missing retained rejected attempt detail")
	return nil
}

// Source: immutable successful/rejected UUID retention, current caller-safe
// history, R1.2 acceptance, R1.6 counteroffers, and suspension denying mutation
// without changing the outcome of an earlier admitted financial attempt.
func TestAcceptanceThroughConnectAndPostgres(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	pool := acceptancePostgres(t, ctx)
	repo := app.NewPostgresRepository(pool)
	base := time.Now().UTC().Truncate(time.Microsecond)
	requester, courier, other := uuid.NewString(), uuid.NewString(), uuid.NewString()
	first := seedAcceptanceOrder(t, ctx, repo, requester, base)
	second := seedAcceptanceOrder(t, ctx, repo, requester, base)
	clock := atomic.Int64{}
	clock.Store(base.Add(5 * time.Second).UnixNano())
	now := func() time.Time { return time.Unix(0, clock.Load()).UTC() }
	credit := &mountedAcceptanceCredit{
		views:     map[string]app.AssignmentReservation{},
		applied:   map[string]bool{},
		reverted:  map[string]bool{},
		available: 30,
	}
	for _, o := range []app.Order{first, second} {
		credit.views[o.ID] = app.AssignmentReservation{
			OrderID:        o.ID,
			RequesterID:    requester,
			OriginalAmount: 20,
			ReservedAmount: 20,
			State:          "held",
		}
	}
	assigner := app.NewAssigner(repo, credit, app.AssignmentOptions{Now: now})
	authenticate := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(
				w,
				r.WithContext(
					auth.WithCaller(
						r.Context(),
						auth.Caller{ID: r.Header.Get("X-Test-User"), Role: r.Header.Get("X-Test-Role")},
					),
				),
			)
		})
	}
	server := httptest.NewServer(
		router.Setup(
			router.RPCServices{
				Health: &orderv1connect.UnimplementedHealthServiceHandler{},
				Order:  rpcorder.NewServer(app.NewService(repo), nil, app.NewItemImages(repo, nil), assigner),
				Admin:  &orderv1connect.UnimplementedOrderAdminServiceHandler{},
			},
			authenticate,
		),
	)
	t.Cleanup(server.Close)
	transport := &loseFirstAcceptance{base: server.Client().Transport}
	client := orderv1connect.NewOrderServiceClient(&http.Client{Transport: transport}, server.URL)
	key := uuid.NewString()
	accept := func(id, attempt, actor, role string) (*connect.Response[pb.AcceptOrderResponse], error) {
		return client.AcceptOrder(
			ctx,
			acceptanceRequest(&pb.AcceptOrderRequest{Id: id, AttemptId: attempt}, actor, role),
		)
	}
	if _, err := accept(first.ID, key, courier, "user"); err == nil {
		t.Fatal("response loss was not injected")
	}
	var storedID string
	if err := pool.QueryRow(ctx, "SELECT id::text FROM order_assignment_attempts WHERE idempotency_key=$1 AND phase='succeeded'", key).
		Scan(&storedID); err != nil {
		t.Fatal("lost response did not follow committed assignment", err)
	}
	if _, err := accept(first.ID, key, courier, "suspended_user"); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("suspended replay: %v", err)
	}
	replay, err := accept(first.ID, key, courier, "user")
	if err != nil {
		t.Fatal(err)
	}
	if replay.Msg.Attempt.AttemptId != storedID ||
		replay.Msg.Attempt.Status != pb.AssignmentAttemptStatus_ASSIGNMENT_ATTEMPT_STATUS_SUCCEEDED ||
		replay.Msg.Order.CourierId == nil ||
		*replay.Msg.Order.CourierId != courier ||
		replay.Msg.Order.Delivery == nil {
		t.Fatalf("active replay: %v", replay.Msg)
	}
	losingKey := uuid.NewString()
	_, err = accept(first.ID, losingKey, other, "user")
	if connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatalf("private fresh acceptance: %v", err)
	}
	// Exercise the existing aggregate removal hook, not an unimplemented
	// withdrawal RPC or a real cross-service compensation workflow.
	clock.Add(int64(time.Second))
	if err := credit.Revert(ctx, first.ID, storedID); err != nil {
		t.Fatal(err)
	}
	err = app.NewMutator(repo).Mutate(ctx, first.ID, func(before app.Order, _ []app.Offer) (*app.Change, error) {
		after := before
		after.Status, after.CourierID, after.AssignedAt = app.AwaitingCourier, nil, nil
		after.Revision++
		after.UpdatedAt = now()
		change, err := app.NewChange(
			&before,
			after,
			app.Actor{Kind: app.User, ID: courier},
			app.Withdraw,
			nil,
			uuid.NewString,
		)
		return &change, err
	})
	if err != nil {
		t.Fatal(err)
	}
	clock.Add(int64(time.Second))
	if _, err = accept(first.ID, uuid.NewString(), other, "user"); err != nil {
		t.Fatal(err)
	}
	historical, err := accept(first.ID, key, courier, "user")
	if err != nil {
		t.Fatal(err)
	}
	if historical.Msg.Attempt.AttemptId != storedID ||
		historical.Msg.Attempt.Status != pb.AssignmentAttemptStatus_ASSIGNMENT_ATTEMPT_STATUS_SUCCEEDED ||
		historical.Msg.Order == nil ||
		historical.Msg.Order.GetCourierId() != other ||
		historical.Msg.Order.Delivery != nil ||
		historical.Msg.Order.DeliveryBuildingName != "COM3" {
		t.Fatalf("historical outcome exposed stale/private current assignment: %v", historical.Msg)
	}
	var episodes int
	if err = pool.QueryRow(ctx, "SELECT count(*) FROM order_assignment_history WHERE order_id=$1", first.ID).
		Scan(&episodes); err != nil ||
		episodes != 2 {
		t.Fatalf("atomic history episodes=%d err=%v", episodes, err)
	}
	proposal, err := client.MakeOffer(
		ctx,
		acceptanceRequest(
			&pb.MakeOfferRequest{OrderId: second.ID, Credits: 60, IdempotencyKey: uuid.NewString()},
			courier,
			"user",
		),
	)
	if err != nil {
		t.Fatal(err)
	}
	offerID := proposal.Msg.Offer.Id
	counter := func() (*connect.Response[pb.AcceptOfferResponse], error) {
		return client.AcceptOffer(ctx, acceptanceRequest(&pb.AcceptOfferRequest{Id: offerID}, requester, "user"))
	}
	_, err = counter()
	refused := acceptanceRejection(t, err)
	if refused.Rejection != "insufficient_credits" {
		t.Fatalf("unexpected rejection: %v", refused)
	}
	var offerState string
	if err = pool.QueryRow(ctx, "SELECT status FROM offers WHERE id=$1", offerID).
		Scan(&offerState); err != nil ||
		offerState != "open" {
		t.Fatalf("refusal offer=%s err=%v", offerState, err)
	}
	credit.mu.Lock()
	held := credit.views[second.ID]
	available := credit.available
	fenced := credit.reverted[refused.AttemptId]
	credit.available = 40
	credit.loseObservation = true
	credit.mu.Unlock()
	if held.State != "held" || held.ReservedAmount != 20 || available != 30 || !fenced {
		t.Fatalf("refusal changed baseline or lacked fence: %+v available=%d fence=%v", held, available, fenced)
	}
	pending, err := counter()
	if err != nil {
		t.Fatal(err)
	}
	if pending.Msg.Attempt.Status != pb.AssignmentAttemptStatus_ASSIGNMENT_ATTEMPT_STATUS_PENDING ||
		pending.Msg.Attempt.AttemptId == refused.AttemptId ||
		pending.Msg.Order.Status != pb.OrderStatus_ORDER_STATUS_AWAITING_COURIER {
		t.Fatalf("pending misreported as assignment: %v", pending.Msg)
	}
	clock.Add(int64(30 * time.Second))
	settled, err := counter()
	if err != nil {
		t.Fatal(err)
	}
	if settled.Msg.Attempt.AttemptId != pending.Msg.Attempt.AttemptId ||
		settled.Msg.Attempt.Status != pb.AssignmentAttemptStatus_ASSIGNMENT_ATTEMPT_STATUS_SUCCEEDED ||
		settled.Msg.Order.RewardCredits != 60 ||
		settled.Msg.Offer.Id != offerID {
		t.Fatalf("counteroffer recovery: %v", settled.Msg)
	}
	credit.mu.Lock()
	applied := len(credit.applied)
	remaining := credit.available
	credit.mu.Unlock()
	if applied != 3 || remaining != 0 {
		t.Fatalf("scripted intent duplication: applied=%d remaining=%d", applied, remaining)
	}
	t.Log(
		"PASS: mounted generated Connect acceptance -> real PostgreSQL18 migrations00004/00005 + Apply/history; committed lost-response retry, suspended replay denied without changing retained success, private-order fresh-key admission rejection, redacted former-courier current state, insufficient-credit typed detail + fenced baseline, pending counteroffer and same-attempt recovery. Credit/auth/removal remain explicitly scripted seams.",
	)
}
