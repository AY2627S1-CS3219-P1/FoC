-- +goose Up
CREATE TABLE order_proof_uploads (
 id uuid PRIMARY KEY,
 order_id uuid NOT NULL REFERENCES orders(id),
 courier_id uuid NOT NULL,
 upload_id uuid NOT NULL,
 object_key text NOT NULL UNIQUE,
 content_type text NOT NULL,
 size_bytes bigint NOT NULL CHECK (size_bytes BETWEEN 1 AND 10000000),
 state text NOT NULL CHECK (state IN ('uploading','accepted','deleting')),
 generation bigint NOT NULL CHECK (generation > 0),
 signing_epoch text NOT NULL DEFAULT '',
 granted_at timestamptz NOT NULL,
 expires_at timestamptz NOT NULL,
 created_at timestamptz NOT NULL,
 verified_at timestamptz,
 UNIQUE(order_id,courier_id,upload_id)
);
CREATE TABLE order_proof_fences (
 order_id uuid NOT NULL REFERENCES orders(id),
 courier_id uuid NOT NULL,
 upload_id uuid NOT NULL,
 retired_at timestamptz NOT NULL,
 PRIMARY KEY(order_id,courier_id,upload_id)
);
CREATE TABLE order_delivery_receipts (
 id uuid PRIMARY KEY,
 order_id uuid NOT NULL REFERENCES orders(id),
 courier_id uuid NOT NULL,
 proof_ids uuid[] NOT NULL,
 unattended boolean NOT NULL,
 without_proof boolean NOT NULL,
 received_at timestamptz NOT NULL,
 processed_at timestamptz,
 next_attempt_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 attempt_count bigint NOT NULL DEFAULT 0,
 alerted_at timestamptz,
 state text NOT NULL CHECK(state IN ('pending','accepted','rejected','superseded')),
 outage jsonb,
 CHECK (NOT without_proof OR (outage IS NOT NULL AND jsonb_typeof(outage)='object')),
 CHECK ((without_proof AND cardinality(proof_ids)=0) OR
        (NOT without_proof AND cardinality(proof_ids) BETWEEN 1 AND 5))
);
CREATE UNIQUE INDEX order_delivery_one_pending ON order_delivery_receipts(order_id) WHERE state='pending';
CREATE INDEX order_delivery_pending_scan ON order_delivery_receipts(received_at,order_id) WHERE state='pending';
CREATE TABLE order_proof_confirmations (
 order_id uuid PRIMARY KEY REFERENCES orders(id),
 payload jsonb NOT NULL
);
CREATE TABLE order_proof_outage_state (
 scope text PRIMARY KEY,
 evidence_id uuid NOT NULL,
 generation bigint NOT NULL,
 observed_at timestamptz NOT NULL,
 valid_until timestamptz NOT NULL,
 reason text NOT NULL,
 active boolean NOT NULL
);

-- +goose Down
DROP TABLE order_proof_outage_state;
DROP TABLE order_proof_confirmations;
DROP TABLE order_delivery_receipts;
DROP TABLE order_proof_fences;
DROP TABLE order_proof_uploads;
