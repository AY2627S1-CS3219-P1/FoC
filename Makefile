BUF_VERSION := 1.73.0
BUF := .tools/buf/$(BUF_VERSION)/buf

GOLANGCI_VERSION := $(patsubst v%,%,$(shell cat .golangci-lint-version))
GOLANGCI := .tools/golangci-lint/$(GOLANGCI_VERSION)/golangci-lint
GO_MODULES := pkg user-service supplier-service

.PHONY: buf-lint buf-generate buf-generate-rpc buf-generate-kafka fmt fmt-check lint

$(GOLANGCI):
	mkdir -p $(dir $(GOLANGCI))
	GOTOOLCHAIN="$$(go env GOVERSION)" GOBIN=$(abspath $(dir $(GOLANGCI))) go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v$(GOLANGCI_VERSION)

fmt: $(GOLANGCI)
	@set -e; for module in $(GO_MODULES); do \
		(cd $$module && $(abspath $(GOLANGCI)) fmt ./...); \
	done

fmt-check: $(GOLANGCI)
	@set -e; for module in $(GO_MODULES); do \
		(cd $$module && $(abspath $(GOLANGCI)) fmt --diff ./...); \
	done

lint: fmt-check $(GOLANGCI)
	@set -e; for module in $(GO_MODULES); do \
		(cd $$module && $(abspath $(GOLANGCI)) config verify); \
		(cd $$module && GOWORK=off GOFLAGS=-mod=readonly $(abspath $(GOLANGCI)) run ./...); \
	done

$(BUF):
	mkdir -p $(dir $(BUF))
	GOBIN=$(abspath $(dir $(BUF))) go install github.com/bufbuild/buf/cmd/buf@v$(BUF_VERSION)

buf-lint: $(BUF)
	$(BUF) lint

buf-generate: buf-generate-rpc buf-generate-kafka

buf-generate-rpc: $(BUF)
	$(BUF) generate

buf-generate-kafka: $(BUF)
	$(BUF) generate --template buf.kafka.gen.yaml

.PHONY: seed-demo seed-demo-reset
SEED_RESET ?= false
# Opt-in local SQL fixtures. Check both schemas before writing either database.
seed-demo:
	@docker compose exec -T postgres sh -eu -c 'psql -X -qAt -U "$$POSTGRES_USER" -d "$$USER_DB" -v ON_ERROR_STOP=1 -c "SELECT 1 FROM role_changes LIMIT 0"; psql -X -qAt -U "$$POSTGRES_USER" -d "$$SUPPLIER_DB" -v ON_ERROR_STOP=1 -c "SELECT 1 FROM supplier_idempotency LIMIT 0"'
	@docker compose exec -T postgres sh -eu -c 'exec psql -X -q -U "$$POSTGRES_USER" -d "$$USER_DB" -v ON_ERROR_STOP=1 -v reset_demo="$$1"' sh "$(SEED_RESET)" < data/seeds/users.sql
	@docker compose exec -T postgres sh -eu -c 'exec psql -X -q -U "$$POSTGRES_USER" -d "$$SUPPLIER_DB" -v ON_ERROR_STOP=1 -v reset_demo="$$1"' sh "$(SEED_RESET)" < data/seeds/suppliers.sql

# Restore fixed fixture IDs without deleting unrelated records.
seed-demo-reset:
	@$(MAKE) --no-print-directory seed-demo SEED_RESET=true
