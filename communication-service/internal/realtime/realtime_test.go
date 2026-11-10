package realtime

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/AY2627S1-CS3219-P1/FoC/communication-service/internal/router"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/auth"
	pb "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/communication/v1"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/communication/v1/communicationv1connect"
)

func TestRecipientsCoalescingAndListenerLoss(t *testing.T) {
	h := NewHub()
	h.SetReady(true)
	alice := h.subscribe(auth.Caller{ID: "alice"})
	bob := h.subscribe(auth.Caller{ID: "bob"})
	admin := h.subscribe(auth.Caller{ID: "admin", Admin: true})
	suspended := h.subscribe(auth.Caller{ID: "suspended", Role: "suspended_user"})
	for range 100 {
		h.Notify(Notice{UserID: "alice", Scopes: []pb.ChangeScope{pb.ChangeScope_CHANGE_SCOPE_CREDITS}})
	}
	if len(alice.wake) != 1 || len(alice.pending) != 1 || len(bob.pending) != 0 {
		t.Fatal("private hints did not stay bounded and isolated")
	}
	h.Notify(Notice{Public: true, Scopes: []pb.ChangeScope{pb.ChangeScope_CHANGE_SCOPE_OPEN_ORDERS}})
	h.Notify(Notice{Admins: true, Scopes: []pb.ChangeScope{pb.ChangeScope_CHANGE_SCOPE_ORDERS}})
	if len(h.take(admin)) != 2 || len(h.take(bob)) != 1 || len(h.take(suspended)) != 0 {
		t.Fatal("audience filtering failed")
	}
	h.Notify(Notice{UserID: "suspended", Scopes: []pb.ChangeScope{pb.ChangeScope_CHANGE_SCOPE_ORDERS}})
	if len(h.take(suspended)) != 1 {
		t.Fatal("suspended participants still receive their own updates")
	}
	alice.since = time.Now().Add(-time.Minute)
	h.Notify(Notice{UserID: "alice", Scopes: []pb.ChangeScope{pb.ChangeScope_CHANGE_SCOPE_CREDITS}})
	select {
	case <-alice.done:
	default:
		t.Fatal("stalled subscription retained")
	}
	h.SetReady(false)
	select {
	case <-bob.done:
	default:
		t.Fatal("listener failure did not force reconnection")
	}
	if h.subscribe(auth.Caller{ID: "late"}) != nil {
		t.Fatal("subscribed before listener was ready")
	}
}

func streamClient(t *testing.T, h *Hub, caller auth.Caller) communicationv1connect.RealtimeServiceClient {
	t.Helper()
	authenticate := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if caller.ID != "" {
				r = r.WithContext(auth.WithCaller(r.Context(), caller))
			}
			next.ServeHTTP(w, r)
		})
	}
	server := httptest.NewServer(router.New(router.Services{
		Health:       &communicationv1connect.UnimplementedHealthServiceHandler{},
		TelegramLink: &communicationv1connect.UnimplementedTelegramLinkServiceHandler{},
		Realtime:     &Handler{Hub: h},
	}, authenticate))
	t.Cleanup(server.Close)
	return communicationv1connect.NewRealtimeServiceClient(server.Client(), server.URL)
}

func TestStreamReadyChangesAndTokenExpiry(t *testing.T) {
	h := NewHub()
	h.SetReady(true)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client := streamClient(t, h, auth.Caller{ID: "alice", ExpiresAt: time.Now().Add(150 * time.Millisecond)})
	stream, err := client.WatchChanges(ctx, connect.NewRequest(&pb.WatchChangesRequest{}))
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close() //nolint:errcheck
	if !stream.Receive() || !stream.Msg().Ready {
		t.Fatalf("missing ready: %v", stream.Err())
	}
	h.Notify(Notice{UserID: "alice", Scopes: []pb.ChangeScope{pb.ChangeScope_CHANGE_SCOPE_CREDITS}})
	if !stream.Receive() || len(stream.Msg().Scopes) != 1 {
		t.Fatalf("missing change: %v", stream.Err())
	}
	if stream.Receive() || connect.CodeOf(stream.Err()) != connect.CodeUnauthenticated {
		t.Fatalf("expiry = %v", stream.Err())
	}
}

func TestStreamRequiresAuthenticationAndListener(t *testing.T) {
	for _, caller := range []auth.Caller{{}, {ID: "alice"}} {
		client := streamClient(t, NewHub(), caller)
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		stream, err := client.WatchChanges(ctx, connect.NewRequest(&pb.WatchChangesRequest{}))
		if err == nil {
			stream.Receive()
			err = stream.Err()
			_ = stream.Close()
		}
		cancel()
		want := connect.CodeUnavailable
		if caller.ID == "" {
			want = connect.CodeUnauthenticated
		}
		if connect.CodeOf(err) != want {
			t.Fatalf("caller %q: %v", caller.ID, err)
		}
	}
}
