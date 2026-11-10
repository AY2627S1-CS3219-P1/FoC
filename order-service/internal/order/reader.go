package order

import (
	"context"
	"time"
)

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
	CountByStatus(context.Context) (map[Status]int64, error)
	CountByDay(ctx context.Context, from, to time.Time) (map[Date]int64, error)
}
