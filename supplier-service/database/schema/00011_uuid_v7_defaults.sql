-- +goose Up
-- Defaults cover direct inserts; application paths that supply IDs use UUIDv7
-- too. Explicit legacy and deterministic seed IDs remain valid.
ALTER TABLE categories ALTER COLUMN id SET DEFAULT uuidv7();
ALTER TABLE buildings ALTER COLUMN id SET DEFAULT uuidv7();
ALTER TABLE locations ALTER COLUMN id SET DEFAULT uuidv7();
ALTER TABLE location_photos ALTER COLUMN id SET DEFAULT uuidv7();
ALTER TABLE location_disablements ALTER COLUMN id SET DEFAULT uuidv7();
ALTER TABLE location_addition_requests ALTER COLUMN id SET DEFAULT uuidv7();

-- +goose Down
ALTER TABLE location_addition_requests ALTER COLUMN id SET DEFAULT gen_random_uuid();
ALTER TABLE location_disablements ALTER COLUMN id SET DEFAULT gen_random_uuid();
ALTER TABLE location_photos ALTER COLUMN id SET DEFAULT gen_random_uuid();
ALTER TABLE locations ALTER COLUMN id SET DEFAULT gen_random_uuid();
ALTER TABLE buildings ALTER COLUMN id SET DEFAULT gen_random_uuid();
ALTER TABLE categories ALTER COLUMN id SET DEFAULT gen_random_uuid();
