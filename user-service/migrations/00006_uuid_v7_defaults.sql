-- +goose Up
-- PostgreSQL 18 supplies time-ordered UUIDv7 IDs for new rows. Existing IDs stay
-- unchanged, including foreign keys and externally referenced UUIDv4 values.
ALTER TABLE users ALTER COLUMN id SET DEFAULT uuidv7();
ALTER TABLE allowed_email_domains ALTER COLUMN id SET DEFAULT uuidv7();
ALTER TABLE auth_tokens ALTER COLUMN id SET DEFAULT uuidv7();
ALTER TABLE sessions ALTER COLUMN id SET DEFAULT uuidv7();
ALTER TABLE role_changes ALTER COLUMN id SET DEFAULT uuidv7();
ALTER TABLE account_warnings ALTER COLUMN id SET DEFAULT uuidv7();

-- +goose Down
ALTER TABLE account_warnings ALTER COLUMN id SET DEFAULT gen_random_uuid();
ALTER TABLE role_changes ALTER COLUMN id SET DEFAULT gen_random_uuid();
ALTER TABLE sessions ALTER COLUMN id SET DEFAULT gen_random_uuid();
ALTER TABLE auth_tokens ALTER COLUMN id SET DEFAULT gen_random_uuid();
ALTER TABLE allowed_email_domains ALTER COLUMN id SET DEFAULT gen_random_uuid();
ALTER TABLE users ALTER COLUMN id SET DEFAULT gen_random_uuid();
