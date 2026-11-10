-include .env

REPORTS_MODERATION_DB ?= reports_moderation_dev
SQLC_PATH=./database

sqlc:
	@cd $(SQLC_PATH) && sqlc generate

test:
	@go test ./...

# Integration tests need TEST_DATABASE_URL pointing at a throwaway database.
test-integration:
	@go test -p 1 ./internal/... -count=1 -timeout=10m

migrate-up:
	@cd database/schema && goose postgres "$(DATABASE_URL)" up

migrate-down:
	@cd database/schema && goose postgres "$(DATABASE_URL)" down

goose-create:
	@cd database/schema && goose create $(name) sql

dropDB:
	@dropdb --if-exists $(REPORTS_MODERATION_DB)

createDB:
	@createdb $(REPORTS_MODERATION_DB)
