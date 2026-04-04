# Changelog

## [Unreleased]

### Removed

- **graphkit**: Removed knowledge graph client (graphiti_svc HTTP client). Zero consumers across all codebases. Service was never adopted.

## [10.2.11] - 2026-04-04

### Added

- **meilikit**: Add test verifying non-404 client errors (e.g. 403) are not swallowed by GetDocument's 404→nil,nil handling

(Claude Code:Opus 4.6 (1M context))

## [10.2.10] - 2026-04-04

### Added

- **meilikit**: Add `GetDocument(ctx, docID)` — retrieves a single document by ID; returns `nil, nil` on 404
- **meilikit**: Add `DeleteDocument(ctx, docID)` — deletes a single document by ID; returns async `*TaskInfo`
- **meilikit**: Add `validateDocID` internal helper for document ID validation

(Claude Code:Opus 4.6 (1M context))

## [10.2.9] - 2026-04-04

### Fixed

- **meilikit**: Fix Configure error path discarding original error, returning generic message instead
- **meilikit**: Fix Configure race — now waits for async index creation task before applying settings
- **meilikit**: Fix WaitForTask tight 250ms polling loop — now uses exponential backoff (250ms → 5s cap)
- **meilikit**: Fix WaitForTask leaking `time.After` timers on context cancellation
- **meilikit**: Cap all success response body reads at 8MB via `io.LimitReader` (error bodies were already capped)
- **meilikit**: Fix `json.Marshal` error handling consistency — all marshal calls now check errors
- **meilikit**: Remove duplicate timeout default in struct tag (single source of truth in `New()`)
- **meilikit**: Eliminate code duplication in requests.go — embed `SearchOptions` instead of copying 14 fields

(Claude Code:Opus 4.6 (1M context))

## [10.2.8] - 2026-04-03

### Documentation

- **INTEGRATING.md**: Fix tick `OnError(Skip)` doc — correctly states it silently ignores errors (does not log)
- **INTEGRATING.md**: Fix external dependency count from "four" to "six" — adds `hamba/avro` (schemakit) and `franz-go` (kafkakit)

(Claude Code:Opus 4.6 (1M context))

## [10.2.7] - 2026-04-03

### Documentation

- **INTEGRATING.md**: Add "don't skip these" summary table mapping 13 common needs to the right addon package with reasons not to hand-roll each one

(Claude Code:Opus 4.6 (1M context))

## [10.2.6] - 2026-04-03

### Documentation

- **INTEGRATING.md**: Add comprehensive Addon Packages section covering `cache`, `seal`, `tick`, and `webhook` with full API examples and integration guidance
- **INTEGRATING.md**: Add Service Client Kits section documenting `graphkit` (knowledge graph), `registrykit` (entity registry), and `lakekit` (data lake) clients
- **INTEGRATING.md**: Add Event Bus Kits section with full documentation for `tracekit`, `schemakit`, `heartbeatkit`, and `announcekit`
- **INTEGRATING.md**: Update module taxonomy to classify service client kits and event bus kits as distinct categories

(Claude Code:Opus 4.6 (1M context))

## [10.2.5] - 2026-03-30

### Documentation

- **INTEGRATING.md**: Add AtLeastOnce delivery section documenting manual commit mode, comparison table (default vs AtLeastOnce), when to use, handler error/DLQ semantics
- **README.md**: Update kafkakit package description to mention AtLeastOnce delivery; update current version to 10.2.4
- **PRODUCT.md**: Update Event Bus Integration section to describe configurable delivery guarantees

(Claude Code:Opus 4.6 (1M context))

## [10.2.4] - 2026-03-30

### Features

- **kafkakit**: Add `AtLeastOnce` delivery mode to `SubscriberConfig`. When enabled:
  - Disables auto-commit; offsets are committed only after all handlers in a batch complete
  - Adds `OnPartitionsRevoked` callback to commit offsets before partition reassignment
  - Uses batch-and-wait dispatch (vs rolling) so commits reflect completed work
  - Adds unconditional shutdown commit to flush processed offsets before close
  - Recommended for slow handlers (>1s processing time). Trade-off: at-least-once instead of at-most-once delivery

(Claude Code:Opus 4.6 (1M context))

## [10.2.3] - 2026-03-30

### Bug Fixes

- **kafkakit**: Fix subscriber concurrency model that only processed 1 record at a time. Two changes:
  1. **Wire up MaxPollRecords**: Switch from `PollFetches` to `PollRecords(ctx, maxPoll)`. When `Concurrency > 1`, auto-scales `maxPoll` to `Concurrency * 2` (unless `MaxPollRecords` is explicitly set higher) so each poll returns enough records to saturate the worker pool.
  2. **Rolling semaphore model**: Remove per-batch `wg.Wait()` that blocked after each poll. The semaphore now acts as a rolling concurrency limiter — the poll loop continues immediately after dispatching workers. `WaitGroup` is retained only for graceful shutdown drain via `defer wg.Wait()`.

(Claude Code:Opus 4.6 (1M context))

## [10.2.2] - 2026-03-29

### Documentation

- **INTEGRATING.md**: Add mandatory AGENTS.md snippet for consumer repos using vendoring with local `replace` directives — tells consumers they MUST add vendor freshness guidance to their own AGENTS.md so AI agents know to run `go mod vendor`.

(Claude Code:Opus 4.6 (1M context))

## [10.2.1] - 2026-03-29

### Documentation

- **INTEGRATING.md**: Add vendor freshness warning to "Things to watch out for" — documents that `go mod vendor` must be re-run after local chassis-go updates when using `replace` directives, and includes explicit guidance for AI agents/coding assistants to check vendor currency before building or debugging.

(Claude Code:Opus 4.6 (1M context))

## [10.2.0] - 2026-03-29

### New Features

- **kafkakit**: Add subscriber concurrency support. New `Concurrency` field on `SubscriberConfig` controls parallel message dispatch: `0` or `1` = sequential (default, backward-compatible), `>1` = concurrent workers with semaphore-bounded parallelism. When concurrency is enabled, each poll batch dispatches handlers via goroutines gated by a channel semaphore, with `sync.WaitGroup` drain before the next poll. The semaphore is allocated once and reused across batches.

### Tests

- **kafkakit**: Add subscriber_test.go -- `TestSubscriberConfig_ConcurrencyDefault`, `TestNewSubscriber_StoresConcurrency`, `TestNewSubscriber_ConcurrencyZeroIsSequential`, `TestNewSubscriber_NegativeConcurrencyTreatedAsSequential`, `TestConcurrentDispatch_MaxActiveWorkers`, `TestConcurrentDispatch_ErrorIsolation`, `TestConcurrentDispatch_DrainOnClose` (7 new tests)

(Claude Code:Opus 4.6 (1M context))

## [10.1.0] - 2026-03-29

### Documentation

- **README.md**: Update version badge to 10.1.0, add `SetAppVersion` to quick start and version gate section with auto-rebuild description
- **GO-BEST-PRACTICES.md**: Rewrite section 4 (VERSION & LDFLAGS → VERSION & App Version) — `appversion.go` + `SetAppVersion` is now the primary recommendation, LDFLAGS is legacy
- **INTEGRATING.md**: Add `SetAppVersion(yourpkg.AppVersion)` to all code examples: CLI/batch mode, minimal HTTP service, XYOps client, XYOps worker
- **chassis-docs**: Update Go examples in README.md and 06-service-adoption.md with `SetAppVersion` pattern, language-qualified (Go only, Python/TypeScript unchanged)

(Claude Code:Opus 4.6 (1M context))

## [10.0.15] - 2026-03-29

### Documentation

- **CLAUDE.md**: Add mandatory Version & Freshness section — documents `appversion.go` root-package embed pattern, `SetAppVersion` requirement for all consumer binaries, `--version` flag behavior, auto-rebuild behavior, and explicit warnings against symlinks/copies of VERSION in cmd/ directories.
- **INTEGRATING.md**: Rewrite "Version gate" section to "Version gate and app version" — documents the complete `appversion.go` + `SetAppVersion` + `RequireMajor` wiring pattern with step-by-step instructions.

(Claude Code:Opus 4.6 (1M context))

## [10.0.14] - 2026-03-28

### New Features

- **chassis**: Auto-rebuild freshness check. When `SetAppVersion()` is called and the binary's compiled version is older than the VERSION file on disk, `RequireMajor()` automatically recompiles the binary and re-execs. Walks up from binary location to find `go.mod` + `VERSION`, verifies module path via `debug.ReadBuildInfo`, builds to temp file with atomic rename, 2-minute build timeout. Opt out with `CHASSIS_NO_REBUILD=1`. Loop guard via `CHASSIS_REBUILD_GUARD` env var. Dormant until consumer calls `SetAppVersion()`.

(Claude Code:Opus 4.6 (1M context))

## [10.0.11] - 2026-03-28

### New Features

- **chassis**: Automatic `--version` flag for all binaries. `RequireMajor()` now intercepts `--version` in os.Args, prints version info, and exits 0. Every binary built with chassis-go gets this for free — no consumer code changes needed.
- **chassis**: Add `SetAppVersion(v string)` so consumers can optionally include their own app version in `--version` output (e.g. `mybinary 2.1.0 (chassis-go 10.0.11)`).

(Claude Code:Opus 4.6 (1M context))

## [10.0.10] - 2026-03-27

### Tests

- **internal/otelutil**: Add histogram_test.go -- first-ever tests for LazyHistogram (basic usage, concurrent init safety, value recording)
- **kafkakit**: Add tenant_test.go -- Revoke, concurrent access, grant idempotency, multiple grants
- **kafkakit**: Add envelope_test.go -- nil entity refs normalization, JSON roundtrip, invalid JSON error, non-JSON data handling, event ID format
- **call**: Add RemoveBreaker and RemoveBreaker_Nonexistent tests
- **errors**: Add FromError(nil), WriteProblem(nil), ProblemDetail(nil request), WithDetail/WithCause immutability tests
- **secval**: Add ValidateIdentifier 64/65 char boundary, SafeFilename unicode/null byte/whitespace collapse, all-non-ASCII filename tests
- **health**: Add empty checks map, Content-Type assertion, valid JSON response tests
- **guard/cors**: Add AllowCredentials header, preflight custom methods/headers tests
- **guard/timeout**: Add write-after-deadline discard, panic re-propagation tests
- **cache**: Add concurrent read/write/delete/prune race-safety test
- **work**: Add Map context cancellation early abort, Race single task, Stream context cancellation tests

(Claude Code:Opus 4.6 (1M context))

## [10.0.9] - 2026-03-27

### New Features

- **registry**: Add `killPreviousInstances()` — on startup, scans PID files for the same service, sends SIGTERM to any live stale instances, waits up to 3 seconds for graceful shutdown, then SIGKILL. Called from both `Init()` and `InitCLI()` before `cleanStale()`. Prevents port conflicts and duplicate daemons on restart.

(Claude Code:Opus 4.6 (1M context))

## [10.0.8] - 2026-03-27

### Fixed

- xyops.Config: add `required:"false"` to BaseURL, APIKey, and ServiceName fields — the v10.0.7 fix removed `required:"true"` but config.loadFields defaults to required when no tag is present, so services embedding xyops.Config still panicked without XYOPS env vars

(Claude Code:Opus 4.6)

## [10.0.7] - 2026-03-26

### Fixed

- xyops.Config: removed required:"true" from BaseURL and APIKey fields so services that embed xyops.Config in their main config struct no longer panic on startup when XYOPS env vars are not set
- xyops.New: monitoring bridge is now automatically disabled when BaseURL is empty, even if MonitorEnabled is true
- xyops.Client.apiRequest: returns a clear error ("not configured") instead of making requests to an empty URL

(Claude Code:Opus 4.6 (1M context))

## [10.0.6] - 2026-03-26

### Fixed

- config.MustLoad now recurses into nested struct fields (e.g. xyops.Config, kafkakit.Config) so their env tags and defaults are applied correctly; previously nested structs were silently left at zero values
- tick.Every returns an error instead of panicking when given a zero/negative interval

(Claude Code:Opus 4.6 (1M context))

## [10.0.5] - 2026-03-25

### Docs

- Add Go Best Practices reference section to INTEGRATING.md with key highlights (cross-platform builds, binary naming, VERSION injection, Dockerfile conventions, required Makefile targets)

(Claude Code:Opus 4.6 (1M context))

## [10.0.4] - 2026-03-23

### Docs

- Fix GO-BEST-PRACTICES.md Dockerfile template: add GOARCH=amd64 to match actual reference implementations

(Claude Code:Opus 4.6 (1M context))

## [10.0.3] - 2026-03-23

### Docs

- Added GO-BEST-PRACTICES.md: prescriptive build and project conventions for agents maintaining Go services. Covers cross-platform builds (Makefile cross-compile targets, launcher scripts), Makefile conventions, binary naming, VERSION/LDFLAGS injection, and Dockerfile patterns. Includes copy-paste templates and reference implementations.

(Claude Code:Opus 4.6 (1M context))

## [10.0.2] - 2026-03-22

### Docs

- Bulk v9→v10 update: all import paths, RequireMajor, version badge across README, AGENTS.md, INTEGRATING.md, XYOPS.md
- Added 8 new v10 modules to README (kafkakit, schemakit, tracekit, heartbeatkit, announcekit, registrykit, graphkit, lakekit)
- Added franz-go and hamba/avro to README dependencies section
- Added event bus integration section to INTEGRATING.md with cross-references to chassis-docs
- Fixed NewSubscriber quick reference to include required consumerGroup param
- Removed nonexistent DisableHeartbeat/DisableAnnounce config flag references
- Fixed deploy.json spec version from "9.0" to "10.0"
- Fixed registry PID JSON example from "8.0.0" to "10.0.1"

(Claude Code:Opus 4.6)

## [10.0.1] - 2026-03-22

### New Features

- **lifecycle**: Wire kafkakit, heartbeatkit, and announcekit into the lifecycle module. `Run()` now accepts `Option` values mixed with components: `WithKafkaConfig(cfg)` enables automatic kafkakit publisher creation, heartbeatkit liveness publishing, and announcekit service lifecycle events (started/stopping). `WithServiceName(name)` overrides the service name for these integrations. When kafkakit is configured, the publisher is created on startup and closed on shutdown; heartbeatkit and announcekit are started/stopped automatically. All announce calls use a best-effort timeout (`AnnounceTimeout`, default 5s) to avoid blocking the service lifecycle when brokers are unreachable. If `Config.Source` is empty, it defaults to the resolved service name. The entire integration is conditional -- if `BootstrapServers` is empty, everything is silently skipped.

### Tests

- **lifecycle**: 11 new tests -- kafkakit disabled config, enabled config, source defaults to service name, custom service name option, component error with kafkakit, registry integration with kafkakit, mixed options and components, resolveName from cwd, resolveName from env, WithKafkaConfig unit, WithServiceName unit

(Claude Code:Opus 4.6)

## [10.0.0] - 2026-03-22

### Breaking Changes

- **Module path migrated to v10**: All import paths changed from `chassis-go/v9` to `chassis-go/v10`. All consumer code must update imports and call `chassis.RequireMajor(10)`.

### Theme: Event Bus + Platform Connectivity

(Claude Code:Opus 4.6)

## [9.0.8] - 2026-03-22

### New Features

- **graphkit**: New HTTP client for graphiti_svc -- `NewClient(baseURL, opts...)` creates a client with configurable tenant ID and timeout (default 5s); `Search(ctx, query)` searches the knowledge graph; `Recall(ctx, query, at)` retrieves entities optionally at a point in time; `Cypher(ctx, query, params)` executes Cypher queries; `EntityGraph(ctx, entityName, opts...)` returns graph neighborhood with configurable depth; `EntityTimeline(ctx, entityName)` returns temporal event history; `Paths(ctx, from, to, opts...)` finds paths with configurable MaxHops; all requests set X-Tenant-ID and X-Trace-ID headers via tracekit
- **lakekit**: New HTTP client for lake_svc -- `NewClient(baseURL, opts...)` creates a client with configurable tenant ID and timeout (default 5s); `Query(ctx, sql, params...)` executes SQL queries; `EntityHistory(ctx, entityID)` returns entity event history; `Datasets(ctx)` lists all datasets with schema; `DatasetStats(ctx, name)` returns dataset metadata; all requests set X-Tenant-ID and X-Trace-ID headers via tracekit

### Tests

- **graphkit**: 12 tests -- search with header verification, recall with/without time, cypher with/without params, entity graph with depth, entity graph 404, entity timeline with headers, entity timeline 404, paths with MaxHops, service unavailable 503, network timeout
- **lakekit**: 10 tests -- query with header verification, query without params, entity history with headers, entity history 404, datasets list with headers, dataset stats with headers, dataset stats 404, service unavailable 503, network timeout, forbidden 403

(Claude Code:Opus 4.6)

## [9.0.7] - 2026-03-22

### New Features

- **registrykit**: New HTTP client for registry_svc — `NewClient(baseURL, opts...)` creates a client with configurable tenant ID and timeout (default 5s); `Resolve(ctx, entityType, opts...)` looks up entities by CRD, domain, email, slug, or namespaced identifier (404 returns nil, nil); `Related(ctx, entityID, opts...)` returns relationships with optional type/rel/as_of filters; `Descendants(ctx, entityID, opts...)` and `Ancestors(ctx, entityID)` traverse entity hierarchy; `Graph(ctx, entityID, opts...)` returns nested graph with configurable depth; `CreateEntity(ctx, req)` creates entities; `AddIdentifier(ctx, entityID, ns, val)` adds identifiers; `CreateRelationship(ctx, req)` creates relationships; `Merge(ctx, winnerID, loserID, reason)` merges entities; all requests set X-Tenant-ID and X-Trace-ID headers via tracekit

### Tests

- **registrykit**: 14 tests — resolve found with header verification, resolve 404 returns nil, resolve 503 error, resolve 403 forbidden, related returns relationships, create entity, merge 409 conflict, network timeout, resolve by identifier, graph returns tree, descendants, ancestors, add identifier, create relationship

(Claude Code:Opus 4.6)

## [9.0.6] - 2026-03-22

### New Features

- **heartbeatkit**: New module for zero-config automatic liveness events — `Start(ctx, pub, cfg)` publishes heartbeat payloads to `ai8.infra.heartbeat` at a configurable interval (default 30s) with service name, hostname, PID, uptime, version, and status; enriches payload with publisher stats if the publisher implements `statsProvider` interface; `Stop()` halts publishing; supports context cancellation
- **announcekit**: New module for standardized service and job lifecycle events — `SetServiceName(name)` configures identity; service lifecycle: `Started()`, `Ready()`, `Stopping()`, `Failed(err)` publish to `ai8.infra.{service}.lifecycle.{state}`; job lifecycle: `JobStarted()`, `JobComplete()`, `JobFailed()` publish to `ai8.infra.{service}.job.{state}`; all functions accept a `publisher` interface for dependency inversion

### Tests

- **heartbeatkit**: 7 tests — publishes at interval, correct subject, payload fields, stop halts publishing, default interval, context cancellation, stats provider enrichment
- **announcekit**: 9 tests — started/ready/stopping/failed correct subjects, failed includes error, job started/complete/failed correct subjects with job_name/job_id, lifecycle pattern validation, job pattern validation

(Claude Code:Opus 4.6)

## [9.0.5] - 2026-03-22

### New Features

- **tracekit**: New module for trace ID propagation across events and HTTP calls — `GenerateID()` creates `tr_` + 12 hex random chars; `NewTrace(ctx)` creates a new trace ID on context; `WithTraceID(ctx, id)` sets a specific trace ID; `TraceID(ctx)` extracts trace ID from context; `Middleware(next)` HTTP middleware that extracts `X-Trace-ID` header (or generates new), sets on context, and adds to response header

### Tests

- **tracekit**: Add TestGenerateID_Format, TestGenerateID_Uniqueness, TestNewTrace, TestWithTraceID, TestTraceID_EmptyContext, TestMiddleware_ExtractsHeader, TestMiddleware_GeneratesIfMissing, TestMiddleware_SetsResponseHeader (8 tests, all pass)

(Claude Code:Opus 4.6)

## [9.0.4] - 2026-03-22

### New Features

- **kafkakit**: New module for Kafka/Redpanda publish/subscribe — `Config` with bootstrap servers, schema registry, tenant, and source identity; `Event`/`OutboundEvent` types with Ack/Reject/Header methods; `Publisher` with `Publish(ctx, subject, data)` and `PublishBatch(ctx, events)` using franz-go, atomic `Stats()` counters; `Subscriber` with `Subscribe(pattern, handler)`, `SubscribeMulti(handlers)`, `Start(ctx)` blocking consumer loop, wildcard pattern matching (`ai8.scanner.>` matches `ai8.scanner.gdelt.signal.surge`), and DLQ routing to `ai8._dlq.{subject}` on handler errors; `TenantFilter` with own/shared/granted tenant delivery logic; envelope wrapping with `evt_` + 12 hex ID, millisecond timestamps, OTel trace ID extraction, and JSON wire format

### Tests

- **kafkakit**: Add TestConfigEnabled, TestConfigDisabled, TestWrapUnwrapEnvelope, TestWrapEnvelope_UniqueIDs, TestTenantFilter_OwnTenant, TestTenantFilter_SharedTenant, TestTenantFilter_OtherTenant, TestTenantFilter_GrantedTenant, TestTenantFilter_EmptyTenantDelivers, TestPublisherStats, TestSubscribePattern, TestEvent_Ack, TestEvent_Reject, TestEvent_Header, TestOutboundEvent, TestDLQTopic, TestEnvelopeToEvent (17 tests, all pass)

(Claude Code:Opus 4.6)

## [9.0.3] - 2026-03-22

### New Features

- **schemakit**: New module for Avro schema management — `NewRegistry(url)` creates a Schema Registry client, `LoadSchemas(dir)` walks directories to load `.avsc` files keyed by `namespace.name`, `Validate(schema, data)` checks data against Avro schemas, `Serialize(schema, data)` encodes with Confluent wire format header (0x00 + 4-byte schema ID), `Deserialize(raw)` decodes wire-format payloads, `Register(ctx, schema)` registers schemas with Redpanda/Confluent Schema Registry via HTTP

### Tests

- **schemakit**: Add TestNewRegistry, TestGetSchema_NotFound, TestLoadSchemas, TestValidate_Valid, TestValidate_Invalid, TestSerializeDeserialize, TestRegister (7 tests, all pass)

(Claude Code:Opus 4.6)

## [9.0.2] - 2026-03-16

### Improvements

- **deploy**: `RunHook()` now returns `error` instead of silently discarding hook execution failures
- **deploy**: Default `Protocol` to `"tcp"` for dependencies missing the field in deploy.json
- **deploy**: New tests for environment override (provider/region/cluster) and default dependency protocol
- **docs**: Update INTEGRATING.md with full `deploy` module documentation section
- **docs**: Update README.md import paths from v6/v7 to v9, fix version badge

(Claude Code:Opus 4.6)

## [9.0.1] - 2026-03-16

### New Features

- **call**: Add `WithHTTPClient(hc)` option to replace the underlying `*http.Client`, enabling custom transports (proxy routing, SSRF-safe dialer) and redirect policies while preserving call-level retry, circuit breaker, and timeout middleware

(Claude Code:Opus 4.6)

## [9.0.0] - 2026-03-08

### Breaking Changes

- **Module path migrated to v9**: All import paths changed from `chassis-go/v8` to `chassis-go/v9`. All consumer code must update imports and call `chassis.RequireMajor(9)`.

### New Features

- **deploy**: Add `Spec()` method — reads `"chassis"` field from deploy.json for spec versioning (defaults to `"8.0"` for pre-v9 files)
- **deploy**: Add `Environment()` method — auto-detects runtime environment (kubernetes/container/vm/bare-metal) with deploy.json `environment` block and env var overrides (`CHASSIS_ENV`, `CHASSIS_PROVIDER`, `CHASSIS_REGION`, `CHASSIS_CLUSTER`); Kubernetes auto-detects namespace and pod name
- **deploy**: Add `Endpoints()` and `Endpoint(name)` methods — typed endpoint objects with port, protocol (default `"http"`), and path from deploy.json
- **deploy**: Add `Dependencies()` method — service topology declarations with required/optional (`Required` defaults to `true` when omitted)
- **deploy**: Add `Health(components)` method — structured health payload with service name, version, chassis spec, runtime, uptime (float64 seconds), environment, endpoints, and component status
- **deploy**: New types: `Environment`, `Endpoint`, `Dependency`, `HealthStatus`
- **deploy**: Add K8s discovery path `/app/deploy/<svc>/` at priority 2 in search order (`CHASSIS_DEPLOY_DIR` → `/app/deploy/<svc>/` → `~/deploy/<svc>/` → `/deploy/<svc>/`)
- **deploy**: Deploy struct gained `name` and `created` fields for service identity and uptime tracking

(Claude Code:Opus 4.6)

## [8.0.3] - 2026-03-07

### Bug Fixes

- **registry**: Fix CLI command polling goroutine to use channel-based shutdown instead of polling active flag before first tick
- **registry**: Stop flag parsing after `--` separator in `parseFlags`
- **registry**: Properly close `cliDone` channel in `ShutdownCLI` and `ResetForTest`
- **xyops**: Check all `json.Unmarshal` return errors instead of silently ignoring them (RunEvent, GetJobStatus, SearchJobs, ListEvents, GetEvent, ListActiveAlerts)

### Docs

- **AGENTS.md**: Update module path reference from v7 to v8, add XYOps integration guidance
- **XYOPS.md**: Add full XYOps integration guide with code examples

(Claude Code:Opus 4.6)

## [8.0.2] - 2026-03-07

### Bug Fixes

- **deploy**: Use `os.LookupEnv` instead of `os.Getenv` to correctly distinguish empty env vars from unset ones in `LoadEnv`
- **deploy**: Strip surrounding quotes (single/double) from `.env` file values for Docker/dotenv compatibility
- **deploy**: Add path confinement check in `RunHook` to prevent path traversal outside hooks directory
- **tick**: Implement `Jitter` option — previously accepted but silently ignored; now applies random delay before each tick
- **config**: Panic on malformed `min`/`max` values in validate tag instead of silently defaulting to 0
- **config**: Extend `fieldAsFloat` to handle all integer, unsigned integer, and float types for validation
- **secval**: Hoist `\s+` regex to package-level var in `SafeFilename` to avoid recompilation on every call

(Claude Code:Opus 4.6)

## [8.0.1] - 2026-03-07

### New Features

- **xyops**: Add xyops client module — curated API methods (RunEvent, GetJobStatus, CancelJob, SearchJobs, ListEvents, GetEvent, ListActiveAlerts, AckAlert, Ping, FireWebhook), Raw escape hatch, monitoring bridge with metric push via tick.Every, response caching via cache, webhook dispatch via webhook.Sender

### Tests

- **xyops**: Add TestPing, TestRunEvent, TestGetJobStatusWithCaching, TestFireWebhook, TestClientConstruction, TestMonitoringBridgeDisabled, TestMonitoringBridgeEnabled, TestListEvents, TestListActiveAlerts, TestRawEscapeHatch

(Claude Code:Opus 4.6)

## [7.0.0] - 2026-03-07

### Breaking Changes

- **Module path migrated to v7**: All import paths changed from `chassis-go/v6` to `chassis-go/v7`. All consumer code must update imports and call `chassis.RequireMajor(7)`.

### New Features

- **registry**: Add CLI/batch mode support via `InitCLI(chassisVersion)` for CLI tools and batch processes that need visibility without being long-running services
- **registry**: Add `Progress(done, total, failed)` for tracking batch progress with percentage calculation
- **registry**: Add `StopRequested()` for cooperative stop signaling in CLI mode
- **registry**: Add `ShutdownCLI(exitCode)` which rewrites the PID file with completion status instead of deleting it
- **registry**: Add `ProgressSummary` struct for progress tracking state
- **registry**: Add `parseFlags()` helper that parses CLI arguments into a `map[string]string` with sensitive flag redaction
- **registry**: Add `Mode`, `Flags`, `Status`, `ExitedAt`, `ExitCode`, and `Summary` fields to `Registration` struct
- **registry**: Service mode `Init()` now sets `mode: "service"` and `status: "running"` in the PID file
- **registry**: Stale cleanup now preserves completed/failed CLI PID files for 24 hours before removing them
- **registry**: Stop command in CLI mode sets `stopRequested` flag instead of calling cancelFn

### Tests

- **registry**: Add `TestInitCLI` — verify PID file has mode "cli" and parsed flags
- **registry**: Add `TestInitServiceMode` — verify service mode sets mode and status
- **registry**: Add `TestProgress` — verify progress events in log
- **registry**: Add `TestShutdownCLI` — verify PID file is rewritten (not deleted) with completion status
- **registry**: Add `TestShutdownCLIFailed` — verify failed exit code handling
- **registry**: Add `TestStopRequested` — verify stop command sets the flag in CLI mode
- **registry**: Add `TestParseFlags` — verify various flag formats (equals, space-separated, boolean, short, sensitive redaction)
- **registry**: Add `TestIsSensitiveFlag` — verify sensitive flag detection

### Documentation

- **INTEGRATING.md**: Add CLI/batch mode section with usage example
- **INTEGRATING.md, README.md, AGENTS.md**: Update all version references from v6 to v7

(Claude Code:Opus 4.6)

## [6.0.11] - 2026-03-07

### Breaking Changes

- **registry**: `Status()` and `Errorf()` now crash the process (via `os.Exit(1)`) if called before `Init()` / `lifecycle.Run()`. Previously they were silent no-ops.
- **registry**: New `AssertActive()` function crashes the process if the registry is not initialized. All post-lifecycle chassis modules (`httpkit`, `grpckit`, `call`, `work`, `health`) now call `AssertActive()` at runtime, enforcing that `lifecycle.Run()` must be called before any chassis service module is used.

### Improvements

- **httpkit**: `RequestID`, `Logging`, `Recovery`, and `Tracing` middleware handlers call `registry.AssertActive()` on first request
- **grpckit**: `UnaryLogging`, `UnaryRecovery`, `UnaryTracing`, `UnaryMetrics`, `StreamLogging`, `StreamRecovery`, `StreamTracing`, and `StreamMetrics` interceptors call `registry.AssertActive()` on each RPC
- **call**: `Client.Do()` calls `registry.AssertActive()` before executing requests
- **work**: `Map`, `All`, `Race`, and `Stream` call `registry.AssertActive()` alongside `chassis.AssertVersionChecked()`
- **health**: `Handler()`, `All()`, and `CheckFunc()` call `registry.AssertActive()` at construction time

### Tests

- **registry**: Replace `TestStatusNoOpBeforeInit` with `TestStatusCrashesBeforeInit`, `TestErrorfCrashesBeforeInit`, and `TestAssertActiveCrashesBeforeInit` using subprocess crash detection
- **call, grpckit, health, httpkit, work**: Add registry initialization to `TestMain` so tests pass with mandatory registry enforcement

(Claude Code:Opus 4.6)

## [6.0.10] - 2026-03-07
- Sync uncommitted changes

All notable changes to this project will be documented in this file.

## [6.0.9] - 2026-03-07

### Documentation

- **INTEGRATING.md**: Fix grpckit metric name (`rpc.server.duration`, not `grpc_server_duration_seconds`)
- **INTEGRATING.md**: Fix testkit.SetEnv description (uses `t.Setenv`, not `os.Setenv` + `t.Cleanup`)

(Claude Code:Opus 4.6)

## [6.0.8] - 2026-03-07

### Documentation

- **README.md**: Update version to 6.0.8, fix secval key list (only 3 prototype pollution keys), add missing `PayloadTooLargeError` to factory list, fix `guard.IPFilter` field names (`Allow`/`Deny` not `AllowCIDRs`/`DenyAction`), fix `guard.CORS` `MaxAge` type (`time.Duration` not int), add `chassis.Port()` and `registry.Port()` mentions

(Claude Code:Opus 4.6)

## [6.0.7] - 2026-03-07

### Documentation

- **INTEGRATING.md**: Fix `EnabledFor` example — restore missing `ctx` first argument to match actual function signature

(Claude Code:Opus 4.6)

## [6.0.6] - 2026-03-07

### Documentation

- **INTEGRATING.md**: Fix secval dangerous keys list to match implementation (only `__proto__`, `constructor`, `prototype` — business-domain words intentionally excluded)
- **INTEGRATING.md**: Fix lifecycle.Run docs — unsupported argument types panic at startup, not silently ignored

(Claude Code:Opus 4.6)

## [6.0.5] - 2026-03-07

### New Features

- **chassis**: Add `Port(name, offset...)` — deterministic port assignment using djb2 hash, mapping service names to stable ports in range 5000–48000
- **chassis**: Add `PortHTTP` (0), `PortGRPC` (1), `PortMetrics` (2) standard role offset constants
- **registry**: Add `Port(role, port, label, ...opts)` for declaring service ports in PID registration JSON
- **registry**: Add `Proto(string)` option to override default wire protocol per port declaration
- **registry**: Add `BasePort` and `Ports` fields to `Registration` struct; `base_port` and `ports` now appear in PID JSON for viewer/operational tooling

(Claude Code:Opus 4.6)

## [6.0.4] - 2026-03-07

### Bug Fixes

- **call**: Fix data race in retry body rewind — move `GetBody` call from shared `Retrier.req` field into per-call closure with local attempt counter, preventing races under concurrent `Client.Do` use
- **logz**: Add regression test for `WithGroup` + `WithAttrs` + trace context interaction

(Claude Code:Opus 4.6)

## [6.0.3] - 2026-03-07

### Bug Fixes

- **call**: Rewind request body via `GetBody` before each retry attempt; previously POST/PUT retries silently sent empty bodies
- **logz**: Fix `traceHandler` dropping `WithAttrs` attributes when groups are active and trace context is present; attrs added after `WithGroup` are now included in reconstructed records
- **guard**: `timeoutWriter.Write` now returns `http.ErrHandlerTimeout` after the deadline fires, preventing unbounded buffer growth from slow handler goroutines
- **work**: `Map` and `All` now use `select` with `ctx.Done()` when acquiring the semaphore, so cancelled contexts are respected immediately instead of blocking

### Security Fixes

- **registry**: Redact sensitive command-line arguments (passwords, tokens, keys) from PID file `args` field to prevent credential leakage
- **registry**: Validate service directory permissions on Init; reject directories with group/world-readable permissions (must be 0700)
- **secval**: Reduce dangerous key list to prototype-pollution vectors only (`__proto__`, `constructor`, `prototype`); remove common business-domain words that caused false positives

### Improvements

- **lifecycle**: Add `RunComponents()` type-safe variant of `Run()` that accepts `...Component` for compile-time type checking
- **call**: Add `RemoveBreaker(name)` to allow cleanup of named circuit breakers, preventing memory leaks with dynamic breaker names
- **httpkit**: `errorForStatus` now preserves the caller's original HTTP status code for unmapped values instead of silently replacing with 500
- **config**: Remove redundant hand-rolled `contains`/`searchString` test helpers; use `strings.Contains`
- **registry**: Document that exported config variables (`BasePath`, `HeartbeatInterval`, `CmdPollInterval`) must be set before `lifecycle.Run` and are `time.Duration` values
- **testkit**: Document TOCTOU race inherent in `GetFreePort`

(Claude Code:Opus 4.6)

## [6.0.2] - 2026-03-07

### Security Fixes

- **registry**: Restrict directory permissions from 0755 to 0700 and file permissions from 0644 to 0600, preventing local users from enumerating/controlling services
- **registry**: Replace predictable `.tmp` path in atomicWrite with `os.CreateTemp` to prevent symlink attacks
- **call**: Remove full URL (including query params) from OTel span attributes; log only `url.path` to prevent leaking secrets
- **httpkit**: Use HTTP method only as OTel span name instead of `method + path` to prevent high-cardinality span explosion

### Bug Fixes

- **httpkit**: Add `Write()` override to `responseWriter` so `headerWritten` is tracked correctly; fixes garbled output when panic occurs after partial response write
- **registry**: Change `active` from plain `bool` to `atomic.Bool` to eliminate data race in `Status()` and `Errorf()`
- **registry**: Read `cancelFn` under mutex lock in `pollOnce()` to eliminate data race on stop/restart commands
- **registry**: Clean up orphaned `.log.jsonl` and `.cmd.json` files for dead processes in `cleanStale()`
- **lifecycle**: Handle `syscall.Exec` error on restart; previously the error was silently discarded
- **lifecycle**: Fix unreliable signal detection by removing redundant `signal.Notify` registration; use `signalCtx.Err()` check instead, which is deterministic
- **guard**: Add `Unwrap()` to `timeoutWriter` so `http.NewResponseController` can access `http.Flusher`/`http.Hijacker` through timeout middleware

### Improvements

- **testkit**: Delegate `SetEnv` to `t.Setenv` for automatic parallel-test safety and cleanup
- **work**: Use `select` with `ctx.Done()` when sending `Stream` results to prevent goroutine leaks if consumer stops reading
- **call**: Remove redundant `http.Client.Timeout` assignment; context-based timeout in `Do()` is sufficient

(Claude Code:Opus 4.6)

## [6.0.1] - 2026-03-07

- Update README.md and INTEGRATING.md for v6: change all v5 references to v6, update RequireMajor(5) to RequireMajor(6), add `registry` module to package tables and import lists, add registry documentation sections covering Status/Errorf/Handle API, file layout, built-in commands, and automatic lifecycle integration. (Claude Code:Opus 4.6)

## [6.0.0] - 2026-03-07

### Breaking Changes

- **Module path migrated to v6**: All import paths changed from `chassis-go/v5` to `chassis-go/v6`
- **`lifecycle.Run()` now auto-initializes registry**: Every service is automatically registered at `/tmp/chassis/` on startup. This is mandatory and cannot be disabled.

### New Features

- **`registry` module**: File-based service self-registration with heartbeat, status logging, error reporting, and bidirectional command system
  - `registry.Status(msg)`: Write progress/status updates
  - `registry.Errorf(fmt, args...)`: Write error events
  - `registry.Handle(name, desc, fn)`: Register custom commands
  - Built-in `stop` and `restart` commands
  - Automatic heartbeat every 30s, command polling every 3s
  - Stale PID cleanup on startup
  - Atomic file writes for crash safety

(Claude Code:Opus 4.6)

## [5.0.3] - 2026-03-07

- Integrate `registry` module into `lifecycle.Run()`: auto-initializes registry on startup, runs heartbeat and command-poll goroutines, determines shutdown reason (clean/error/signal), calls `registry.Shutdown()`, and supports `syscall.Exec` restart on restart command. Added integration tests verifying PID file creation during Run and cleanup after shutdown. (Claude Code:Opus 4.6)

## [5.0.2] - 2026-03-07

- New `registry` package: file-based service registration at `/tmp/chassis/` with PID tracking, JSONL logging, heartbeat, command polling (stop/restart/custom), and stale PID cleanup. Zero chassis dependencies — stdlib only. (Claude Code:Opus 4.6)

## [5.0.1] - 2026-02-17

- Comprehensive README.md rewrite with full package documentation, usage examples, design principles, and observability reference (Claude Code:Opus 4.6)

## [5.0.0] - 2026-02-08

### Breaking Changes

- **Module path migrated to v5**: All import paths changed from `chassis-go/v4` to `chassis-go/v6`. All consumer code must update imports and call `chassis.RequireMajor(5)`.
- **OTLP defaults to TLS**: `otel.Init()` now uses TLS for OTLP gRPC connections by default. Set `Insecure: true` in `otel.Config` to use plaintext (dev/test environments).
- **Rate limiter requires MaxKeys**: `guard.RateLimitConfig` now requires a `MaxKeys int` field for LRU capacity. Rate limiter internals rewritten from O(n) sweep to O(1) LRU eviction using `container/list`.
- **Guard config validation panics**: `guard.RateLimit`, `guard.MaxBody`, and `guard.Timeout` now panic at construction on invalid config (zero rate, zero window, nil KeyFunc, zero MaxKeys, non-positive maxBytes, non-positive duration).
- **httpkit.JSONProblem delegates to errors.WriteProblem**: The `httpkit.JSONProblem` function now delegates to the consolidated `errors.WriteProblem` for RFC 9457 Problem Details rendering.
- **Health error wrapping preserves originals**: `health.All` now wraps check errors with `fmt.Errorf("%s: %w", name, err)`, preserving the original error chain for `errors.Is`/`errors.As`.
- **Metrics label hashing includes keys**: `metrics.CounterVec` and `metrics.HistogramVec` now hash `key=value` pairs (not just values), preventing collisions when different keys share the same values.

### New Features

- **`flagz` module**: Feature flags with pluggable sources (`FromEnv`, `FromMap`, `FromJSON`, `Multi`), boolean checks (`Enabled`), percentage rollouts with consistent FNV-1a hashing (`EnabledFor`), variant strings (`Variant`), and OTel span event integration.
- **`guard.CORS`**: Cross-Origin Resource Sharing middleware with preflight handling (204), origin matching, configurable methods/headers/max-age, and credentials validation.
- **`guard.SecurityHeaders`**: Security headers middleware (CSP, HSTS, X-Frame-Options, X-Content-Type-Options, Referrer-Policy, Permissions-Policy) with `DefaultSecurityHeaders` for secure defaults.
- **`guard.IPFilter`**: IP filtering middleware with CIDR-based allow/deny lists. Deny rules evaluated first (take precedence). Returns 403 Forbidden with RFC 9457 Problem Details.
- **`errors.ForbiddenError`**: New factory for 403 / PERMISSION_DENIED errors.
- **`errors.WriteProblem`**: Consolidated RFC 9457 Problem Details writer, used by `httpkit`, `guard`, and available for direct use. Accepts an optional `requestID` parameter.
- **Custom domain metrics**: `metrics.Recorder.Counter(name)` and `metrics.Recorder.Histogram(name, buckets)` for application-specific counters and histograms with cardinality protection.
- **`otel.Config.Insecure` field**: Explicit control over TLS vs plaintext for OTLP connections.

### Bug Fixes

- **Fix call retry panic on zero BaseDelay**: `backoff()` now defaults to 100ms when `BaseDelay <= 0`, preventing `rand.Int64N(0)` panic.
- **Fix httpkit generateID panic on crypto/rand failure**: Falls back to timestamp + atomic counter instead of panicking.

## [4.0.0] - 2026-02-08

### Breaking Changes

- **Module path migrated to v4**: The Go module path is now `github.com/ai8future/chassis-go/v4`. All import paths across all packages include the `/v4` suffix. This was done to unify the Go module version with the internal `VERSION` / `chassis.Version` which was already at `4.0.0`. (Claude:Opus 4.6)
- All consumer code must update imports from `github.com/ai8future/chassis-go/...` to `github.com/ai8future/chassis-go/v4/...`
- All consumer code must call `chassis.RequireMajor(4)`
- Tracer name constants updated to include `/v4` in their package paths

## [1.0.4] - 2026-02-03

- Fix chassis.Version constant drift (was stuck at 1.0.0), add float64 to INTEGRATING.md type list (Claude:Opus 4.5)

## [1.0.3] - 2026-02-03

- Add float64 support to config.MustLoad (Claude:Opus 4.5)

## [1.0.2] - 2026-02-03

- Document chassis.Version in INTEGRATING.md (Claude:Opus 4.5)

## [1.0.1] - 2026-02-03

- Add exported `chassis.Version` constant for integrator diagnostics (Claude:Opus 4.5)

## [1.0.0] - 2026-02-03

- Initial project setup with VERSION, CHANGELOG, AGENTS.md, and standard directories
- Existing codebase includes: call (retry/breaker), config, grpckit, health, httpkit, lifecycle, logz, testkit
