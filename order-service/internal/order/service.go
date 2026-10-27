package order

import (
	"context"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

// Service applies caller visibility rules to order and offer reads.
type Service struct{ reader Reader }

func NewService(r Reader) *Service { return &Service{reader: r} }

// GetOrder reads membership and the aggregate from one snapshot. Historical
// membership is read-only and never widens the aggregate's mutation predicates.
func (s *Service) GetOrder(ctx context.Context, c Caller, id string) (Order, error) {
	if c.ID == "" {
		return Order{}, ErrForbidden
	}
	c = canonicalReadCaller(c)
	o, historical, err := s.reader.GetWithParticipation(ctx, id, c.ID)
	if err != nil {
		return Order{}, err
	}
	if !c.Admin && !o.HasParticipant(c.ID) && !historical &&
		(c.Suspended || o.Status != AwaitingCourier || !o.ExpiresAt.After(time.Now())) {
		return Order{}, ErrNotFound
	}
	return ProjectForCaller(c, o), nil
}

func canonicalReadCaller(c Caller) Caller {
	if id, err := uuid.Parse(c.ID); err == nil {
		c.ID = id.String()
	}
	return c
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

	return s.list(ctx, ReadQuery{Caller: canonicalReadCaller(c), Open: true, ListRequest: r})
}

func (s *Service) ListMyOrders(ctx context.Context, c Caller, r ListRequest) (OrderPage, error) {
	return s.list(ctx, ReadQuery{Caller: canonicalReadCaller(c), UserID: c.ID, ListRequest: r})
}

func (s *Service) ListUserOrders(ctx context.Context, c Caller, id string, r ListRequest) (OrderPage, error) {
	if !c.Admin {
		return OrderPage{}, ErrForbidden
	}
	if id == "" {
		return OrderPage{}, ErrInvalid
	}

	return s.list(ctx, ReadQuery{Caller: canonicalReadCaller(c), UserID: id, ListRequest: r})
}

// list validates filters and applies the same safe projection to every scope.
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

	if q.ListRequest.DeliveryID != "" || utf8.RuneCountInString(q.ListRequest.DeliveryBuildingName) > 200 {
		return OrderPage{}, ErrInvalid
	}
	out, err := s.reader.List(ctx, q)
	if err != nil {
		return OrderPage{}, err
	}
	for i := range out.Orders {
		out.Orders[i] = ProjectForCaller(q.Caller, out.Orders[i])
	}
	return out, nil
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
