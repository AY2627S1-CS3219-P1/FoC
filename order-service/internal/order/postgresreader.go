package order

import (
	"context"
	"time"

	db "github.com/AY2627S1-CS3219-P1/FoC/order-service/internal/database/orderdb"
	"github.com/jackc/pgx/v5"
)

// Get reads one order without taking a mutation lock.
func (r *PostgresRepository) Get(ctx context.Context, id string) (Order, error) {
	uid, err := parseID(id)
	if err != nil {
		return Order{}, err
	}

	row, err := db.New(r.pool).GetOrder(ctx, uid)
	if err != nil {
		return Order{}, persistenceError(err)
	}

	return fromOrder(row)
}

// GetWithParticipation keeps the Order and historical membership on one snapshot.
func (r *PostgresRepository) GetWithParticipation(ctx context.Context, id, callerID string) (Order, bool, error) {
	uid, err := parseID(id)
	if err != nil {
		return Order{}, false, err
	}
	caller, err := parseID(callerID)
	if err != nil {
		return Order{}, false, err
	}
	var result Order
	var historical bool
	err = r.snapshot(ctx, func(q *db.Queries) error {
		row, err := q.GetOrder(ctx, uid)
		if err != nil {
			return err
		}
		result, err = fromOrder(row)
		if err != nil {
			return err
		}
		historical, err = q.HasAssignmentHistory(ctx, db.HasAssignmentHistoryParams{OrderID: uid, CourierID: caller})
		return err
	})
	return result, historical, persistenceError(err)
}

// Each list/count and Order/offer read sees one read-only snapshot, without locks.
func (r *PostgresRepository) snapshot(ctx context.Context, fn func(*db.Queries) error) error {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return err
	}
	defer rollback(ctx, tx)

	if err = fn(db.New(tx)); err != nil {
		return err
	}

	return persistenceError(tx.Commit(ctx))
}

// List reads a page and its total from the same snapshot and visibility filters.
func (r *PostgresRepository) List(ctx context.Context, q ReadQuery) (OrderPage, error) {
	p, err := q.ListRequest.Page.Normalize()
	if err != nil {
		return OrderPage{}, err
	}

	caller, err := parseID(q.Caller.ID)
	if err != nil {
		return OrderPage{}, err
	}

	for _, id := range []*string{&q.UserID, &q.ListRequest.PickupID} {
		if *id != "" {
			v, e := parseID(*id)
			if e != nil {
				return OrderPage{}, e
			}
			*id = v.String()
		}
	}

	args := db.ListOrdersParams{
		OpenOnly:             q.Open,
		CallerID:             caller,
		NowAt:                time.Now(),
		UserID:               q.UserID,
		Role:                 string(q.ListRequest.Role),
		StatusFilter:         string(q.ListRequest.Status),
		PickupID:             q.ListRequest.PickupID,
		DeliveryBuildingName: q.ListRequest.DeliveryBuildingName,
		PageSize:             p.Size,
		PageOffset:           int64(p.Number-1) * int64(p.Size),
	}
	out := OrderPage{PageInfo: PageInfo{Page: p}}

	err = r.snapshot(ctx, func(s *db.Queries) error {
		rows, e := s.ListOrders(ctx, args)
		if e != nil {
			return e
		}

		out.Orders = make([]Order, 0, len(rows))
		for _, row := range rows {
			o, e := fromOrder(row)
			if e != nil {
				return e
			}
			out.Orders = append(out.Orders, o)
		}

		out.PageInfo.Total, e = s.CountOrders(ctx, db.CountOrdersParams{
			OpenOnly:             args.OpenOnly,
			CallerID:             args.CallerID,
			NowAt:                args.NowAt,
			UserID:               args.UserID,
			Role:                 args.Role,
			StatusFilter:         args.StatusFilter,
			PickupID:             args.PickupID,
			DeliveryBuildingName: args.DeliveryBuildingName,
		})
		return e
	})

	return out, persistenceError(err)
}

// Offers reads the order and its offers together before applying caller visibility.
func (r *PostgresRepository) Offers(ctx context.Context, c Caller, id string, status OfferStatus) ([]Offer, error) {
	uid, err := parseID(id)
	if err != nil {
		return nil, err
	}

	caller, err := parseID(c.ID)
	if err != nil {
		return nil, err
	}
	c.ID = caller.String()

	out := []Offer{}
	err = r.snapshot(ctx, func(q *db.Queries) error {
		row, e := q.GetOrder(ctx, uid)
		if e != nil {
			return e
		}

		o, e := fromOrder(row)
		if e != nil {
			return e
		}

		rows, e := q.OrderOffers(ctx, uid)
		if e != nil {
			return e
		}

		// A prior offer grants access only to that courier's own offers, including
		// after reassignment. Unknown/invisible orders remain not_found.
		own := false
		for _, v := range rows {
			if v.CourierID.String() == c.ID {
				own = true
			}
		}
		if !c.Admin && !o.HasParticipant(c.ID) && !own && (c.Suspended || o.Status != AwaitingCourier || !o.ExpiresAt.After(time.Now())) {
			return ErrNotFound
		}

		for _, v := range fromOffers(rows) {
			if status != "" && v.Status != status {
				continue
			}
			if c.Admin || o.IsRequestedBy(c.ID) || v.CourierID == c.ID {
				out = append(out, v)
			}
		}

		return nil
	})

	return out, persistenceError(err)
}
