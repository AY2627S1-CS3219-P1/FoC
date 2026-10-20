package server

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	userv1 "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/user/v1"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/user/v1/userv1connect"
)

type healthyService struct {
	userv1connect.UnimplementedHealthServiceHandler
}

func (healthyService) Check(context.Context, *connect.Request[userv1.CheckRequest]) (*connect.Response[userv1.CheckResponse], error) {
	return connect.NewResponse(&userv1.CheckResponse{Status: "ok"}), nil
}

func listen(t *testing.T) net.Listener {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	return listener
}

func awaitResult(t *testing.T, result <-chan error) error {
	t.Helper()
	select {
	case err := <-result:
		return err
	case <-time.After(5 * time.Second):
		t.Fatal("server group did not stop")
		return nil
	}
}

func TestBothListenersServeConnectAndNativeGRPC(t *testing.T) {
	path, handler := userv1connect.NewHealthServiceHandler(healthyService{})
	public := http.NewServeMux()
	public.Handle(path, handler)
	internal := http.NewServeMux()
	internal.HandleFunc("/internal", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	internal.Handle("/", public)
	servers := (Config{Mode: Mesh, Port: "8080"}).Servers(public, internal)
	listeners := []net.Listener{listen(t), listen(t)}
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() { result <- runBound(ctx, time.Second, servers, listeners) }()
	t.Cleanup(func() {
		cancel()
		if err := awaitResult(t, result); err != nil {
			t.Error(err)
		}
	})

	for i, listener := range listeners {
		for _, grpc := range []bool{false, true} {
			protocols := new(http.Protocols)
			protocols.SetHTTP1(!grpc)
			protocols.SetUnencryptedHTTP2(grpc)
			transport := &http.Transport{Protocols: protocols}
			t.Cleanup(transport.CloseIdleConnections)
			client := &http.Client{Transport: transport, Timeout: time.Second}
			options := []connect.ClientOption{}
			if grpc {
				options = append(options, connect.WithGRPC())
			}
			health := userv1connect.NewHealthServiceClient(client, "http://"+listener.Addr().String(), options...)
			response, err := health.Check(ctx, connect.NewRequest(&userv1.CheckRequest{}))
			if err != nil || response.Msg.Status != "ok" {
				t.Fatalf("listener=%d grpc=%t response=%v err=%v", i, grpc, response, err)
			}
		}
		request := httptest.NewRequest(http.MethodGet, "/internal", nil)
		response := httptest.NewRecorder()
		servers[i].Handler.ServeHTTP(response, request)
		want := http.StatusNotFound
		if i == 1 {
			want = http.StatusNoContent
		}
		if response.Code != want {
			t.Fatalf("internal route on listener=%d returned %d", i, response.Code)
		}
	}
}

func TestRunRollsBackListenersOnBindFailure(t *testing.T) {
	reserved, occupied := listen(t), listen(t)
	address := reserved.Addr().String()
	if err := reserved.Close(); err != nil {
		t.Fatal(err)
	}
	err := Run(context.Background(), time.Second, New(address, http.NotFoundHandler()), New(occupied.Addr().String(), http.NotFoundHandler()))
	if err == nil {
		t.Fatal("bind failure was ignored")
	}
	listener, err := net.Listen("tcp", address)
	if err != nil {
		t.Fatalf("first listener leaked: %v", err)
	}
	_ = listener.Close()
}

func TestServeFailureStopsSibling(t *testing.T) {
	listeners := []net.Listener{listen(t), listen(t)}
	servers := []*http.Server{New("first", http.NotFoundHandler()), New("second", http.NotFoundHandler())}
	result := make(chan error, 1)
	go func() { result <- runBound(context.Background(), time.Second, servers, listeners) }()
	_ = listeners[0].Close()
	if err := awaitResult(t, result); err == nil {
		t.Fatal("serve failure was ignored")
	}
	connection, err := net.DialTimeout("tcp", listeners[1].Addr().String(), time.Second)
	if err == nil {
		_ = connection.Close()
		t.Fatal("sibling listener still accepts connections")
	}
}

func TestShutdownDrainsActiveRequest(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	releaseRequest := func() { once.Do(func() { close(release) }) }
	t.Cleanup(releaseRequest)
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		select {
		case <-release:
			w.WriteHeader(http.StatusNoContent)
		case <-r.Context().Done():
			t.Error("active request canceled before shutdown grace period")
		}
	})
	listener := listen(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() {
		result <- runBound(ctx, time.Second, []*http.Server{New("test", handler)}, []net.Listener{listener})
	}()
	requestDone := make(chan error, 1)
	go func() {
		client := &http.Client{Timeout: 3 * time.Second}
		response, err := client.Get("http://" + listener.Addr().String())
		if err == nil {
			_ = response.Body.Close()
			if response.StatusCode != http.StatusNoContent {
				err = errors.New("unexpected response status")
			}
		}
		requestDone <- err
	}()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("request did not start")
	}
	cancel()
	select {
	case err := <-result:
		t.Fatalf("shutdown returned with active request: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	releaseRequest()
	if err := awaitResult(t, requestDone); err != nil {
		t.Fatal(err)
	}
	if err := awaitResult(t, result); err != nil {
		t.Fatal(err)
	}
}

func TestShutdownDeadlineClosesConnections(t *testing.T) {
	started := make(chan struct{})
	handler := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		close(started)
		<-r.Context().Done()
	})
	listener := listen(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() {
		result <- runBound(ctx, 20*time.Millisecond, []*http.Server{New("test", handler)}, []net.Listener{listener})
	}()
	requestDone := make(chan error, 1)
	go func() {
		response, err := (&http.Client{Timeout: time.Second}).Get("http://" + listener.Addr().String())
		if err == nil {
			_ = response.Body.Close()
		}
		requestDone <- err
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("request did not start")
	}
	cancel()
	if err := awaitResult(t, result); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("shutdown error=%v", err)
	}
	if err := awaitResult(t, requestDone); err == nil {
		t.Fatal("request survived forced close")
	}
}

func TestRunCanceledBeforeBinding(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := Run(ctx, time.Second, New("invalid address", http.NotFoundHandler())); !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v", err)
	}
}
