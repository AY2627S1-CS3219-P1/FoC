-include .env

SQLC_PATH=./database

sqlc:
	@cd $(SQLC_PATH) && sqlc generate

test:
	@go test ./...

migrate-up:
	@cd database/schema && goose postgres "$(DATABASE_URL)" up

migrate-down:
	@cd database/schema && goose postgres "$(DATABASE_URL)" down

goose-create:
	@cd database/schema && goose create $(name) sql

dropDB:
	@dropdb credit_dev

createDB:
	@createdb credit_dev
