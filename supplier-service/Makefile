-include .env

SERVER_PATH=./cmd/server/main.go
SQLC_PATH=./database

run:
	@air

sqlc:
	@cd $(SQLC_PATH) && sqlc generate

test:
	@go test ./...

test-integration:
	@go test -tags=integration ./internal/seed ./internal/location ./internal/idempotency ./internal/rpc ./internal/database/workflowrepo -count=1 -timeout=10m

migrate-up:
	@cd database/schema && goose postgres "$(DATABASE_URL)" up

migrate-down:
	@cd database/schema && goose postgres "$(DATABASE_URL)" down

goose-create:
	@cd database/schema && goose create $(name) sql

seed:
	@DATABASE_URL="$(DATABASE_URL)" go run ./cmd/seed

dropDB:
	@dropdb supplier_dev

createDB:
	@createdb supplier_dev
