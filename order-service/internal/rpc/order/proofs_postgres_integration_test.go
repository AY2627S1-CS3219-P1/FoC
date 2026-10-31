//go:build integration

package order_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/AY2627S1-CS3219-P1/FoC/order-service/internal/images"
	app "github.com/AY2627S1-CS3219-P1/FoC/order-service/internal/order"
	"github.com/AY2627S1-CS3219-P1/FoC/order-service/internal/proofimage"
	"github.com/AY2627S1-CS3219-P1/FoC/order-service/internal/router"
	rpcorder "github.com/AY2627S1-CS3219-P1/FoC/order-service/internal/rpc/order"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/auth"
	pb "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/order/v1"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/order/v1/orderv1connect"
	"github.com/google/uuid"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

type loseProofAcknowledgment struct {
	base          http.RoundTripper
	enabled, lost atomic.Bool
}

func (r *loseProofAcknowledgment) RoundTrip(request *http.Request) (*http.Response, error) {
	response, err := r.base.RoundTrip(request)
	if err == nil && r.enabled.Load() && response.StatusCode == 200 &&
		request.URL.Path == orderv1connect.OrderServiceConfirmDeliveryProcedure &&
		r.lost.CompareAndSwap(false, true) {
		_, _ = io.Copy(io.Discard, response.Body)
		_ = response.Body.Close()
		return nil, errors.New("scripted acknowledgment loss after delivery commit")
	}
	return response, err
}

// Approved sources: immutable retry identity, actual pixels, on-time durable
// receipt/recovery, accepted replay, effective delivery time and participant-only
// evidence. PostgreSQL18, signed/auth-enforced S3 and ImageMagick are real.
// User identity and Credit are explicit external seams, not live dependencies.
func TestProofsThroughConnectPostgresAndS3(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	t.Setenv("AWS_ACCESS_KEY_ID", "test")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test")
	t.Setenv("AWS_SESSION_TOKEN", "")
	t.Setenv("AWS_EC2_METADATA_DISABLED", "true")
	policy, err := filepath.Abs("../../../config/imagemagick")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("MAGICK_CONFIGURE_PATH", policy)
	decoder := proofimage.NewDecoder("")
	if err = decoder.CheckCapabilities(ctx); err != nil {
		t.Fatal("all five actual codecs are required, no skip:", err)
	}
	pool := acceptancePostgres(t, ctx)
	repo := app.NewPostgresRepository(pool)
	container, err := testcontainers.Run(
		ctx,
		"floci/floci:2.3.0",
		testcontainers.WithExposedPorts("4566/tcp"),
		testcontainers.WithEnv(map[string]string{"FLOCI_SERVICES_S3_ENFORCE_AUTH": "true"}),
		testcontainers.WithWaitStrategy(wait.ForListeningPort("4566/tcp")),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = container.Terminate(context.Background()) })
	endpoint, err := container.PortEndpoint(ctx, "4566/tcp", "http")
	if err != nil {
		t.Fatal(err)
	}
	upstream, err := url.Parse(endpoint)
	if err != nil {
		t.Fatal(err)
	}
	proxy := httputil.NewSingleHostReverseProxy(upstream)
	var unavailable atomic.Bool
	storageRoute := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if unavailable.Load() && (r.Method == http.MethodHead || r.Method == http.MethodGet) {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		proxy.ServeHTTP(w, r)
	}))
	defer storageRoute.Close()
	cfg := images.S3Config{
		Bucket:         "delivery-proof-tests",
		Region:         "us-east-1",
		Endpoint:       storageRoute.URL,
		PublicEndpoint: storageRoute.URL,
	}
	storage, err := images.NewS3(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err = storage.InitializeLocalBucket(ctx); err != nil {
		t.Fatal(err)
	}
	proofs := app.NewProofs(
		repo,
		app.NewS3ProofGrants(storage),
		storage,
		decoder,
		app.ProofOptions{UploadScope: storage.ProofScope(), OutageScope: storage.ProofOutageScope()},
	)
	receipts := app.NewProofReceipts(proofs, repo)
	requester, courier, other := uuid.NewString(), uuid.NewString(), uuid.NewString()
	credit := &mountedAcceptanceCredit{
		views:     map[string]app.AssignmentReservation{},
		applied:   map[string]bool{},
		reverted:  map[string]bool{},
		available: 100,
	}
	assigner := app.NewAssigner(repo, credit, app.AssignmentOptions{})
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
	server := httptest.NewServer(router.Setup(router.RPCServices{
		Health: &orderv1connect.UnimplementedHealthServiceHandler{},
		Order: rpcorder.NewServer(app.NewService(repo), nil, app.NewItemImages(repo, storage), assigner).
			WithParticipantActions(app.NewProgress(app.NewMutator(repo)), app.NewDisposition(app.NewMutator(repo))).
			WithProofs(proofs, receipts),
		Admin: &orderv1connect.UnimplementedOrderAdminServiceHandler{},
	}, authenticate))
	defer server.Close()
	transport := &loseProofAcknowledgment{base: server.Client().Transport}
	client := orderv1connect.NewOrderServiceClient(&http.Client{Transport: transport}, server.URL)
	activeOrder := func(t *testing.T) app.Order {
		t.Helper()
		o := seedAcceptanceOrder(t, ctx, repo, requester, time.Now().UTC().Truncate(time.Microsecond))
		credit.mu.Lock()
		credit.views[o.ID] = app.AssignmentReservation{
			OrderID:        o.ID,
			RequesterID:    requester,
			OriginalAmount: 20,
			ReservedAmount: 20,
			State:          "held",
		}
		credit.mu.Unlock()
		if _, err := client.AcceptOrder(
			ctx,
			acceptanceRequest(&pb.AcceptOrderRequest{Id: o.ID, AttemptId: uuid.NewString()}, courier, "user"),
		); err != nil {
			t.Fatal(err)
		}
		if _, err := client.ConfirmPickup(
			ctx,
			acceptanceRequest(&pb.ConfirmPickupRequest{OrderId: o.ID}, courier, "user"),
		); err != nil {
			t.Fatal(err)
		}
		return o
	}
	put := func(grant *pb.CreateProofUploadResponse, body []byte) int {
		t.Helper()
		request, err := http.NewRequestWithContext(ctx, http.MethodPut, grant.UploadUrl, bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		for key, value := range grant.UploadHeaders {
			request.Header.Set(key, value)
		}
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.Copy(io.Discard, response.Body)
		_ = response.Body.Close()
		return response.StatusCode
	}

	t.Run("signed upload delayed validation restart lost acknowledgment and completion", func(t *testing.T) {
		o := activeOrder(t)
		body, err := os.ReadFile("../../proofimage/testdata/valid.png")
		if err != nil {
			t.Fatal(err)
		}
		upload := &pb.CreateProofUploadRequest{
			OrderId:     o.ID,
			UploadId:    uuid.NewString(),
			ContentType: "image/png",
			SizeBytes:   int64(len(body)),
		}
		grant, err := client.CreateProofUpload(ctx, acceptanceRequest(upload, courier, "user"))
		if err != nil {
			t.Fatal(err)
		}
		replicaStorage, err := images.NewS3(ctx, cfg)
		if err != nil {
			t.Fatal(err)
		}
		replicaProofs := app.NewProofs(
			app.NewPostgresRepository(pool),
			app.NewS3ProofGrants(replicaStorage),
			replicaStorage,
			proofimage.NewDecoder(""),
			app.ProofOptions{UploadScope: replicaStorage.ProofScope(), OutageScope: replicaStorage.ProofOutageScope()},
		)
		row, repeat, err := replicaProofs.CreateUpload(
			ctx,
			app.Caller{ID: courier},
			app.ProofUploadInput{
				OrderID:     o.ID,
				UploadID:    upload.UploadId,
				ContentType: upload.ContentType,
				SizeBytes:   upload.SizeBytes,
			},
		)
		if err != nil || row.ID != grant.Msg.ProofId || repeat.URL != grant.Msg.UploadUrl {
			t.Fatalf("replica changed grant: %v", err)
		}
		if status := put(grant.Msg, body); status < 200 || status >= 300 {
			t.Fatalf("signed PUT: %d", status)
		}
		if status := put(grant.Msg, bytes.Repeat([]byte("x"), len(body))); status != http.StatusPreconditionFailed {
			t.Fatalf("immutable overwrite was not fenced: %d", status)
		}
		if _, err = client.VerifyProofUpload(
			ctx,
			acceptanceRequest(&pb.VerifyProofUploadRequest{OrderId: o.ID, ProofId: grant.Msg.ProofId}, courier, "user"),
		); err != nil {
			t.Fatal(err)
		}
		if _, err = pool.Exec(
			ctx,
			"UPDATE orders SET picked_up_at=clock_timestamp()-((delivery_duration_ns*2)/1000)*interval '1 microsecond'+interval '1 second' WHERE id=$1",
			o.ID,
		); err != nil {
			t.Fatal(err)
		}
		unavailable.Store(true)
		input := &pb.ConfirmDeliveryRequest{OrderId: o.ID, ProofIds: []string{grant.Msg.ProofId}, Unattended: true}
		if _, err = client.ConfirmDelivery(
			ctx,
			acceptanceRequest(input, courier, "user"),
		); connect.CodeOf(
			err,
		) != connect.CodeUnavailable {
			t.Fatalf("inspection failure: %v", err)
		}
		var received time.Time
		if err = pool.QueryRow(ctx, "SELECT received_at FROM order_delivery_receipts WHERE order_id=$1 AND state='pending'", o.ID).
			Scan(&received); err != nil {
			t.Fatal(err)
		}
		pending, err := client.GetDeliveryProof(
			ctx,
			acceptanceRequest(&pb.GetDeliveryProofRequest{OrderId: o.ID}, courier, "user"),
		)
		if err != nil || pending.Msg.Pending == nil || !pending.Msg.Pending.Unattended {
			t.Fatalf("pending input unavailable after reload: %+v %v", pending, err)
		}
		// Advance beyond both the deadline and the first persisted retry interval.
		timer := time.NewTimer(2100 * time.Millisecond)
		defer timer.Stop()
		<-timer.C
		unavailable.Store(false)
		restarted := app.NewProofReceipts(replicaProofs, app.NewPostgresRepository(pool))
		if err = restarted.RecoverOnce(ctx); err != nil {
			t.Fatal(err)
		}
		delivered, err := repo.Get(ctx, o.ID)
		if err != nil || delivered.Status != app.Delivered || delivered.DeliveredAt == nil ||
			!delivered.DeliveredAt.Equal(received) ||
			!delivered.UpdatedAt.After(delivered.PickedUpAt.Add(2*delivered.DeliveryDuration)) {
			t.Fatalf("durable effective/audit time lost: %+v %v", delivered, err)
		}
		transport.enabled.Store(true)
		if _, err = client.ConfirmDelivery(
			ctx,
			acceptanceRequest(input, courier, "user"),
		); err == nil ||
			!transport.lost.Load() {
			t.Fatal("accepted acknowledgment loss not exercised")
		}
		if _, err = client.ConfirmDelivery(ctx, acceptanceRequest(input, courier, "user")); err != nil {
			t.Fatalf("unknown accepted retry: %v", err)
		}
		for _, role := range []string{"user", "admin"} {
			if _, err = client.GetDeliveryProof(
				ctx,
				acceptanceRequest(&pb.GetDeliveryProofRequest{OrderId: o.ID}, other, role),
			); connect.CodeOf(
				err,
			) != connect.CodeNotFound {
				t.Fatalf("private proof visible to %s: %v", role, err)
			}
		}
		evidence, err := client.GetDeliveryProof(
			ctx,
			acceptanceRequest(&pb.GetDeliveryProofRequest{OrderId: o.ID}, requester, "user"),
		)
		if err != nil || len(evidence.Msg.Proofs) != 1 {
			t.Fatalf("requester proof read: %+v %v", evidence, err)
		}
		download, err := http.Get(evidence.Msg.Proofs[0].DownloadUrl)
		if err != nil {
			t.Fatal(err)
		}
		saved, readErr := io.ReadAll(download.Body)
		_ = download.Body.Close()
		if readErr != nil || download.StatusCode != 200 || !bytes.Equal(saved, body) {
			t.Fatal("signed private proof download changed bytes")
		}
		if _, err = client.ConfirmReceipt(
			ctx,
			acceptanceRequest(&pb.ConfirmReceiptRequest{OrderId: o.ID}, requester, "user"),
		); err != nil {
			t.Fatal(err)
		}
		if _, err = client.ConfirmDelivery(ctx, acceptanceRequest(input, courier, "user")); err != nil {
			t.Fatal("accepted replay after completion:", err)
		}
		var deliveries, outcomes int
		if err = pool.QueryRow(ctx, "SELECT count(*) FROM order_audit WHERE order_id=$1 AND action='deliver'", o.ID).
			Scan(&deliveries); err != nil {
			t.Fatal(err)
		}
		if err = pool.QueryRow(ctx, "SELECT count(*) FROM order_outbox WHERE order_id=$1 AND kind='order.outcome'", o.ID).
			Scan(&outcomes); err != nil ||
			deliveries != 1 ||
			outcomes != 1 {
			t.Fatalf("duplicate effects: %d deliveries %d outcomes %v", deliveries, outcomes, err)
		}
		var payload []byte
		if err = pool.QueryRow(ctx, "SELECT payload FROM order_outbox WHERE order_id=$1 AND kind='order.outcome'", o.ID).
			Scan(&payload); err != nil {
			t.Fatal(err)
		}
		var fact app.FactsPayload
		if err = json.Unmarshal(
			payload,
			&fact,
		); err != nil || fact.DeliveredAt == nil ||
			!fact.DeliveredAt.Equal(received) {
			t.Fatal("settlement observation lost server receipt time")
		}
	})

	t.Run("disguised media and unsupported outage requests never deliver", func(t *testing.T) {
		o := activeOrder(t)
		body := []byte("not a JPEG photograph")
		grant, err := client.CreateProofUpload(
			ctx,
			acceptanceRequest(
				&pb.CreateProofUploadRequest{
					OrderId:     o.ID,
					UploadId:    uuid.NewString(),
					ContentType: "image/jpeg",
					SizeBytes:   int64(len(body)),
				},
				courier,
				"user",
			),
		)
		if err != nil {
			t.Fatal(err)
		}
		if status := put(grant.Msg, body); status < 200 || status >= 300 {
			t.Fatal("could not stage malformed fixture")
		}
		if _, err = client.VerifyProofUpload(
			ctx,
			acceptanceRequest(&pb.VerifyProofUploadRequest{OrderId: o.ID, ProofId: grant.Msg.ProofId}, courier, "user"),
		); connect.CodeOf(
			err,
		) != connect.CodeFailedPrecondition {
			t.Fatalf("disguised media accepted: %v", err)
		}
		if _, err = client.ConfirmDelivery(
			ctx,
			acceptanceRequest(
				&pb.ConfirmDeliveryRequest{OrderId: o.ID, ProofIds: []string{grant.Msg.ProofId}},
				courier,
				"user",
			),
		); connect.CodeOf(
			err,
		) != connect.CodeFailedPrecondition {
			t.Fatalf("unverified image admitted: %v", err)
		}
		if _, err = client.ConfirmDelivery(
			ctx,
			acceptanceRequest(
				&pb.ConfirmDeliveryRequest{OrderId: o.ID, WithoutProof: true, Unattended: true},
				courier,
				"user",
			),
		); connect.CodeOf(
			err,
		) != connect.CodeFailedPrecondition {
			t.Fatalf("uncertified outage accepted: %v", err)
		}
		current, err := repo.Get(ctx, o.ID)
		if err != nil || current.Status != app.Delivering {
			t.Fatal("invalid media changed lifecycle")
		}
	})
}
