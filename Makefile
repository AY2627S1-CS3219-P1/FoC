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
K8S_TOOLS_DIR ?= .tools/k8s
K8S_BUN_VERSION := $(shell cat .bun-version)
K8S_CACHED_BUN := $(K8S_TOOLS_DIR)/bun
K8S_SYSTEM_BUN := $(shell command -v bun 2>/dev/null)
ifeq ($(shell test -n "$(K8S_SYSTEM_BUN)" && "$(K8S_SYSTEM_BUN)" --version | grep -Fxq "$(K8S_BUN_VERSION)" && echo yes),yes)
K8S_BUN := $(K8S_SYSTEM_BUN)
else
K8S_BUN := $(K8S_CACHED_BUN)
endif

.PHONY: k8s-bun k8s-tools k8s-up k8s-deploy k8s-access k8s-status k8s-demo k8s-delete-cluster k8s-kafka-ui k8s-mailpit

k8s-bun:
	@if [ "$(K8S_BUN)" = "$(K8S_CACHED_BUN)" ]; then bash scripts/k8s/bootstrap-bun.sh; fi
	@test "$$("$(K8S_BUN)" --version)" = "$(K8S_BUN_VERSION)"

k8s-tools: k8s-bun
	"$(K8S_BUN)" run scripts/k8s/tools.ts

k8s-up: k8s-bun
	"$(K8S_BUN)" run scripts/k8s/k8s.ts up

k8s-deploy: k8s-tools
	"$(K8S_BUN)" run scripts/k8s/k8s.ts deploy "$(service)"

k8s-access: k8s-tools
	"$(K8S_BUN)" run scripts/k8s/k8s.ts access

k8s-status: k8s-tools
	"$(K8S_BUN)" run scripts/k8s/k8s.ts status

k8s-demo: k8s-tools
	"$(K8S_BUN)" run scripts/k8s/k8s.ts demo

k8s-delete-cluster: k8s-tools
	"$(K8S_BUN)" run scripts/k8s/k8s.ts delete-cluster

k8s-kafka-ui: k8s-tools
	"$(K8S_BUN)" run scripts/k8s/k8s.ts kafka-ui

k8s-mailpit: k8s-tools
	"$(K8S_BUN)" run scripts/k8s/k8s.ts mailpit

.PHONY: seed-demo seed-demo-reset
# The manifest owns file order and database targets; SQL owns transactions.
seed-demo:
	@go run ./pkg/cmd/seed --manifest data/seeds/manifest.json

# Restore fixed fixture IDs without deleting unrelated records.
seed-demo-reset:
	@go run ./pkg/cmd/seed --manifest data/seeds/manifest.json --reset
