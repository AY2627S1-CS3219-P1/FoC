-- +goose Up
-- Who is in each order, copied from order.status_changed, so chat doesn't call
-- Order Service. revision makes replayed or older events no-ops.
CREATE TABLE order_participants (
    order_id             uuid PRIMARY KEY,
    requester_id         uuid        NOT NULL,
    courier_id           uuid,
    status               text        NOT NULL,
    revision             bigint      NOT NULL,
    pickup_location_name text        NOT NULL,
    -- When the order reached a terminal status.
    ended_at             timestamptz,
    updated_at           timestamptz NOT NULL
);
CREATE INDEX order_participants_requester ON order_participants (requester_id);
CREATE INDEX order_participants_courier ON order_participants (courier_id);

-- Relayed chat messages, kept for disputes until the retention period after
-- the order ends.
CREATE TABLE chat_messages (
    id           uuid PRIMARY KEY,
    order_id     uuid        NOT NULL,
    sender_id    uuid        NOT NULL,
    recipient_id uuid        NOT NULL,
    body         text        NOT NULL,
    sent_at      timestamptz NOT NULL
);
CREATE INDEX chat_messages_order ON chat_messages (order_id, sent_at);

-- Bot messages a user can reply to, and the order each belongs to.
CREATE TABLE chat_reply_targets (
    chat_id    bigint      NOT NULL,
    message_id bigint      NOT NULL,
    order_id   uuid        NOT NULL,
    created_at timestamptz NOT NULL,
    PRIMARY KEY (chat_id, message_id)
);
CREATE INDEX chat_reply_targets_order ON chat_reply_targets (order_id);

-- The order a user chose with /chat, for messages that aren't replies.
CREATE TABLE chat_selections (
    user_id     uuid PRIMARY KEY,
    order_id    uuid        NOT NULL,
    selected_at timestamptz NOT NULL
);

-- +goose Down
DROP TABLE IF EXISTS chat_selections;
DROP TABLE IF EXISTS chat_reply_targets;
DROP TABLE IF EXISTS chat_messages;
DROP TABLE IF EXISTS order_participants;
