package realtime

import (
	"context"
	"errors"

	pb "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/communication/v1"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/kafka"
	credits "github.com/AY2627S1-CS3219-P1/FoC/pkg/kafkagen/credit/events/v1"
	orders "github.com/AY2627S1-CS3219-P1/FoC/pkg/kafkagen/order/events/v1"
	"github.com/google/uuid"
)

type Consumer struct {
	Fanout interface {
		Publish(context.Context, Notice) error
	}
}

func (c *Consumer) Account(ctx context.Context, _ kafka.Metadata, e *credits.AccountChanged) error {
	if _, err := uuid.Parse(e.UserId); err != nil {
		return errors.New("invalid account event user")
	}
	return c.Fanout.Publish(
		ctx,
		Notice{UserID: e.UserId, Scopes: []pb.ChangeScope{pb.ChangeScope_CHANGE_SCOPE_CREDITS}},
	)
}

func (c *Consumer) Order(ctx context.Context, _ kafka.Metadata, e *orders.OrderChanged) error {
	seen := map[string]bool{}
	for _, id := range e.RecipientUserIds {
		if _, err := uuid.Parse(id); err != nil {
			return errors.New("invalid order event recipient")
		}
		if seen[id] {
			continue
		}
		seen[id] = true
		if err := c.Fanout.Publish(
			ctx,
			Notice{UserID: id, Scopes: []pb.ChangeScope{pb.ChangeScope_CHANGE_SCOPE_ORDERS}},
		); err != nil {
			return err
		}
	}
	if err := c.Fanout.Publish(
		ctx,
		Notice{Admins: true, Scopes: []pb.ChangeScope{pb.ChangeScope_CHANGE_SCOPE_ORDERS}},
	); err != nil {
		return err
	}
	if e.OpenOrdersChanged {
		return c.Fanout.Publish(
			ctx,
			Notice{Public: true, Scopes: []pb.ChangeScope{pb.ChangeScope_CHANGE_SCOPE_OPEN_ORDERS}},
		)
	}
	return nil
}
