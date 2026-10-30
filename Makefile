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

# Local Kubernetes operators; see docs/runbooks/kubernetes.md.
AQUA ?= aqua
AQUA_ROOT_DIR ?= $(CURDIR)/.tools/aqua
K8S_BUN_VERSION := $(shell cat .bun-version)
K8S_BUN := $(AQUA_ROOT_DIR)/bin/bun

.PHONY: k8s-bun k8s-tools k8s-up k8s-deploy k8s-access k8s-status k8s-demo k8s-delete-cluster k8s-kafka-ui k8s-mailpit

k8s-bun: k8s-tools
	@test "$$(AQUA_ROOT_DIR="$(AQUA_ROOT_DIR)" "$(K8S_BUN)" --version)" = "$(K8S_BUN_VERSION)"

k8s-tools:
	@command -v "$(AQUA)" >/dev/null 2>&1 || { echo "Install aqua v2.65.2 or newer; see docs/runbooks/kubernetes.md." >&2; exit 1; }
	AQUA_ROOT_DIR="$(AQUA_ROOT_DIR)" "$(AQUA)" install

k8s-up: k8s-bun
	AQUA_ROOT_DIR="$(AQUA_ROOT_DIR)" "$(K8S_BUN)" run scripts/k8s/k8s.ts up

k8s-deploy: k8s-tools
	AQUA_ROOT_DIR="$(AQUA_ROOT_DIR)" "$(K8S_BUN)" run scripts/k8s/k8s.ts deploy "$(service)"

k8s-access: k8s-tools
	AQUA_ROOT_DIR="$(AQUA_ROOT_DIR)" "$(K8S_BUN)" run scripts/k8s/k8s.ts access

k8s-status: k8s-tools
	AQUA_ROOT_DIR="$(AQUA_ROOT_DIR)" "$(K8S_BUN)" run scripts/k8s/k8s.ts status

k8s-demo: k8s-tools
	AQUA_ROOT_DIR="$(AQUA_ROOT_DIR)" "$(K8S_BUN)" run scripts/k8s/k8s.ts demo

k8s-delete-cluster: k8s-tools
	AQUA_ROOT_DIR="$(AQUA_ROOT_DIR)" "$(K8S_BUN)" run scripts/k8s/k8s.ts delete-cluster

k8s-kafka-ui: k8s-tools
	AQUA_ROOT_DIR="$(AQUA_ROOT_DIR)" "$(K8S_BUN)" run scripts/k8s/k8s.ts kafka-ui

k8s-mailpit: k8s-tools
	AQUA_ROOT_DIR="$(AQUA_ROOT_DIR)" "$(K8S_BUN)" run scripts/k8s/k8s.ts mailpit

.PHONY: seed-demo seed-demo-reset
# The manifest owns file order and database targets; SQL owns transactions.
seed-demo:
	@go run ./pkg/cmd/seed --manifest data/seeds/manifest.json

# Restore fixed fixture IDs without deleting unrelated records.
seed-demo-reset:
	@go run ./pkg/cmd/seed --manifest data/seeds/manifest.json --reset
