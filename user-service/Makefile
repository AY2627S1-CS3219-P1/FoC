-include .env

SERVER_PATH=./cmd/server/main.go
MIGRATIONS_DIR=./migrations
SQLC_PATH=./database

.PHONY: sqlc

run:
	@air

sqlc:
	@cd $(SQLC_PATH) && sqlc generate

# Integration tests run against throwaway testcontainers databases, so
# packages must run one at a time.
test-integration:
	@GOWORK=off go test -mod=readonly -p 1 -tags=integration ./... -count=1 -timeout=20m

migrate-up:
	@goose -dir $(MIGRATIONS_DIR) postgres "$(DATABASE_URL)" up

migrate-down:
	@goose -dir $(MIGRATIONS_DIR) postgres "$(DATABASE_URL)" down

goose-create:
	@goose -dir $(MIGRATIONS_DIR) create $(name) sql

dropDB:
	@dropdb user_dev

createDB:
	@createdb user_dev
