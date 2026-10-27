//go:build integration

package seed

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/auth/httpauth"
	creditpb "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/credit/v1"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/credit/v1/creditv1connect"
	orderpb "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/order/v1"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/order/v1/orderv1connect"
	locationpb "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/supplier/location/v1"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/supplier/location/v1/locationv1connect"
	userpb "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/user/v1"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/user/v1/userv1connect"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/kafka"
	usersevents "github.com/AY2627S1-CS3219-P1/FoC/pkg/kafkagen/users/events/v1"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/database"
	usersjwt "github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/jwt"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/models"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/outbox"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/service"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/store"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	kafkago "github.com/segmentio/kafka-go"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// This regression uses the real User relay, Kafka broker and Credit/Order
// executables. Public keys and Supplier locations use test servers.
func TestSeedCreditProvisioning(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	db, userDSN := testPostgres(t, ctx)
	mustExec(t, db, "CREATE DATABASE credit_seed_test")
	creditDSN := strings.Replace(userDSN, "/users_seed_test?", "/credit_seed_test?", 1)
	broker := testBroker(t, ctx)
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	codec, err := usersjwt.NewES256Codec(key, "seed-test", httpauth.TokenIssuer, httpauth.TokenAudience)
	if err != nil {
		t.Fatal(err)
	}
	publicKeys := &userpb.GetPublicKeysResponse{}
	for _, key := range codec.PublicKeys().Keys {
		publicKeys.Keys = append(publicKeys.Keys, &userpb.JsonWebKey{
			Kty: key.KeyType, Crv: key.Curve, X: key.X, Y: key.Y,
			Use: key.Use, Alg: key.Algorithm, Kid: key.KeyID,
		})
	}
	_, keyHandler := userv1connect.NewPublicKeyServiceHandler(&testKeys{keys: publicKeys})
	keys := httptest.NewServer(keyHandler)
	defer keys.Close()
	public, internal := testPort(t), testPort(t)
	startService(t, ctx, "credit-service", []string{
		"DATABASE_URL=" + creditDSN, "PORT=" + public, "INTERNAL_PORT=" + internal,
		"USER_SERVICE_BASE_URL=" + keys.URL, "KAFKA_BROKERS=" + broker,
	})
	client := creditv1connect.NewCreditAccountServiceClient(
		&http.Client{Timeout: 2 * time.Second},
		"http://127.0.0.1:"+public,
	)
	token := func(user User) string {
		t.Helper()
		role := usersjwt.Role(user.Role)
		if user.Role == models.RoleSuspended {
			role = usersjwt.RoleSuspendedUser
		}
		now := time.Now().UTC()
		value, err := codec.Sign(usersjwt.Claims{
			Type: usersjwt.AccessToken, Subject: user.ID.String(), SessionID: uuid.NewString(),
			TokenID: uuid.NewString(), Role: role, IssuedAt: now, ExpiresAt: now.Add(5 * time.Minute),
		})
		if err != nil {
			t.Fatal(err)
		}
		return "Bearer " + value
	}
	fixtures := testFixtures(t)
	seeder := Seeder{DB: db}
	if err := seeder.Run(ctx, fixtures, false); err != nil {
		t.Fatal(err)
	}
	persistence := store.New(db)
	publisher, err := kafka.NewPublisher(kafka.PublisherConfig{Brokers: []string{broker}})
	if err != nil {
		t.Fatal(err)
	}
	defer publisher.Close() //nolint:errcheck
	relay := outbox.New(persistence.UserCreatedOutbox, publisher)
	if err := relay.Drain(ctx, 50); err != nil {
		t.Fatal(err)
	}
	for _, user := range fixtures.Users {
		eventually(t, ctx, func() bool {
			request := connect.NewRequest(&creditpb.GetMyAccountRequest{})
			request.Header().Set("Authorization", token(user))
			response, err := client.GetMyAccount(ctx, request)
			return err == nil && response.Msg.GetAccount().GetAvailable() == 100
		})
		request := connect.NewRequest(&creditpb.ListMyLedgerEntriesRequest{})
		request.Header().Set("Authorization", token(user))
		ledger, err := client.ListMyLedgerEntries(ctx, request)
		if err != nil || len(ledger.Msg.GetEntries()) != 1 || ledger.Msg.GetEntries()[0].GetBalanceDelta() != 100 {
			t.Fatalf("fixture %s: expected exactly one starting grant: %v, %v", user.Email, ledger, err)
		}
	}
	creditDB, err := database.Open(creditDSN, 2, 2)
	if err != nil {
		t.Fatal(err)
	}
	creditSQL, err := creditDB.DB()
	if err != nil {
		t.Fatal(err)
	}
	defer creditSQL.Close() //nolint:errcheck
	// Exercise a real hold before reseeding, so preserving financial history is
	// checked against non-default state rather than only untouched accounts.
	reservations := creditv1connect.NewCreditReservationServiceClient(http.DefaultClient, "http://127.0.0.1:"+internal)
	if _, err := reservations.Reserve(ctx, connect.NewRequest(&creditpb.ReserveRequest{
		RequestId: uuid.NewString(), RequesterId: fixtures.Users[0].ID.String(), Amount: 7,
	})); err != nil {
		t.Fatal(err)
	}
	const finances = `SELECT jsonb_build_object(
		'accounts',(SELECT jsonb_agg(to_jsonb(a) ORDER BY user_id) FROM accounts a),
		'ledger',(SELECT jsonb_agg(to_jsonb(l) ORDER BY id) FROM ledger_entries l))::text`
	original := scalar(t, creditDB, finances)
	for _, reset := range []bool{false, true} {
		if err := seeder.Run(ctx, fixtures, reset); err != nil {
			t.Fatal(err)
		}
		if err := relay.Drain(ctx, 50); err != nil {
			t.Fatal(err)
		}
		if scalar(t, creditDB, finances) != original {
			t.Fatal("seed rerun/reset changed existing accounts or ledger history")
		}
	}
	// Redeliver creation events. A sentinel on the same single partition proves
	// Credit processed every duplicate before the final financial assertion.
	var events []models.UserCreatedOutbox
	if err := db.Find(&events).Error; err != nil {
		t.Fatal(err)
	}
	for _, event := range events {
		if err := publisher.Publish(ctx, "users.create", []byte(event.UserID.String()), &usersevents.UserCreated{
			EventId: event.EventID.String(), UserId: event.UserID.String(), Email: event.Email,
			DisplayName: event.DisplayName, CreatedAt: timestamppb.New(event.UserCreatedAt),
		}); err != nil {
			t.Fatal(err)
		}
	}
	// Use a genuine persisted user/event as the sentinel, through the same logic.
	sentinel := models.User{Email: "sentinel@demo.foc.test", DisplayName: "Delivery barrier"}
	if err := persistence.WithTransaction(ctx, func(tx *store.Store) error {
		return (service.UserProvisioner{Users: tx.Users, Events: tx.UserCreatedOutbox}).Create(ctx, &sentinel)
	}); err != nil {
		t.Fatal(err)
	}
	if err := relay.Drain(ctx, 50); err != nil {
		t.Fatal(err)
	}
	eventually(t, ctx, func() bool {
		return scalar(t, creditDB, "SELECT count(*)::text FROM accounts WHERE user_id=?", sentinel.ID) == "1"
	})
	// Exclude the sentinel's legitimate account/grant from the original snapshot.
	const fixtureFinances = `SELECT jsonb_build_object(
		'accounts',(SELECT jsonb_agg(to_jsonb(a) ORDER BY user_id) FROM accounts a WHERE user_id<>?),
		'ledger',(SELECT jsonb_agg(to_jsonb(l) ORDER BY id) FROM ledger_entries l WHERE user_id<>?))::text`
	if scalar(t, creditDB, fixtureFinances, sentinel.ID, sentinel.ID) != original {
		t.Fatal("duplicate creation events granted credits again or changed existing finances")
	}

	t.Run("seeded users can create accept and cancel against actual Credit", func(t *testing.T) {
		mustExec(t, db, "CREATE DATABASE order_seed_test")
		orderDSN := strings.Replace(userDSN, "/users_seed_test?", "/order_seed_test?", 1)
		_, locationsHandler := locationv1connect.NewLocationDiscoveryServiceHandler(&testLocations{})
		locations := httptest.NewServer(locationsHandler)
		defer locations.Close()
		credential, err := jwt.NewWithClaims(jwt.SigningMethodES256, jwt.MapClaims{
			"sub": "order-service", "aud": "credit-service", "exp": time.Now().Add(5 * time.Minute).Unix(),
		}).SignedString(key)
		if err != nil {
			t.Fatal(err)
		}
		credentialPath := filepath.Join(t.TempDir(), "workload.jwt")
		if err := os.WriteFile(credentialPath, []byte(credential), 0600); err != nil {
			t.Fatal(err)
		}
		port := testPort(t)
		startService(t, ctx, "order-service", []string{
			"DATABASE_URL=" + orderDSN, "PORT=" + port, "USER_SERVICE_BASE_URL=" + keys.URL,
			"SUPPLIER_SERVICE_BASE_URL=" + locations.URL, "CREDIT_SERVICE_BASE_URL=http://127.0.0.1:" + internal,
			"CREDIT_WORKLOAD_TOKEN_FILE=" + credentialPath, "KAFKA_BROKERS=" + broker,
			"ORDER_S3_BUCKET=", "ORDER_S3_ENDPOINT=", "ORDER_S3_PUBLIC_ENDPOINT=",
		})
		baseURL := "http://127.0.0.1:" + port
		health := orderv1connect.NewHealthServiceClient(http.DefaultClient, baseURL)
		eventually(t, ctx, func() bool {
			_, err := health.Check(ctx, connect.NewRequest(&orderpb.CheckRequest{}))
			return err == nil
		})
		orders := orderv1connect.NewOrderServiceClient(http.DefaultClient, baseURL)
		create := connect.NewRequest(&orderpb.CreateOrderRequest{
			PickupLocationId: uuid.NewString(), Delivery: &orderpb.DeliveryInput{LocationId: uuid.NewString()},
			ItemDescription: "Seed integration verification", RewardCredits: 3,
			DeliveryDuration: durationpb.New(15 * time.Minute), ExpiresAt: timestamppb.New(time.Now().Add(time.Hour)),
			IdempotencyKey: uuid.NewString(),
		})
		create.Header().Set("Authorization", token(fixtures.Users[2]))
		created, err := orders.CreateOrder(ctx, create)
		if err != nil {
			t.Fatal(err)
		}
		order := created.Msg.GetOrder()
		if order.GetStatus() != orderpb.OrderStatus_ORDER_STATUS_AWAITING_COURIER {
			t.Fatalf("created status=%v", order.GetStatus())
		}
		if scalar(t, creditDB, "SELECT reserved_amount::text FROM reservations WHERE order_id=?", order.Id) != "3" {
			t.Fatal("Order did not reserve the seeded requester's credits")
		}
		accept := connect.NewRequest(&orderpb.AcceptOrderRequest{Id: order.Id, AttemptId: uuid.NewString()})
		accept.Header().Set("Authorization", token(fixtures.Users[4]))
		accepted, err := orders.AcceptOrder(ctx, accept)
		if err != nil {
			t.Fatal(err)
		}
		if accepted.Msg.GetOrder().GetStatus() != orderpb.OrderStatus_ORDER_STATUS_AWAITING_PICKUP {
			t.Fatalf("accepted status=%v", accepted.Msg.GetOrder().GetStatus())
		}
		idempotencyKey := uuid.NewString()
		cancelRequest := connect.NewRequest(&orderpb.CancelOrderRequest{
			Id: order.Id, ExpectedRevision: accepted.Msg.GetOrder().GetRevision(), IdempotencyKey: &idempotencyKey,
		})
		cancelRequest.Header().Set("Authorization", token(fixtures.Users[2]))
		cancelled, err := orders.CancelOrder(ctx, cancelRequest)
		if err != nil {
			t.Fatal(err)
		}
		if cancelled.Msg.GetOrder().GetStatus() != orderpb.OrderStatus_ORDER_STATUS_CANCELLED {
			t.Fatalf("cancelled status=%v", cancelled.Msg.GetOrder().GetStatus())
		}
		eventually(t, ctx, func() bool {
			return scalar(t, creditDB, "SELECT status FROM reservations WHERE order_id=?", order.Id) == "RELEASED"
		})
		if scalar(
			t,
			creditDB,
			"SELECT sum(reserved_delta)::text FROM ledger_entries WHERE order_id=?",
			order.Id,
		) != "0" {
			t.Fatal("Kafka cancellation settlement did not return the held credits")
		}
	})
}

func testBroker(t *testing.T, ctx context.Context) string {
	t.Helper()
	container := testContainer(t, ctx, "apache/kafka:4.2.2", []string{
		"-p", "127.0.0.1::19092", "--entrypoint", "/bin/sh",
		"-e", "KAFKA_NODE_ID=1", "-e", "KAFKA_PROCESS_ROLES=broker,controller",
		"-e", "KAFKA_CONTROLLER_QUORUM_VOTERS=1@localhost:9093", "-e", "KAFKA_CONTROLLER_LISTENER_NAMES=CONTROLLER",
		"-e", "KAFKA_LISTENERS=BROKER://:9092,EXTERNAL://:19092,CONTROLLER://:9093",
		"-e", "KAFKA_LISTENER_SECURITY_PROTOCOL_MAP=BROKER:PLAINTEXT,EXTERNAL:PLAINTEXT,CONTROLLER:PLAINTEXT",
		"-e", "KAFKA_INTER_BROKER_LISTENER_NAME=BROKER", "-e", "KAFKA_OFFSETS_TOPIC_REPLICATION_FACTOR=1",
		"-e", "KAFKA_AUTO_CREATE_TOPICS_ENABLE=false",
	}, "-c", "while [ ! -f /tmp/start-seed-kafka ]; do sleep 0.1; done; exec sh /tmp/start-seed-kafka")
	address := strings.TrimSpace(docker(t, ctx, "port", container, "19092/tcp"))
	script := fmt.Sprintf(
		"export KAFKA_ADVERTISED_LISTENERS='BROKER://localhost:9092,EXTERNAL://%s'\nexec /etc/kafka/docker/run\n",
		address,
	)
	command := exec.CommandContext(ctx, "docker", "exec", "-i", container, "sh", "-c", "cat > /tmp/start-seed-kafka")
	command.Stdin = strings.NewReader(script)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("configure test broker: %v: %s", err, output)
	}
	manifest, err := os.ReadFile("../../../infra/kafka/topics.conf")
	if err != nil {
		t.Fatal(err)
	}
	var topics []kafkago.TopicConfig
	for _, line := range strings.Split(string(manifest), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 || strings.HasPrefix(fields[0], "#") {
			continue
		}
		topics = append(topics, kafkago.TopicConfig{Topic: fields[0], NumPartitions: 1, ReplicationFactor: 1})
	}
	eventually(t, ctx, func() bool {
		attempt, cancel := context.WithTimeout(ctx, time.Second)
		defer cancel()
		connection, err := kafkago.DialContext(attempt, "tcp", address)
		if err != nil {
			return false
		}
		defer connection.Close() //nolint:errcheck
		if err := connection.SetDeadline(time.Now().Add(time.Second)); err != nil {
			return false
		}
		return connection.CreateTopics(topics...) == nil
	})
	eventually(t, ctx, func() bool {
		attempt, cancel := context.WithTimeout(ctx, time.Second)
		defer cancel()
		for _, topic := range topics {
			connection, err := kafkago.DialLeader(attempt, "tcp", address, topic.Topic, 0)
			if err != nil {
				return false
			}
			if err := connection.Close(); err != nil {
				return false
			}
		}
		return true
	})
	return address
}

type testKeys struct {
	userv1connect.UnimplementedPublicKeyServiceHandler
	keys *userpb.GetPublicKeysResponse
}

func (s *testKeys) GetPublicKeys(
	context.Context,
	*connect.Request[userpb.GetPublicKeysRequest],
) (*connect.Response[userpb.GetPublicKeysResponse], error) {
	return connect.NewResponse(s.keys), nil
}

func testPort(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close() //nolint:errcheck
	_, port, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	return port
}

func startService(t *testing.T, ctx context.Context, name string, environment []string) {
	t.Helper()
	directory := t.TempDir()
	binary := filepath.Join(directory, name)
	build := exec.CommandContext(ctx, "go", "build", "-mod=readonly", "-o", binary, "./cmd/server")
	build.Dir = filepath.Join("..", "..", "..", name)
	build.Env = append(os.Environ(), "GOWORK=off")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build actual %s executable: %v: %s", name, err, output)
	}
	logPath := filepath.Join(directory, name+".log")
	log, err := os.Create(logPath)
	if err != nil {
		t.Fatal(err)
	}
	command := exec.CommandContext(ctx, binary)
	command.Dir = directory
	command.Env = append(os.Environ(), "APP_ENV=local", "RUN_MIGRATIONS=true")
	command.Env = append(command.Env, environment...)
	command.Stdout, command.Stderr = log, log
	if err := command.Start(); err != nil {
		_ = log.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = command.Process.Kill()
		_ = command.Wait()
		_ = log.Close()
		if t.Failed() {
			contents, _ := os.ReadFile(logPath)
			t.Logf("%s executable log:\n%s", name, contents)
		}
	})
}

type testLocations struct {
	locationv1connect.UnimplementedLocationDiscoveryServiceHandler
}

func (s *testLocations) GetLocation(
	_ context.Context,
	req *connect.Request[locationpb.GetLocationRequest],
) (*connect.Response[locationpb.GetLocationResponse], error) {
	if req.Header().Get("Authorization") == "" {
		return nil, connect.NewError(connect.CodeUnauthenticated, fmt.Errorf("expected delegated user token"))
	}
	return connect.NewResponse(&locationpb.GetLocationResponse{Location: &locationpb.Location{
		Id: req.Msg.Id, Name: "Test meeting point", Building: &locationpb.Building{Name: "Test building"},
	}}), nil
}
