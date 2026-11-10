-- +goose Up
-- Each user's email, copied from users.create, for email notifications to
-- users without a linked Telegram chat.
CREATE TABLE user_emails (
    user_id    uuid        PRIMARY KEY,
    email      text        NOT NULL,
    created_at timestamptz NOT NULL
);

-- +goose Down
DROP TABLE IF EXISTS user_emails;
