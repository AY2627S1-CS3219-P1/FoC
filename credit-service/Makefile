-include .env

CREDIT_DB ?= credit_dev

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
