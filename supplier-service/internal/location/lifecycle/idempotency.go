package lifecycle

import (
	"context"
	"errors"
	"time"

	"github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/idempotency"
)

func (s *Service) idempotent(ctx context.Context, tx Tx, c Caller, method, key string, payload any, create func(time.Time) (string, error)) (string, error) {
	hash, err := idempotency.Hash(payload)
	if err != nil {
		return "", err
	}
	id, err := s.runner.Run(ctx, tx.Idempotency(), idempotency.Scope{Caller: c.ID, Method: method, Key: key}, hash, create)
	switch {
	case errors.Is(err, idempotency.ErrInvalidKey):
		return "", ErrInvalidArgument
	case errors.Is(err, idempotency.ErrConflict):
		return "", ErrAlreadyExists
	default:
		return id, err
	}
}
