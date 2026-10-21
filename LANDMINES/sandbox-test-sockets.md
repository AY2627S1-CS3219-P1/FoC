# Sandbox blocks local HTTP test servers

**Applies when:** Go tests use `httptest.NewServer` and an agent sandbox denies
local socket creation.

**Symptom:** tests panic with `httptest: failed to listen on a port` and
`socket: operation not permitted`. This can affect several packages at once.

**Reproduced:** PR #149 follow-up on 21 Oct 2026: Communication Service server,
router and Telegram tests failed at listener creation inside the sandbox. The
same `GOWORK=off go test -mod=readonly -race ./...` command passed when run with
approved local socket access.

**Workaround:** run these tests with approved local socket access. Keep writable
Go caches as described in [sandbox-go-caches.md](sandbox-go-caches.md). Do not
change application networking or skip the tests to work around this sandbox
restriction. No application fix is required.

**Remove this entry when:** the agent test environment permits local socket
creation by default.
