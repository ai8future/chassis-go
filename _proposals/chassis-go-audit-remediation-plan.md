# Chassis-Go Audit Remediation Plan

**Date:** July 2, 2026

## Summary

A six-agent audit of chassis-go v11.1.14 (verified by build, vet, govulncheck, and race-enabled tests) found 1 critical, 4 high, ~13 medium, and ~10 low defects. This plan remediates them in severity order as small TDD tasks: the posthogkit shutdown panic, the `call` retry silent-empty-body bug, kafkakit/lakekit/ollamakit client defects, the freshness rebuild race, registry symlink hardening, webhook replay-dedup support, panic recovery in `work`, and a tail of small guards and test-hygiene fixes.

---

# Chassis-Go Audit Remediation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Fix all confirmed defects from the 2026-07-02 audit of chassis-go without changing public API shape except where a defect requires it (noted per task).

**Architecture:** Each task is an isolated fix + focused test in one package. No cross-task code dependencies except where an "Interfaces" block says so. Severity order: Critical → High → Medium → Low → finalization (VERSION/CHANGELOG/commit).

**Tech Stack:** Go 1.26, module `github.com/ai8future/chassis-go/v11`, franz-go (`kgo`), OpenTelemetry, stdlib `net/http`.

## Global Constraints

- Repo root: `/Users/cliff/Desktop/_code/chassis_suite/chassis-go`. All paths below are relative to it.
- Module path is `github.com/ai8future/chassis-go/v11`; every internal import uses the `/v11` suffix.
- Every NEW test file must call `chassis.RequireMajor(11)` via `init` or `TestMain`, matching the package's current convention. Existing files are mixed; do not assume they all already comply unless verified.
- Do not read `VERSION` until the release/bookkeeping step for a code-change story requires it. Planning-only edits to this proposal do not require a VERSION bump. For code changes, follow the repo rule: VERSION and CHANGELOG must be updated before committing code; do not run the per-task commit snippets below as literal instructions unless that repo rule has been satisfied for that code change.
- The per-task `git commit` snippets are message templates only. The actual executing agent must use its real agent/model identity in release notes and commits, and must push only when repo policy and host checks permit it. If `git rev-parse --is-inside-work-tree` fails, or the machine hostname starts with `z` (`hostname` output), skip all commit/push steps and say so.
- Stay out of `_studies/`, `_proposals/`, `_rcodegen/`, `_bugs_open/`; only write (never read) `_bugs_fixed/` in Task 20.
- Gate for every task: `go build ./... && go vet ./...` clean, plus the task's test command.
- Python is irrelevant here; if any tooling script is ever needed use `/opt/homebrew/bin/python3.13`.

## Breaking/API/Wire Release Gate (mandatory before Tasks 3, 8, 13, 18 are releasable)

Tasks 3, 8, 13, and 18 affect exported behavior, wire format, public signatures, or security-sensitive API behavior. Before any of those changes are considered complete:

- Run caller grep including tests and examples (`grep -rn "<symbol>" --include="*.go" .`) and update every call site.
- Run targeted package tests and `go build ./...` after the API/wire change.
- Add explicit CHANGELOG breaking notes and bug-note coverage.
- Handle VERSION timing exactly as required by current repo policy; do not hide a breaking change in a later unrelated finalization step.
- Do not use string matching on error messages for control flow when an exported or typed error sentinel is appropriate.

## Deferred (explicitly NOT in this plan)

- Migrating `lakekit` from raw `http.Client` to `call.Client` (retry/breaker/tracing) — behavior change needing its own design pass.
- `config` recursion into pointer-to-struct fields with `env` tags — needs a decision (allocate vs. fail fast).
- `inngestkit` `Dev` mode config — product decision.
- A durable nonce store for webhook replay (Task 8 gives callers the signed ID to dedup with; storage is theirs).
- kafkakit `PublishBatch` API redesign beyond per-record error reporting.

---

### Task 1: posthogkit — Capture after Close must not panic (CRITICAL)

**Files:**
- Modify: `posthogkit/posthogkit.go` (struct at ~line 60, `New` at ~line 71, `Close` at ~line 201)
- Test: `posthogkit/posthogkit_test.go`

**Why:** `Close()` closes `c.flushCh`; `enqueue` later sends on it. A send on a closed channel panics **even inside `select` with `default`**. Fix: never close `flushCh`; stop the flusher goroutine via a separate `done` channel.

- [ ] **Step 1: Write the failing test**

```go
func TestCaptureAfterCloseDoesNotPanic(t *testing.T) {
	c := New(Config{
		APIKey:    "phc_test",
		Host:      "http://127.0.0.1:1", // unreachable; flush errors are logged, not fatal
		Enabled:   true,
		FlushSize: 2,
	})
	c.Close()
	// Reaching FlushSize triggers the flush-signal send that panicked before the fix.
	for i := 0; i < 10; i++ {
		c.Capture("user-1", "event", nil)
	}
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `go test -run TestCaptureAfterCloseDoesNotPanic ./posthogkit/ -v`
Expected: FAIL with `panic: send on closed channel`

- [ ] **Step 3: Implement the fix**

In the `Client` struct add a `done` channel below `flushCh`:

```go
	flushCh   chan struct{}
	done      chan struct{}
	closeOnce sync.Once
```

In `New`, initialize it and make the flusher goroutine exit via `done` instead of channel close:

```go
	c := &Client{
		cfg:     cfg,
		http:    call.New(call.WithTimeout(10 * time.Second)),
		log:     slog.Default(),
		buf:     make([]event, 0, cfg.FlushSize),
		flushCh: make(chan struct{}, 1),
		done:    make(chan struct{}),
	}
	go func() {
		for {
			select {
			case <-c.done:
				return
			case <-c.flushCh:
				if err := c.flush(context.Background()); err != nil {
					c.log.Warn("posthogkit: auto-flush failed", "error", err)
				}
			}
		}
	}()
```

In `Close`, replace `close(c.flushCh)` with `close(c.done)`:

```go
func (c *Client) Close() {
	c.closeOnce.Do(func() {
		close(c.done) // flushCh is never closed, so late Capture calls can never panic
		if err := c.flush(context.Background()); err != nil {
			c.log.Warn("posthogkit: final flush failed", "error", err)
		}
	})
}
```

`enqueue` stays unchanged — sending to a never-closed buffered channel with a `default` case can neither panic nor block.

- [ ] **Step 4: Run the tests**

Run: `go test -race ./posthogkit/ -v` — Expected: PASS (all tests, including the new one)

- [ ] **Step 5: Commit**

```bash
git add posthogkit/posthogkit.go posthogkit/posthogkit_test.go
git commit -m "fix(posthogkit): stop flusher via done channel so Capture after Close cannot panic"
```

---

### Task 2: call — retry must not silently send an empty body (HIGH)

**Files:**
- Modify: `call/call.go:200-206` (the `exec` closure inside `Do`)
- Test: `call/call_test.go`

**Why:** When `req.GetBody()` fails on a retry, the error is swallowed and the consumed (EOF) body is re-sent — silent data loss presenting as a server-side 400. The same class of bug exists when a request has `Body != nil` but no `GetBody`: retrying it is unsafe because the body cannot be rewound.

- [ ] **Step 1: Write the failing test**

```go
func TestRetryFailsWhenGetBodyFails(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable) // always force a retry
	}))
	defer srv.Close()

	c := New(WithRetry(3, 1*time.Millisecond))
	req, err := http.NewRequest(http.MethodPost, srv.URL, strings.NewReader(`{"k":"v"}`))
	if err != nil {
		t.Fatal(err)
	}
	req.GetBody = func() (io.ReadCloser, error) {
		return nil, errors.New("body source invalidated")
	}

	resp, err := c.Do(context.Background(), req)
	if resp != nil {
		resp.Body.Close()
	}
	if !errors.Is(err, ErrBodyNotRewindable) {
		t.Fatalf("expected ErrBodyNotRewindable, got resp=%v err=%v", resp, err)
	}
	if hits.Load() != 1 {
		t.Fatalf("expected exactly 1 attempt (no blind retries), got %d", hits.Load())
	}
}

func TestRetryFailsWhenBodyCannotBeRewound(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	c := New(WithRetry(3, 1*time.Millisecond))
	req, err := http.NewRequest(http.MethodPost, srv.URL, io.NopCloser(strings.NewReader(`{"k":"v"}`)))
	if err != nil {
		t.Fatal(err)
	}
	if req.GetBody != nil {
		t.Fatal("test setup expected nil GetBody for non-rewindable body")
	}

	resp, err := c.Do(context.Background(), req)
	if resp != nil {
		resp.Body.Close()
	}
	if !errors.Is(err, ErrBodyNotRewindable) {
		t.Fatalf("expected ErrBodyNotRewindable, got resp=%v err=%v", resp, err)
	}
	if hits.Load() != 1 {
		t.Fatalf("expected exactly 1 attempt (no blind retries), got %d", hits.Load())
	}
}
```

Adjust `New(WithRetry(...))` to the constructor/option names already used in `call/call_test.go` — mirror an existing retry test's setup.

- [ ] **Step 2: Run it to verify it fails**

Run: `go test -run TestRetryFailsWhenGetBodyFails ./call/ -v`
Expected: FAIL — old code retries with an empty/consumed body and returns the 503 response instead of a permanent rewind error.

- [ ] **Step 3: Implement the fix**

Add an exported sentinel (or existing permanent-error mechanism if one exists) and use it instead of string matching:

```go
// ErrBodyNotRewindable means a retryable response was received, but the
// request body cannot be restored for another attempt. Retrying would send an
// empty or partial body, so the request fails permanently instead.
var ErrBodyNotRewindable = errors.New("call: request body is not rewindable for retry")
```

Replace the swallow in the `exec` closure and reject non-rewindable request bodies before any blind retry:

```go
	exec := func() (*http.Response, error) {
		if attempt > 0 {
			if req.Body != nil && req.GetBody == nil {
				return nil, ErrBodyNotRewindable
			}
			if req.GetBody != nil {
				body, gbErr := req.GetBody()
				if gbErr != nil {
					return nil, fmt.Errorf("%w: %v", ErrBodyNotRewindable, gbErr)
				}
				req.Body = body
			}
		}
		attempt++
		return c.httpClient.Do(req)
	}
```

Then check `call/retry.go`: if the retrier retries on any non-nil error, short-circuit permanent errors using `errors.Is(err, ErrBodyNotRewindable)` before scheduling another attempt. Do **not** use `strings.Contains(err.Error(), ...)` as retry control flow.

- [ ] **Step 4: Run the tests**

Run: `go test -race ./call/ -v` — Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add call/call.go call/retry.go call/call_test.go
git commit -m "fix(call): fail retry when GetBody rewind fails instead of silently sending empty body"
```

---

### Task 3: kafkakit — PublishBatch reports per-record outcomes (HIGH)

**Files:**
- Modify: `kafkakit/publisher.go` (`PublishBatch`, ~lines 96-135)
- Test: `kafkakit/publisher_test.go`

**Interfaces:**
- Produces: `type BatchFailure struct { Index int; Subject string; Err error }`, `type BatchError struct { Failures []BatchFailure; Succeeded int }` with `Error() string` and `Unwrap() []error`. `PublishBatch` keeps signature `(ctx, []OutboundEvent) error` but returns `*BatchError` on partial/total failure.

**Why:** `ProduceSync` results were scanned only to the first error; the caller couldn't tell which records were durably produced, so a full-batch retry duplicates them.

- [ ] **Step 1: Write the failing test**

```go
func TestPublishBatchReportsPerRecordFailures(t *testing.T) {
	// Unreachable broker: every record fails. The point is the error SHAPE.
	pub, err := NewPublisher(Config{BootstrapServers: "127.0.0.1:1", Source: "test-svc"})
	if err != nil {
		t.Fatal(err)
	}
	defer pub.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	events := []OutboundEvent{
		{Subject: "ai8.test.a", Data: map[string]string{"k": "1"}},
		{Subject: "ai8.test.b", Data: map[string]string{"k": "2"}},
	}
	err = pub.PublishBatch(ctx, events)

	var be *BatchError
	if !errors.As(err, &be) {
		t.Fatalf("expected *BatchError, got %T: %v", err, err)
	}
	if len(be.Failures) != 2 || be.Succeeded != 0 {
		t.Fatalf("expected 2 failures / 0 succeeded, got %+v", be)
	}
	if be.Failures[0].Subject == "" {
		t.Fatal("expected failure to carry its subject")
	}
}
```

(Adapt the `Config` literal to the fields `NewPublisher` actually validates — mirror existing publisher tests.)

- [ ] **Step 2: Run it to verify it fails**

Run: `go test -run TestPublishBatchReportsPerRecordFailures ./kafkakit/ -v`
Expected: FAIL — `errors.As` finds no `*BatchError` (type doesn't exist yet / compile error first).

- [ ] **Step 3: Implement**

Add to `kafkakit/publisher.go`:

```go
// BatchFailure records one failed record in a batch publish.
type BatchFailure struct {
	Index   int
	Subject string
	Err     error
}

// BatchError reports per-record outcomes of PublishBatch. Records not listed
// in Failures were durably produced — retry only the failed ones, or you will
// publish duplicates.
type BatchError struct {
	Failures  []BatchFailure
	Succeeded int
}

func (e *BatchError) Error() string {
	return fmt.Sprintf("kafkakit: %d of %d batch record(s) failed", len(e.Failures), e.Succeeded+len(e.Failures))
}

func (e *BatchError) Unwrap() []error {
	out := make([]error, len(e.Failures))
	for i, f := range e.Failures {
		out[i] = f.Err
	}
	return out
}
```

Replace the result loop in `PublishBatch` (results may not be input-ordered, so match by record pointer):

```go
	recIndex := make(map[*kgo.Record]int, len(records))
	for i, rec := range records {
		recIndex[rec] = i
	}

	results := p.client.ProduceSync(ctx, records...)
	var failures []BatchFailure
	for _, r := range results {
		idx := recIndex[r.Record]
		if r.Err != nil {
			p.stats.incErrors()
			failures = append(failures, BatchFailure{Index: idx, Subject: events[idx].Subject, Err: r.Err})
			continue
		}
		p.stats.incPublished()
	}
	if len(failures) > 0 {
		return &BatchError{Failures: failures, Succeeded: len(results) - len(failures)}
	}
	return nil
```

- [ ] **Step 4: Run the tests**

Run: `go test -race ./kafkakit/ -v` — Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add kafkakit/publisher.go kafkakit/publisher_test.go
git commit -m "fix(kafkakit): PublishBatch returns per-record BatchError so callers can retry only failures"
```

---

### Task 4: lakekit — bound all response reads (HIGH)

**Files:**
- Modify: `lakekit/lakekit.go` (the four `json.NewDecoder(resp.Body).Decode(...)` sites in `Query`, `EntityHistory`, `Datasets`, `DatasetStats`)
- Test: `lakekit/lakekit_test.go`

**Why:** Unbounded decode of SQL query results = OOM risk. Sibling kits cap (qdrantkit 32 MB); mirror that.

- [ ] **Step 1: Write the failing test**

```go
// spaceReader emits JSON whitespace forever — a decoder will read until a cap stops it.
type spaceReader struct{}

func (spaceReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = ' '
	}
	return len(p), nil
}

func TestDecodeJSONIsBounded(t *testing.T) {
	resp := &http.Response{Body: io.NopCloser(spaceReader{})}
	var dst map[string]any
	done := make(chan error, 1)
	go func() { done <- decodeJSON(resp, &dst) }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected bounded decode to error on oversized body")
		}
	case <-time.After(30 * time.Second):
		t.Fatal("decode did not terminate: response read is unbounded")
	}
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `go test -run TestDecodeJSONIsBounded ./lakekit/ -v`
Expected: FAIL to compile (`decodeJSON` undefined) — that is the failing state.

- [ ] **Step 3: Implement**

Add to `lakekit/lakekit.go`:

```go
// maxResponseBytes caps decoded lake_svc responses, mirroring qdrantkit's
// 32 MB bound, so a runaway query result cannot OOM the process.
const maxResponseBytes = 32 << 20

func decodeJSON(resp *http.Response, dst any) error {
	return json.NewDecoder(io.LimitReader(resp.Body, maxResponseBytes)).Decode(dst)
}
```

Replace all four decode sites, e.g. in `Query`:

```go
	var result QueryResult
	if err := decodeJSON(resp, &result); err != nil {
		return nil, fmt.Errorf("lakekit: decode query result: %w", err)
	}
```

(Same pattern for `[]HistoryEntry`, `[]Dataset`, `Dataset`.)

- [ ] **Step 4: Run the tests**

Run: `go test -race ./lakekit/ -v` — Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add lakekit/lakekit.go lakekit/lakekit_test.go
git commit -m "fix(lakekit): cap response decoding at 32MB to prevent OOM on huge query results"
```

---

### Task 5: ollamakit — zero-timeout client for streaming and pulls (HIGH)

**Files:**
- Modify: `ollamakit/ollamakit.go` (`Client` struct, `New`, `ChatStream` ~line 246, `PullModel` ~line 436)
- Test: `ollamakit/ollamakit_test.go`

**Why:** `http.Client.Timeout` (default 120s) includes body-read time — it severs streams mid-generation and kills multi-GB model pulls. `inferkit` already uses a separate zero-timeout client for streaming; do the same.

- [ ] **Step 1: Write the failing test**

```go
func TestChatStreamOutlivesClientTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fl := w.(http.Flusher)
		for i := 0; i < 4; i++ {
			fmt.Fprintln(w, `{"message":{"content":"x"},"done":false}`)
			fl.Flush()
			time.Sleep(150 * time.Millisecond)
		}
		fmt.Fprintln(w, `{"message":{"content":"end"},"done":true}`)
		fl.Flush()
	}))
	defer srv.Close()

	c := New(Config{Host: srv.URL, Timeout: 200 * time.Millisecond}) // stream runs ~750ms
	ch, err := c.ChatStream(context.Background(), ChatRequest{Messages: []Message{{Role: "user", Content: "hi"}}})
	if err != nil {
		t.Fatal(err)
	}
	var sawDone bool
	for chunk := range ch {
		if chunk.Err != nil {
			t.Fatalf("stream truncated by client timeout: %v", chunk.Err)
		}
		if chunk.Done {
			sawDone = true
		}
	}
	if !sawDone {
		t.Fatal("stream ended without done=true")
	}
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `go test -run TestChatStreamOutlivesClientTimeout ./ollamakit/ -v`
Expected: FAIL — chunk error caused by the 200ms client timeout severing the stream.

- [ ] **Step 3: Implement**

Add a field to the `Client` struct: `streamHTTP *http.Client`. In `New`:

```go
	c := &Client{
		host:  strings.TrimRight(host, "/"),
		model: model,
		http:  &http.Client{Timeout: timeout},
		// No Timeout: stream/pull lifetimes are governed by the caller's ctx.
		// http.Client.Timeout includes body reads, which would sever long
		// generations and large model downloads.
		streamHTTP: &http.Client{},
	}
```

In `ChatStream`, change `resp, err := c.http.Do(httpReq)` → `resp, err := c.streamHTTP.Do(httpReq)`.
In `PullModel`, change `resp, err := c.http.Do(httpReq)` → `resp, err := c.streamHTTP.Do(httpReq)`.
Update the `PullModel` doc comment: "Blocks until complete; bound it with a context deadline sized for multi-GB downloads."

- [ ] **Step 4: Run the tests**

Run: `go test -race ./ollamakit/ -v` — Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add ollamakit/ollamakit.go ollamakit/ollamakit_test.go
git commit -m "fix(ollamakit): use zero-timeout client for ChatStream/PullModel so ctx governs lifetime"
```

---

### Task 6: freshness — unique rebuild temp path (MEDIUM)

**Files:**
- Modify: `freshness.go:118-144` (`rebuild`)
- Test: `freshness_test.go`

**Why:** Fixed temp path `binPath + ".chassis-rebuild.tmp"` lets two concurrently-starting stale instances run `go build -o` onto the same file, then rename+exec a corrupt binary.

- [ ] **Step 1: Write the failing test**

```go
func TestRebuildTempPathUnique(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "svc")
	a, err := rebuildTempPath(bin)
	if err != nil {
		t.Fatal(err)
	}
	b, err := rebuildTempPath(bin)
	if err != nil {
		t.Fatal(err)
	}
	if a == b {
		t.Fatalf("expected unique temp paths, got %q twice", a)
	}
	if filepath.Dir(a) != dir || filepath.Dir(b) != dir {
		t.Fatalf("temp paths must sit alongside the binary (same-filesystem rename): %q, %q", a, b)
	}
	os.Remove(a)
	os.Remove(b)
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `go test -run TestRebuildTempPathUnique ./ -v`
Expected: FAIL to compile (`rebuildTempPath` undefined).

- [ ] **Step 3: Implement**

```go
// rebuildTempPath reserves a unique temp file next to binPath for go build
// output, so concurrently-starting stale instances never clobber each other's
// in-progress build and install a corrupt binary.
func rebuildTempPath(binPath string) (string, error) {
	f, err := os.CreateTemp(filepath.Dir(binPath), filepath.Base(binPath)+".chassis-rebuild-*.tmp")
	if err != nil {
		return "", err
	}
	name := f.Name()
	f.Close()
	return name, nil
}
```

Rework `rebuild` to use it:

```go
func rebuild(moduleRoot, pkgPath, binPath string) error {
	goBin, err := exec.LookPath("go")
	if err != nil {
		return fmt.Errorf("go not found in PATH: %w", err)
	}

	tmpPath, err := rebuildTempPath(binPath)
	if err != nil {
		return fmt.Errorf("create temp build target: %w", err)
	}
	defer os.Remove(tmpPath) // no-op once renamed into place

	ctx, cancel := context.WithTimeout(context.Background(), rebuildTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, goBin, "build", "-o", tmpPath, pkgPath)
	cmd.Dir = moduleRoot
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("go build failed: %w", err)
	}
	if err := os.Chmod(tmpPath, 0o755); err != nil {
		return fmt.Errorf("chmod rebuilt binary: %w", err)
	}
	if err := os.Rename(tmpPath, binPath); err != nil {
		return fmt.Errorf("rename failed: %w", err)
	}
	return nil
}
```

- [ ] **Step 4: Run the tests**

Run: `go test -race -run 'TestRebuild|TestFreshness|TestSemver' ./ -v` then `go test -race ./` — Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add freshness.go freshness_test.go
git commit -m "fix(freshness): unique temp path per rebuild prevents concurrent-start binary corruption"
```

---

### Task 7: registry — refuse hostile base paths; claim command files atomically (MEDIUM)

**Files:**
- Modify: `registry/registry.go` (`Init` ~line 204, `pollOnce` ~line 784)
- Test: `registry/registry_test.go` (match the existing test file name in `registry/`)

**Why:** (a) A local attacker who pre-creates `/tmp/chassis` as a symlink redirects PID/log/command files and can issue stop/restart commands. (b) `pollOnce` does ReadFile-then-Remove, deleting unread commands written in between.

- [ ] **Step 1: Write the failing test**

```go
func TestInitRejectsSymlinkBasePath(t *testing.T) {
	old := registry.BasePath
	t.Cleanup(func() { registry.BasePath = old })

	target := t.TempDir()
	link := filepath.Join(t.TempDir(), "chassis-link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	registry.BasePath = link

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	err := registry.Init(cancel, "11.0.0")
	if err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("expected symlink rejection, got %v", err)
	}
	_ = ctx
}
```

This test belongs in the existing external `registry_test` package, so exported names must be qualified with `registry.`.

(If `Init` in this package's tests needs teardown, mirror whatever the existing registry tests do around `Init`/`Shutdown`.)

- [ ] **Step 2: Run it to verify it fails**

Run: `go test -run TestInitRejectsSymlinkBasePath ./registry/ -v`
Expected: FAIL — Init currently succeeds through the symlink.

- [ ] **Step 3: Implement both fixes**

In `Init`, immediately before `if err := os.MkdirAll(svcDir, 0o700)`:

```go
	// Refuse a symlinked or world/group-writable base path. On shared systems
	// an attacker who pre-creates /tmp/chassis can otherwise redirect PID,
	// log, and command files — and drive stop/restart via .cmd.json.
	if info, err := os.Lstat(BasePath); err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("registry: base path %s is a symlink; refusing to use it", BasePath)
		}
		if runtime.GOOS != "windows" {
			if perm := info.Mode().Perm(); perm&0o022 != 0 {
				return fmt.Errorf("registry: base path %s is group/world-writable (%o); refusing to use it", BasePath, perm)
			}
		}
	}
```

(Add `"runtime"` to imports.)

In `pollOnce`, replace the read/remove:

```go
	// Claim the command file atomically before reading: rename-then-read means
	// a command written after our claim lands in a fresh file instead of being
	// deleted unread (ReadFile+Remove lost such writes).
	claimed := path + ".claimed"
	if err := os.Rename(path, claimed); err != nil {
		return
	}
	data, err := os.ReadFile(claimed)
	os.Remove(claimed)
	if err != nil {
		return
	}
```

- [ ] **Step 4: Run the tests**

Run: `go test -race ./registry/ -v` — Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add registry/
git commit -m "fix(registry): reject symlinked/world-writable base path; claim command files atomically"
```

---

### Task 8: webhook — sign the delivery ID and expose it for replay dedup (MEDIUM)

**Files:**
- Modify: `webhook/webhook.go` (`Send` sig construction ~line 73, `VerifyPayload` ~line 171)
- Test: `webhook/webhook_test.go`

**Interfaces:**
- Produces: `func VerifyPayloadID(headers http.Header, body []byte, secret string) (id string, payload []byte, err error)`. `VerifyPayload` becomes a wrapper that discards the id.

**⚠ Wire-format change:** the signature input becomes `timestamp.id.body` (was `timestamp.body`). Senders and receivers must upgrade together — call this out in the CHANGELOG (part of why Task 20 bumps to 11.2.0).

- [ ] **Step 1: Write the failing test**

```go
func TestVerifyPayloadIDRoundTripAndTamperedID(t *testing.T) {
	secret := "whsec_test"
	var gotHeaders http.Header
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeaders = r.Header.Clone()
		gotBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	s := NewSender()
	sentID, err := s.Send(srv.URL, map[string]string{"k": "v"}, secret)
	if err != nil {
		t.Fatal(err)
	}

	id, payload, err := VerifyPayloadID(gotHeaders, gotBody, secret)
	if err != nil {
		t.Fatalf("verify failed: %v", err)
	}
	if id != sentID {
		t.Fatalf("expected id %q, got %q", sentID, id)
	}
	if len(payload) == 0 {
		t.Fatal("expected payload back")
	}

	// A replayer must not be able to swap the ID to dodge dedup: ID is signed.
	tampered := gotHeaders.Clone()
	tampered.Set("X-Webhook-Id", "ffffffffffffffffffffffffffffffff")
	if _, _, err := VerifyPayloadID(tampered, gotBody, secret); !errors.Is(err, ErrBadSignature) {
		t.Fatalf("expected ErrBadSignature for tampered id, got %v", err)
	}
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `go test -run TestVerifyPayloadIDRoundTripAndTamperedID ./webhook/ -v`
Expected: FAIL to compile (`VerifyPayloadID` undefined).

- [ ] **Step 3: Implement**

In `Send`, change the signature input line to include the ID:

```go
	sigPayload := timestamp + "." + id + "." + string(body)
```

Replace `VerifyPayload` with:

```go
// VerifyPayloadID verifies a webhook delivery and returns its delivery ID.
// The ID is covered by the HMAC, so receivers can safely use it to
// deduplicate replayed deliveries within the timestamp window.
// Wire format (v11.2+): signature input is "timestamp.id.body".
func VerifyPayloadID(headers http.Header, body []byte, secret string) (string, []byte, error) {
	sig := headers.Get("X-Webhook-Signature")
	timestamp := headers.Get("X-Webhook-Timestamp")
	id := headers.Get("X-Webhook-Id")
	if sig == "" || timestamp == "" || id == "" {
		return "", nil, ErrBadSignature
	}

	ts, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil {
		return "", nil, ErrBadSignature
	}
	age := time.Since(time.Unix(ts, 0))
	if age < 0 {
		age = -age
	}
	if age > 5*time.Minute {
		return "", nil, fmt.Errorf("%w: timestamp expired", ErrBadSignature)
	}

	if len(sig) > 7 && sig[:7] == "sha256=" {
		sig = sig[7:]
	}

	sigPayload := timestamp + "." + id + "." + string(body)
	if !seal.Verify([]byte(sigPayload), sig, secret) {
		return "", nil, ErrBadSignature
	}
	return id, body, nil
}

// VerifyPayload verifies a webhook delivery, discarding the delivery ID.
// Prefer VerifyPayloadID so replays can be deduplicated.
func VerifyPayload(headers http.Header, body []byte, secret string) ([]byte, error) {
	_, payload, err := VerifyPayloadID(headers, body, secret)
	return payload, err
}
```

- [ ] **Step 4: Run the tests and fix any that hand-roll the old signature format**

Run: `go test -race ./webhook/ -v`
Any existing test constructing `timestamp + "." + body` signatures must be updated to `timestamp + "." + id + "." + body` (with an `X-Webhook-Id` header). Expected: PASS after updates.

- [ ] **Step 5: Commit**

```bash
git add webhook/
git commit -m "fix(webhook): sign delivery ID and expose VerifyPayloadID for replay dedup (wire format change)"
```

---

### Task 9: work — recover panics in library goroutines (MEDIUM)

**Files:**
- Modify: `work/work.go` (`Map`, `All`, `Race`, `Stream`)
- Test: `work/work_test.go`

**Why:** A panic in a caller-supplied `fn` inside a work-spawned goroutine kills the whole process; callers cannot recover across the goroutine boundary. In `Race` it can also strand the collector.

- [ ] **Step 1: Write the failing tests**

```go
func TestMapRecoversPanic(t *testing.T) {
	_, err := Map(context.Background(), []int{1, 2}, func(ctx context.Context, n int) (int, error) {
		if n == 2 {
			panic("boom")
		}
		return n, nil
	})
	var werr *Errors
	if !errors.As(err, &werr) || len(werr.Failures) != 1 {
		t.Fatalf("expected 1 recovered failure, got %v", err)
	}
	if !strings.Contains(werr.Failures[0].Err.Error(), "panicked") {
		t.Fatalf("expected panic-derived error, got %v", werr.Failures[0].Err)
	}
}

func TestRaceRecoversPanic(t *testing.T) {
	got, err := Race(context.Background(),
		func(ctx context.Context) (string, error) { panic("boom") },
		func(ctx context.Context) (string, error) { return "ok", nil },
	)
	if err != nil || got != "ok" {
		t.Fatalf("expected surviving winner, got %q err %v", got, err)
	}
}

func TestAllRecoversPanic(t *testing.T) {
	err := All(context.Background(), []func(context.Context) error{
		func(context.Context) error { return nil },
		func(context.Context) error { panic("boom") },
	})
	var werr *Errors
	if !errors.As(err, &werr) || len(werr.Failures) != 1 {
		t.Fatalf("expected 1 recovered failure, got %v", err)
	}
}

func TestStreamRecoversPanic(t *testing.T) {
	in := make(chan int, 2)
	in <- 1
	in <- 2
	close(in)
	var sawPanicErr bool
	for r := range Stream(context.Background(), in, func(ctx context.Context, n int) (int, error) {
		if n == 2 { panic("boom") }
		return n, nil
	}) {
		if r.Err != nil && strings.Contains(r.Err.Error(), "panicked") {
			sawPanicErr = true
		}
	}
	if !sawPanicErr {
		t.Fatal("expected recovered panic error from Stream")
	}
}
```

- [ ] **Step 2: Run to verify they fail**

Run: `go test -run 'TestMapRecoversPanic|TestRaceRecoversPanic' ./work/ -v`
Expected: FAIL — the test process itself crashes with the panic (that IS the bug).

- [ ] **Step 3: Implement**

Add the helper (imports: `runtime/debug`):

```go
// recoverErr converts a panic in a caller-supplied task into an error, so a
// panicking task cannot crash the process from a work-spawned goroutine.
func recoverErr(err *error) {
	if r := recover(); r != nil {
		*err = fmt.Errorf("work: task panicked: %v\n%s", r, debug.Stack())
	}
}
```

Wrap every user-function invocation:

`Map` goroutine body:
```go
			val, err := func() (v R, e error) {
				defer recoverErr(&e)
				return fn(childCtx, item)
			}()
```

`All` goroutine body:
```go
			err := func() (e error) {
				defer recoverErr(&e)
				return task(childCtx)
			}()
```

`Race` goroutine body:
```go
		go func() {
			val, err := func() (v R, e error) {
				defer recoverErr(&e)
				return task(ctx)
			}()
			ch <- raceResult{value: val, err: err, index: i}
		}()
```

`Stream` goroutine body:
```go
				val, err := func() (v R, e error) {
					defer recoverErr(&e)
					return fn(childCtx, currentItem)
				}()
```

- [ ] **Step 4: Run the tests**

Run: `go test -race ./work/ -v` — Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add work/work.go work/work_test.go
git commit -m "fix(work): recover task panics as errors instead of crashing the process"
```

---

### Task 10: config — bracket-aware validate-tag splitting (MEDIUM)

**Files:**
- Modify: `config/config.go` (`Check`, ~line 178)
- Test: `config/config_test.go`

**Why:** `strings.Split(tag, ",")` truncates regex patterns containing commas (`{1,3}`, `[a,b]`), producing invalid-regex panics with misleading messages.

- [ ] **Step 1: Write the failing test**

```go
func TestCheckPatternContainingComma(t *testing.T) {
	if err := Check("Port", "123", `pattern=^[0-9]{1,3}$`); err != nil {
		t.Fatalf("pattern with comma should validate cleanly, got: %v", err)
	}
	if err := Check("Port", "1234", `pattern=^[0-9]{1,3}$`); err == nil {
		t.Fatal("expected mismatch error for 4 digits against {1,3}")
	}
	// Plain comma-separated constraints must keep working.
	if err := Check("Port", 80, "min=1,max=65535"); err != nil {
		t.Fatalf("combined min/max should pass, got: %v", err)
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test -run TestCheckPatternContainingComma ./config/ -v`
Expected: FAIL — "invalid pattern" error from the truncated regex `^[0-9]{1`.

- [ ] **Step 3: Implement**

```go
// splitConstraints splits a validate tag on commas that are not inside
// (), [], or {} groups, so regex patterns like {1,3} or [a,b] survive.
func splitConstraints(tag string) []string {
	var parts []string
	depth, start := 0, 0
	for i, r := range tag {
		switch r {
		case '(', '[', '{':
			depth++
		case ')', ']', '}':
			depth--
		case ',':
			if depth == 0 {
				parts = append(parts, tag[start:i])
				start = i + 1
			}
		}
	}
	return append(parts, tag[start:])
}
```

In `Check`, replace `parts := strings.Split(validateTag, ",")` with `parts := splitConstraints(validateTag)`.

- [ ] **Step 4: Run the tests**

Run: `go test -race ./config/ -v` — Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add config/config.go config/config_test.go
git commit -m "fix(config): bracket-aware validate-tag splitting so regex patterns with commas work"
```

---

### Task 11: schemakit — reject schema ID 0 on both sides (MEDIUM)

**Files:**
- Modify: `schemakit/schemakit.go` (`Serialize` ~line 145, `Deserialize` ~line 164)
- Test: `schemakit/schemakit_test.go`

**Why:** `Serialize` writes ID 0 for unregistered schemas; `Deserialize` then matches ID 0 against arbitrary unregistered cached schemas by random map order — silent wrong-schema decoding.

- [ ] **Step 1: Write the failing tests** (construct the `Registry` the same way the existing tests in `schemakit/schemakit_test.go` do)

```go
func TestSerializeRejectsUnregisteredSchema(t *testing.T) {
	r, schema := newTestRegistryWithLoadedSchema(t) // mirror existing test setup; schema loaded but NOT registered
	_, err := r.Serialize(schema, map[string]any{"field": "v"})
	if err == nil || !strings.Contains(err.Error(), "not registered") {
		t.Fatalf("expected not-registered error, got %v", err)
	}
}

func TestDeserializeRejectsSchemaIDZero(t *testing.T) {
	r, _ := newTestRegistryWithLoadedSchema(t)
	payload := []byte{0x00, 0, 0, 0, 0, 'x'}
	_, err := r.Deserialize(payload)
	if err == nil || !strings.Contains(err.Error(), "schema ID 0") {
		t.Fatalf("expected schema-ID-0 rejection, got %v", err)
	}
}
```

- [ ] **Step 2: Run to verify they fail**

Run: `go test -run 'TestSerializeRejectsUnregisteredSchema|TestDeserializeRejectsSchemaIDZero' ./schemakit/ -v`
Expected: FAIL — Serialize currently emits ID 0; Deserialize matches a cached unregistered schema.

- [ ] **Step 3: Implement**

In `Serialize`, before building the wire header:

```go
	id := r.schemaID(schema)
	if id == 0 {
		return nil, fmt.Errorf("schemakit: schema %q is not registered (SchemaID 0); call Register before Serialize", schema.Subject)
	}
	// ...
	binary.BigEndian.PutUint32(idBytes, uint32(id))
```

In `Deserialize`, right after extracting `schemaID`:

```go
	if schemaID == 0 {
		return nil, fmt.Errorf("schemakit: payload carries unregistered schema ID 0; refusing ambiguous decode")
	}
```

- [ ] **Step 4: Run the tests**

Run: `go test -race ./schemakit/ -v` — Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add schemakit/
git commit -m "fix(schemakit): reject schema ID 0 in Serialize/Deserialize to prevent wrong-schema decode"
```

---

### Task 12: kafkakit — honor MaxRetries, exponential backoff, deterministic dispatch (MEDIUM)

**Files:**
- Modify: `kafkakit/publisher.go:36-41`, `kafkakit/subscriber.go` (`handleRecord` ~line 253)
- Test: `kafkakit/subscriber_test.go`

- [ ] **Step 1: Write the failing dispatch test**

Extract handler selection into a method so it is testable, then:

```go
func TestSelectHandlerPrefersMostSpecificPattern(t *testing.T) {
	var fired string
	s := &Subscriber{handlers: map[string]HandlerFunc{
		"ai8.scanner.>":       func(ctx context.Context, e Event) error { fired = "general"; return nil },
		"ai8.scanner.gdelt.>": func(ctx context.Context, e Event) error { fired = "specific"; return nil },
	}}
	for i := 0; i < 20; i++ { // 20 runs flush out map-order nondeterminism
		h := s.selectHandler("ai8.scanner.gdelt.signal.surge")
		if h == nil {
			t.Fatal("expected a handler")
		}
		_ = h(context.Background(), Event{})
		if fired != "specific" {
			t.Fatalf("run %d: expected most-specific handler, got %q", i, fired)
		}
	}
}
```

(Adjust `HandlerFunc`/`Event` names to the package's actual declarations if they differ.)

- [ ] **Step 2: Run to verify it fails**

Run: `go test -run TestSelectHandlerPrefersMostSpecificPattern ./kafkakit/ -v`
Expected: FAIL to compile (`selectHandler` undefined).

- [ ] **Step 3: Implement**

In `subscriber.go`, add and use:

```go
// selectHandler returns the handler for the most specific (longest) matching
// pattern, tie-broken lexicographically. Map iteration order must never
// decide which handler fires.
func (s *Subscriber) selectHandler(subject string) HandlerFunc {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var handler HandlerFunc
	var matched string
	for pattern, h := range s.handlers {
		if !matchPattern(pattern, subject) {
			continue
		}
		if handler == nil || len(pattern) > len(matched) ||
			(len(pattern) == len(matched) && pattern < matched) {
			handler, matched = h, pattern
		}
	}
	return handler
}
```

In `handleRecord`, replace the inline map loop (and its RLock/RUnlock) with `handler := s.selectHandler(evt.Subject)`.

In `publisher.go`, make `MaxRetries` real and backoff exponential with jitter (imports: `math/rand/v2`):

```go
	if cfg.Publisher.MaxRetries > 0 {
		opts = append(opts, kgo.RecordRetries(cfg.Publisher.MaxRetries))
		base := time.Duration(cfg.Publisher.RetryBackoffMs) * time.Millisecond
		if base <= 0 {
			base = 100 * time.Millisecond
		}
		opts = append(opts, kgo.RetryBackoffFn(func(attempt int) time.Duration {
			if attempt < 1 {
				attempt = 1
			}
			shift := attempt - 1
			if shift > 6 {
				shift = 6 // cap at 64x base
			}
			d := base << shift
			return d/2 + rand.N(d/2+1) // half fixed, half jitter
		}))
	}
```

- [ ] **Step 4: Run the tests**

Run: `go test -race ./kafkakit/ -v` — Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add kafkakit/
git commit -m "fix(kafkakit): wire MaxRetries, exponential jittered backoff, deterministic handler dispatch"
```

---

### Task 13: meilikit — BulkImport returns task infos (MEDIUM)

**Files:**
- Modify: `meilikit/index.go` (`BulkImport`, success path ~line 160)
- Test: `meilikit/index_test.go` (or the existing meilikit test file)

**⚠ Signature change:** `BulkImport(...) error` → `BulkImport(...) ([]TaskInfo, error)`. After implementing, run `grep -rn "BulkImport" --include="*.go" .` and update every call site (including `examples/` if present — the module must still build).

- [ ] **Step 1: Write the failing test**

```go
func TestBulkImportReturnsTaskInfos(t *testing.T) {
	var batches int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		batches++
		w.WriteHeader(http.StatusAccepted)
		fmt.Fprintf(w, `{"taskUid": %d, "status": "enqueued"}`, 100+batches)
	}))
	defer srv.Close()

	client := NewClient(srv.URL, "test-key") // mirror the constructor used in existing meilikit tests
	idx := client.Index("things")

	records := make([]any, 3)
	for i := range records {
		records[i] = map[string]any{"id": i}
	}
	tasks, err := idx.BulkImport(context.Background(), records, BulkOptions{BatchSize: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 2 { // 3 records / batch size 2 = 2 batches
		t.Fatalf("expected 2 task infos, got %d", len(tasks))
	}
}
```

(Match `NewClient`/`Index`/`BulkOptions` spellings to the package's actual API — copy the setup from an existing meilikit test.)

- [ ] **Step 2: Run to verify it fails**

Run: `go test -run TestBulkImportReturnsTaskInfos ./meilikit/ -v`
Expected: FAIL to compile (assignment mismatch — BulkImport returns only error today).

- [ ] **Step 3: Implement**

Change the signature to return `([]TaskInfo, error)`, declare `var tasks []TaskInfo` before the loop, change every `return fmt.Errorf(...)` inside to `return tasks, fmt.Errorf(...)`, and replace the success path body handling:

```go
		if resp.StatusCode >= 400 {
			err := decodeMeiliError(resp)
			resp.Body.Close()
			return tasks, err
		}
		var task TaskInfo
		decErr := json.NewDecoder(resp.Body).Decode(&task)
		resp.Body.Close()
		if decErr != nil {
			return tasks, fmt.Errorf("meilikit: decode bulk import task for %q: %w", idx.name, decErr)
		}
		tasks = append(tasks, task)
```

Final return: `return tasks, nil`. Update the doc comment: "Returns one TaskInfo per batch; Meilisearch indexing is asynchronous, so poll these tasks to confirm documents actually indexed."

- [ ] **Step 4: Run the tests and fix call sites**

Run: `grep -rn "BulkImport" --include="*.go" .` → update all callers, including `_test.go` files and examples, then `go build ./... && go test -race ./meilikit/ -v` — Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add meilikit/ $(git diff --name-only | grep -v meilikit || true)
git commit -m "fix(meilikit): BulkImport returns per-batch TaskInfo so async indexing failures are observable"
```

---

### Task 14: heartbeatkit — bound each publish so shutdown can't hang (MEDIUM)

**Files:**
- Modify: `heartbeatkit/heartbeatkit.go` (ticker case, ~lines 62-84)
- Test: `heartbeatkit/heartbeatkit_test.go`

- [ ] **Step 1: Write the failing test**

```go
type blockingPublisher struct {
	started chan struct{}
	finished chan error
	once sync.Once
}

func (b *blockingPublisher) Publish(ctx context.Context, subject string, data any) error {
	b.once.Do(func() { close(b.started) })
	<-ctx.Done() // block until the per-publish context gives up
	err := ctx.Err()
	select {
	case b.finished <- err:
	default:
	}
	return err
}

func TestPublishCannotBlockForever(t *testing.T) {
	pub := &blockingPublisher{started: make(chan struct{}), finished: make(chan error, 1)}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	defer Stop()

	Start(ctx, pub, Config{Interval: 50 * time.Millisecond, ServiceName: "t"})

	select {
	case <-pub.started:
	case <-time.After(2 * time.Second):
		t.Fatal("publish did not start")
	}

	select {
	case err := <-pub.finished:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("expected per-publish deadline, got %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("publish context is unbounded: blocking Publish did not return")
	}
}
```

The old `time.AfterFunc(...).C` form is invalid because `AfterFunc` timers do not expose a receive channel. `Stop()` alone is not proof here because current `Stop()` does not wait for the publish goroutine.

- [ ] **Step 2: Run to verify current behavior** — `go test -run TestPublishCannotBlockForever ./heartbeatkit/ -v`. (Old code: publish blocks on the long-lived `ctx`; the loop goroutine stays stuck in `Publish` — the test documents the bounded behavior.)

- [ ] **Step 3: Implement**

Replace the publish call inside `case <-ticker.C:`:

```go
				timeout := cfg.Interval / 2
				if timeout < time.Second {
					timeout = time.Second
				}
				pubCtx, cancelPub := context.WithTimeout(ctx, timeout)
				if err := pub.Publish(pubCtx, "ai8.infra.heartbeat", payload); err != nil {
					slog.Warn("heartbeatkit: publish failed", "error", err)
				}
				cancelPub()
```

A small helper closure with `defer cancelPub()` is also acceptable if it keeps the cancellation scoped to each single publish attempt.

- [ ] **Step 4: Run the tests**

Run: `go test -race ./heartbeatkit/ -v` — Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add heartbeatkit/
git commit -m "fix(heartbeatkit): bound each publish with a timeout so a dead broker cannot wedge shutdown"
```

---

### Task 15: deploy — surface env-file read errors (MEDIUM)

**Files:**
- Modify: `deploy/deploy.go` (`parseEnvFile`, ~line 336)
- Test: add an internal package test file such as `deploy/env_internal_test.go` (`package deploy`) for unexported `parseEnvFile`; keep public-behavior tests in `deploy/deploy_test.go` (`package deploy_test`).

**Why:** `scanner.Err()` was never checked: a >64 KB line stops the scan and the service silently starts with a PARTIAL environment. Partial secrets are worse than none.

- [ ] **Step 1: Write the failing test**

```go
func TestParseEnvFileOversizedLine(t *testing.T) {
	dir := t.TempDir()

	// A line beyond the enlarged 1 MB cap must yield an EMPTY map, not a
	// silently partial one.
	bad := filepath.Join(dir, "bad.env")
	content := "GOOD=1\nBIG=" + strings.Repeat("x", 2<<20) + "\nAFTER=2\n"
	if err := os.WriteFile(bad, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := parseEnvFile(bad); len(got) != 0 {
		t.Fatalf("expected empty map on unreadable env file, got %d entries", len(got))
	}

	// 100 KB values (e.g. base64 certs) now fit within the enlarged buffer.
	ok := filepath.Join(dir, "ok.env")
	if err := os.WriteFile(ok, []byte("CERT="+strings.Repeat("y", 100*1024)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := parseEnvFile(ok); len(got["CERT"]) != 100*1024 {
		t.Fatalf("expected 100KB value to parse, got len %d", len(got["CERT"]))
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test -run TestParseEnvFileOversizedLine ./deploy/ -v` (the test must be in `package deploy` if it calls unexported `parseEnvFile`).
Expected: FAIL — old code returns the partial map `{GOOD:1}` and can't parse 100 KB lines at all.

- [ ] **Step 3: Implement**

In `parseEnvFile`, after `scanner := bufio.NewScanner(f)` add a bigger buffer, and check the error after the loop (import `log/slog` if not present):

```go
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
```

```go
	if err := scanner.Err(); err != nil {
		// A partial environment (missing credentials below the bad line) is
		// worse than none: fall back to empty so startup fails loudly.
		slog.Error("deploy: env file read failed; ignoring its contents", "path", path, "error", err)
		return map[string]string{}
	}
	return result
```

- [ ] **Step 4: Run the tests**

Run: `go test -race ./deploy/ -v` — Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add deploy/
git commit -m "fix(deploy): detect env-file scan errors and refuse partial environments; allow 1MB lines"
```

---

### Task 16: phasekit — make tests idempotent and load-tolerant (MEDIUM, test-only)

**Files:**
- Modify: `phasekit/phasekit_test.go`

**Why (verified):** `go test -count=2 ./phasekit/` fails today — `Hydrate` writes secrets with `os.Setenv` and tests never clean them, so run 2 sees them as "existing" and skips. Separately, the default 10s CLI timeout flaked under full-suite `-race` load.

- [ ] **Step 1: Reproduce**

Run: `go test -count=2 ./phasekit/`
Expected: FAIL on `TestHydrateHappyPath` and `TestHydratePreservesExistingByDefault`.

- [ ] **Step 2: Add the cleanup helper**

```go
// restoreEnvAfter restores env keys a test's Hydrate call may set, keeping repeated
// in-process runs (go test -count>1) independent without clobbering a caller's
// pre-existing environment.
func restoreEnvAfter(t *testing.T, keys ...string) {
	t.Helper()
	prior := make(map[string]struct{ value string; ok bool }, len(keys))
	for _, k := range keys {
		v, ok := os.LookupEnv(k)
		prior[k] = struct{ value string; ok bool }{v, ok}
	}
	t.Cleanup(func() {
		for _, k := range keys {
			p := prior[k]
			if p.ok {
				os.Setenv(k, p.value)
			} else {
				os.Unsetenv(k)
			}
		}
	})
}
```

- [ ] **Step 3: Apply it to every test that calls Hydrate, naming that test's secret keys**

Example for the happy path (same pattern everywhere):

```go
func TestHydrateHappyPath(t *testing.T) {
	restoreEnvAfter(t, "PHASEKIT_ALPHA", "PHASEKIT_BETA", "PHASEKIT_GAMMA")
	fake := phasetest.WithFakeBinary(t, phasetest.FakeOptions{
		...
```

Apply to (at minimum): `TestHydrateHappyPath`, `TestHydrateExplicitBinaryPathDoesNotRequirePATH` (`PHASEKIT_EXPLICIT_BINARY`), `TestHydratePreservesExistingByDefault` (`PHASEKIT_NEW`; the EXISTING keys already use `t.Setenv`), `TestHydrateOverwriteExisting`, `TestHydrateMultiLineValue`, and every other test whose fake binary defines `Secrets`. Grep: `grep -n "Secrets: map" phasekit/phasekit_test.go` and cover each hit.

- [ ] **Step 4: Raise the subprocess timeout in tests**

In `validConfig()` and every inline `Config{...}` literal passed to `Hydrate` alongside a fake binary, add:

```go
	Timeout: 60 * time.Second, // generous: full-suite -race runs starve subprocess spawns
```

- [ ] **Step 5: Verify and commit**

Run: `go test -race -count=3 ./phasekit/` — Expected: PASS (three consecutive in-process runs)

```bash
git add phasekit/phasekit_test.go
git commit -m "test(phasekit): clean hydrated env keys between runs; raise fake-CLI timeout for loaded machines"
```

---

### Task 17: call — span-name cardinality and bounded retry drain (LOW)

**Files:**
- Modify: `call/call.go:148` (span start), `call/retry.go:45,74` (drains)
- Test: existing `call` tests

- [ ] **Step 1: Fix the span name** — at `call/call.go:148`, change the span name from `req.Method+" "+req.URL.Path` to `req.Method`, keeping/adding the path as an attribute (skip adding it if an equivalent `url.path`/`http.url` attribute is already recorded a few lines below):

```go
	ctx, span := tracer.Start(ctx, req.Method,
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(
			attribute.String("url.path", req.URL.Path),
```

- [ ] **Step 2: Bound the drains** — in `call/retry.go`, replace both occurrences:

```go
	io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20)) // cap drain at 1MB; huge 5xx bodies aren't worth reading
```

- [ ] **Step 3: Verify and commit**

Run: `go test -race ./call/ -v` — Expected: PASS

```bash
git add call/
git commit -m "fix(call): method-only client span names; cap retry body drain at 1MB"
```

---

### Task 18: small guards — seal, flagz, errors, tick (LOW)

**Files:**
- Modify: `seal/seal.go` (`Sign` ~line 142, `Verify`, `Encrypt` ~line 50, `Decrypt`)
- Modify: `flagz/sources.go` (`Multi` ~line 96)
- Modify: `errors/problem.go:124` (`WriteProblem` encode-error branch)
- Modify: `tick/tick.go:70-81` (jitter timer)
- Test: `seal/seal_test.go`, `flagz/flagz_test.go`

- [ ] **Step 1: Write the failing tests**

```go
// seal/seal_test.go
func TestSignPanicsOnEmptySecret(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic for empty secret")
		}
	}()
	seal.Sign([]byte("payload"), "")
}

func TestVerifyEmptySecretIsFalse(t *testing.T) {
	if seal.Verify([]byte("payload"), seal.Sign([]byte("payload"), "k"), "") {
		t.Fatal("empty secret must never verify")
	}
}

func TestEncryptEmptyPassphraseErrors(t *testing.T) {
	if _, err := seal.Encrypt([]byte("data"), ""); err == nil {
		t.Fatal("expected error for empty passphrase")
	}
}
```

```go
// flagz/flagz_test.go
func TestMultiPanicsOnNilSource(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic for nil source")
		}
	}()
	flagz.Multi(flagz.FromEnv("FF"), nil)
}
```

Because `seal/seal_test.go` and `flagz/flagz_test.go` are external test packages today, qualify calls as `seal.Sign`, `seal.Verify`, `seal.Encrypt`, `flagz.Multi`, and `flagz.FromEnv`, or intentionally add new internal-package tests if unqualified access is required.

- [ ] **Step 2: Run to verify they fail** — `go test ./seal/ ./flagz/ -run 'EmptySecret|EmptyPassphrase|NilSource' -v` → FAIL.

- [ ] **Step 3: Implement all four guards**

`seal/seal.go` — top of `Sign`:
```go
	if secret == "" {
		panic("seal: Sign called with empty secret — signatures would be forgeable; check your secret env var")
	}
```
Top of `Verify`:
```go
	if secret == "" {
		return false
	}
```
Top of `Encrypt` (and mirror in `Decrypt`):
```go
	if passphrase == "" {
		return Envelope{}, errors.New("seal: empty passphrase")
	}
```

`flagz/sources.go` — top of `Multi`:
```go
	for i, s := range sources {
		if s == nil {
			panic(fmt.Sprintf("flagz: Multi received nil source at index %d", i))
		}
	}
```

`errors/problem.go:124` — the encode-error branch currently calls `slog.ErrorContext(r.Context(), ...)`; guard it:
```go
		if r != nil {
			slog.ErrorContext(r.Context(), "errors: write problem response failed", "error", encErr)
		} else {
			slog.Error("errors: write problem response failed", "error", encErr)
		}
```
(Keep the existing message/attrs; only add the nil guard.)

`tick/tick.go` — replace the inline-closure timer so it is always stopped:
```go
				if cfg.jitter > 0 {
					jt := time.NewTimer(time.Duration(rand.Int64N(int64(cfg.jitter))))
					select {
					case <-ctx.Done():
						jt.Stop()
						return nil
					case <-jt.C:
					}
				}
```

- [ ] **Step 4: Run the tests**

Run: `go test -race ./seal/ ./flagz/ ./errors/ ./tick/ ./webhook/ -v` — Expected: PASS (webhook included because it calls `seal.Sign`; its tests use non-empty secrets).

- [ ] **Step 5: Commit**

```bash
git add seal/ flagz/ errors/ tick/
git commit -m "fix: fail fast on empty seal secrets, nil flagz sources; guard nil request; stop jitter timer"
```

---

### Task 19: misc — docs, hook output, inferkit close, x/crypto bump (LOW)

**Files:**
- Modify: `guard/keyfunc.go:86` (HeaderKey doc), `deploy/deploy.go` (`runHookExec` ~line 366), `inferkit/inferkit.go:604` (decode/close), `go.mod`

- [ ] **Step 1: HeaderKey warning** — replace the doc comment above `func HeaderKey`:

```go
// HeaderKey returns a KeyFunc using the value of a request header as the
// rate-limit key.
//
// WARNING: only use headers set by trusted infrastructure (e.g. a load
// balancer). A client-controlled header lets an attacker mint a fresh
// rate-limit bucket per request, trivially bypassing the limit.
```

- [ ] **Step 2: Hook output in errors** — replace `runHookExec`'s body tail:

```go
	out, err := cmd.CombinedOutput()
	if err != nil {
		tail := out
		if len(tail) > 2048 {
			tail = tail[len(tail)-2048:]
		}
		return fmt.Errorf("deploy: hook %s failed: %w: %s", filepath.Base(path), err, strings.TrimSpace(string(tail)))
	}
	return nil
```

- [ ] **Step 3: inferkit deferred close** — in `doWithRetry`'s 2xx branch, make the close panic-safe:

```go
			decErr := func() error {
				defer resp.Body.Close()
				return json.NewDecoder(resp.Body).Decode(dst)
			}()
			if decErr != nil {
				return ResponseMeta{}, fmt.Errorf("inferkit: decode response: %w", decErr)
			}
			return meta, nil
```

- [ ] **Step 4: Dependency bump**

```bash
go get golang.org/x/crypto@v0.53.0 && go mod tidy && go build ./...
```
Expected: clean build. (x/crypto v0.48.0 is stale; bump to the latest verified compatible version at execution time. v0.53.0 was observed available during plan repair.)

- [ ] **Step 5: Verify and commit**

Run: `go vet ./... && go test -race ./guard/ ./deploy/ ./inferkit/` — Expected: PASS

```bash
git add guard/keyfunc.go deploy/deploy.go inferkit/inferkit.go go.mod go.sum
git commit -m "chore: HeaderKey trust warning, hook failure output, panic-safe inferkit close, x/crypto bump"
```

---

### Task 20: Finalization — full gates, bug notes, VERSION, CHANGELOG, push

- [ ] **Step 1: Full gates**

```bash
go build ./... && go vet ./... && go test -race -count=1 ./... && go test -count=2 ./phasekit/
```
Expected: all PASS. If anything fails, fix before proceeding — do not skip.

- [ ] **Step 2: Bug notes** — write to `_bugs_fixed/` (lowercase, today's date):
  - `2026-07-02-posthogkit-capture-after-close-panic.md` — send-on-closed-channel panic during shutdown; fixed with done-channel.
  - `2026-07-02-call-retry-silent-empty-body.md` — swallowed GetBody error re-sent consumed body; now fails the request.
  - `2026-07-02-webhook-unsigned-delivery-id.md` — ID now signed and exposed via VerifyPayloadID (wire format change).
  - `2026-07-02-audit-medium-low-batch.md` — one file briefly listing the remaining fixes (kafkakit, lakekit, ollamakit, freshness, registry, work, config, schemakit, meilikit, heartbeatkit, deploy, phasekit tests, seal/flagz/errors/tick/guard/inferkit, x/crypto).

- [ ] **Step 3: VERSION + CHANGELOG** — NOW read `VERSION`. Current line is 11.1.x with revisions capped at 15, and the webhook wire format changed, so set `VERSION` to `11.2.0`. Prepend a CHANGELOG.md entry listing every fix, with a **BREAKING/wire** callout: webhook signatures now cover the delivery ID (`timestamp.id.body`) — upgrade senders and receivers together; `meilikit.BulkImport` and error shape of `kafkakit.PublishBatch` changed; `seal.Sign` now panics on empty secret.

- [ ] **Step 4: Commit and push** (skip on `z*` hostnames)

```bash
git add VERSION CHANGELOG.md _bugs_fixed/
git commit -m "release: 11.2.0 — audit remediation (1 critical, 4 high, 13 medium, 10 low). <ActualAgent:ActualModel>"
git push
```

---

## Self-Review Notes

- Every audit finding maps to a task or the Deferred list; nothing is silently dropped.
- Tasks 2, 3, 11, 12, 13 tell the implementer to mirror existing in-package test constructors where exact names weren't verified — check the neighboring tests first in those packages.
- Type consistency: `BatchError`/`BatchFailure` (Task 3) and `VerifyPayloadID` (Task 8) are defined once and referenced nowhere else; `recoverErr` (Task 9), `splitConstraints` (Task 10), `decodeJSON` (Task 4), `rebuildTempPath` (Task 6), `selectHandler` (Task 12) are package-private helpers used only in their own task.
