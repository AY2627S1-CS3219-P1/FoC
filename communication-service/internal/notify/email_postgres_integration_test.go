package notify

import (
	"context"
	"errors"
	"fmt"
	"net/textproto"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/AY2627S1-CS3219-P1/FoC/communication-service/internal/database"
	db "github.com/AY2627S1-CS3219-P1/FoC/communication-service/internal/database/communicationdb"
	"github.com/AY2627S1-CS3219-P1/FoC/communication-service/internal/email"
	"github.com/AY2627S1-CS3219-P1/FoC/communication-service/internal/users"
	"github.com/AY2627S1-CS3219-P1/FoC/communication-service/migrations"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/kafka"
	usereventsv1 "github.com/AY2627S1-CS3219-P1/FoC/pkg/kafkagen/users/events/v1"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// Each test owns a schema so other packages can exercise their truncation and
// migration tests against the same throwaway database without erasing its work.
func openEmailPostgres(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	admin, err := database.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	schema := "email_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = admin.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE")
		admin.Close()
	})
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := database.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	return pool
}

type mailerFunc func(context.Context, email.Email) error

func (f mailerFunc) Send(ctx context.Context, message email.Email) error { return f(ctx, message) }

func enqueueEmail(t *testing.T, queue *PostgresEmailQueue, userID uuid.UUID, body string, at time.Time) uuid.UUID {
	t.Helper()
	eventID := uuid.New()
	if err := queue.Enqueue(context.Background(), eventID, userID, body, at); err != nil {
		t.Fatal(err)
	}
	return eventID
}

func projectEmail(t *testing.T, pool *pgxpool.Pool, userID uuid.UUID, address string) {
	t.Helper()
	projection := &users.Projection{Store: &users.PostgresStore{Queries: db.New(pool)}}
	if err := projection.Handle(context.Background(), kafka.Metadata{}, &usereventsv1.UserCreated{
		UserId: userID.String(), Email: address, CreatedAt: timestamppb.New(now),
	}); err != nil {
		t.Fatal(err)
	}
}

func TestQueuedEmailSurvivesRestartAndLaterUserProjection(t *testing.T) {
	pool := openEmailPostgres(t)
	ctx := context.Background()
	handler, telegram, links, _ := newHandler()
	delete(links.chats, requesterID)
	handler.Deliveries = &PostgresDeliveries{Queries: db.New(pool)}
	handler.EmailQueue = &PostgresEmailQueue{Pool: pool}
	if err := handler.Handle(ctx, kafka.Metadata{}, outcome()); err != nil {
		t.Fatalf("outcome must be accepted before the user event: %v", err)
	}

	// Reopen the database and reconstruct every delivery dependency. No
	// in-memory continuation or replay of the outcome is needed to deliver it.
	cfg := pool.Config().Copy()
	pool.Close()
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	queue := &PostgresEmailQueue{Pool: pool}
	mailer := &fakeMailer{}
	worker := &EmailWorker{Queue: queue, Sender: mailer, Log: handler.Log, Now: handler.Now}
	if processed, err := worker.DeliverNext(ctx); err != nil || processed {
		t.Fatalf("missing email must remain pending: processed=%v err=%v", processed, err)
	}

	// Linking Telegram after acceptance must not redirect a replay to a second
	// channel. The queued email remains the accepted delivery for this event.
	links.chats[requesterID] = 3
	handler.Deliveries = &PostgresDeliveries{Queries: db.New(pool)}
	handler.EmailQueue = queue
	if err := handler.Handle(ctx, kafka.Metadata{}, outcome()); err != nil || len(telegram.sent) != 1 {
		t.Fatalf("replay of queued delivery: telegram=%+v err=%v", telegram.sent, err)
	}
	projectEmail(t, pool, requesterID, "alice@foc.local")
	if processed, err := worker.DeliverNext(ctx); err != nil || !processed {
		t.Fatalf("later user event must unblock email: processed=%v err=%v", processed, err)
	}
	if len(mailer.sent) != 1 || mailer.sent[0].To[0] != "alice@foc.local" ||
		!strings.HasPrefix(mailer.sent[0].TextBody, "Your order from Fine Food is complete.") {
		t.Fatalf("emails = %+v", mailer.sent)
	}
	var pending, sent int
	if err := pool.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM notification_emails),
		(SELECT count(*) FROM order_notifications_sent WHERE user_id = $1)`, requesterID).Scan(&pending, &sent); err != nil || pending != 0 || sent != 1 {
		t.Fatalf("pending=%d sent=%d err=%v", pending, sent, err)
	}
	if err := handler.Handle(ctx, kafka.Metadata{}, outcome()); err != nil {
		t.Fatal(err)
	}
	if err := queue.Enqueue(ctx, eventID, requesterID, "replay", now); err != nil {
		t.Fatal(err)
	}
	if processed, err := worker.DeliverNext(ctx); err != nil || processed || len(mailer.sent) != 1 {
		t.Fatalf("completed delivery was repeated: processed=%v emails=%+v err=%v", processed, mailer.sent, err)
	}
}

func TestEmailWorkerSkipsUnknownUsersAndRetriesWithoutBlockingOthers(t *testing.T) {
	pool := openEmailPostgres(t)
	ctx := context.Background()
	queue := &PostgresEmailQueue{Pool: pool}
	unknown, unavailable, rejected, ready := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	enqueueEmail(t, queue, unknown, "no projected email", now.Add(-4*time.Second))
	failedEvent := enqueueEmail(t, queue, unavailable, "retry", now.Add(-3*time.Second))
	rejectedEvent := enqueueEmail(t, queue, rejected, "rejected", now.Add(-2*time.Second))
	enqueueEmail(t, queue, ready, "ready", now.Add(-time.Second))
	for userID, address := range map[uuid.UUID]string{
		unavailable: "retry@foc.local", rejected: "rejected@foc.local", ready: "ready@foc.local",
	} {
		projectEmail(t, pool, userID, address)
	}
	var delivered []string
	recovered := false
	handler, _, _, _ := newHandler()
	worker := &EmailWorker{Queue: queue, Log: handler.Log, Now: handler.Now, Sender: mailerFunc(
		func(_ context.Context, message email.Email) error {
			switch message.To[0] {
			case "retry@foc.local":
				if !recovered {
					return errors.New("connection refused")
				}
			case "rejected@foc.local":
				return &textproto.Error{Code: 550, Msg: "unknown recipient"}
			}
			delivered = append(delivered, message.To[0])
			return nil
		})}
	for range 3 {
		if processed, err := worker.DeliverNext(ctx); err != nil || !processed {
			t.Fatalf("processed=%v err=%v", processed, err)
		}
	}
	if len(delivered) != 1 || delivered[0] != "ready@foc.local" {
		t.Fatalf("another recipient was blocked: %v", delivered)
	}
	if processed, err := worker.DeliverNext(ctx); err != nil || processed {
		t.Fatalf("retry must wait: processed=%v err=%v", processed, err)
	}
	var status, lastError string
	var attempts int
	if err := pool.QueryRow(ctx, `SELECT status, attempts, last_error FROM notification_emails
		WHERE event_id = $1`, rejectedEvent).Scan(&status, &attempts, &lastError); err != nil || status != "rejected" || attempts != 1 || !strings.Contains(lastError, "550") {
		t.Fatalf("rejection not recorded: status=%q attempts=%d error=%q err=%v", status, attempts, lastError, err)
	}
	if err := queue.Enqueue(ctx, rejectedEvent, rejected, "replay must not replace rejection", now); err != nil {
		t.Fatal(err)
	}
	if accepted, err := (&PostgresDeliveries{Queries: db.New(pool)}).HasBeenAccepted(
		ctx,
		rejectedEvent,
		rejected,
	); err != nil ||
		!accepted {
		t.Fatalf("rejected delivery must remain accepted: %v, %v", accepted, err)
	}
	recovered = true
	worker.Now = func() time.Time { return now.Add(emailRetryDelay) }
	if processed, err := worker.DeliverNext(ctx); err != nil || !processed || len(delivered) != 2 {
		t.Fatalf("retry after recovery: processed=%v delivered=%v err=%v", processed, delivered, err)
	}
	var remaining int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM notification_emails WHERE event_id = $1`, failedEvent).
		Scan(&remaining); err != nil || remaining != 0 {
		t.Fatalf("successful retry is still pending: %d, %v", remaining, err)
	}
}

func TestEmailWorkersClaimDifferentRows(t *testing.T) {
	pool := openEmailPostgres(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	queue := &PostgresEmailQueue{Pool: pool}
	for i, userID := range []uuid.UUID{uuid.New(), uuid.New()} {
		projectEmail(t, pool, userID, "alice@foc.local")
		enqueueEmail(t, queue, userID, string(rune('a'+i)), now)
	}
	claimed := make(chan string, 1)
	release := make(chan struct{})
	unblock := sync.OnceFunc(func() { close(release) })
	defer unblock()
	result := make(chan error, 1)
	handler, _, _, _ := newHandler()
	first := &EmailWorker{Queue: queue, Log: handler.Log, Now: handler.Now, Sender: mailerFunc(
		func(ctx context.Context, message email.Email) error {
			claimed <- message.TextBody
			select {
			case <-release:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		})}
	go func() { _, err := first.DeliverNext(ctx); result <- err }()
	var firstBody string
	select {
	case firstBody = <-claimed:
	case <-ctx.Done():
		t.Fatal("first worker did not claim a row")
	}
	mailer := &fakeMailer{}
	second := &EmailWorker{Queue: queue, Sender: mailer, Log: handler.Log, Now: handler.Now}
	if processed, err := second.DeliverNext(ctx); err != nil || !processed || len(mailer.sent) != 1 ||
		mailer.sent[0].TextBody == firstBody {
		t.Fatalf("second worker repeated or waited for the locked row: processed=%v emails=%+v err=%v",
			processed, mailer.sent, err)
	}
	if processed, err := second.DeliverNext(ctx); err != nil || processed {
		t.Fatalf("locked row must be skipped: processed=%v err=%v", processed, err)
	}
	unblock()
	select {
	case err := <-result:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("first worker did not complete")
	}
}

func TestEmailCompletionRollsBackAsOneTransaction(t *testing.T) {
	pool := openEmailPostgres(t)
	ctx := context.Background()
	queue := &PostgresEmailQueue{Pool: pool}
	userID := uuid.New()
	projectEmail(t, pool, userID, "alice@foc.local")
	eventID := enqueueEmail(t, queue, userID, "outcome", now)
	if _, err := pool.Exec(ctx, `CREATE FUNCTION fail_email_delete() RETURNS trigger LANGUAGE plpgsql AS
		$$ BEGIN RAISE EXCEPTION 'simulated completion failure'; END $$;
		CREATE TRIGGER fail_delete BEFORE DELETE ON notification_emails
		FOR EACH ROW EXECUTE FUNCTION fail_email_delete()`); err != nil {
		t.Fatal(err)
	}
	handler, _, _, _ := newHandler()
	worker := &EmailWorker{Queue: queue, Sender: &fakeMailer{}, Log: handler.Log, Now: handler.Now}
	if _, err := worker.DeliverNext(ctx); err == nil {
		t.Fatal("completion failure was not returned")
	}
	var queued, sent int
	if err := pool.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM notification_emails WHERE event_id = $1),
		(SELECT count(*) FROM order_notifications_sent WHERE event_id = $1)`, eventID).Scan(&queued, &sent); err != nil || queued != 1 || sent != 0 {
		t.Fatalf("partial completion committed: queued=%d sent=%d err=%v", queued, sent, err)
	}
}

func TestConcurrentEnqueueDuringEmailCompletionDoesNotResend(t *testing.T) {
	pool := openEmailPostgres(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	queue := &PostgresEmailQueue{Pool: pool}
	userID := uuid.New()
	projectEmail(t, pool, userID, "alice@foc.local")
	eventID := enqueueEmail(t, queue, userID, "outcome", now)

	// Pause completion after DELETE has marked the row, but before COMMIT.
	// The replay's INSERT then reads the old sent-record snapshot and waits on
	// that deletion's unique-key conflict, exactly as during a group rebalance.
	control, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer control.Release()
	lockID := time.Now().UnixNano()
	if _, err := control.Exec(ctx, "SELECT pg_advisory_lock($1)", lockID); err != nil {
		t.Fatal(err)
	}
	unblock := sync.OnceFunc(func() {
		_, _ = control.Exec(context.Background(), "SELECT pg_advisory_unlock($1)", lockID)
	})
	defer unblock()
	if _, err := pool.Exec(
		ctx,
		fmt.Sprintf(`CREATE FUNCTION pause_email_completion() RETURNS trigger LANGUAGE plpgsql AS
		$$ BEGIN PERFORM pg_advisory_xact_lock(%d); RETURN OLD; END $$;
		CREATE TRIGGER pause_completion AFTER DELETE ON notification_emails
		FOR EACH ROW EXECUTE FUNCTION pause_email_completion()`, lockID),
	); err != nil {
		t.Fatal(err)
	}
	awaitBlocked := func(description string, blocked func() bool) {
		t.Helper()
		ticker := time.NewTicker(10 * time.Millisecond)
		defer ticker.Stop()
		for !blocked() {
			select {
			case <-ctx.Done():
				t.Fatalf("timed out waiting for %s", description)
			case <-ticker.C:
			}
		}
	}
	handler, _, _, _ := newHandler()
	mailer := &fakeMailer{}
	worker := &EmailWorker{Queue: queue, Sender: mailer, Log: handler.Log, Now: handler.Now}
	completed := make(chan error, 1)
	go func() { _, err := worker.DeliverNext(ctx); completed <- err }()
	awaitBlocked("worker completion", func() bool {
		var blocked bool
		err := pool.QueryRow(ctx, `SELECT EXISTS (
			SELECT 1 FROM pg_stat_activity WHERE $1 = ANY(pg_blocking_pids(pid)))`, control.Conn().PgConn().PID()).
			Scan(&blocked)
		return err == nil && blocked
	})
	replay, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer replay.Release()
	enqueued := make(chan error, 1)
	go func() {
		enqueued <- db.New(replay).EnqueueNotificationEmail(ctx, db.EnqueueNotificationEmailParams{
			EventID: eventID, UserID: userID, Body: "outcome", CreatedAt: now,
		})
	}()
	awaitBlocked("concurrent replay insertion", func() bool {
		var blocked bool
		err := pool.QueryRow(ctx, "SELECT cardinality(pg_blocking_pids($1)) > 0", replay.Conn().PgConn().PID()).
			Scan(&blocked)
		return err == nil && blocked
	})
	unblock()
	for _, result := range []<-chan error{completed, enqueued} {
		if err := <-result; err != nil {
			t.Fatal(err)
		}
	}
	if _, err := worker.DeliverNext(ctx); err != nil {
		t.Fatal(err)
	}
	if len(mailer.sent) != 1 {
		t.Fatalf("concurrent replay sent the same email %d times", len(mailer.sent))
	}
}

func TestEmailAttemptCancellationLeavesWorkPending(t *testing.T) {
	pool := openEmailPostgres(t)
	queue := &PostgresEmailQueue{Pool: pool}
	userID := uuid.New()
	projectEmail(t, pool, userID, "alice@foc.local")
	enqueueEmail(t, queue, userID, "outcome", now)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	handler, _, _, _ := newHandler()
	worker := &EmailWorker{Queue: queue, Log: handler.Log, Now: handler.Now, Sender: mailerFunc(
		func(context.Context, email.Email) error { cancel(); return context.Canceled })}
	if _, err := worker.DeliverNext(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation = %v", err)
	}
	worker.Sender = &fakeMailer{}
	if processed, err := worker.DeliverNext(context.Background()); err != nil || !processed {
		t.Fatalf("cancelled attempt was lost: processed=%v err=%v", processed, err)
	}
}

func TestNotificationEmailMigrationDownAndUp(t *testing.T) {
	pool := openEmailPostgres(t)
	sqlDB := stdlib.OpenDBFromPool(pool)
	t.Cleanup(func() { _ = sqlDB.Close() })
	provider, err := goose.NewProvider(goose.DialectPostgres, sqlDB, migrations.FS)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := provider.DownTo(ctx, 5); err != nil {
		t.Fatal(err)
	}
	if _, err := provider.Up(ctx); err != nil {
		t.Fatal(err)
	}
	enqueueEmail(t, &PostgresEmailQueue{Pool: pool}, uuid.New(), "after migration", now)
}

func TestConcurrentEmailMigrations(t *testing.T) {
	pool := openEmailPostgres(t)
	sqlDB := stdlib.OpenDBFromPool(pool)
	t.Cleanup(func() { _ = sqlDB.Close() })
	provider, err := goose.NewProvider(goose.DialectPostgres, sqlDB, migrations.FS)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if _, err := provider.DownTo(ctx, 5); err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	results := make(chan error, 3)
	for range 3 {
		go func() {
			<-start
			results <- database.Migrate(ctx, pool)
		}()
	}
	close(start)
	for range 3 {
		if err := <-results; err != nil {
			t.Fatalf("concurrent upgrade failed: %v", err)
		}
	}
	enqueueEmail(t, &PostgresEmailQueue{Pool: pool}, uuid.New(), "after concurrent upgrades", now)
}
