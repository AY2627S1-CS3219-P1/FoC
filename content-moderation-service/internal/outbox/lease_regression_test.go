//go:build integration

package outbox

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/AY2627S1-CS3219-P1/FoC/content-moderation-service/database"
	"github.com/AY2627S1-CS3219-P1/FoC/content-moderation-service/internal/database/moderationdb"
	"github.com/google/uuid"
)

func TestRelayWritesRejectLeaseExpiryDuringRowLockWait(t *testing.T) {
	for _, operation := range []string{"published", "release"} {
		t.Run(operation, func(t *testing.T) {
			url := os.Getenv("TEST_DATABASE_URL")
			if url == "" {
				t.Skip("TEST_DATABASE_URL not set")
			}
			ctx := context.Background()
			if err := database.Migrate(ctx, url); err != nil {
				t.Fatal(err)
			}
			pool := database.Connect(url)
			defer pool.Close()
			q := moderationdb.New(pool)
			job, err := q.InsertJob(
				ctx,
				moderationdb.InsertJobParams{
					ModerationRequestID: uuid.New(),
					SourceEventID:       uuid.New(),
					ReviewID:            uuid.New(),
					ReviewerID:          uuid.New(),
					ContentRevision:     1,
					Comment:             "lease",
					RunGeneration:       1,
					Status:              "queued",
				},
			)
			if err != nil {
				t.Fatal(err)
			}
			eventID, token := uuid.New(), uuid.New()
			defer func() {
				_, _ = pool.Exec(ctx, `DELETE FROM moderation_outbox WHERE job_id=$1`, job.ID)
				_, _ = pool.Exec(ctx, `DELETE FROM moderation_jobs WHERE id=$1`, job.ID)
			}()
			if _, err := pool.Exec(
				ctx,
				`INSERT INTO moderation_outbox(event_id,job_id,run_generation,payload_version,payload,available_at,lease_token,lease_until) VALUES($1,$2,1,1,'{}',now(),$3,clock_timestamp()+interval '400 milliseconds')`,
				eventID,
				job.ID,
				token,
			); err != nil {
				t.Fatal(err)
			}
			locker, err := pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = locker.Rollback(ctx) }()
			if _, err := locker.Exec(
				ctx,
				`SELECT event_id FROM moderation_outbox WHERE event_id=$1 FOR UPDATE`,
				eventID,
			); err != nil {
				t.Fatal(err)
			}
			relay := NewRelay(pool, nil, RelayConfig{}, nil)
			done := make(chan error, 1)
			go func() {
				if operation == "published" {
					done <- relay.markPublished(ctx, eventID, &token)
				} else {
					done <- relay.releaseLease(ctx, eventID, &token)
				}
			}()
			deadline := time.Now().Add(time.Second)
			for {
				var waiting bool
				if err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND pid<>pg_backend_pid() AND state='active' AND wait_event_type='Lock' AND query LIKE '%moderation_outbox%')`).
					Scan(&waiting); err != nil {
					t.Fatal(err)
				}
				if waiting {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("relay did not wait on row lock")
				}
				time.Sleep(time.Millisecond)
			}
			time.Sleep(450 * time.Millisecond)
			if err := locker.Commit(ctx); err != nil {
				t.Fatal(err)
			}
			if err := <-done; !errors.Is(err, ErrFenced) {
				t.Fatalf("expired relay %s accepted: %v", operation, err)
			}
			var unchanged bool
			if err := pool.QueryRow(ctx, `SELECT published_at IS NULL AND lease_token=$2 FROM moderation_outbox WHERE event_id=$1`, eventID, token).
				Scan(&unchanged); err != nil ||
				!unchanged {
				t.Fatalf("expired relay changed durable state: %v,%v", unchanged, err)
			}
		})
	}
}
