include .env

USER_HOST_DATABASE_URL = postgresql://$(POSTGRES_USER):$(POSTGRES_PASSWORD)@localhost:$(POSTGRES_PORT)/$(USER_DB)?sslmode=disable
SUPPLIER_HOST_DATABASE_URL = postgresql://$(POSTGRES_USER):$(POSTGRES_PASSWORD)@localhost:$(POSTGRES_PORT)/$(SUPPLIER_DB)?sslmode=disable

.PHONY: postgres user-migrate supplier-migrate user-run supplier-run

postgres:
	docker compose up -d postgres

user-migrate:
	@$(MAKE) -C user-service migrate-up DATABASE_URL="$(USER_HOST_DATABASE_URL)"

supplier-migrate:
	@$(MAKE) -C supplier-service migrate-up DATABASE_URL="$(SUPPLIER_HOST_DATABASE_URL)"

user-run:
	@cd user-service && PORT=$(USER_PORT) DATABASE_URL="$(USER_HOST_DATABASE_URL)" air

supplier-run:
	@cd supplier-service && PORT=$(SUPPLIER_PORT) DATABASE_URL="$(SUPPLIER_HOST_DATABASE_URL)" air
