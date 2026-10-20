package order

import (
	"context"
	"errors"
	"time"
)

const CreateOrderMethod = "/order.v1.OrderService/CreateOrder"

// A missing or inconsistent resource is not an unused key. Keep the mapping
// intact and report an internal integrity failure instead of executing again.
var ErrIdempotencyIntegrity = errors.New("order idempotency resource integrity violation")

type IdempotencyScope struct {
	CallerID string
	Method   string
}

// ResourceID references an operation-owned resource: a creation workflow or an
// offer. The key stores comparison metadata, never workflow progress or results.
type IdempotencyKey struct {
	Scope       IdempotencyScope
	Key         string
	ResourceID  string
	RequestHash string
	ExpiresAt   time.Time
}

func (k IdempotencyKey) CheckRequestHash(hash string) error {
	if k.RequestHash != hash {
		return ErrAlreadyExists
	}
	return nil
}

// Lock a caller/method before reading or changing its keys, and insert the
// resource and key in this same transaction. Lookup does not expire mappings:
// the owning operation must load its resource and decide whether it is resolved
// before deleting an expired key. A missing resource is an integrity failure.
type IdempotencyTx interface {
	LockIdempotencyCaller(context.Context, IdempotencyScope) error
	FindIdempotencyKey(context.Context, IdempotencyScope, string) (*IdempotencyKey, error)
	InsertIdempotencyKey(context.Context, IdempotencyKey) error
	DeleteIdempotencyKey(context.Context, IdempotencyScope, string) error
}

// Creation keeps its resource-specific recovery and retention policy separate
// from the generic key metadata used to compare retry payloads.
type CreationReplay struct {
	Key      IdempotencyKey
	Workflow CreationWorkflow
}

func creationIdempotencyKey(w CreationWorkflow, key string) IdempotencyKey {
	return IdempotencyKey{
		Scope: IdempotencyScope{CallerID: w.RequesterID, Method: CreateOrderMethod},
		Key:   key, ResourceID: w.ID, RequestHash: w.Hash, ExpiresAt: w.CreatedAt.Add(24 * time.Hour),
	}
}
