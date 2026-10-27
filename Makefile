BUF_VERSION := 1.73.0
BUF := .tools/buf/$(BUF_VERSION)/buf

GOLANGCI_VERSION := $(patsubst v%,%,$(shell cat .golangci-lint-version))
GOLANGCI := .tools/golangci-lint/$(GOLANGCI_VERSION)/golangci-lint
GO_MODULES := pkg user-service supplier-service order-service communication-service credit-service

.PHONY: buf-lint buf-generate buf-generate-rpc buf-generate-kafka fmt fmt-check lint

$(GOLANGCI):
	mkdir -p $(dir $(GOLANGCI))
	GOTOOLCHAIN="$$(go env GOVERSION)" GOBIN=$(abspath $(dir $(GOLANGCI))) go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v$(GOLANGCI_VERSION)

fmt: $(GOLANGCI)
	@set -e; for attempt in 1 2 3 4 5; do \
		for module in $(GO_MODULES); do \
			(cd $$module && $(abspath $(GOLANGCI)) fmt ./...); \
		done; \
		if $(MAKE) --no-print-directory fmt-check; then \
			exit 0; \
		fi; \
	done; \
	echo "Go formatting did not pass fmt-check after 5 attempts." >&2; \
	exit 1

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
# User owns user/event transactions; the SQL manifest owns Supplier fixtures.
seed-demo:
	@docker compose exec -T user-service go run ./cmd/seed --fixtures /seed-data/users.json
	@go run ./pkg/cmd/seed --manifest data/seeds/manifest.json

# Restore fixed fixture IDs without deleting unrelated records.
seed-demo-reset:
	@docker compose exec -T user-service go run ./cmd/seed --fixtures /seed-data/users.json --reset
	@go run ./pkg/cmd/seed --manifest data/seeds/manifest.json --reset
