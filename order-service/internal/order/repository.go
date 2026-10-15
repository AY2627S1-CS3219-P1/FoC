package order

import "context"

// Within uses READ COMMITTED, commits only on nil, never retries the callback,
// and rolls back on errors or panic. Callbacks must not perform network calls.
type Repository interface {
	Within(context.Context, func(Tx) error) error
}
type Tx interface {
	LockOrder(context.Context, string) (Order, error)
	// Relationships is a separate statement AFTER LockOrder completes.
	Relationships(context.Context, string) ([]Offer, error)
	Apply(context.Context, Change) error
}
type OfferChange struct {
	Offer          Offer
	ExpectedStatus OfferStatus
}
type Change struct {
	ExpectedRevision int64
	Order            Order
	Offers           []OfferChange
	Actor            Actor
	Action           Action
	Audit            AuditEntry
	Facts            []Fact
}
