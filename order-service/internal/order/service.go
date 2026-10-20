package order

import (
	"context"
	"time"
)

// Service applies caller visibility rules to order and offer reads.
type Service struct{ reader Reader }

func NewService(r Reader) *Service { return &Service{reader: r} }

// GetOrder preserves participant access and hides delivery instructions from
// callers browsing an open order.
func (s *Service) GetOrder(ctx context.Context, c Caller, id string) (Order, error) {
	o, err := s.reader.Get(ctx, id)
	if err != nil {
		return Order{}, err
	}

	if c.ID == "" {
		return Order{}, ErrForbidden
	}
	if c.Admin || o.HasParticipant(c.ID) {
		return o, nil
	}

	if !visibleTo(o, c, time.Now()) {
		return Order{}, ErrNotFound
	}

	o.DeliveryInstructions = nil
	return o, nil
}

// visibleTo reports whether the caller may see the order: admins and
// participants always, other active users only while it is open for couriers.
func visibleTo(o Order, c Caller, now time.Time) bool {
	if c.Admin || o.HasParticipant(c.ID) {
		return true
	}

	return !c.Suspended && o.Status == AwaitingCourier && o.ExpiresAt.After(now)
}

// Order listings select the visible scope before shared validation and redaction.
func (s *Service) ListOpenOrders(ctx context.Context, c Caller, r ListRequest) (OrderPage, error) {
	if c.Suspended {
		return OrderPage{}, ErrForbidden
	}

	return s.list(ctx, ReadQuery{Caller: c, Open: true, ListRequest: r})
}

func (s *Service) ListMyOrders(ctx context.Context, c Caller, r ListRequest) (OrderPage, error) {
	return s.list(ctx, ReadQuery{Caller: c, UserID: c.ID, ListRequest: r})
}

func (s *Service) ListUserOrders(ctx context.Context, c Caller, id string, r ListRequest) (OrderPage, error) {
	if !c.Admin {
		return OrderPage{}, ErrForbidden
	}
	if id == "" {
		return OrderPage{}, ErrInvalid
	}

	return s.list(ctx, ReadQuery{Caller: c, UserID: id, ListRequest: r})
}

// list validates filters and removes participant-only instructions from browsing
// results. Participants and admins retain those instructions.
func (s *Service) list(ctx context.Context, q ReadQuery) (OrderPage, error) {
	if q.Caller.ID == "" {
		return OrderPage{}, ErrForbidden
	}

	p, err := q.ListRequest.Page.Normalize()
	if err != nil {
		return OrderPage{}, err
	}
	q.ListRequest.Page = p
	if q.ListRequest.Status != "" && !q.ListRequest.Status.IsValid() {
		return OrderPage{}, ErrInvalid
	}
	if q.ListRequest.Role != AnyRole && q.ListRequest.Role != RequesterRole && q.ListRequest.Role != CourierRole {
		return OrderPage{}, ErrInvalid
	}

	out, err := s.reader.List(ctx, q)
	if q.Open {
		for i := range out.Orders {
			if !q.Caller.Admin && !out.Orders[i].HasParticipant(q.Caller.ID) {
				out.Orders[i].DeliveryInstructions = nil
			}
		}
	}

	return out, err
}

// ListOffers validates the request before the reader applies relationship-based
// visibility to the offers.
func (s *Service) ListOffers(ctx context.Context, c Caller, id string, status OfferStatus) ([]Offer, error) {
	if c.ID == "" {
		return nil, ErrForbidden
	}
	if status != "" && status != OfferOpen && status != OfferAccepting && status != OfferAccepted && status != OfferVoid {
		return nil, ErrInvalid
	}

	return s.reader.Offers(ctx, c, id, status)
}
