-- +goose Up
-- +goose StatementBegin
SELECT 'up SQL query';
CREATE TABLE users (
    id            SERIAL      PRIMARY KEY,
    firebase_uid  VARCHAR(255) UNIQUE   NOT NULL,
    created_at    TIMESTAMPTZ  NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at    TIMESTAMPTZ  NOT NULL DEFAULT CURRENT_TIMESTAMP,
    display_name  VARCHAR(100) NOT NULL,
    email         VARCHAR(100) UNIQUE   NOT NULL,
    is_admin      BOOLEAN      NOT NULL DEFAULT FALSE,
    date_of_birth DATE
);

CREATE OR REPLACE FUNCTION UPDATE_TIMESTAMP_FUNC()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = NOW();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER update_timestamp
BEFORE UPDATE ON users
FOR EACH ROW
EXECUTE FUNCTION UPDATE_TIMESTAMP_FUNC();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SELECT 'down SQL query';
DROP TRIGGER IF EXISTS update_timestamp ON users;
DROP FUNCTION IF EXISTS UPDATE_TIMESTAMP_FUNC();
DROP TABLE IF EXISTS users;
-- +goose StatementEnd
