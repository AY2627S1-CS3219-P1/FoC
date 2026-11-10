package orderinternal_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/AY2627S1-CS3219-P1/FoC/order-service/database"
	app "github.com/AY2627S1-CS3219-P1/FoC/order-service/internal/order"
	"github.com/AY2627S1-CS3219-P1/FoC/order-service/internal/router"
	rpc "github.com/AY2627S1-CS3219-P1/FoC/order-service/internal/rpc/orderinternal"
	pb "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/order/v1"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/order/v1/orderv1connect"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type recoveryCredit struct {
	view      app.AssignmentReservation
	reversals int
}

func (c *recoveryCredit) Adjust(_ context.Context, in app.AdjustmentIntent) error {
	c.view.State, c.view.OfferID, c.view.CourierID, c.view.ReservedAmount = "assigned", in.OfferID, in.CourierID, in.Amount
	return nil
}

func (c *recoveryCredit) Revert(_ context.Context, orderID, assignmentID string) error {
	if c.view.OrderID != orderID || c.view.OfferID != assignmentID {
		return errors.New("wrong immutable reversal identity")
	}
	c.reversals++
	c.view.State, c.view.ReservedAmount, c.view.OfferID, c.view.CourierID = "held", c.view.OriginalAmount, "", ""
	return nil
}

func (c *recoveryCredit) AssignmentReservation(context.Context, string) (app.AssignmentReservation, error) {
	return c.view, nil
}

// Source: the approved suspension behavior requires durable lost-reply recovery,
// assignment-specific reversal, original-reward restoration, and stale-decision
// rejection after reinstatement. This uses actual HTTP, migrations, and Postgres.
// Credit is scripted. This does not certify the separate Istio identity boundary.
func TestSuspensionRPCDatabaseLostReplyAndReplacement(t *testing.T) {
	dsn := os.Getenv("SUSPENSION_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("SUSPENSION_TEST_DATABASE_URL must name a disposable PostgreSQL 18 database")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if _, err := pool.Exec(ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
		t.Fatal(err)
	}
	if err := database.Migrate(ctx, dsn); err != nil {
		t.Fatal(err)
	}
	repo := app.NewPostgresRepository(pool)
	now := time.Now().UTC().Truncate(time.Microsecond)
	o := app.Order{
		ID: uuid.NewString(), RequesterID: uuid.NewString(), Status: app.PendingReservation,
		Pickup:          app.LocationSnapshot{LocationID: uuid.NewString(), Name: "Cafe", BuildingName: "COM2"},
		Delivery:        app.LocationSnapshot{LocationID: uuid.NewString(), Name: "Lobby", BuildingName: "PGP"},
		ItemDescription: "Lunch", RewardCredits: 15, ReservedCredits: 15,
		DeliveryDuration: 15 * time.Minute, ExpiresAt: now.Add(time.Hour), Revision: 1, CreatedAt: now, UpdatedAt: now,
	}
	change, err := app.NewChange(nil, o, app.Actor{Kind: app.User, ID: o.RequesterID}, app.Create, nil, uuid.NewString)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.Within(ctx, func(tx app.Tx) error { return tx.Apply(ctx, change) }); err != nil {
		t.Fatal(err)
	}
	if err := app.NewMutator(repo).Mutate(ctx, o.ID, func(before app.Order, _ []app.Offer) (*app.Change, error) {
		after := before
		after.Status, after.Revision = app.AwaitingCourier, before.Revision+1
		change, err := app.NewChange(
			&before,
			after,
			app.Actor{Kind: app.System},
			app.ConfirmReservation,
			nil,
			uuid.NewString,
		)
		return &change, err
	}); err != nil {
		t.Fatal(err)
	}
	credit := &recoveryCredit{
		view: app.AssignmentReservation{
			OrderID:        o.ID,
			RequesterID:    o.RequesterID,
			State:          "held",
			OriginalAmount: 15,
			ReservedAmount: 15,
		},
	}
	options := app.AssignmentOptions{Now: func() time.Time { return now }}
	assigner := app.NewAssigner(repo, credit, options)
	courier := app.Caller{ID: uuid.NewString()}
	offer, err := assigner.MakeOffer(ctx, courier, o.ID, 35, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	assigned, err := assigner.AcceptOffer(ctx, app.Caller{ID: o.RequesterID}, offer.ID)
	if err != nil || assigned.Phase != app.AssignmentSucceeded {
		t.Fatalf("raised-reward assignment: %+v %v", assigned, err)
	}
	mount := func(loseReply bool) *httptest.Server {
		// Rebuild the coordinator/repository as on restart; only database state survives.
		service := &rpc.Server{Operations: app.NewAccountSuspender(app.NewPostgresRepository(pool), credit, options)}
		handler := router.Internal(router.RPCServices{
			Health: &orderv1connect.UnimplementedHealthServiceHandler{},
			Order:  &orderv1connect.UnimplementedOrderServiceHandler{},
			Admin:  &orderv1connect.UnimplementedOrderAdminServiceHandler{}, Suspension: service,
		}, func(h http.Handler) http.Handler { return h })
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if loseReply {
				recorder := httptest.NewRecorder()
				handler.ServeHTTP(recorder, r)
				if recorder.Code != http.StatusOK {
					t.Errorf("decision did not commit before reply loss: %d %s", recorder.Code, recorder.Body.String())
				}
				http.Error(w, "reply lost after commit", http.StatusServiceUnavailable)
				return
			}
			handler.ServeHTTP(w, r)
		}))
		t.Cleanup(server.Close)
		return server
	}
	message := &pb.ApplyAccountSuspensionRequest{
		DecisionId: uuid.NewString(),
		UserId:     courier.ID,
		AdminId:    uuid.NewString(),
		Revision:   1,
		Suspended:  true,
		Reason:     "explicit decision",
	}
	lost := mount(true)
	if _, err := orderv1connect.NewOrderInternalServiceClient(lost.Client(), lost.URL).
		ApplyAccountSuspension(ctx, connect.NewRequest(message)); err == nil {
		t.Fatal("lost acknowledgment reported success")
	}
	fenced, err := repo.Get(ctx, o.ID)
	if err != nil || fenced.PendingCancellationID == nil || !fenced.IsAssignedTo(courier.ID) {
		t.Fatalf("lost durable removal intent: %+v %v", fenced, err)
	}
	operationID := *fenced.PendingCancellationID
	if _, err := app.NewProgress(app.NewMutator(repo)).ConfirmPickup(ctx, courier, o.ID); err == nil {
		t.Fatal("stale-session pickup bypassed committed fence")
	}
	restarted := mount(false)
	client := orderv1connect.NewOrderInternalServiceClient(restarted.Client(), restarted.URL)
	for range 3 {
		if _, err := client.ApplyAccountSuspension(ctx, connect.NewRequest(message)); err != nil {
			t.Fatal(err)
		}
	}
	var operations int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM order_cancellations WHERE suspension_decision_id=$1", message.DecisionId).
		Scan(&operations); err != nil ||
		operations != 1 ||
		credit.reversals != 0 {
		t.Fatalf("replay duplicated work: %d reversals=%d %v", operations, credit.reversals, err)
	}
	if err := app.NewCanceller(app.NewPostgresRepository(pool), credit, options).
		ProcessCancellation(ctx, operationID); err != nil {
		t.Fatal(err)
	}
	recovered, err := repo.Get(ctx, o.ID)
	if err != nil || recovered.Status != app.AwaitingCourier || recovered.CourierID != nil ||
		recovered.RewardCredits != 15 ||
		recovered.ReservedCredits != 15 ||
		!recovered.ExpiresAt.Equal(o.ExpiresAt) ||
		credit.reversals != 1 {
		t.Fatalf("incorrect financial recovery: %+v reversals=%d %v", recovered, credit.reversals, err)
	}
	reinstatement := &pb.ApplyAccountSuspensionRequest{
		DecisionId: uuid.NewString(),
		UserId:     courier.ID,
		AdminId:    message.AdminId,
		Revision:   2,
		Suspended:  false,
		Reason:     "appeal granted",
	}
	if _, err := client.ApplyAccountSuspension(ctx, connect.NewRequest(reinstatement)); err != nil {
		t.Fatal(err)
	}
	replacement := app.Caller{ID: uuid.NewString()}
	next, err := assigner.AcceptOrder(ctx, replacement, o.ID, uuid.NewString())
	if err != nil || next.Phase != app.AssignmentSucceeded {
		t.Fatalf("replacement: %+v %v", next, err)
	}
	if _, err := client.ApplyAccountSuspension(
		ctx,
		connect.NewRequest(message),
	); connect.CodeOf(
		err,
	) != connect.CodeAborted {
		t.Fatalf("stale decision acknowledged: %v", err)
	}
	final, err := repo.Get(ctx, o.ID)
	if err != nil || !final.IsAssignedTo(replacement.ID) || final.Revision != next.Order.Revision ||
		credit.reversals != 1 {
		t.Fatalf("stale delivery changed replacement: %+v reversals=%d %v", final, credit.reversals, err)
	}
	t.Log(
		"PASS lost acknowledgment -> reconstructed coordinator -> one durable removal -> original reward restored -> replacement preserved against stale replay",
	)
}
