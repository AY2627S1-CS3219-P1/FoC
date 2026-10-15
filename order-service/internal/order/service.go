package order

import (
	"context"
	"time"
)

type Service struct{ reader Reader }

func NewService(r Reader) *Service { return &Service{reader: r} }
func (s *Service) GetOrder(ctx context.Context, c Caller, id string) (Order, error) {
	o, err := s.reader.Get(ctx, id)
	if err != nil {
		return Order{}, err
	}
	if c.ID == "" {
		return Order{}, ErrForbidden
	}
	if c.Admin || o.Participant(c.ID) {
		return o, nil
	}
	if c.Suspended || o.Status != AwaitingCourier || !o.ExpiresAt.After(time.Now()) {
		return Order{}, ErrNotFound
	}
	o.DeliveryInstructions = nil
	return o, nil
}
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
func (s *Service) list(ctx context.Context, q ReadQuery) (OrderPage, error) {
	if q.Caller.ID == "" {
		return OrderPage{}, ErrForbidden
	}
	p, err := q.ListRequest.Page.Normalize()
	if err != nil {
		return OrderPage{}, err
	}
	q.ListRequest.Page = p
	if q.ListRequest.Status != "" && !q.ListRequest.Status.Valid() {
		return OrderPage{}, ErrInvalid
	}
	if q.ListRequest.Role != AnyRole && q.ListRequest.Role != RequesterRole && q.ListRequest.Role != CourierRole {
		return OrderPage{}, ErrInvalid
	}
	out, err := s.reader.List(ctx, q)
	if q.Open {
		for i := range out.Orders {
			if !q.Caller.Admin && !out.Orders[i].Participant(q.Caller.ID) {
				out.Orders[i].DeliveryInstructions = nil
			}
		}
	}
	return out, err
}
func (s *Service) ListOffers(ctx context.Context, c Caller, id string, status OfferStatus) ([]Offer, error) {
	if c.ID == "" {
		return nil, ErrForbidden
	}
	if status != "" && status != OfferOpen && status != OfferAccepting && status != OfferAccepted && status != OfferVoid {
		return nil, ErrInvalid
	}
	return s.reader.Offers(ctx, c, id, status)
}
