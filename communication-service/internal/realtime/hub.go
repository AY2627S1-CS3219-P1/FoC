// Package realtime delivers disposable read invalidations to authenticated tabs.
package realtime

import (
	"log/slog"
	"sync"
	"time"

	"github.com/AY2627S1-CS3219-P1/FoC/pkg/auth"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/auth/tokenclaims"
	pb "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/communication/v1"
)

// Notice stays inside Communication's database fanout. Browser responses contain
// only scopes; identity and audience metadata never cross that boundary.
type Notice struct {
	UserID string           `json:"user,omitempty"`
	Admins bool             `json:"admins,omitempty"`
	Public bool             `json:"public,omitempty"`
	Scopes []pb.ChangeScope `json:"scopes"`
}

type subscription struct {
	caller  auth.Caller
	wake    chan struct{}
	done    chan struct{}
	pending map[pb.ChangeScope]bool
	since   time.Time
}

type Hub struct {
	mu      sync.Mutex
	ready   bool
	clients map[*subscription]bool
}

func NewHub() *Hub { return &Hub{clients: map[*subscription]bool{}} }

func (h *Hub) SetReady(ready bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.ready = ready
	if !ready {
		for s := range h.clients {
			close(s.done)
			delete(h.clients, s)
		}
	}
}

func (h *Hub) subscribe(caller auth.Caller) *subscription {
	h.mu.Lock()
	defer h.mu.Unlock()
	if !h.ready {
		return nil
	}
	s := &subscription{
		caller:  caller,
		wake:    make(chan struct{}, 1),
		done:    make(chan struct{}),
		pending: map[pb.ChangeScope]bool{},
	}
	h.clients[s] = true
	slog.Info("Live stream opened", "connections", len(h.clients))
	return s
}

func (h *Hub) remove(s *subscription) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.clients[s] {
		delete(h.clients, s)
		close(s.done)
		slog.Info("Live stream closed", "connections", len(h.clients))
	}
}

func (h *Hub) take(s *subscription) []pb.ChangeScope {
	h.mu.Lock()
	defer h.mu.Unlock()
	scopes := make([]pb.ChangeScope, 0, len(s.pending))
	for _, scope := range []pb.ChangeScope{pb.ChangeScope_CHANGE_SCOPE_CREDITS, pb.ChangeScope_CHANGE_SCOPE_ORDERS, pb.ChangeScope_CHANGE_SCOPE_OPEN_ORDERS} {
		if s.pending[scope] {
			scopes = append(scopes, scope)
		}
	}
	clear(s.pending)
	s.since = time.Time{}
	return scopes
}

func (h *Hub) Notify(n Notice) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for s := range h.clients {
		if !(n.UserID != "" && n.UserID == s.caller.ID || n.Admins && s.caller.Admin || n.Public && s.caller.Role != string(tokenclaims.RoleSuspendedUser)) {
			continue
		}
		if !s.since.IsZero() && time.Since(s.since) > 45*time.Second {
			close(s.done)
			delete(h.clients, s)
			slog.Warn("Stalled live stream disconnected", "connections", len(h.clients))
			continue
		}
		for _, scope := range n.Scopes {
			if scope >= pb.ChangeScope_CHANGE_SCOPE_CREDITS && scope <= pb.ChangeScope_CHANGE_SCOPE_OPEN_ORDERS {
				s.pending[scope] = true
			}
		}
		if len(s.pending) == 0 {
			continue
		}
		if s.since.IsZero() {
			s.since = time.Now()
		}
		select {
		case s.wake <- struct{}{}:
		default:
		}
	}
}
