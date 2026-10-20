-- Telegram is linked through the Communication Service, which stores chat IDs.
-- A handle can't be messaged by the bot, so User Service no longer stores one.
-- +goose Up
ALTER TABLE users DROP COLUMN telegram_handle;

-- +goose Down
ALTER TABLE users ADD COLUMN telegram_handle TEXT CHECK (telegram_handle ~ '^[A-Za-z0-9_]{5,32}$');
