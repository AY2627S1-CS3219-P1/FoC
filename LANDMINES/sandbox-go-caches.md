# Read-only Go caches can hide the real lint failure

**Applies when:** an agent sandbox permits workspace and `/tmp` writes but the
existing Go build or golangci-lint caches live outside those writable roots.

**Symptom:** module tests report `open .../.cache/go-build/...: read-only file
system`. Lint can instead fail during package loading with `no go files to
analyze`, even though the module contains valid packages and tests pass outside
the sandbox. The lint cache can also report permission errors.

**Reproduced:** Order foundation development on 15 Oct 2026: isolated `go test`
failed writing the default build cache, and `make lint GO_MODULES=order-service`
failed loading packages. Both succeeded with writable cache paths; repository
source and dependency versions did not need to change to resolve this failure.

**Workaround:** use writable caches for sandboxed checks:

```sh
GOCACHE=/tmp/foc-go-build GOLANGCI_LINT_CACHE=/tmp/foc-go-lint make lint
```

Use the same `GOCACHE` for tests/builds to retain compilation work. This does not
change the module download cache; missing module downloads may still require an
approved tool invocation. No repository implementation fix is required for these
sandbox filesystem permissions.

**Remove this entry when:** the agent environment provides writable default Go
and lint caches, or required checks always run with writable cache paths.
