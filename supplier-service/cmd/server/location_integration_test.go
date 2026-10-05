//go:build integration

package main

import (
	"context"
	"net"
	"net/http"
	"testing"
	"time"

	"connectrpc.com/connect"
	pb "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/supplier/location/v1"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/supplier/location/v1/locationv1connect"
	"github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/deps"
	"github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/router"
	"github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/testsupport/locationfixture"
	"github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/testsupport/rpcauth"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// TestLocationProductionComposition proves the real dependency and mounting
// chain with signed JWTs, generated clients, transactions, and PostGIS.
func TestLocationProductionComposition(t *testing.T) {
	fixturePool := locationfixture.SetupDatabase(t)
	// Eight blocked RPCs, their lock holder and the observer need independent
	// connections even on small CI runners. This capacity is test-only.
	config := fixturePool.Config()
	config.MaxConns = 12
	pool, err := pgxpool.NewWithConfig(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	auth := rpcauth.New(t)
	services, err := newRPCServices(pool, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	routes := router.Setup(deps.New(pool), auth.Authenticator, services)
	server := newServer("127.0.0.1:0", routes)
	listener, err := net.Listen("tcp", server.Addr)
	if err != nil {
		t.Fatal(err)
	}
	exited := make(chan error, 1)
	go func() { exited <- server.Serve(listener) }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(ctx); err != nil {
			t.Error(err)
		}
		<-exited
	})
	url := "http://" + listener.Addr().String()
	http1 := new(http.Protocols)
	http1.SetHTTP1(true)
	transport := &http.Transport{Protocols: http1}
	t.Cleanup(transport.CloseIdleConnections)
	client := &http.Client{Transport: transport}
	admin := locationv1connect.NewLocationAdminServiceClient(client, url, rpcauth.Bearer(auth.Token(t, "admin")))
	discovery := locationv1connect.NewLocationDiscoveryServiceClient(client, url, rpcauth.Bearer(auth.Token(t, "admin")))
	disablement := locationv1connect.NewLocationDisablementServiceClient(client, url, rpcauth.Bearer(auth.Token(t, "admin")))
	requests := locationv1connect.NewLocationAdditionRequestServiceClient(client, url, rpcauth.Bearer(auth.Token(t, "user")))
	reviewer := locationv1connect.NewLocationAdditionRequestServiceClient(client, url, rpcauth.Bearer(auth.Token(t, "admin")))
	ctx := context.Background()
	input := &pb.LocationInput{Name: "Production composition supplier", IsSupplier: proto.Bool(true), BuildingId: "a7ddb3ee-f24e-4464-bc33-6507ac5f5d68", CategoryIds: []string{"b526b558-e2ec-4db1-b873-13a9f490e07d"}, Coordinates: &pb.Coordinates{Latitude: 1.294, Longitude: 103.774}, Details: "counter"}
	create := &pb.CreateLocationRequest{Location: input, IdempotencyKey: uuid.NewString()}
	created, err := admin.CreateLocation(ctx, connect.NewRequest(create))
	if err != nil {
		t.Fatal(err)
	}
	retried, err := admin.CreateLocation(ctx, connect.NewRequest(create))
	if err != nil || retried.Msg.Location.Id != created.Msg.Location.Id {
		t.Fatalf("admin retry: %v %v", retried, err)
	}
	id := created.Msg.Location.Id
	got, err := discovery.GetLocation(ctx, connect.NewRequest(&pb.GetLocationRequest{Id: id}))
	if err != nil || got.Msg.Location.Name != input.Name {
		t.Fatalf("discovery after admin write: %v %v", got, err)
	}
	scheduled, err := disablement.CreateDisablement(ctx, connect.NewRequest(&pb.CreateDisablementRequest{LocationId: id, StartsAt: timestamppb.New(time.Now().Add(time.Hour)), Reason: "maintenance", IdempotencyKey: uuid.NewString()}))
	if err != nil {
		t.Fatal(err)
	}
	cancelled, err := disablement.CancelDisablement(ctx, connect.NewRequest(&pb.CancelDisablementRequest{Id: scheduled.Msg.Disablement.Id}))
	if err != nil || cancelled.Msg.Disablement.State != pb.DisablementState_DISABLEMENT_STATE_CANCELLED {
		t.Fatalf("cancel scheduled: %v %v", cancelled, err)
	}
	submitted, err := requests.SubmitLocationAdditionRequest(ctx, connect.NewRequest(&pb.SubmitLocationAdditionRequestRequest{Proposal: input, IdempotencyKey: uuid.NewString()}))
	if err != nil {
		t.Fatal(err)
	}
	approved, err := reviewer.ApproveLocationAdditionRequest(ctx, connect.NewRequest(&pb.ApproveLocationAdditionRequestRequest{Id: submitted.Msg.Request.Id}))
	if err != nil || approved.Msg.Location.Id == id {
		t.Fatalf("approval: %v %v", approved, err)
	}
	got, err = discovery.GetLocation(ctx, connect.NewRequest(&pb.GetLocationRequest{Id: approved.Msg.Location.Id}))
	if err != nil || len(got.Msg.Location.Categories) != 1 {
		t.Fatalf("discovery after approval: %v %v", got, err)
	}
	archived, err := admin.ArchiveLocation(ctx, connect.NewRequest(&pb.ArchiveLocationRequest{Id: id}))
	if err != nil || archived.Msg.Location.ArchivedAt == nil {
		t.Fatalf("archive: %v %v", archived, err)
	}
	got, err = discovery.GetLocation(ctx, connect.NewRequest(&pb.GetLocationRequest{Id: id}))
	if err != nil || got.Msg.Location.ArchivedAt == nil {
		t.Fatalf("archive discovery: %v %v", got, err)
	}
	denied := locationv1connect.NewLocationDisablementServiceClient(client, url, rpcauth.Bearer(auth.Token(t, "user")))
	_, err = denied.ListDisablements(ctx, connect.NewRequest(&pb.ListDisablementsRequest{LocationId: id}))
	if connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("workflow authorization: %v", err)
	}
	t.Run("admin integration proofs", func(t *testing.T) {
		proveAdminIntegration(t, pool, admin, discovery, disablement, input)
	})
	// Native gRPC is checked once at the production transport boundary.
	h2c := new(http.Protocols)
	h2c.SetUnencryptedHTTP2(true)
	h2transport := &http.Transport{Protocols: h2c}
	t.Cleanup(h2transport.CloseIdleConnections)
	grpc := locationv1connect.NewLocationDiscoveryServiceClient(&http.Client{Transport: h2transport}, url, connect.WithGRPC(), rpcauth.Bearer(auth.Token(t, "admin")))
	got, err = grpc.GetLocation(ctx, connect.NewRequest(&pb.GetLocationRequest{Id: approved.Msg.Location.Id}))
	if err != nil || got.Msg.Location.Id != approved.Msg.Location.Id {
		t.Fatalf("native gRPC discovery: %v %v", got, err)
	}

	t.Run("verified caller roles survive shared authentication", func(t *testing.T) {
		for _, protocol := range []struct {
			name    string
			client  *http.Client
			options []connect.ClientOption
		}{
			{"Connect", client, nil}, {"gRPC", &http.Client{Transport: h2transport}, []connect.ClientOption{connect.WithGRPC()}},
		} {
			t.Run(protocol.name, func(t *testing.T) {
				for _, role := range []string{"admin", "super_admin", "user", "suspended_user", ""} {
					t.Run(role, func(t *testing.T) {
						options := append([]connect.ClientOption{}, protocol.options...)
						if role != "" {
							options = append(options, rpcauth.Bearer(auth.Token(t, role)))
						}
						intervals := locationv1connect.NewLocationDisablementServiceClient(protocol.client, url, options...)
						proposals := locationv1connect.NewLocationAdditionRequestServiceClient(protocol.client, url, options...)
						_, err := intervals.ListDisablements(ctx, connect.NewRequest(&pb.ListDisablementsRequest{LocationId: approved.Msg.Location.Id}))
						switch role {
						case "admin", "super_admin":
							if err != nil {
								t.Fatal(err)
							}
						case "":
							if connect.CodeOf(err) != connect.CodeUnauthenticated {
								t.Fatalf("missing caller: %v", err)
							}
						default:
							if connect.CodeOf(err) != connect.CodePermissionDenied {
								t.Fatalf("role lost: %v", err)
							}
						}
						if role == "user" || role == "suspended_user" {
							_, err = proposals.ApproveLocationAdditionRequest(ctx, connect.NewRequest(&pb.ApproveLocationAdditionRequestRequest{}))
							if connect.CodeOf(err) != connect.CodePermissionDenied {
								t.Fatalf("authorization must precede validation: %v", err)
							}
						}
						if role == "suspended_user" {
							_, err = proposals.SubmitLocationAdditionRequest(ctx, connect.NewRequest(&pb.SubmitLocationAdditionRequestRequest{}))
							if connect.CodeOf(err) != connect.CodePermissionDenied {
								t.Fatalf("suspended submit: %v", err)
							}
						}
					})
				}
			})
		}
	})
	t.Run("native gRPC workflow mutation and retry", func(t *testing.T) {
		intervals := locationv1connect.NewLocationDisablementServiceClient(&http.Client{Transport: h2transport}, url, connect.WithGRPC(), rpcauth.Bearer(auth.Token(t, "super_admin")))
		request := &pb.CreateDisablementRequest{LocationId: approved.Msg.Location.Id, StartsAt: timestamppb.New(time.Now().Add(time.Hour)), Reason: "gRPC maintenance", IdempotencyKey: uuid.NewString()}
		first, err := intervals.CreateDisablement(ctx, connect.NewRequest(request))
		if err != nil {
			t.Fatal(err)
		}
		retry, err := intervals.CreateDisablement(ctx, connect.NewRequest(proto.Clone(request).(*pb.CreateDisablementRequest)))
		if err != nil || !proto.Equal(first.Msg, retry.Msg) {
			t.Fatalf("gRPC retry differs: %v %v", retry, err)
		}
		cancelled, err := intervals.CancelDisablement(ctx, connect.NewRequest(&pb.CancelDisablementRequest{Id: first.Msg.Disablement.Id}))
		if err != nil || cancelled.Msg.Disablement.State != pb.DisablementState_DISABLEMENT_STATE_CANCELLED {
			t.Fatalf("gRPC cancellation: %v %v", cancelled, err)
		}
	})
	t.Log("PASS: production composition, signed JWT, admin retry, discovery, scheduled cancellation, request approval, archive, authorization, Connect HTTP/1.1 and native gRPC h2c")
}
