package additionrequest

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/idempotency"
	"github.com/jackc/pgx/v5/pgconn"
)

type failingRetryStore struct{ err error }

func (s failingRetryStore) Lock(context.Context, idempotency.Scope) error { return s.err }
func (s failingRetryStore) Find(context.Context, idempotency.Scope, time.Time) (*idempotency.Record, error) {
	return nil, s.err
}
func (s failingRetryStore) Save(context.Context, idempotency.Scope, idempotency.Record) error {
	return s.err
}

func TestRetryStoreRetainsLifecycleErrorMapping(t *testing.T) {
	for _, row := range []struct {
		name         string
		source, want error
	}{
		{"foreign key", &pgconn.PgError{Code: "23503"}, ErrFailedPrecondition},
		{"check", &pgconn.PgError{Code: "23514"}, ErrFailedPrecondition},
		{"unique", &pgconn.PgError{Code: "23505"}, ErrAlreadyExists},
		{"exclusion", &pgconn.PgError{Code: "23P01"}, ErrAlreadyExists},
		{"serialization", &pgconn.PgError{Code: "40001"}, ErrAborted},
		{"canceled", context.Canceled, context.Canceled},
		{"deadline", context.DeadlineExceeded, context.DeadlineExceeded},
	} {
		t.Run(row.name, func(t *testing.T) {
			store := mappedIdempotencyStore{failingRetryStore{fmt.Errorf("shared store: %w", row.source)}}
			scope := idempotency.Scope{Caller: "owner", Method: "CreateDisablement", Key: "1e3f8ee1-1362-4e73-bd7f-2c4b61600b29"}
			find := func() error { _, err := store.Find(context.Background(), scope, time.Now()); return err }
			for name, call := range map[string]func() error{
				"lock": func() error { return store.Lock(context.Background(), scope) },
				"find": find,
				"save": func() error { return store.Save(context.Background(), scope, idempotency.Record{}) },
			} {
				if err := call(); !errors.Is(err, row.want) {
					t.Fatalf("%s: %v, want %v", name, err, row.want)
				}
			}
		})
	}
}
