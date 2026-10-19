BUF_VERSION := 1.73.0
BUF := .tools/buf/$(BUF_VERSION)/buf

GOLANGCI_VERSION := $(patsubst v%,%,$(shell cat .golangci-lint-version))
GOLANGCI := .tools/golangci-lint/$(GOLANGCI_VERSION)/golangci-lint
GO_MODULES := pkg user-service supplier-service order-service

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

# Local Kubernetes operators; see docs/runbooks/kubernetes.md.
.PHONY: k8s-tools k8s-up k8s-deploy k8s-access k8s-status k8s-smoke k8s-demo k8s-delete-cluster k8s-kafka-ui k8s-mailpit k8s-test

k8s-tools:
	bash scripts/k8s/tools.sh

k8s-up:
	bash scripts/k8s/k8s.sh up

k8s-deploy:
	bash scripts/k8s/k8s.sh deploy "$(service)"

k8s-access:
	bash scripts/k8s/k8s.sh access

k8s-status:
	bash scripts/k8s/k8s.sh status

k8s-smoke:
	bash scripts/k8s/k8s.sh smoke

k8s-demo:
	bash scripts/k8s/k8s.sh demo

k8s-delete-cluster:
	bash scripts/k8s/k8s.sh delete-cluster

k8s-kafka-ui:
	bash scripts/k8s/k8s.sh kafka-ui

k8s-mailpit:
	bash scripts/k8s/k8s.sh mailpit

k8s-test:
	bash scripts/k8s/tests.sh
