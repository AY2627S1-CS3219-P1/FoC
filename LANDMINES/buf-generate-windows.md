# Buf cannot run the TypeScript generator on Windows

**Applies when:** You run `buf generate` (or `make buf-generate`) on native Windows, outside WSL.

**Symptom:** Generation fails with `'frontend' is not recognized as an internal or external command` and `Failure: plugin frontend/node_modules/.bin/protoc-gen-es: exit status 1`. When generation succeeds, Git may also show unchanged generated files as modified because only their line endings changed.

**Cause:** `buf.gen.yaml` runs the local plugin `frontend/node_modules/.bin/protoc-gen-es`. Windows launches it through `cmd.exe`, which does not accept the forward-slash relative path. The generators write LF line endings, while a checkout with `core.autocrlf=true` has CRLF line endings.

**Current workaround:** Copy `buf.gen.yaml` to a scratch file. In the copy, replace the `local:` value with `["node", "frontend/node_modules/@bufbuild/protoc-gen-es/bin/protoc-gen-es"]`. Then run `buf generate --template <copy>` from the repository root. Before committing, check `git diff --stat`. Restore generated files that have no content changes with `git checkout -- <path>`.

**Remove this entry when:** `buf.gen.yaml` invokes the generator in a way that also works on Windows, or the team stops generating on native Windows.
