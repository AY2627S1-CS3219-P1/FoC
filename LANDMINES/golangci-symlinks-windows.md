# Service lint configurations are not symlinks on Windows

**Applies when:** `git config core.symlinks` prints `false` (the Git for Windows default) and you run `make fmt`, `make fmt-check`, `make lint` or golangci-lint inside a Go module.

**Symptom:** golangci-lint stops with `can't load config: can't read viper config: While parsing config: yaml: unmarshal errors: line 1: cannot unmarshal !!str `../.gol...` into map[string]interface {}`.

**Cause:** Each module's `.golangci.yaml` is a symlink to the root `.golangci.yaml`. Without symlink support, Git checks it out as a one-line text file containing `../.golangci.yaml`, which golangci-lint reads as YAML.

**Current workaround:** Run golangci-lint from the module directory with `--config ../.golangci.yaml`, for example `../.tools/golangci-lint/<version>/golangci-lint fmt --config ../.golangci.yaml ./...` and `GOWORK=off GOFLAGS=-mod=readonly ../.tools/golangci-lint/<version>/golangci-lint run --config ../.golangci.yaml ./...`. `fmt` also rewrites CRLF files to LF; check `git diff --stat` and restore files with no content changes with `git checkout -- <path>`.

**Remove this entry when:** the Makefile passes the root configuration explicitly, or module configurations stop being symlinks.
