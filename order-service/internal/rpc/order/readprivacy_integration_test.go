//go:build integration

package order_test

import (
	"connectrpc.com/connect"
	"context"
	"database/sql"
	app "github.com/AY2627S1-CS3219-P1/FoC/order-service/internal/order"
	"github.com/AY2627S1-CS3219-P1/FoC/order-service/internal/router"
	rpcorder "github.com/AY2627S1-CS3219-P1/FoC/order-service/internal/rpc/order"
	"github.com/AY2627S1-CS3219-P1/FoC/order-service/internal/rpc/orderadmin"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/auth"
	pb "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/order/v1"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/order/v1/orderv1connect"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"google.golang.org/protobuf/proto"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"
)

// R1.8/U6.7/NFR-06.2: exercise real Apply -> PostgreSQL -> mounted Connect
// service -> generated client after withdrawal, reassignment and completion.
// Identity is injected at the authentication seam; S3/Credit/Kafka are not mocked
// as passing services and are not certified by this read-path test.
func TestReadPrivacyThroughConnectAndPostgres(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		container, err := postgres.Run(ctx, "postgres:18", postgres.WithDatabase("f0_read_test"), postgres.WithUsername("order"), postgres.WithPassword("order"), postgres.BasicWaitStrategies())
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := container.Terminate(context.Background()); err != nil {
				t.Error(err)
			}
		})
		url, err = container.ConnectionString(ctx, "sslmode=disable")
		if err != nil {
			t.Fatal(err)
		}
	}
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	var version int
	if err = pool.QueryRow(ctx, "SELECT current_setting('server_version_num')::integer").Scan(&version); err != nil || version < 180000 || version >= 190000 {
		t.Fatalf("requires disposable PostgreSQL18: %d %v", version, err)
	}
	if _, err = pool.Exec(ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
		t.Fatal(err)
	}
	sqlDB, err := sql.Open("pgx", url)
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	provider, err := goose.NewProvider(goose.DialectPostgres, sqlDB, os.DirFS("../../../database/schema"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = provider.Up(ctx); err != nil {
		t.Fatal(err)
	}
	repo := app.NewPostgresRepository(pool)
	requester, former, current := uuid.NewString(), uuid.NewString(), uuid.NewString()
	now := time.Now().UTC().Truncate(time.Microsecond)
	secret := "Call Room 02-15"
	o := app.Order{ID: uuid.NewString(), RequesterID: requester, Status: app.PendingReservation, Pickup: app.LocationSnapshot{LocationID: uuid.NewString(), Name: "Campus Cafe", BuildingName: "COM2"}, Delivery: app.LocationSnapshot{LocationID: uuid.NewString(), Name: "Room 02-15", BuildingName: "COM3"}, DeliveryInstructions: &secret, ItemDescription: "Two meals", RewardCredits: 20, ReservedCredits: 20, DeliveryDuration: 30 * time.Minute, ExpiresAt: now.Add(time.Hour), Revision: 1, CreatedAt: now, UpdatedAt: now}
	created, err := app.NewChange(nil, o, app.Actor{Kind: app.User, ID: requester}, app.Create, nil, uuid.NewString)
	if err != nil {
		t.Fatal(err)
	}
	if err = repo.Within(ctx, func(tx app.Tx) error { return tx.Apply(ctx, created) }); err != nil {
		t.Fatal(err)
	}
	step := func(action app.Action, actor app.Actor) {
		t.Helper()
		err := app.NewMutator(repo).Mutate(ctx, o.ID, func(before app.Order, _ []app.Offer) (*app.Change, error) {
			status, repeat, err := app.Transition(before, action, actor)
			if err != nil || repeat {
				return nil, err
			}
			after := before
			after.Status = status
			after.Revision++
			after.UpdatedAt = before.UpdatedAt.Add(time.Second)
			switch action {
			case app.Assign:
				after.CourierID = &actor.ID
				after.AssignedAt = &after.UpdatedAt
			case app.Withdraw:
				after.CourierID = nil
				after.AssignedAt = nil
			case app.Pickup:
				after.PickedUpAt = &after.UpdatedAt
			case app.Deliver:
				after.DeliveredAt = &after.UpdatedAt
			case app.ConfirmReceipt:
				after.CompletedAt = &after.UpdatedAt
			}
			c, err := app.NewChange(&before, after, actor, action, nil, uuid.NewString)
			return &c, err
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	step(app.ConfirmReservation, app.Actor{Kind: app.System})
	authenticate := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			role := r.Header.Get("X-Test-Role")
			r = r.WithContext(auth.WithCaller(r.Context(), auth.Caller{ID: r.Header.Get("X-Test-User"), Role: role, Admin: role == "admin"}))
			next.ServeHTTP(w, r)
		})
	}
	service := app.NewService(repo)
	images := app.NewItemImages(repo, nil)
	server := httptest.NewServer(router.Setup(router.RPCServices{Health: &orderv1connect.UnimplementedHealthServiceHandler{}, Order: rpcorder.NewServer(service, nil, images), Admin: orderadmin.NewServer(service, images)}, authenticate))
	defer server.Close()
	client := orderv1connect.NewOrderServiceClient(server.Client(), server.URL)
	caller := func(id, role string) *connect.Request[pb.GetOrderRequest] {
		r := connect.NewRequest(&pb.GetOrderRequest{Id: o.ID})
		r.Header().Set("X-Test-User", id)
		r.Header().Set("X-Test-Role", role)
		return r
	}
	browser := uuid.NewString()
	open := connect.NewRequest(&pb.ListOpenOrdersRequest{DeliveryBuildingName: "COM3", PageSize: 1})
	open.Header().Set("X-Test-User", browser)
	open.Header().Set("X-Test-Role", "user")
	listed, err := client.ListOpenOrders(ctx, open)
	if err != nil || listed.Msg.PageInfo.TotalItems != 1 || listed.Msg.Orders[0].Delivery != nil || listed.Msg.Orders[0].DeliveryBuildingName != "COM3" {
		t.Fatalf("coarse RPC: %v %v", listed, err)
	}
	//nolint:staticcheck // Exercise rejection of the retained legacy wire field.
	legacy := connect.NewRequest(&pb.ListOpenOrdersRequest{DeliveryLocationId: proto.String(o.Delivery.LocationID)})
	legacy.Header().Set("X-Test-User", browser)
	legacy.Header().Set("X-Test-Role", "user")
	if _, err = client.ListOpenOrders(ctx, legacy); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatal("legacy exact-filter inference", err)
	}
	step(app.Assign, app.Actor{Kind: app.User, ID: former})
	step(app.Withdraw, app.Actor{Kind: app.User, ID: former})
	step(app.Assign, app.Actor{Kind: app.User, ID: current})
	step(app.Pickup, app.Actor{Kind: app.User, ID: current})
	step(app.Deliver, app.Actor{Kind: app.User, ID: current})
	step(app.ConfirmReceipt, app.Actor{Kind: app.User, ID: requester})
	for _, c := range []struct{ id, role string }{{former, "suspended_user"}, {uuid.NewString(), "admin"}} {
		response, err := client.GetOrder(ctx, caller(c.id, c.role))
		if err != nil || response.Msg.Order.Delivery != nil || response.Msg.Order.DeliveryBuildingName != "COM3" || response.Msg.Order.Status != pb.OrderStatus_ORDER_STATUS_COMPLETED || len(response.Msg.Order.ItemImages) != 0 {
			t.Fatalf("redacted history/admin RPC: %v %v", response, err)
		}
	}
	for _, id := range []string{requester, current} {
		response, err := client.GetOrder(ctx, caller(id, "user"))
		if err != nil || response.Msg.Order.GetDelivery().GetLocation().GetLocationId() != o.Delivery.LocationID || response.Msg.Order.GetDelivery().GetInstructions() != secret {
			t.Fatalf("participant RPC: %v %v", response, err)
		}
	}
	history := connect.NewRequest(&pb.ListMyOrdersRequest{Role: pb.ParticipantRole_PARTICIPANT_ROLE_COURIER, PageSize: 1})
	history.Header().Set("X-Test-User", former)
	history.Header().Set("X-Test-Role", "suspended_user")
	page, err := client.ListMyOrders(ctx, history)
	if err != nil || page.Msg.PageInfo.TotalItems != 1 || len(page.Msg.Orders) != 1 || page.Msg.Orders[0].Delivery != nil {
		t.Fatalf("history RPC: %v %v", page, err)
	}
	adminClient := orderv1connect.NewOrderAdminServiceClient(server.Client(), server.URL)
	adminHistory := connect.NewRequest(&pb.ListUserOrdersRequest{UserId: former, Role: pb.ParticipantRole_PARTICIPANT_ROLE_COURIER})
	adminHistory.Header().Set("X-Test-User", uuid.NewString())
	adminHistory.Header().Set("X-Test-Role", "admin")
	adminPage, err := adminClient.ListUserOrders(ctx, adminHistory)
	if err != nil || adminPage.Msg.PageInfo.TotalItems != 1 || adminPage.Msg.Orders[0].Delivery != nil {
		t.Fatalf("admin history RPC: %v %v", adminPage, err)
	}
	if _, err = client.GetOrder(ctx, caller(browser, "user")); connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatal("stranger saw completed order", err)
	}
	t.Log("PASS: real PostgreSQL18 Apply/history -> mounted Connect -> generated client; coarse browse, exact-filter rejection, withdrawal/reassignment/completion, suspended former history, admin redaction, current exact access and stranger denial")
}
