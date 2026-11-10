package outbox_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"

	"github.com/AY2627S1-CS3219-P1/FoC/moderation-service/internal/appeal"
	"github.com/AY2627S1-CS3219-P1/FoC/moderation-service/internal/database/outboxdb"
	"github.com/AY2627S1-CS3219-P1/FoC/moderation-service/internal/outbox"
	"github.com/AY2627S1-CS3219-P1/FoC/moderation-service/internal/report"
	"github.com/AY2627S1-CS3219-P1/FoC/moderation-service/internal/testsupport"
	eventsv1 "github.com/AY2627S1-CS3219-P1/FoC/pkg/kafkagen/moderation/events/v1"
)

type sent struct {
	topic string
	key   string
	event *eventsv1.OutcomeRequested
}

type fakePublisher struct {
	mu   sync.Mutex
	sent []sent
	err  error
}

func (p *fakePublisher) Publish(_ context.Context, topic string, key []byte, message proto.Message) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.err != nil {
		return p.err
	}
	p.sent = append(p.sent, sent{topic: topic, key: string(key), event: message.(*eventsv1.OutcomeRequested)})
	return nil
}

func TestRelayPublishesEachOutcomeOnce(t *testing.T) {
	db := testsupport.Database(t)
	ctx := context.Background()
	// Earlier tests may leave unpublished rows; start from a drained outbox.
	if _, err := db.ExecContext(
		ctx,
		"UPDATE outcome_outbox SET published_at = now() WHERE published_at IS NULL",
	); err != nil {
		t.Fatal(err)
	}

	subject, reportID, appealID, actor := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	queries := outboxdb.New(tx)
	if err := outbox.ForReport(queries).Suspend(ctx, report.Suspension{
		UserID: subject, ReportID: reportID, ActorID: actor, Reason: "fraud",
	}); err != nil {
		t.Fatal(err)
	}
	if err := outbox.ForAppeal(queries).Reinstate(ctx, appeal.Reinstatement{
		UserID: subject, ReportID: reportID, AppealID: appealID, ActorID: actor, Reason: "overturned",
	}); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	// A failed publish keeps both outcomes for a later attempt.
	failing := &fakePublisher{err: errors.New("broker down")}
	if err := outbox.NewRelay(db, failing).Drain(ctx, 10); err == nil {
		t.Fatal("expected the failed publish to be reported")
	}

	// Retries are leased for a short while; clear that so the test need not wait.
	if _, err := db.ExecContext(
		ctx,
		"UPDATE outcome_outbox SET lease_token = NULL, lease_until = NULL WHERE published_at IS NULL",
	); err != nil {
		t.Fatal(err)
	}
	publisher := &fakePublisher{}
	relay := outbox.NewRelay(db, publisher)
	if err := relay.Drain(ctx, 10); err != nil {
		t.Fatal(err)
	}
	if err := relay.Drain(ctx, 10); err != nil {
		t.Fatal(err)
	}

	if len(publisher.sent) != 2 {
		t.Fatalf("published %d outcomes, want 2", len(publisher.sent))
	}
	suspend, reinstate := publisher.sent[0], publisher.sent[1]
	suspended := suspend.event.GetSuspendUser().GetUserId()
	if suspend.topic != outbox.Topic || suspend.key != subject.String() ||
		suspended != subject.String() || suspend.event.GetReportId() != reportID.String() {
		t.Fatalf("first outcome = %+v", suspend)
	}
	// Both outcomes about one user share its key, so they stay in order.
	if reinstate.key != subject.String() || reinstate.event.GetAppealId() != appealID.String() ||
		reinstate.event.GetReinstateUser().GetUserId() != subject.String() {
		t.Fatalf("second outcome = %+v", reinstate)
	}
	if suspend.event.GetEventId() == reinstate.event.GetEventId() {
		t.Fatal("outcomes share an event ID")
	}
}
