# Testing policy

This repository separates evidence by boundary. A green lower tier never implies
that a higher tier ran. The deterministic default suite remains live-service-free;
G005 adds opt-in T2 live adapter suites for public, pinned local services.

## Five tiers

| Tier | Boundary | Required selector | Current command | Current claim |
|---|---|---|---|---|
| T0 | unit, component, `httptest`, compile-time contracts | none | `./scripts/test-unit.sh` or `go test ./...` | dependency-free deterministic repository suite |
| T1 | built subprocess and real loopback TCP/HTTP/gRPC/Docker artifact | `e2e` build tag; `CHASSIS_E2E_DOCKER_REQUIRED=1` for required Docker proof | `./scripts/test-e2e.sh` | shipped executable and subprocess/loopback evidence; Docker evidence is complete only in required mode |
| T2 | isolated live open-source dependency | `integration` build tag plus one exact `CHASSIS_INTEGRATION_SERVICES` value | `./scripts/test-integration.sh <service>` or `all` | pinned local containers for Redpanda, Qdrant, Meilisearch, OpenTelemetry Collector contrib, and Inngest Dev Server |
| T3 | scheduled/manual stress, restart, repetition, and extended fuzz | workflow/manual opt-in | `./scripts/test-nightly.sh` | extended fuzz for discovered fuzz targets, focused race repetitions, repeated T2 suites, and real pinned local service restart probes |
| T4 | credentialed, hosted, heavyweight, or otherwise unsuitable for public PRs | trusted manual environment | adapter-specific manual commands | hosted/provider evidence and credentialed cloud execution remain limitations, never public-PR success claims |

Default `go test ./...` is T0. It does not use the `integration` tag, read live
service endpoints, start containers, or require Docker. Integration files must
use `//go:build integration`; normal package initialization must remain inert.

## T1 Docker evidence contract

`./scripts/test-e2e.sh` defaults to optional local Docker mode. It always runs
the subprocess and loopback E2E matrix, but visibly skips the Docker artifact
test when the Docker CLI or daemon is unavailable; that successful command is
not complete Docker evidence. Set `CHASSIS_E2E_DOCKER_REQUIRED=1` to make Docker
CLI/daemon absence a hard failure. Hosted PR/push CI always sets required mode.
The required negative probe uses an unavailable socket and must fail:

```bash
CHASSIS_E2E_DOCKER_REQUIRED=1 \
DOCKER_HOST=unix:///tmp/chassis-missing-docker.sock \
  ./scripts/test-e2e.sh
```

## Selected integration contract

`testing/integration-suites.tsv` is the authoritative service-to-package
registry. It currently registers:

| Service | Package | T2 proof |
|---|---|---|
| `redpanda` | `./testing/redpanda` | pinned Redpanda Kafka/Schema Registry behavior |
| `qdrant` | `./qdrantkit` | create collection, upsert points, search/query, retrieve/delete points, delete collection, bad collection/request error mapping |
| `meilisearch` | `./meilikit` | index create, document add, task wait, filtered search, delete, bad request/error mapping |
| `otel-collector` | `./otel` | real collector-contrib, repo-owned config, OTLP gRPC, SDK init, one trace and one metric, force flush/shutdown, machine-readable receipt for both signals and `service.name`/`service.version` resource attributes |
| `inngest` | `./inngestkit` | credential-free pinned Inngest Dev Server with dummy local dev key, local event ingestion, and `inngestkit.Send` request proof |

`testing/integration-images.tsv` is the companion immutable image registry. Every
registered suite must have exactly one non-`latest` image reference pinned by
manifest digest plus linux/amd64 and linux/arm64 manifest digests and an official
source URL. The integration script prints `using pinned integration image:` before
running each suite, and the package helper logs `CHASSIS_INTEGRATION_IMAGE` from
the same registry. Current official source references include Redpanda Docker
labs, Qdrant quickstart, Meilisearch Docker install docs, OpenTelemetry Collector
Docker install docs, and Inngest local-development docs.

Every registered package owns a build-tagged top-level test using
`internal/integrationtest.Run`. The harness:

1. parses exact, lowercase comma-separated service names;
2. skips only when that test's service was not selected;
3. hard-fails selected suites on missing/unhealthy Docker, missing image pins,
   invalid config, service startup/readiness failures, callback failures, or
   callback skips;
4. writes `<service>.complete` only after the selected callback returns without
   failure or skip.

The script runs one package-scoped `go test -json -tags=integration` command per
selected service and requires its marker. A zero exit with no marker is a hard
failure, so an empty or wholly skipped selected suite cannot silently pass.
Direct `go test -tags=integration` is useful for debugging, but only the script
proves the selected-suite contract.

Live suites allocate loopback ports with `testkit.GetFreePort`; do not add a
second allocator or fixed test port. Callers must still handle its documented
close/rebind TOCTOU window. Suites must use bounded contexts/readiness probes,
unique container/resource names, and cleanup that emits container logs/inspect
on failure. Exact suite-owned containers are removed with their attached
anonymous volumes; cleanup must never use global volume pruning, name/hash
heuristics, or delete unrelated volumes. Removal failures are test failures even
when the primary assertion succeeded; when a primary failure already exists,
cleanup diagnostics are added without replacing it. The selected Redpanda suite
asserts that its anonymous volume IDs disappear and the Docker volume inventory
returns exactly to its preflight snapshot.


## CI topology and artifacts

Every PR/push keeps deterministic T0/T1 gates service-free and bounded, then runs one package-scoped live integration job per registered public credential-free service: `redpanda`, `qdrant`, `meilisearch`, `otel-collector`, and `inngest`. Each live job sets exactly one `CHASSIS_INTEGRATION_SERVICES` value, calls `./scripts/test-integration.sh <service>`, records the image tag+digest and per-arch manifest digests from `testing/integration-images.tsv`, and uploads diagnostics with `actions/upload-artifact` even when tests fail.

The scheduled/manual nightly job runs only from `schedule` or `workflow_dispatch`. It uses `scripts/test-nightly.sh` to discover all repo fuzz targets, repeat focused race packages, repeat selected live integrations, and perform real Docker `restart` probes against pinned local runtimes. The Redpanda restart probe publishes and consumes through `kafkakit` before restart, keeps the same publisher client across the broker restart, waits for admin and Kafka metadata readiness through the existing admin client, then opens a new subscriber session and proves publish/consume after restart. This matches the documented one-shot subscriber lifecycle after `Close` while proving client reconnection behavior instead of broker health alone. Set `CHASSIS_NIGHTLY_INTEGRATIONS=none` or `CHASSIS_NIGHTLY_RESTART_SERVICES=none` only for bounded local smoke; those selectors are reported as disabled and must not be used as release evidence for live resilience.

Nightly and live CI diagnostics remove only exact discovered `chassis-*`
containers together with volumes attached to those containers. They never run a
global volume prune or infer volume ownership from a volume name/hash. They write
`cleanup_complete` only after inventory and all removals succeed; otherwise they
write `cleanup_failed` and fail the cleanup step. The nightly owner preserves a
nonzero primary test status while recording any additional cleanup failure, and
cleanup failure makes an otherwise successful owner fail.

## Inngest T2/T4 boundary

The T2 Inngest suite uses the official local-development path: a pinned
`inngest/inngest` Dev Server container started with local dev settings, no cloud
credentials, and dummy local event/signing keys. It proves bounded local event
acceptance and the repository `inngestkit.Send` HTTP protocol against that live
runtime.

Credentialed Inngest Cloud execution, production signing-key validation against
a hosted account, webhook fanout, and long-running durable workflow execution are
T4/manual or later-goal evidence. Restart and repetition scenarios are covered by the scheduled/manual G006 nightly tier. Broader long-duration soak remains an explicit T3 extension, not a public-PR gate.

## Coverage policy

Run `./scripts/check-coverage.sh`. The shell wrapper creates a fresh profile and
the standard-library-only checker validates `testing/coverage-policy.json`.
The current aggregate floor is 85%, non-`main` library packages have a 75% floor,
and there are zero policy exceptions.

An exception is valid only when its exact `go list` package exists, is not a
`main` entrypoint, is currently below the normal floor, remains at or above its
temporary floor, and has a non-empty owner/rationale plus a non-expired
`YYYY-MM-DD` date. Unknown fields, duplicates, expired/unmatched exceptions, and
stale exceptions for packages that now meet 75% fail the check. Entrypoints are
classified by `go list` and are not hidden by exceptions.

## Current package and adapter inventory

| Packages/artifacts | Strongest current evidence | Residual boundary |
|---|---|---|
| root `chassis`, `announcekit`, `authkit`, `cache`, `call`, `config`, `conformance`, `deploy`, `errors`, `flagz`, `grpckit`, `guard`, `health`, `heartbeatkit`, `httpkit`, `idemkit`, `lifecycle`, `logz`, `metrics`, `orchestration`, `phasekit`, `phasekit/phasetest`, `registry`, `registrykit`, `seal`, `secval`, `testkit`, `tick`, `tracekit`, `webhook`, `work` | T0 unit/component/contract tests | live external systems only when a package registers a T2/T4 suite |
| `clikit` and `examples/05-clikit` consumer fixture | T1 subprocess build/version/JSON/failure/signal/freshness tests | no broader CLI behavior is implied |
| `cmd/demo-shutdown`, `examples/01-cli`, `examples/02-service`, `examples/03-client`, `examples/04-full-service` | T0 compile and package-level deterministic dependencies | broader binary/TCP/Docker behavior is T1/T2 when registered |
| `kafkakit`, `schemakit` | T2 pinned Redpanda plus T0 deterministic protocol/error tests | T3 proves the same publisher before/after broker restart and an explicit new one-shot subscriber session after readiness; longer soak and hosted broker variants remain T3/T4 |
| `qdrantkit`, `meilikit` | T2 pinned local Qdrant and Meilisearch plus T0 HTTP/client contract tests | local service restart is covered by T3 nightly; provider/hosted variants are T4 if introduced |
| `otel`, `internal/otelutil` | T2 pinned collector-contrib with machine-readable trace+metric receipt plus T0 SDK/contract tests | collector restart is covered by T3 nightly; soak/telemetry-volume scenarios remain T3 extensions |
| `inngestkit` | T2 pinned credential-free Dev Server plus T0 protocol/config tests | local dev-server restart is covered by T3 nightly; credentialed cloud/self-hosted production execution is T4/manual |
| `ollamakit` | T0 HTTP contract tests | model download/inference remains T3/T4 opt-in |
| `posthogkit` | T0 deterministic HTTP contract tests | full PostHog deployment remains T4, not a public-PR gate |
| `inferkit`, `lakekit` | T0 deterministic client/contract tests | hosted/provider execution is T4 with credentials |
| internal coverage checker and integration harness | T0 unit and subprocess regression tests | governance only; these tests do not count as live-adapter evidence |

## Verification commands

```bash
./scripts/test-unit.sh
./scripts/test-integration.sh qdrant
./scripts/test-integration.sh meilisearch
./scripts/test-integration.sh otel-collector
./scripts/test-integration.sh inngest
./scripts/test-integration.sh all
# Selected unhealthy hard-fail probe example:
DOCKER_HOST=unix:///tmp/chassis-missing-docker.sock ./scripts/test-integration.sh qdrant
go test ./...
go test -race ./...
./scripts/check-coverage.sh
# Optional local Docker mode; a Docker skip is visible and is not complete T1 Docker proof:
./scripts/test-e2e.sh
# Required Docker evidence (the mode used by hosted CI):
CHASSIS_E2E_DOCKER_REQUIRED=1 ./scripts/test-e2e.sh
go vet ./...
git diff --check
# Scheduled/manual T3 with all defaults: extended fuzz, focused race repetitions, repeated T2, and real restart probes:
CHASSIS_FUZZTIME=60s ./scripts/test-nightly.sh
# Safe bounded local smoke that avoids Docker/live services while validating nightly fuzz/race plumbing:
CHASSIS_FUZZTIME=1s CHASSIS_NIGHTLY_RACE_COUNT=1 CHASSIS_NIGHTLY_INTEGRATIONS=none CHASSIS_NIGHTLY_RESTART_SERVICES=none ./scripts/test-nightly.sh
```

T4 commands and any additional long-duration T3 soak scenarios must be documented beside the implementation that makes them real. Public PR success never implies credentialed hosted-provider coverage.
