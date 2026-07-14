# Testing policy

This repository separates evidence by boundary. A green lower tier never implies
that a higher tier ran. The G001 baseline implements the deterministic policy,
coverage validator, and selected-suite harness; later goals add the live suites
listed as planned below.

## Five tiers

| Tier | Boundary | Required selector | Current command | Current claim |
|---|---|---|---|---|
| T0 | unit, component, `httptest`, compile-time contracts | none | `./scripts/test-unit.sh` | dependency-free deterministic repository suite |
| T1 | built subprocess and real loopback TCP/HTTP/gRPC/Docker artifact | none | `./scripts/test-e2e.sh` | only the existing clikit subprocess/generated-consumer checks; the full binary/TCP/Docker matrix is planned |
| T2 | isolated live open-source dependency | `integration` build tag plus one exact `CHASSIS_INTEGRATION_SERVICES` value | `./scripts/test-integration.sh <service>` | no live suite is registered in G001; an empty `all` run fails rather than claiming success |
| T3 | scheduled/manual stress, restart, repetition, and extended fuzz | workflow/manual opt-in | `./scripts/test-nightly.sh` | only extended deterministic seal/webhook fuzz exists; live restart/repetition is planned |
| T4 | credentialed, hosted, heavyweight, or otherwise unsuitable for public PRs | trusted manual environment | adapter-specific commands added with their suites | limitation/defer record only; never a public-PR success claim |

Default `go test ./...` is T0. It does not use the `integration` tag, read live
service endpoints, start containers, or require Docker. Integration files must
use `//go:build integration`; normal package initialization must remain inert.

## Selected integration contract

`testing/integration-suites.tsv` is the authoritative service-to-package
registry. It is intentionally empty until a real suite is implemented.
`scripts/test-integration.sh all` enumerates this file and fails if it is empty.
A named service not present in the registry is also an error.

Every registered package owns a build-tagged top-level test using
`internal/integrationtest.Run`. The harness:

1. parses exact, lowercase comma-separated service names;
2. skips only when that test's service was not selected;
3. runs selected endpoint/config checks as hard failures (use
   `integrationtest.RequireEnv`, never `t.Skip`);
4. writes `<service>.complete` only after the selected callback returns without
   failure or skip.

The script runs one package-scoped `go test -json -tags=integration` command per
selected service and requires its marker. A zero exit with no marker is a hard
failure, so an empty or wholly skipped selected suite cannot silently pass.
Direct `go test -tags=integration` is useful for debugging, but only the script
proves the selected-suite contract.

Live suites must allocate loopback ports with `testkit.GetFreePort`; do not add a
second allocator or fixed test port. Callers must still handle its documented
close/rebind TOCTOU window.

## Coverage policy

Run `./scripts/check-coverage.sh`. The shell wrapper creates a fresh profile and
the standard-library-only checker validates `testing/coverage-policy.json`.
The G001 gate preserves the current required aggregate floor of 75%; G002 owns
the approved raise to 85% after adding deterministic tests. Non-`main` library
packages have a 75% floor now.

An exception is valid only when its exact `go list` package exists, is not a
`main` entrypoint, is currently below the normal floor, remains at or above its
temporary floor, and has a non-empty owner/rationale plus a non-expired
`YYYY-MM-DD` date. Unknown fields, duplicates, expired/unmatched exceptions, and
stale exceptions for packages that now meet 75% fail the check. The G001
baseline exceptions are the root package, `conformance`, and
`internal/appversion`; they expire on 2026-08-15. Entrypoints are classified by
`go list` and are not hidden by exceptions.

## Current package and adapter inventory

| Packages/artifacts | Strongest current evidence | Planned/residual boundary |
|---|---|---|
| root `chassis`, `announcekit`, `authkit`, `cache`, `call`, `config`, `conformance`, `deploy`, `errors`, `flagz`, `grpckit`, `guard`, `health`, `heartbeatkit`, `httpkit`, `idemkit`, `lifecycle`, `logz`, `metrics`, `orchestration`, `phasekit`, `phasekit/phasetest`, `registry`, `registrykit`, `seal`, `secval`, `testkit`, `tick`, `tracekit`, `webhook`, `work` | T0 unit/component/contract tests | G002 closes named deterministic coverage gaps; G003 adds real transport where applicable |
| `clikit` and `examples/05-clikit` consumer fixture | T1 subprocess build/version/JSON/failure/signal/freshness tests | no broader CLI behavior is implied |
| `cmd/demo-shutdown`, `examples/01-cli`, `examples/02-service`, `examples/03-client`, `examples/04-full-service` | T0 compile and package-level deterministic dependencies only | G003 adds build/version, functional subprocess/TCP, shutdown, and Docker evidence |
| `kafkakit`, `schemakit` | T0 deterministic protocol/error tests | T2 pinned Redpanda/Schema Registry in G004; restart/rebalance repetition T3 |
| `qdrantkit`, `meilikit` | T0 HTTP/client contract tests | T2 pinned live services in G005 |
| `otel`, `internal/otelutil` | T0 SDK/contract tests | T2 pinned collector with machine-readable trace+metric receipt in G005 |
| `inngestkit` | T0 protocol/config tests | T2 only if a pinned credential-free bounded dev server is feasible; otherwise T4 limitation |
| `ollamakit` | T0 HTTP contract tests | lightweight health/list may become T2; model download/inference remains T3/T4 opt-in |
| `posthogkit` | T0 deterministic HTTP contract tests | full PostHog deployment remains T4, not a public-PR gate |
| `inferkit`, `lakekit` | T0 deterministic client/contract tests | hosted/provider execution is T4 with credentials |
| internal coverage checker and integration harness | T0 unit and subprocess regression tests | governance only; these tests do not count as live-adapter evidence |

## Verification commands

```bash
./scripts/test-unit.sh
go test -race -timeout=180s -count=1 ./...
./scripts/check-coverage.sh
./scripts/test-e2e.sh
# Fails truthfully until a live suite is registered:
./scripts/test-integration.sh all
# Scheduled/manual and currently limited to deterministic fuzz:
CHASSIS_FUZZTIME=30s ./scripts/test-nightly.sh
go vet ./...
```

T4 commands and T2/T3 image pins must be documented beside the implementation
that makes them real. Until then they are limitations, not passing evidence.
