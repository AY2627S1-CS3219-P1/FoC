package order

import (
	"context"
	"time"

	db "github.com/AY2627S1-CS3219-P1/FoC/order-service/internal/database/orderdb"
	"github.com/google/uuid"
)

func (r *PostgresRepository) SaveItemImage(ctx context.Context, image ItemImageUpload) error {
	return persistenceError(db.New(r.pool).InsertItemImage(ctx, db.InsertItemImageParams{ID: uuid.MustParse(image.ID), OwnerID: uuid.MustParse(image.OwnerID), ObjectKey: image.Key, ContentType: image.ContentType, SizeBytes: image.SizeBytes, CreatedAt: image.CreatedAt, UploadExpiresAt: image.UploadExpiresAt}))
}

func (r *PostgresRepository) ItemImage(ctx context.Context, owner, id string) (ItemImageUpload, error) {
	oid, err := parseID(owner)
	if err != nil {
		return ItemImageUpload{}, err
	}
	iid, err := parseID(id)
	if err != nil {
		return ItemImageUpload{}, err
	}
	row, err := db.New(r.pool).GetItemImage(ctx, db.GetItemImageParams{ID: iid, OwnerID: oid})
	if err != nil {
		return ItemImageUpload{}, persistenceError(err)
	}
	return fromItemImage(row), nil
}

func (r *PostgresRepository) OrderImages(ctx context.Context, orderID string) ([]ItemImageUpload, error) {
	id, err := parseID(orderID)
	if err != nil {
		return nil, err
	}
	rows, err := db.New(r.pool).OrderItemImages(ctx, &id)
	if err != nil {
		return nil, err
	}
	return fromItemImages(rows), nil
}

func (r *PostgresRepository) ClaimOrphanImages(ctx context.Context, before, now time.Time) ([]ItemImageUpload, error) {
	rows, err := db.New(r.pool).ClaimOrphanImages(ctx, db.ClaimOrphanImagesParams{CreatedBefore: before, NowAt: now})
	return fromItemImages(rows), err
}

func (r *PostgresRepository) DeleteOrphanImage(ctx context.Context, id string) error {
	parsed, err := parseID(id)
	if err != nil {
		return err
	}
	return db.New(r.pool).DeleteOrphanImage(ctx, parsed)
}

func (t *postgresTx) AttachCreationImages(ctx context.Context, owner, orderID string, items []ItemImageUpload) error {
	if len(items) > 5 || !t.idempotencyCallers[IdempotencyScope{CallerID: owner, Method: CreateOrderMethod}] || !t.applied[orderID] {
		return t.writeResult(ErrPrecondition)
	}
	for i, item := range items {
		orderUUID := uuid.MustParse(orderID)
		ordinal := int32(i)
		if item.OwnerID != owner || item.State != "uploading" {
			return t.writeResult(ErrPrecondition)
		}
		n, err := t.q.AttachItemImage(ctx, db.AttachItemImageParams{ID: uuid.MustParse(item.ID), OwnerID: uuid.MustParse(owner), OrderID: &orderUUID, Ordinal: &ordinal, ObjectKey: item.Key, SizeBytes: item.SizeBytes, ContentType: item.ContentType})
		if err != nil {
			return t.writeResult(err)
		}
		if n != 1 {
			return t.writeResult(ErrPrecondition)
		}
	}
	return nil
}

func fromItemImage(row db.OrderItemImage) ItemImageUpload {
	return ItemImageUpload{ID: row.ID.String(), OwnerID: row.OwnerID.String(), Key: row.ObjectKey, ContentType: row.ContentType, State: row.State, SizeBytes: row.SizeBytes, CreatedAt: row.CreatedAt, UploadExpiresAt: row.UploadExpiresAt}
}

func fromItemImages(rows []db.OrderItemImage) []ItemImageUpload {
	result := make([]ItemImageUpload, len(rows))
	for i, row := range rows {
		result[i] = fromItemImage(row)
	}
	return result
}
