-- +goose Up
CREATE TABLE order_item_images (
 id uuid PRIMARY KEY,
 owner_id uuid NOT NULL,
 object_key text NOT NULL UNIQUE,
 content_type text NOT NULL CHECK (content_type IN ('image/jpeg','image/png','image/webp','image/heic','image/heif')),
 size_bytes bigint NOT NULL CHECK (size_bytes BETWEEN 1 AND 10000000),
 state text NOT NULL DEFAULT 'uploading' CHECK (state IN ('uploading','attached','deleting')),
 order_id uuid REFERENCES orders(id),
 ordinal integer CHECK (ordinal BETWEEN 0 AND 4),
 created_at timestamptz NOT NULL,
 upload_expires_at timestamptz NOT NULL,
 UNIQUE(order_id,ordinal),
 CHECK ((state = 'attached') = (order_id IS NOT NULL)),
 CHECK ((order_id IS NOT NULL) = (ordinal IS NOT NULL))
);
CREATE INDEX images_owner ON order_item_images(owner_id);
CREATE INDEX images_cleanup ON order_item_images(created_at) WHERE state <> 'attached';
ALTER TABLE order_outbox ADD COLUMN lease_token uuid;
ALTER TABLE order_outbox ADD COLUMN lease_until timestamptz;
-- +goose Down
ALTER TABLE order_outbox DROP COLUMN lease_until;
ALTER TABLE order_outbox DROP COLUMN lease_token;
DROP TABLE order_item_images;
