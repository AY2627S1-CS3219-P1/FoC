# Go line formatting can need multiple passes

**Applies when:** `golangci-lint` v2.14.0 runs `golines` with a 120-character
target on long boolean expressions.

## Failure and reproduction

A single formatter pass can leave changes that `make fmt-check` rejects.
The formatter splits some boolean expressions incrementally on successive runs.
This was reproduced while resolving PR #143 against main at `9075a80`, using
`order-service/internal/order/postgrescancellation.go` and
`order-service/internal/order/readprojection_test.go` from that commit with the
PR's formatter configuration. Formatting the former file three times produced
three different outputs; the repository output eventually stabilized.

## Workaround

Run `make fmt`: it now runs `make fmt-check` after each formatter pass and stops
as soon as the check passes, with a maximum of five attempts. A formatter
execution error stops the target immediately; five unsuccessful checks also
fail the target. Run `make lint` afterward. Direct formatter invocations still
need a separate formatting check.

## Removal condition

[Issue #208](https://github.com/AY2627S1-CS3219-P1/FoC/issues/208) tracks making
the formatter stable in one pass. Remove this entry and close the issue after
the pinned tool/configuration formats the reproduction inputs in one run and
a second run leaves them unchanged.
