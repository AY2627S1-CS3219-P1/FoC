package order

import (
	"context"
	"errors"
	"strings"

	db "github.com/AY2627S1-CS3219-P1/FoC/order-service/internal/database/orderdb"
	"github.com/jackc/pgx/v5"
)

func canonicalIdempotencyScope(scope IdempotencyScope) (IdempotencyScope, error) {
	caller, err := parseID(scope.CallerID)
	if err != nil || strings.TrimSpace(scope.Method) == "" {
		return IdempotencyScope{}, ErrInvalid
	}
	scope.CallerID = caller.String()
	return scope, nil
}

func (t *postgresTx) LockIdempotencyCaller(ctx context.Context, scope IdempotencyScope) error {
	scope, err := canonicalIdempotencyScope(scope)
	if err != nil {
		return err
	}
	if err := t.writeResult(
		t.q.LockOrderIdempotencyCaller(ctx, "order.idempotency:"+scope.CallerID+":"+scope.Method),
	); err != nil {
		return err
	}
	if t.idempotencyCallers == nil {
		t.idempotencyCallers = map[IdempotencyScope]bool{}
	}
	t.idempotencyCallers[scope] = true
	return nil
}

func (t *postgresTx) FindIdempotencyKey(
	ctx context.Context,
	scope IdempotencyScope,
	key string,
) (*IdempotencyKey, error) {
	scope, err := canonicalIdempotencyScope(scope)
	if err != nil {
		return nil, err
	}
	caller, _ := parseID(scope.CallerID)
	id, err := parseID(key)
	if err != nil {
		return nil, err
	}
	if !t.idempotencyCallers[scope] {
		return nil, ErrPrecondition
	}
	row, err := t.q.GetOrderIdempotencyKey(
		ctx,
		db.GetOrderIdempotencyKeyParams{CallerID: caller, Method: scope.Method, IdempotencyKey: id},
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &IdempotencyKey{
		Scope: IdempotencyScope{CallerID: row.CallerID.String(), Method: row.Method},
		Key:   row.IdempotencyKey.String(), ResourceID: row.ResourceID.String(),
		RequestHash: row.RequestHash, ExpiresAt: row.ExpiresAt,
	}, nil
}

func (t *postgresTx) InsertIdempotencyKey(ctx context.Context, key IdempotencyKey) error {
	scope, err := canonicalIdempotencyScope(key.Scope)
	if err != nil {
		return t.writeResult(err)
	}
	caller, _ := parseID(scope.CallerID)
	id, err := parseID(key.Key)
	if err != nil {
		return t.writeResult(err)
	}
	resource, err := parseID(key.ResourceID)
	if err != nil {
		return t.writeResult(err)
	}
	if !t.idempotencyCallers[scope] || len(key.RequestHash) != 64 {
		return t.writeResult(ErrPrecondition)
	}
	return t.writeResult(t.q.InsertOrderIdempotencyKey(ctx, db.InsertOrderIdempotencyKeyParams{
		CallerID: caller, Method: scope.Method, IdempotencyKey: id,
		ResourceID: resource, RequestHash: key.RequestHash, ExpiresAt: key.ExpiresAt,
	}))
}

func (t *postgresTx) DeleteIdempotencyKey(ctx context.Context, scope IdempotencyScope, key string) error {
	scope, err := canonicalIdempotencyScope(scope)
	if err != nil {
		return t.writeResult(err)
	}
	caller, _ := parseID(scope.CallerID)
	id, err := parseID(key)
	if err != nil {
		return t.writeResult(err)
	}
	if !t.idempotencyCallers[scope] {
		return t.writeResult(ErrPrecondition)
	}
	return t.writeResult(
		t.q.DeleteOrderIdempotencyKey(
			ctx,
			db.DeleteOrderIdempotencyKeyParams{CallerID: caller, Method: scope.Method, IdempotencyKey: id},
		),
	)
}
