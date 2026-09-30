// Package idempotency implements the shared policy for idempotent operations.
// The caller owns the transaction containing Lock, Find, create, and Save.
package idempotency

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

var (
	ErrInvalidKey = errors.New("invalid idempotency key")
	ErrConflict   = errors.New("idempotency conflict")
)

// Scope has deliberately untagged fields: its JSON encoding is used as a
// structured lock key and must retain the Caller, Method, Key field order.
type Scope struct{ Caller, Method, Key string }

type Record struct {
	Hash, ResourceID string
	ExpiresAt        time.Time
}

// Store operates within the transaction provided by its caller. Find removes
// an expired record only for the locked scope before returning a result.
type Store interface {
	Lock(context.Context, Scope) error
	Find(context.Context, Scope, time.Time) (*Record, error)
	Save(context.Context, Scope, Record) error
}

// Hash retains the JSON and SHA-256 encoding used by the workflow service.
func Hash(v any) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}

type Runner struct{ clock func() time.Time }

func New(clock func() time.Time) *Runner { return &Runner{clock: clock} }

// Run must be called inside the transaction that owns store. The lock is held
// through creation and saving by that transaction.
func (r *Runner) Run(ctx context.Context, store Store, scope Scope, hash string, create func(time.Time) (string, error)) (string, error) {
	key, err := uuid.Parse(scope.Key)
	if err != nil {
		return "", fmt.Errorf("parse idempotency key: %w", ErrInvalidKey)
	}
	scope.Key = key.String()
	if err := store.Lock(ctx, scope); err != nil {
		return "", fmt.Errorf("lock idempotency scope: %w", err)
	}
	now := r.clock().UTC()
	record, err := store.Find(ctx, scope, now)
	if err != nil {
		return "", fmt.Errorf("find idempotency record: %w", err)
	}
	if record != nil {
		if record.Hash != hash {
			return "", ErrConflict
		}
		return record.ResourceID, nil
	}
	id, err := create(now)
	if err != nil {
		return "", fmt.Errorf("create idempotent resource: %w", err)
	}
	if err := store.Save(ctx, scope, Record{Hash: hash, ResourceID: id, ExpiresAt: now.Add(24 * time.Hour)}); err != nil {
		return "", fmt.Errorf("save idempotency record: %w", err)
	}
	return id, nil
}
