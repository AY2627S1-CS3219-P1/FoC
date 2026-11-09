-include .env

CREDIT_DB ?= credit_dev
SQLC_PATH=./database

sqlc:
	@cd $(SQLC_PATH) && sqlc generate

test:
	@go test ./...

test-integration:
	@go test -p 1 -tags=integration ./internal/... -count=1 -timeout=10m

migrate-up:
	@cd database/schema && goose postgres "$(DATABASE_URL)" up

migrate-down:
	@cd database/schema && goose postgres "$(DATABASE_URL)" down

goose-create:
	@cd database/schema && goose create $(name) sql

dropDB:
	@dropdb --if-exists $(CREDIT_DB)

createDB:
	@createdb $(CREDIT_DB)
