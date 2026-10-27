package outbox

import (
	"context"
	"encoding/json"
	"errors"
	app "github.com/AY2627S1-CS3219-P1/FoC/order-service/internal/order"
	"time"

	db "github.com/AY2627S1-CS3219-P1/FoC/order-service/internal/database/orderdb"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Postgres struct{ pool *pgxpool.Pool }

func NewPostgres(pool *pgxpool.Pool) *Postgres { return &Postgres{pool: pool} }

func (r *Postgres) Claim(ctx context.Context, now, until time.Time) (*Fact, error) {
	token := uuid.New()
	q := db.New(r.pool)
	row, err := q.ClaimOrderFact(ctx, db.ClaimOrderFactParams{LeaseToken: &token, LeaseUntil: &until, NowAt: &now})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	actor, err := q.FactActor(ctx, db.FactActorParams{OrderID: row.OrderID, Revision: row.Revision})
	if err != nil {
		return nil, err
	}
	order, err := q.GetOrder(ctx, row.OrderID)
	if err != nil {
		return nil, err
	}
	var pickup app.LocationSnapshot
	if err := json.Unmarshal(order.Pickup, &pickup); err != nil {
		return nil, err
	}
	var actorID *string
	if actor.ActorID != nil {
		value := actor.ActorID.String()
		actorID = &value
	}
	return &Fact{
		ID:         row.EventID.String(),
		OrderID:    row.OrderID.String(),
		Kind:       row.Kind,
		LeaseToken: token.String(),
		Revision:   row.Revision,
		Version:    row.PayloadVersion,
		Payload:    row.Payload,
		ActorKind:  actor.ActorKind,
		PickupName: pickup.Name,
		ActorID:    actorID,
	}, nil
}

func (r *Postgres) Published(ctx context.Context, f Fact, now time.Time) error {
	token := uuid.MustParse(f.LeaseToken)
	n, err := db.New(r.pool).
		MarkOrderFactPublished(ctx, db.MarkOrderFactPublishedParams{EventID: uuid.MustParse(f.ID), LeaseToken: &token, NowAt: &now})
	if err != nil {
		return err
	}
	if n != 1 {
		return errors.New("outbox lease lost before acknowledgement")
	}
	return nil
}

func (r *Postgres) Retry(ctx context.Context, f Fact, next time.Time) error {
	token := uuid.MustParse(f.LeaseToken)
	return db.New(r.pool).
		RetryOrderFact(ctx, db.RetryOrderFactParams{EventID: uuid.MustParse(f.ID), LeaseToken: &token, LeaseUntil: &next})
}
