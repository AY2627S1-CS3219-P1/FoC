package events

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	db "github.com/AY2627S1-CS3219-P1/FoC/credit-service/internal/database/creditdb"
	events "github.com/AY2627S1-CS3219-P1/FoC/pkg/kafkagen/credit/events/v1"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type Publisher interface {
	Publish(context.Context, string, []byte, proto.Message) error
}

// AccountPublisher uses the same claim/publish/acknowledge lease pattern as
// Order. A lost acknowledgement republishes the original ledger/event ID.
type AccountPublisher struct {
	Queries   *db.Queries
	Publisher Publisher
}

func NewAccountPublisher(pool *pgxpool.Pool, publisher Publisher) *AccountPublisher {
	return &AccountPublisher{Queries: db.New(pool), Publisher: publisher}
}

func (p *AccountPublisher) Drain(ctx context.Context, limit int) error {
	eligible := time.Now().UTC()
	var failures []error
	for range limit {
		now, token := time.Now().UTC(), uuid.New()
		until := now.Add(30 * time.Second)
		row, err := p.Queries.ClaimAccountEvent(
			ctx,
			db.ClaimAccountEventParams{LeaseToken: &token, LeaseUntil: &until, NowAt: &eligible},
		)
		if errors.Is(err, pgx.ErrNoRows) {
			break
		}
		if err != nil {
			return errors.Join(append(failures, err)...)
		}
		call, cancel := context.WithTimeout(ctx, 5*time.Second)
		err = p.Publisher.Publish(call, "credit.account_changed", []byte(row.UserID.String()), &events.AccountChanged{
			EventId:    row.EventID.String(),
			UserId:     row.UserID.String(),
			Revision:   row.Revision,
			OccurredAt: timestamppb.New(row.OccurredAt),
		})
		cancel()
		if err != nil {
			next := time.Now().UTC().Add(2 * time.Second)
			retryErr := p.Queries.RetryAccountEvent(
				ctx,
				db.RetryAccountEventParams{EventID: row.EventID, LeaseToken: &token, LeaseUntil: &next},
			)
			failures = append(failures, err, retryErr)
			continue
		}
		at := time.Now().UTC()
		n, err := p.Queries.PublishAccountEvent(
			ctx,
			db.PublishAccountEventParams{EventID: row.EventID, LeaseToken: &token, NowAt: &at},
		)
		if err != nil {
			return errors.Join(append(failures, err)...)
		}
		if n != 1 {
			return errors.Join(append(failures, fmt.Errorf("account outbox lease lost: %s", row.EventID))...)
		}
	}
	return errors.Join(failures...)
}

func (p *AccountPublisher) Run(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for ctx.Err() == nil {
		if err := p.Drain(ctx, 50); err != nil && ctx.Err() == nil {
			slog.ErrorContext(ctx, "Account event publication deferred", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
