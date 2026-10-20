package order

import "context"

type ReadQuery struct {
	Caller      Caller
	UserID      string
	Open        bool
	ListRequest ListRequest
}

// Reader provides non-locking, consistent snapshots for every multi-query call.
type Reader interface {
	GetWithParticipation(context.Context, string, string) (Order, bool, error)
	List(context.Context, ReadQuery) (OrderPage, error)
	Offers(context.Context, Caller, string, OfferStatus) ([]Offer, error)
}
