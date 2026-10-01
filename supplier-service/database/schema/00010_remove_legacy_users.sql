-- +goose Up
-- User profiles and authentication are owned by User Service.
DROP TABLE IF EXISTS users;

-- +goose Down
-- Restores the old table shape for rollback; dropped profile data is not recoverable.
CREATE TABLE users (
    id            SERIAL       PRIMARY KEY,
    firebase_uid  VARCHAR(255) UNIQUE NOT NULL,
    created_at    TIMESTAMPTZ  NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at    TIMESTAMPTZ  NOT NULL DEFAULT CURRENT_TIMESTAMP,
    display_name  VARCHAR(100) NOT NULL,
    email         VARCHAR(100) UNIQUE NOT NULL,
    is_admin      BOOLEAN      NOT NULL DEFAULT FALSE,
    date_of_birth DATE
);

CREATE TRIGGER update_timestamp
BEFORE UPDATE ON users
FOR EACH ROW
EXECUTE FUNCTION UPDATE_TIMESTAMP_FUNC();
