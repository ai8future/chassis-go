# Windmill orchestration readiness

This repository carries the Go-side Wave 0 primitives for making chassis services callable, retry-safe, and machine-checkable from Windmill. The pinned shared contracts live in `testdata/windmill/contracts/`; update them only by copying from the suite contract source and refreshing `checksums.sha256` plus provenance.

## Trace ID contract

`X-Trace-ID` canonical format is:

```text
tr_[0-9a-f]{32}
```

`tracekit.GenerateID` emits that 128-bit lowercase-hex form. `tracekit.Middleware` accepts the canonical form and the bounded legacy migration form `tr_[0-9a-f]{12}`; arbitrary `tr_...` strings are regenerated instead of being logged or forwarded as trusted trace IDs.

## Recommended HTTP middleware stack

For Windmill-callable mutating routes, wire the stack so trace/auth/tenant evidence exists before idempotency claims are opened:

1. `tracekit.Middleware` for `X-Trace-ID` propagation/regeneration.
2. Existing `httpkit`/`guard` recovery, logging, timeouts, security headers, body limits, and request IDs.
3. `authkit.HTTPMiddleware(verifier, "scope:write")` for scoped inbound bearer auth.
4. `idemkit.Middleware(store, idemkit.WithTenantResolver(...))` for POST/PUT/PATCH/DELETE idempotency.
5. Business handler, using `errors.WriteProblem` for `application/problem+json` errors.
6. Public read-only manifest/OpenAPI routes via `orchestration.Handler` and `orchestration.OpenAPIHandler`.

Outbound Windmill/service callers can use `call.WithRetryPolicy` and `call.WithIdempotentOnlyRetries` when retries must be limited to HTTP-idempotent methods; 429/503 `Retry-After` is honored when present.

## Resource provisioning

- **L1 auth**: configure `authkit` static keys from `CHASSIS_AUTHKIT_KEYS` or an equivalent secret source. Store only scrypt hashes in config and grant the narrow scopes required by each route.
- **L2 idempotency local/test**: `idemkit.NewMemoryStore` is tenant-scoped but process-local; it is suitable for tests, demos, and single-instance local services only.
- **L2 idempotency production**: multi-instance services need a durable tenant-scoped store keyed by `(tenant_id, idempotency_key)`. Core `chassis-go` intentionally ships no DB drivers; Postgres/Redis implementations belong in service code or `chassis-go-addons`.
- **Manifests**: expose `/.well-known/chassis-capabilities.json` with `contract_version`, `profile`, `capabilities`, endpoints, and idempotency declaration. Serve authored OpenAPI documents; this package does not reflect over handlers.

## Idempotency limits

`idemkit` fingerprints method, request target, and request body. A matching completed claim replays the captured status, headers, and body with `Idempotency-Replayed: true`. A mismatched fingerprint returns a 422 problem with `code: idempotency_fingerprint_mismatch`; an in-flight duplicate returns 409. 5xx responses release the claim so a later retry can execute again.

The tenant resolver is part of the correctness boundary. Reusing the same `Idempotency-Key` across tenants must create separate claims and must never replay tenant A's response to tenant B.

## Conformance usage

Use the `conformance` package to aggregate evidence collected by service tests:

```go
report := conformance.Check(conformance.LevelL2, conformance.Evidence{
    AcceptsXTraceID:                  conformance.AcceptsTraceID("tr_0123456789abcdef0123456789abcdef"),
    EmitsProblemJSONErrors:           problemProbe.Passed,
    ErrorProblemJSONRetryClass:       problemProbe.Passed,
    ScopedBearerAuthOnMutatingRoutes: true,
    Manifest:                         &manifest,
    IdempotencyKeyReplayForMutating:  replayProbe.Passed,
    IdempotencyKeyTenantScoped:       tenantProbe.Passed,
})
if !report.Passed {
    t.Fatalf("missing Windmill readiness evidence: %v", report.Missing)
}
```

`conformance.Require` is a `testing.TB` convenience wrapper. L0-L2 checks are runtime/fixture evidence. L3 in core is **declaration-only**: true durable outbox readiness and domain-event preservation must be proven by the owning service or addon tests.
