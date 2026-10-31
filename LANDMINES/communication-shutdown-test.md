# Communication shutdown test can hang during cleanup

**Applies when:** running Communication's Go tests, especially
`TestBotShutdownJoinsStartedWorkers`.

## Failure and reproduction

The test can finish its assertions but hang in deferred `httptest.Server.Close`.
The timeout stack shows the server waiting for an active HTTP handler, which is
blocked on `<-r.Context().Done()`. The log reports
`httptest.Server blocked in Close after 5 seconds`.

Reproduced on untouched main at `9075a80`, as well as while validating only
formatter output, using Go 1.27.0 on Linux. From `communication-service`:

```sh
GOWORK=off go test -mod=readonly ./cmd/server \
  -run '^TestBotShutdownJoinsStartedWorkers$' -count=10 -timeout=20s
```

## Workaround and tracking

Use a bounded test timeout and inspect the stack to distinguish this known
cleanup failure from a new failure. A passing rerun does not establish that the
race is fixed. Do not disable the shutdown assertions or alter unrelated
application code to work around it.

[Issue #207](https://github.com/AY2627S1-CS3219-P1/FoC/issues/207) tracks the fix.
Remove this entry and close the issue after cleanup reliably releases active
handlers, the focused test passes repeatedly, and the full module suite passes.
