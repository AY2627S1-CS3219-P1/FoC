-include .env

SERVER_PATH=./cmd/server/main.go
MIGRATIONS_DIR=./migrations

run:
	@air

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
