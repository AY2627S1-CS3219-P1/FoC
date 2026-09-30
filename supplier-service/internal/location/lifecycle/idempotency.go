package lifecycle

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

func requestHash(v any) (string, error) {
	b, e := json.Marshal(v)
	if e != nil {
		return "", e
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}

func idempotent(ctx context.Context, tx Tx, c Caller, method, key string, payload any, now time.Time, create func() (string, error)) (string, error) {
	if validID(key) != nil {
		return "", ErrInvalidArgument
	}
	hash, err := requestHash(payload)
	if err != nil {
		return "", err
	}
	scope := IdempotencyScope{Caller: c.ID, Method: method, Key: uuid.MustParse(key).String()}
	record, err := tx.Idempotency(ctx, scope, now)
	if err != nil {
		return "", err
	}
	if record != nil {
		if record.Hash != hash {
			return "", ErrAlreadyExists
		}
		return record.ResourceID, nil
	}
	id, err := create()
	if err != nil {
		return "", err
	}
	err = tx.SaveIdempotency(ctx, scope, IdempotencyRecord{Hash: hash, ResourceID: id, ExpiresAt: now.Add(24 * time.Hour)})
	return id, err
}
