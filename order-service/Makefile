-include .env

.PHONY: run sqlc test test-creation test-integration migrate-up migrate-down goose-create

run:
	air -c .air.toml

sqlc:
	cd database && sqlc generate

test:
	GOWORK=off go test -mod=readonly ./...

test-creation:
	GOWORK=off go test -mod=readonly -race -tags=integration ./tests/creation -count=1 -timeout=10m

test-integration:
	GOWORK=off go test -mod=readonly -p 1 -tags=integration ./... -count=1 -timeout=10m

migrate-up:
	goose -dir database/schema postgres "$(DATABASE_URL)" up

migrate-down:
	goose -dir database/schema postgres "$(DATABASE_URL)" down

goose-create:
	goose -dir database/schema create $(name) sql
