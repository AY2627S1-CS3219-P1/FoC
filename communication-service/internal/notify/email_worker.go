package notify

import (
	"context"
	"log/slog"
	"time"

	"github.com/AY2627S1-CS3219-P1/FoC/communication-service/internal/email"
	"github.com/google/uuid"
)

const (
	emailPollInterval = time.Second
	emailRetryDelay   = time.Minute
)

// EmailDelivery is a queued notification whose recipient's email is available.
type EmailDelivery struct {
	EventID uuid.UUID
	UserID  uuid.UUID
	Address string
	Text    string
}

// EmailResult records either success, a permanent rejection, or a retry time.
// An empty Error means success; a nonzero RetryAt means a temporary failure.
type EmailResult struct {
	Error   string
	RetryAt time.Time
}

// PendingEmails holds a row lock while send runs and atomically records its
// result. A callback error rolls back the attempt, including on cancellation.
type PendingEmails interface {
	WithNext(ctx context.Context, at time.Time, send func(EmailDelivery) (EmailResult, error)) (bool, error)
}

// EmailWorker delivers queued fallback independently of the Kafka consumer.
// A missing user email remains pending; it never holds up ready recipients.
type EmailWorker struct {
	Queue  PendingEmails
	Sender email.EmailSender
	Log    *slog.Logger
	Now    func() time.Time
}

// DeliverNext processes one ready notification. False means there is no work
// ready now, including when every pending recipient lacks an email projection.
func (w *EmailWorker) DeliverNext(ctx context.Context) (bool, error) {
	return w.Queue.WithNext(ctx, w.Now(), func(delivery EmailDelivery) (EmailResult, error) {
		err := w.Sender.Send(ctx, email.OrderUpdate(delivery.Address, delivery.Text))
		if ctx.Err() != nil {
			return EmailResult{}, ctx.Err()
		}
		if err == nil {
			return EmailResult{}, nil
		}
		if email.Rejected(err) {
			w.Log.ErrorContext(ctx, "notification email permanently rejected",
				"event_id", delivery.EventID, "user_id", delivery.UserID, "err", err)
			return EmailResult{Error: err.Error()}, nil
		}
		w.Log.WarnContext(ctx, "notification email deferred",
			"event_id", delivery.EventID, "user_id", delivery.UserID, "err", err)
		return EmailResult{Error: err.Error(), RetryAt: w.Now().Add(emailRetryDelay)}, nil
	})
}

// Run drains ready notifications, then polls again until shutdown. Failures
// leave durable work for a later poll or a new process.
func (w *EmailWorker) Run(ctx context.Context) {
	ticker := time.NewTicker(emailPollInterval)
	defer ticker.Stop()
	for ctx.Err() == nil {
		processed, err := w.DeliverNext(ctx)
		if err != nil && ctx.Err() == nil {
			w.Log.ErrorContext(ctx, "process notification email", "err", err)
		}
		if processed && err == nil {
			continue
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
