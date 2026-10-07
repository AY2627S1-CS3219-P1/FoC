package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"encoding/hex"
	"errors"
	"net"
	"net/http"
	"testing"
	"time"

	"connectrpc.com/connect"
	userv1 "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/user/v1"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/user/v1/userv1connect"
	healthhandler "github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/handlers/health"
)

func TestKeyIDPreservesSEC1Fingerprint(t *testing.T) {
	point, err := hex.DecodeString("046b17d1f2e12c4247f8bce6e563a440f277037d812deb33a0f4a13945d898c2964fe342e2fe1a7f9b8ee7eb4a7c0f9e162bce33576b315ececbb6406837bf51f5")
	if err != nil {
		t.Fatal(err)
	}
	key, err := ecdsa.ParseUncompressedPublicKey(elliptic.P256(), point)
	if err != nil {
		t.Fatal(err)
	}
	got, err := keyID(key)
	if err != nil {
		t.Fatal(err)
	}
	const want = "698bea63dc44a344663ff1429aea10842df27b6b991ef25866b2c6c02cdcc5be"
	if got != want {
		t.Fatalf("key fingerprint changed: got %s want %s", got, want)
	}
}

func TestKeyIDRejectsInvalidPublicKey(t *testing.T) {
	if _, err := keyID(&ecdsa.PublicKey{}); err == nil {
		t.Fatal("invalid public key accepted")
	}
}

// okPinger reports a reachable database to the health handler.
type okPinger struct{}

func (okPinger) PingContext(context.Context) error { return nil }

func TestServerServesConnectAndNativeGRPC(t *testing.T) {
	path, handler := userv1connect.NewHealthServiceHandler(&healthhandler.Handler{DB: okPinger{}})
	mux := http.NewServeMux()
	mux.Handle(path, handler)

	server := newServer("127.0.0.1:0", mux)
	listener, err := net.Listen("tcp", server.Addr)
	if err != nil {
		t.Fatal(err)
	}
	serveErrors := make(chan error, 1)
	go func() { serveErrors <- server.Serve(listener) }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(ctx); err != nil {
			t.Errorf("shut down server: %v", err)
		}
		select {
		case err := <-serveErrors:
			if !errors.Is(err, http.ErrServerClosed) {
				t.Errorf("serve: %v", err)
			}
		case <-ctx.Done():
			t.Error("server did not stop")
		}
	})

	http1 := new(http.Protocols)
	http1.SetHTTP1(true)
	h2c := new(http.Protocols)
	h2c.SetUnencryptedHTTP2(true)
	for _, tc := range []struct {
		name      string
		protocols *http.Protocols
		options   []connect.ClientOption
	}{
		{name: "Connect over HTTP/1.1", protocols: http1},
		{name: "native gRPC over h2c", protocols: h2c, options: []connect.ClientOption{connect.WithGRPC()}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			transport := &http.Transport{Protocols: tc.protocols}
			t.Cleanup(transport.CloseIdleConnections)
			client := userv1connect.NewHealthServiceClient(
				&http.Client{Transport: transport}, "http://"+listener.Addr().String(), tc.options...)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			response, err := client.Check(ctx, connect.NewRequest(&userv1.CheckRequest{}))
			if err != nil {
				t.Fatal(err)
			}
			if response.Msg.Status != "ok" {
				t.Fatalf("status = %q, want ok", response.Msg.Status)
			}
		})
	}
}
