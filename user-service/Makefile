-include .env

SERVER_PATH=./cmd/server/main.go
MIGRATIONS_DIR=./migrations
SQLC_PATH=./database

.PHONY: sqlc

run:
	@air

sqlc:
	@cd $(SQLC_PATH) && sqlc generate

# Integration tests reset a shared TEST_DATABASE_URL, so packages must run one at a time.
Makefiletest-integration:
	@test -n "$(TEST_DATABASE_URL)" || (echo "TEST_DATABASE_URL is not set" && exit 1)
	@TEST_DATABASE_URL="$(TEST_DATABASE_URL)" go test -p 1 -count=1 ./...

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
