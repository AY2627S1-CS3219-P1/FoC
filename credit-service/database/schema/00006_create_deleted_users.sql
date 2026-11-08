-- +goose Up
-- The users.user_deleted replay marker. User Service inserts one fact when the
-- live account row is removed; Credit records it here so a delayed or replayed
-- users.create, or a lazy account open, cannot re-provision the deleted UUID.
-- Account opening and marker writes serialize on the same per-user advisory
-- lock, so neither can observe the other's intermediate state.
CREATE TABLE deleted_users (
    user_id    UUID        PRIMARY KEY,
    event_id   UUID        NOT NULL UNIQUE,
    deleted_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- +goose Down
DROP TABLE deleted_users;
