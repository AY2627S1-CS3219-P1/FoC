//go:build integration

package creation_test

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/AY2627S1-CS3219-P1/FoC/order-service/internal/clients"
	app "github.com/AY2627S1-CS3219-P1/FoC/order-service/internal/order"
	"github.com/AY2627S1-CS3219-P1/FoC/order-service/internal/router"
	orderrpc "github.com/AY2627S1-CS3219-P1/FoC/order-service/internal/rpc/order"
	adminrpc "github.com/AY2627S1-CS3219-P1/FoC/order-service/internal/rpc/orderadmin"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/auth"
	creditpb "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/credit/v1"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/credit/v1/creditv1connect"
	orderpb "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/order/v1"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/order/v1/orderv1connect"
	locationpb "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/supplier/location/v1"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/supplier/location/v1/locationv1connect"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const requester = "c55ce32c-2af7-46d1-a088-6a976d3bdbaf"
const pickupID = "41dbf670-f32c-4cca-9473-5861c274e789"
const deliveryID = "37dbf670-f32c-4cca-9473-5861c274e789"

type clock struct{ nanos atomic.Int64 }

func newClock() *clock {
	c := new(clock)
	c.nanos.Store(time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC).UnixNano())
	return c
}

func (c *clock) Now() time.Time { return time.Unix(0, c.nanos.Load()).UTC() }

func (c *clock) Advance(d time.Duration) { c.nanos.Add(int64(d)) }

type fixture struct {
	pool *pgxpool.Pool
	repo *app.PostgresRepository
	ctx  context.Context
}

func databaseFixture(t *testing.T) *fixture {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	t.Cleanup(cancel)
	container, err := postgres.Run(ctx, "postgres:18", postgres.WithDatabase("order_creation_test"), postgres.WithUsername("order"), postgres.WithPassword("order"), postgres.BasicWaitStrategies())
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
	config, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatal(err)
	}
	config.MaxConns = 12
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	sqlDB, err := sql.Open("pgx", url)
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	provider, err := goose.NewProvider(goose.DialectPostgres, sqlDB, os.DirFS("../../database/schema"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.Up(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := provider.DownTo(ctx, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := provider.Up(ctx); err != nil {
		t.Fatal(err)
	}
	return &fixture{pool: pool, repo: app.NewPostgresRepository(pool), ctx: ctx}
}

func (f *fixture) reset(t *testing.T) {
	t.Helper()
	if _, err := f.pool.Exec(f.ctx, "TRUNCATE order_outbox,order_audit,offers,orders CASCADE"); err != nil {
		t.Fatal(err)
	}
}

// These scripted Connect servers exercise Order's real adapters. They are test
// fixtures, not an implementation or conformance claim for the Credit service.
type remotes struct {
	creditv1connect.UnimplementedCreditReservationServiceHandler
	locationv1connect.UnimplementedLocationDiscoveryServiceHandler
	t                                                                                                  *testing.T
	mu                                                                                                 sync.Mutex
	reservations                                                                                       map[string]*creditpb.Reservation
	original                                                                                           map[string]*creditpb.Reservation
	reserveCalls, releaseCalls, getCalls, holds, locationCalls                                         int
	unknownReserve, unknownAfterReserve, unknownGet, unknownRelease, refuse, archived, locationFailure bool
	onReserve                                                                                          func()
	onGet                                                                                              func(*creditpb.Reservation)
}

func newRemotes(t *testing.T) *remotes {
	return &remotes{t: t, reservations: map[string]*creditpb.Reservation{}, original: map[string]*creditpb.Reservation{}}
}

func businessError(code connect.Code, reason string) *connect.Error {
	e := connect.NewError(code, errors.New("private Credit detail"))
	detail, _ := connect.NewErrorDetail(&errdetails.ErrorInfo{Domain: "credit.v1", Reason: reason})
	e.AddDetail(detail)
	return e
}

func (r *remotes) checkCreditBearer(value string) {
	if value != "Bearer service-credit-token" {
		r.t.Errorf("incorrect Credit credential separation")
	}
}

func (r *remotes) Reserve(_ context.Context, req *connect.Request[creditpb.ReserveRequest]) (*connect.Response[creditpb.ReserveResponse], error) {
	r.checkCreditBearer(req.Header().Get("Authorization"))
	r.mu.Lock()
	r.reserveCalls++
	hook := r.onReserve
	r.mu.Unlock()
	if hook != nil {
		hook()
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.unknownReserve {
		return nil, connect.NewError(connect.CodeDeadlineExceeded, errors.New("unknown"))
	}
	if r.refuse {
		return nil, businessError(connect.CodeFailedPrecondition, "INSUFFICIENT_CREDITS")
	}
	id := req.Msg.RequestId
	if original := r.original[id]; original != nil {
		return connect.NewResponse(&creditpb.ReserveResponse{Reservation: proto.Clone(original).(*creditpb.Reservation)}), nil
	}
	if r.reservations[id] != nil && r.reservations[id].Status == creditpb.ReservationStatus_RESERVATION_STATUS_RELEASED {
		return nil, businessError(connect.CodeFailedPrecondition, "RESERVATION_RELEASED")
	}
	value := &creditpb.Reservation{RequestId: id, RequesterId: req.Msg.RequesterId, Status: creditpb.ReservationStatus_RESERVATION_STATUS_HELD, OriginalAmount: req.Msg.Amount, ReservedAmount: req.Msg.Amount}
	r.reservations[id] = value
	r.original[id] = proto.Clone(value).(*creditpb.Reservation)
	r.holds++
	if r.unknownAfterReserve {
		return nil, connect.NewError(connect.CodeDeadlineExceeded, errors.New("response lost after reserve commit"))
	}
	return connect.NewResponse(&creditpb.ReserveResponse{Reservation: proto.Clone(value).(*creditpb.Reservation)}), nil
}

func (r *remotes) GetReservation(_ context.Context, req *connect.Request[creditpb.GetReservationRequest]) (*connect.Response[creditpb.GetReservationResponse], error) {
	r.checkCreditBearer(req.Header().Get("Authorization"))
	r.mu.Lock()
	r.getCalls++
	if r.unknownGet {
		r.mu.Unlock()
		return nil, businessError(connect.CodeNotFound, "RESERVATION_NOT_FOUND")
	}
	value := r.reservations[req.Msg.RequestId]
	if value == nil {
		r.mu.Unlock()
		return nil, businessError(connect.CodeNotFound, "RESERVATION_NOT_FOUND")
	}
	snapshot := proto.Clone(value).(*creditpb.Reservation)
	hook := r.onGet
	r.mu.Unlock()
	if hook != nil {
		hook(snapshot)
	}
	return connect.NewResponse(&creditpb.GetReservationResponse{Reservation: snapshot}), nil
}

func (r *remotes) ReleaseReservation(_ context.Context, req *connect.Request[creditpb.ReleaseReservationRequest]) (*connect.Response[creditpb.ReleaseReservationResponse], error) {
	r.checkCreditBearer(req.Header().Get("Authorization"))
	if req.Msg.Reason != creditpb.ReleaseReason_RELEASE_REASON_EXPIRED {
		r.t.Error("incorrect release reason")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.releaseCalls++
	if r.unknownRelease {
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("unknown release"))
	}
	value := r.reservations[req.Msg.RequestId]
	if value == nil {
		value = &creditpb.Reservation{RequestId: req.Msg.RequestId}
		r.reservations[req.Msg.RequestId] = value
	}
	value.Status = creditpb.ReservationStatus_RESERVATION_STATUS_RELEASED
	value.ReservedAmount = 0
	value.ActiveAdjustment = nil
	return connect.NewResponse(&creditpb.ReleaseReservationResponse{Reservation: proto.Clone(value).(*creditpb.Reservation)}), nil
}

func (r *remotes) GetLocation(_ context.Context, req *connect.Request[locationpb.GetLocationRequest]) (*connect.Response[locationpb.GetLocationResponse], error) {
	if req.Header().Get("Authorization") != "Bearer user-token" {
		r.t.Error("Supplier did not receive delegated user credential")
	}
	r.mu.Lock()
	r.locationCalls++
	archived, failed := r.archived, r.locationFailure
	r.mu.Unlock()
	if failed {
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("Supplier down"))
	}
	loc := &locationpb.Location{Id: req.Msg.Id, Name: "Campus location", Building: &locationpb.Building{Name: "COM1"}}
	if archived {
		loc.ArchivedAt = timestamppb.Now()
	}
	return connect.NewResponse(&locationpb.GetLocationResponse{Location: loc}), nil
}

func (r *remotes) adapters(t *testing.T) (*clients.Supplier, *clients.Credit) {
	t.Helper()
	mux := http.NewServeMux()
	cp, ch := creditv1connect.NewCreditReservationServiceHandler(r)
	mux.Handle(cp, ch)
	lp, lh := locationv1connect.NewLocationDiscoveryServiceHandler(r)
	mux.Handle(lp, lh)
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	file := filepath.Join(t.TempDir(), "credit-token")
	if err := os.WriteFile(file, []byte("service-credit-token"), 0600); err != nil {
		t.Fatal(err)
	}
	return &clients.Supplier{Client: locationv1connect.NewLocationDiscoveryServiceClient(server.Client(), server.URL)}, &clients.Credit{Client: creditv1connect.NewCreditReservationServiceClient(server.Client(), server.URL), Tokens: fixtureTokens("service-credit-token")}
}

func mount(t *testing.T, f *fixture, creator *app.Creator) orderv1connect.OrderServiceClient {
	t.Helper()
	return mountImages(t, f, creator, nil, app.Caller{ID: requester})
}

func mountImages(t *testing.T, f *fixture, creator *app.Creator, images *app.ItemImages, caller app.Caller) orderv1connect.OrderServiceClient {
	t.Helper()
	reads := app.NewService(f.repo)
	var imageOperations orderrpc.ImageOperations
	if images != nil {
		imageOperations = images
	}
	authenticate := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Authorization") != "Bearer user-token" {
				http.Error(w, "unauthenticated", http.StatusUnauthorized)
				return
			}
			r = r.WithContext(auth.WithCaller(r.Context(), auth.Caller{ID: caller.ID, Role: callerRole(caller), Admin: caller.Admin}))
			next.ServeHTTP(w, r)
		})
	}
	server := httptest.NewServer(router.Setup(router.RPCServices{Health: &orderv1connect.UnimplementedHealthServiceHandler{}, Order: orderrpc.NewServer(reads, creator, imageOperations), Admin: adminrpc.NewServer(reads, imageOperations)}, authenticate))
	t.Cleanup(server.Close)
	return orderv1connect.NewOrderServiceClient(server.Client(), server.URL)
}

func input(now time.Time) *orderpb.CreateOrderRequest {
	return &orderpb.CreateOrderRequest{
		PickupLocationId: pickupID, Delivery: &orderpb.DeliveryInput{LocationId: deliveryID, Instructions: proto.String("Call outside")},
		ItemDescription: "Book", RewardCredits: 10, DeliveryDuration: durationpb.New(10 * time.Minute), ExpiresAt: timestamppb.New(now.Add(time.Hour)), IdempotencyKey: uuid.NewString(),
	}
}

func create(c orderv1connect.OrderServiceClient, ctx context.Context, in *orderpb.CreateOrderRequest) (*orderpb.Order, error) {
	request := connect.NewRequest(in)
	request.Header().Set("Authorization", "Bearer user-token")
	response, err := c.CreateOrder(ctx, request)
	if err != nil {
		return nil, err
	}
	return response.Msg.Order, nil
}

func get(t *testing.T, c orderv1connect.OrderServiceClient, id string) *orderpb.Order {
	t.Helper()
	request := connect.NewRequest(&orderpb.GetOrderRequest{Id: id})
	request.Header().Set("Authorization", "Bearer user-token")
	response, err := c.GetOrder(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	return response.Msg.Order
}

func (f *fixture) onlyOrder(t *testing.T) string {
	t.Helper()
	var id string
	if err := f.pool.QueryRow(f.ctx, "SELECT id::text FROM orders").Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func (f *fixture) count(t *testing.T, table string) int {
	t.Helper()
	var count int
	// Table names are test constants, never input from an RPC.
	if err := f.pool.QueryRow(f.ctx, "SELECT count(*) FROM "+table).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

type fixtureTokens string

func (t fixtureTokens) Token(context.Context) (string, error) { return string(t), nil }

func callerRole(c app.Caller) string {
	if c.Suspended {
		return "suspended_user"
	}
	return "user"
}
