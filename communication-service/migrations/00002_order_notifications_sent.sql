-- +goose Up
-- Order notifications already sent, so a redelivered event isn't sent again.
CREATE TABLE order_notifications_sent (
    event_id uuid        NOT NULL,
    user_id  uuid        NOT NULL,
    sent_at  timestamptz NOT NULL,
    PRIMARY KEY (event_id, user_id)
);

-- +goose Down
DROP TABLE IF EXISTS order_notifications_sent;
