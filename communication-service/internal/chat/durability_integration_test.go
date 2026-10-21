package chat

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

func TestPostgresSaveMessageRetryIsIdempotent(t *testing.T) {
	s := newPostgresStore(t)
	ctx := context.Background()
	m := Message{ID: uuid.New(), OrderID: uuid.New(), SenderID: courier, RecipientID: requester, Body: "hi", SentAt: now}
	for range 2 {
		if err := s.SaveMessage(ctx, m); err != nil {
			t.Fatal(err)
		}
	}
	var count int
	if err := s.Pool.QueryRow(ctx, `SELECT count(*) FROM chat_messages WHERE id = $1`, m.ID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("saved records = %d, want 1", count)
	}
}
