package order

import (
	"context"
	"encoding/json"
	"sort"
	"time"

	db "github.com/AY2627S1-CS3219-P1/FoC/order-service/internal/database/orderdb"
	"github.com/google/uuid"
)

// ChangedPayload is captured while the order lock is held. Workflow-only
// changes reuse the observed aggregate revision but have a distinct event ID.
type ChangedPayload struct {
	Recipients []string  `json:"recipients"`
	OpenOrders bool      `json:"open_orders"`
	At         time.Time `json:"at"`
}

func (t *postgresTx) markChanged(id string, open bool) {
	if t.changes == nil {
		t.changes = map[string]bool{}
	}
	t.changes[id] = t.changes[id] || open
}

// Flush once at the shared transaction boundary, including receipt/workflow
// transactions which do not call Apply. Recipient reads follow the parent lock.
func (t *postgresTx) flushChanges(ctx context.Context) error {
	ids := make([]string, 0, len(t.changes))
	for id := range t.changes {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		uid := uuid.MustParse(id)
		row, err := t.q.GetOrder(ctx, uid)
		if err != nil {
			return err
		}
		recipients, err := t.q.ChangeRecipients(ctx, uid)
		if err != nil {
			return err
		}
		at := time.Now().UTC()
		payload := ChangedPayload{OpenOrders: t.changes[id], At: at}
		for _, recipient := range recipients {
			payload.Recipients = append(payload.Recipients, recipient.String())
		}
		sort.Strings(payload.Recipients)
		data, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		if err := t.q.InsertOrderChange(ctx, db.InsertOrderChangeParams{
			EventID: uuid.New(), OrderID: uid, Revision: row.Revision, Payload: data, CreatedAt: at,
		}); err != nil {
			return err
		}
	}
	return nil
}
