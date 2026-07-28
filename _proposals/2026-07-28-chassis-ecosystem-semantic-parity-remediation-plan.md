# Chassis Ecosystem Semantic-Parity Remediation Plan

## Plan status

This dated proposal preserves the complete remediation plan requested for:

- `chassis-go`
- `chassis-py`
- `chassis-ts`
- Shared ecosystem documentation, including `chassis-docs` when it is an
  independently connected Git repository

The proposal is roadmap-ready, but behavior-changing implementation is blocked
until the G001 readiness artifacts below are complete. G001 may start
immediately and is limited to repository discovery, baseline capture, contract
decisions, fixture construction, and test-harness work. Do not begin G002-G010
behavior changes or releases until the implementation-readiness gate passes.

## Objective

Bring the three chassis implementations into documented semantic alignment
without introducing runtime coupling between them.

Alignment means equivalent concepts should:

- Produce compatible wire formats.
- Follow equivalent lifecycle and failure semantics.
- Use equivalent configuration rules where SDK constraints permit.
- Clearly advertise intentional capability differences.
- Never report success for unsupported or incomplete behavior.
- Be protected by equivalent contract, integration, and E2E tests.
- Have documentation that describes actual executable behavior.

## Non-negotiable constraints

- Preserve public compatibility wherever reasonably possible.
- Where existing behavior is unsafe or misleading, fail closed rather than
  preserve false-success behavior.
- Do not introduce direct runtime dependencies between language
  implementations.
- Express compatibility through mirrored fixtures and schemas, not
  cross-repository imports.
- Do not add dependencies unless genuinely required and explicitly justified.
- Read each repository's `VERSION` only immediately before its final bump.
- Update `CHANGELOG.md` for every released code or documentation change.
- Add dated lowercase `_bugs_fixed` notes for bug corrections without browsing
  unrelated scratch files.
- Stage each repository with `git add -A`.
- Use the Lore commit protocol.
- Identify the coding agent as `Codex:gpt-5.6-sol-high`.
- Commit and push each verified repository change independently.
- Do not declare parity where a capability remains intentionally unavailable.

## Implementation-readiness gate

G001 must produce a reviewed contract pack before any behavior-changing story
starts. The gate is intentionally explicit so implementation does not silently
invent ecosystem semantics while editing individual language repositories.

### Canonical fixture authority

Record all of the following in the G001 contract pack:

- The authoritative repository and directory for each shared fixture family.
- The mirrored destination path in Go, Python, and TypeScript.
- The fixture file format, schema version, manifest format, and encoding rules.
- Which files require byte identity and which require only semantic identity.
- Checksum generation and verification commands.
- The synchronization procedure and the rule preventing a language-local copy
  from becoming an independent source of truth.
- The review and release procedure for changing fixture expectations.

The contract pack must include concrete expected values rather than prose-only
requirements. It must define:

- Problem Details mappings for every built-in error class.
- Expected feature-flag buckets and decisions for every required vector.
- Expected trace-selection and normalization results for every precedence case.
- The actual per-language Kafka capability state for every matrix entry.

### Semantic decisions that must be closed

The following decisions are blockers until their exact behavior is recorded in
fixtures or a machine-readable capability manifest:

- Feature flags: missing-value behavior, explicit-disable behavior, percentage
  comparison semantics, and the treatment of non-`true` literals.
- Feature flags: an empty user ID must be processed by the normal deterministic
  `flag_name + NUL + user_id` hash path; randomness or process-local state is
  forbidden.
- Trace correlation: response-header name, emission conditions, normalization,
  and behavior when an upstream header is invalid.
- Trace correlation: the exact version or date ending legacy 12-hex support.
- Kafka: the concrete Implemented / Unsupported / Not Applicable / Planned
  value for every language and capability.
- Problem Details: canonical type URI, title, status, code, retryability, and
  `Retry-After` values for each built-in class.

### Reproducible verification manifest

Before implementation, record for each repository:

- Exact targeted and full-suite commands.
- Required toolchain versions and environment variables.
- Required Docker images, ports, health routes, brokers, and OTel backends.
- Bounded startup and test timeouts.
- Whether each integration is required or conditional.
- The evidence captured for a pass, failure, or justified conditional skip.

The phrase `where available` is not completion evidence. A check may be
conditional only when the manifest defines its availability probe and explains
why absence is acceptable. A required check must fail when its infrastructure
is unavailable.

### Compatibility, policy, and workspace prerequisites

Before behavior changes:

- Record compatibility impact, migration guidance, deprecation timing, and a
  rollback approach for every fail-closed or wire-format change.
- Resolve, commit, or explicitly isolate pre-existing edits in every target
  repository. Do not mix unrelated work into remediation commits.
- Assign ownership for shared documentation edits before changing
  `chassis-docs`.
- Identify the authoritative existing legal-policy source for Python package
  metadata. Do not infer or author licensing language during implementation.
- Record the clean branch, upstream synchronization, HEAD, version, and latest
  tag for every repository after workspace cleanup.

### Gate evidence

The gate passes only when:

- The fixture authority and mirror workflow are documented and executable.
- Fixture loaders and checksum checks pass in all three implementations.
- All semantic decisions listed above have exact expected outcomes.
- The verification manifest has no unclassified `where available` checks.
- The Python licensing-policy source is identified.
- Every target repository has a clean, synchronized, ownership-safe baseline.
- An independent reviewer finds no semantic decision left for implementers to
  guess.

## Canonical ecosystem contracts

### Problem Details

Canonical emitted representation:

- RFC Problem Details reserved fields remain top-level.
- `instance` identifies the request path or request-instance URI, never a
  request ID.
- `request_id` is a separate top-level extension.
- `trace_id`, `code`, and `retryable` are top-level extensions.
- Additional application extensions remain top-level.
- Retryable responses emit compatible `Retry-After` behavior.
- The canonical internal code is `internal_error`.
- Compatibility aliases may be accepted internally, but new responses emit
  canonical values.
- Type URI, title, status, code, and retryability mappings are fixture-driven.

Fixtures must cover:

- Every built-in error class.
- Request path and request ID.
- Trace ID.
- Retryable and non-retryable errors.
- Retry-after headers.
- Arbitrary application extensions.
- Reserved-field collision rejection.
- JSON serialization and content type.

### Feature flags

Canonical behavior:

- The explicit enable literal is exactly `true`.
- Explicit-disable and rollout fallback behavior is unambiguous.
- Percentages outside `0..100` fail validation.
- Rollout hashing uses FNV-1a over UTF-8 bytes.
- Hash input is `flag_name + NUL + user_id`.
- Empty user IDs receive deterministic behavior.
- Unicode produces identical buckets in every language.

Required vectors:

- ASCII name and user.
- Unicode name and user.
- Empty user.
- Embedded punctuation.
- Percent boundaries 0, 1, 99, and 100.
- Invalid percentage values.
- Boolean literal variations such as `TRUE`, `1`, `yes`, and `on`.

### Trace correlation

Canonical precedence:

1. Valid W3C `traceparent`.
2. Valid canonical 32-hex `X-Trace-ID`.
3. Legacy 12-hex `X-Trace-ID` during a documented compatibility window.
4. Newly generated 128-bit trace ID.

Required behavior:

- Invalid headers never become accepted correlation identifiers.
- Accepted IDs are normalized consistently.
- Middleware exposes the selected trace ID consistently.
- Response-header behavior is documented.
- Legacy support is explicitly deprecated rather than inconsistently accepted.

### Kafka capability contract

Shared semantic vocabulary:

- Event key.
- Publish and publish-keyed behavior.
- Publisher readiness/ping.
- Subscriber health.
- Delivery acknowledgement.
- Batch behavior.
- JSON envelope behavior.
- Schema integration as a separate explicit capability.
- Trace metadata transport.
- Unsupported-capability behavior.

The capability matrix must distinguish:

- Implemented.
- Unsupported and fail-closed.
- Not applicable because of SDK semantics.
- Planned but not currently available.

No stub may report successful delivery, healthy subscription, or broker
readiness.

## Execution stories

### G001 — Establish contracts and baseline evidence

1. Resolve or isolate pre-existing repository edits and assign documentation
   ownership.
2. Record clean branch and `origin/main` state for every repository.
3. Record current versions, tags, and latest commits.
4. Capture existing focused-test baselines.
5. Create the canonical fixture-authority and synchronization specification.
6. Add mirrored semantic fixtures for errors, flags, traces, and Kafka
   capabilities.
7. Add fixture provenance and checksum validation where byte identity matters.
8. Create the exact per-repository verification manifest.
9. Close every semantic decision listed in the implementation-readiness gate.
10. Identify and cite the existing Python licensing-policy source.
11. Ensure Python participates in the same contract suite as Go and TypeScript.
12. Document intentional SDK-specific differences.

Verification:

- Fixture checksums match where byte identity is required.
- Semantic fixture loaders pass in all repositories.
- Existing public tests remain green before behavior changes.
- The implementation-readiness gate is reviewed and passes.

### G002 — Repair Python full-service Docker E2E

Implementation:

- Bind the HTTP application to `0.0.0.0`.
- Make the Docker healthcheck use the actual application port and route.
- Ensure exposed ports match runtime configuration.
- Build the image in CI.
- Start it with an isolated container name and port.
- Poll health with a bounded timeout.
- Exercise a functional endpoint beyond health.
- Capture logs and inspection output on failure.
- Fail when Docker is required but unavailable.
- Clean up container and image with truthful cleanup status.

Documentation:

- Correct example startup commands.
- Document container ports and health endpoints.
- Update `TESTING.md` with the exact E2E evidence boundary.

Verification:

- Local built-image E2E.
- CI workflow validation.
- Regression proving the former port/path combination is invalid.

Release:

- Python version bump.
- Changelog entry.
- Dated `_bugs_fixed` note.
- Lore commit and push.

### G003 — Make TypeScript Kafka fail closed

Implementation:

- Prevent the stub from incrementing successful-delivery counters.
- Prevent subscriber startup from reporting healthy.
- Prevent lifecycle announcements from reporting broker-backed success.
- Kafka configuration must produce a clear unsupported-capability error until a
  real transport exists.
- Preserve non-transport test utilities only under unmistakable names or
  test-only surfaces.
- Add capability-state types if needed to prevent accidental success reporting.

Testing:

- Configured Kafka mode fails before lifecycle success.
- Publish cannot report success.
- Subscriber cannot report healthy.
- Lifecycle events cannot be falsely attributed to broker delivery.
- Unconfigured services remain unaffected.

Documentation:

- Every TypeScript document says Kafka transport is unavailable and fail-closed.
- Remove examples suggesting real delivery.
- Explain that schemakit does not make the stub a transport.

Release:

- TypeScript version bump.
- Changelog and bug note.
- Lore commit and push.

### G004 — Align Problem Details

Python:

- Separate request path from request ID.
- Stop placing request IDs in `instance`.
- Emit `request_id` as a top-level extension.
- Align internal codes and mappings with canonical fixtures.
- Add compatibility handling where necessary.

TypeScript:

- Flatten application extensions into the canonical top-level representation.
- Align code, title, type, and retryability mappings.
- Reject reserved-field collisions.

Go:

- Confirm current behavior against the full fixture set.
- Make only necessary compatibility corrections.

Testing:

- Golden response bodies.
- Header assertions.
- Writer and middleware integration tests.
- Unknown-extension tests.
- Cross-language fixture checks.

Release each changed repository independently with version, changelog, Lore
commit, and push.

### G005 — Align feature flags and trace correlation

Feature flags:

- Replace TypeScript UTF-16 hashing with UTF-8 byte hashing.
- Remove language-specific random empty-user behavior.
- Align literal parsing.
- Add shared Unicode and boundary vectors.

Tracing:

- Add W3C precedence support to Go and TypeScript middleware.
- Retain legacy 12-character support only under the agreed deprecation policy.
- Align Python legacy handling.
- Add malformed-header and precedence tests.

Documentation:

- Publish the precise hash algorithm.
- Document empty-user behavior.
- Document trace-header precedence and legacy policy.

Release each changed repository independently.

### G006 — Normalize OTel lifecycle invariants

Required invariant:

1. Validate configuration.
2. Construct all exporters and providers.
3. Install global state only after successful construction.
4. Enforce one active lifecycle owner.
5. Return an idempotent shutdown.
6. Reset ownership and globals sufficiently for documented restart behavior.
7. Roll back cleanly after partial construction failure.

Python:

- Do not mark initialized before construction succeeds.
- Reset initialization ownership on shutdown.
- Stop tests from masking behavior through private-state resets.
- Correct example endpoint and TLS configuration.

TypeScript:

- Avoid installing the tracer before metrics and propagation construction
  succeeds.
- Add active-owner protection.
- Define shutdown/reset behavior.
- Test partial construction failure and repeated initialization.

Go:

- Use the current hardened lifecycle as the reference.
- Extend contract coverage if gaps remain.

Endpoint syntax may remain SDK-specific, but every repository must clearly
document accepted syntax and equivalent environment configuration.

### G007 — Close Kafka contract gaps

Python:

- Add record-key support.
- Add publish-keyed behavior.
- Preserve keys in consumed events.
- Add publisher broker readiness/ping.
- Test partition-affinity behavior with a live broker where available.

TypeScript:

- Expose key/readiness semantics only as unavailable fail-closed capabilities
  until transport exists.
- Do not add fake implementations.

Go:

- Validate keyed publish, consumed key, ping, and consumer timeout semantics.
- Rename or document configuration that cannot exactly model native SDK
  behavior.

Documentation:

- Publish a truthful language capability matrix.
- Separate JSON-envelope behavior from optional schema tooling.
- Remove automatic schema-registration claims.

### G008 — Add outbound-call privacy controls

Implement equivalent controls in Python and TypeScript:

- Default global propagation for backward compatibility.
- Explicit trace-context-only propagation.
- Explicit propagation disablement.
- Managed-field scrubbing before injection.
- Telemetry redaction for sensitive targets.
- Redacted span names, URLs, query strings, and error details.
- No stale baggage or propagation headers.

Testing:

- Existing caller headers.
- Invalid or absent active spans.
- Disabled propagation.
- Trace-context-only mode.
- Redacted telemetry.
- Redirect and retry behavior.
- Secret-bearing query strings and error messages.

Documentation:

- State defaults and security implications.
- Show safe configuration for scanners and external providers.

Release Python and TypeScript independently.

### G009 — Correct Python packaging policy

Implementation:

- Make `pyproject.toml` license metadata match the proprietary/internal policy.
- Add required legal metadata or private-license identifier.
- Build the wheel.
- Inspect wheel metadata in an automated regression test.
- Ensure README and package metadata do not conflict.

Do not invent new legal language beyond the project's established policy. G009
may start only after G001 records the authoritative policy source and the exact
metadata representation permitted by that source.

Release with version, changelog, bug note, Lore commit, and push.

### G010 — Rewrite ecosystem documentation

Audit and correct:

- `README.md`
- `PRODUCT.md`
- `INTEGRATING.md`
- `TESTING.md`
- Full-service examples
- Package-level READMEs and API documentation
- Shared event-bus guide
- Shared observability and tracing guidance
- Capability matrices
- Version and freshness examples

Remove false claims about:

- Automatic Avro serialization.
- Automatic schema registration.
- Automatic topic creation.
- TypeScript Kafka transport.
- Subscriber trace-context extraction where absent.
- Incorrect batch or handler signatures.
- Portable OTel endpoint strings.
- Feature-flag parity not backed by fixtures.

Documentation tests should assert:

- Code snippets compile or typecheck where practical.
- Referenced commands exist.
- Capability statements match machine-readable fixtures.
- Full-service ports and health routes match implementation.
- Package metadata matches distribution policy.

Commit and push shared documentation separately if it is its own repository.

### G011 — Full-system verification

Go:

- Targeted contract tests.
- `go test ./...`
- Race tests where established.
- `go vet ./...`
- Repository-standard static analysis.
- Coverage policy.
- Existing E2E and integration scripts.
- Kafka and OTel live integration according to the G001 verification manifest.
- Docker full-service test.

Python:

- Targeted contract tests.
- Full pytest suite.
- Type checking.
- Lint and formatting checks.
- Wheel build and metadata inspection.
- Docker full-service E2E.
- Kafka integration.
- OTel integration.
- Coverage policy.

TypeScript:

- Targeted contract tests.
- Full Vitest suite.
- Typecheck.
- Lint and formatting checks.
- Package build.
- Workspace package tests.
- Docker full-service E2E.
- Fail-closed Kafka lifecycle tests.
- Coverage policy.

Cross-ecosystem:

- Compare mirrored fixture checksums.
- Execute Unicode flag vectors.
- Compare Problem Details outputs.
- Compare trace-precedence scenarios.
- Validate capability matrices against package behavior.
- Search documentation for retired claims.

Every G011 check must map to an exact command and required/conditional
classification in the G001 verification manifest. Unclassified skips do not
count as successful verification.

### G012 — Final cleanup and independent review

1. Run AI-slop cleanup only on changed files.
2. Rerun all affected verification.
3. Have an independent code reviewer audit correctness, compatibility, tests,
   security, and documentation.
4. Have an independent architect verify:
   - No cross-language runtime coupling.
   - Shared contracts are fixture-driven.
   - Unsupported capabilities fail closed.
   - SDK-specific differences remain explicit.
   - Documentation is derived from real behavior.
5. Resolve every blocking finding.
6. Rerun review until recommendation is `APPROVE` and architect status is
   `CLEAR`.

## Commit and push protocol

For every repository and verified story:

1. Confirm no concurrent unintegrated edits.
2. Run targeted verification.
3. Read `VERSION` at the last possible moment.
4. Increment the patch version.
5. Update `CHANGELOG.md` with:
   - User-visible behavior.
   - Compatibility impact.
   - Tests added.
   - Documentation corrected.
   - `Codex:gpt-5.6-sol-high`.
6. Add a dated lowercase `_bugs_fixed` note for bug corrections.
7. Run post-version verification.
8. Run `git add -A`.
9. Commit using the Lore protocol.
10. Push the current branch.
11. Verify local HEAD equals remote branch HEAD.
12. Record SHA, version, tests, and push evidence in the Ultragoal ledger.

Example:

```text
Prevent unsupported broker behavior from appearing successful

Constraint: TypeScript Kafka transport is not implemented
Rejected: Preserve successful stub statistics | Misrepresents delivery guarantees
Confidence: high
Scope-risk: moderate
Directive: Do not report Kafka readiness until backed by real broker transport
Tested: workspace tests, typecheck, lifecycle regressions, Docker E2E
Not-tested: live Kafka transport because the capability remains unavailable
```

## Start procedure

1. Run `omx doctor` and resolve warnings that affect the roles used by the run.
2. Confirm native typed planner, executor, reviewer, and architect delegation
   works.
3. Confirm no foreign-cwd subagent-support marker remains.
4. Confirm this dated proposal is committed and synchronized.
5. Resolve or isolate pre-existing edits in all target repositories.
6. Call `get_goal`; do not overwrite a different active goal.
7. Create durable Ultragoal artifacts from this proposal.
8. Create the aggregate goal only if none exists.
9. Execute G001 and produce the complete implementation-readiness contract
   pack.
10. Obtain independent approval that the readiness gate passes.
11. Execute G002 through G012 sequentially, using parallel agents only for
    independent repository scopes.
12. Checkpoint after every pushed story.
13. Mark the aggregate goal complete only after final independent approval.

## Completion criteria

The remediation is complete only when:

- The implementation-readiness gate passed before behavior-changing work.
- Python's full-service Docker image passes required E2E.
- TypeScript Kafka cannot report false success.
- Problem Details fixtures pass in all languages.
- Feature-flag vectors produce identical results.
- Trace precedence is consistent.
- OTel initialization satisfies the shared lifecycle invariant.
- Kafka capability differences are explicit and tested.
- Python and TypeScript expose equivalent outbound-call privacy controls.
- Python package metadata matches its distribution policy.
- All relevant documentation describes executable behavior.
- All tests, builds, coverage gates, integrations, and Docker checks pass.
- Every changed repository has a version bump and changelog entry.
- Every change is committed and pushed.
- Independent review returns `APPROVE`.
- Architect review returns `CLEAR`.
- All Ultragoal stories and the aggregate Codex goal are complete.
