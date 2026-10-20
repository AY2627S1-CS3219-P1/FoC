package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"encoding/hex"
	"errors"
	"net"
	"net/http"
	"strings"
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

func TestLoadConfigRequiresKafkaBrokers(t *testing.T) {
	setValidConfigEnv(t)
	t.Setenv("KAFKA_BROKERS", "")
	if _, err := loadConfig(); err == nil || !strings.Contains(err.Error(), "KAFKA_BROKERS is not set") {
		t.Fatalf("loadConfig error = %v, want missing KAFKA_BROKERS", err)
	}
}

func TestLoadConfigAcceptsKafkaBrokerWithoutCheckingAvailability(t *testing.T) {
	setValidConfigEnv(t)
	const brokers = "unreachable.invalid:9092"
	t.Setenv("KAFKA_BROKERS", brokers)
	cfg, err := loadConfig()
	if err != nil {
		t.Fatalf("loadConfig with configured broker: %v", err)
	}
	if cfg.kafkaBrokers != brokers {
		t.Fatalf("KAFKA_BROKERS = %q, want %q", cfg.kafkaBrokers, brokers)
	}
}

func setValidConfigEnv(t *testing.T) {
	t.Helper()
	for key, value := range map[string]string{
		"PORT":                              "8080",
		"DATABASE_URL":                      "postgres://user:password@localhost:5432/user_service",
		"DB_MAX_OPEN":                       "10",
		"DB_MAX_IDLE":                       "5",
		"RUN_MIGRATIONS":                    "true",
		"APP_ENV":                           "test",
		"FRONTEND_BASE_URL":                 "https://example.test",
		"JWT_PRIVATE_KEY_FILE":              "/tmp/user-service-key.pem",
		"JWT_ACCESS_TOKEN_TTL":              "10m",
		"JWT_REFRESH_TOKEN_TTL":             "720h",
		"SMTP_ADDR":                         "smtp.example.test:25",
		"SMTP_FROM":                         "FoC <no-reply@example.test>",
		"SMTP_USERNAME":                     "",
		"SMTP_PASSWORD":                     "",
		"SMTP_TIMEOUT":                      "5s",
		"BOOTSTRAP_SUPERADMIN_EMAIL":        "",
		"BOOTSTRAP_SUPERADMIN_DISPLAY_NAME": "",
	} {
		t.Setenv(key, value)
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
