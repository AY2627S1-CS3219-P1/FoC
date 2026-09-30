BUF_VERSION := 1.73.0
BUF := .tools/buf/$(BUF_VERSION)/buf

.PHONY: buf-lint buf-generate

$(BUF):
	mkdir -p $(dir $(BUF))
	GOBIN=$(abspath $(dir $(BUF))) go install github.com/bufbuild/buf/cmd/buf@v$(BUF_VERSION)

buf-lint: $(BUF)
	$(BUF) lint

buf-generate: $(BUF)
	$(BUF) generate
