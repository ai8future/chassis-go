package idemkit

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	chassis "github.com/ai8future/chassis-go/v11"
)

var _ Store = (*legacyRetainingStore)(nil)

func init() { chassis.RequireMajor(11) }

func TestNilTenantResolverRestoresDefault(t *testing.T) {
	store := NewMemoryStore(time.Hour)
	h := Middleware(store, WithTenantResolver(func(*http.Request) string { return "ignored" }), WithTenantResolver(nil))(
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte("created"))
		}),
	)

	first := keyedRequest("nil-resolver", "same")
	firstRec := httptest.NewRecorder()
	h.ServeHTTP(firstRec, first)
	second := keyedRequest("nil-resolver", "same")
	secondRec := httptest.NewRecorder()
	h.ServeHTTP(secondRec, second)

	if firstRec.Code != http.StatusCreated || secondRec.Code != http.StatusCreated {
		t.Fatalf("statuses = %d/%d, want 201/201", firstRec.Code, secondRec.Code)
	}
	if secondRec.Header().Get(ReplayHeader) != "true" {
		t.Fatalf("replay header = %q, want true", secondRec.Header().Get(ReplayHeader))
	}
}

func TestRequestBodyLimit(t *testing.T) {
	var hits atomic.Int32
	h := Middleware(NewMemoryStore(time.Hour), WithRequestBodyLimit(4))(
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			hits.Add(1)
			w.WriteHeader(http.StatusCreated)
		}),
	)

	atLimit := httptest.NewRequest(http.MethodPost, "/limited", strings.NewReader("1234"))
	atLimit.Header.Set(DefaultHeader, "at-limit")
	atLimitRec := httptest.NewRecorder()
	h.ServeHTTP(atLimitRec, atLimit)
	if atLimitRec.Code != http.StatusCreated {
		t.Fatalf("at-limit status = %d, want 201", atLimitRec.Code)
	}

	over := httptest.NewRequest(http.MethodPost, "/limited", strings.NewReader("12345"))
	over.Header.Set(DefaultHeader, "over-limit")
	overRec := httptest.NewRecorder()
	h.ServeHTTP(overRec, over)
	assertProblem(t, overRec, http.StatusRequestEntityTooLarge, "payload_too_large")
	if hits.Load() != 1 {
		t.Fatalf("handler hits = %d, want 1", hits.Load())
	}
}

func TestResponseBodyLimit(t *testing.T) {
	var hits atomic.Int32
	h := Middleware(NewMemoryStore(time.Hour), WithResponseBodyLimit(4))(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			hits.Add(1)
			w.Header().Set("X-Handler-Success", "true")
			if r.URL.Query().Get("over") == "true" {
				_, _ = w.Write([]byte("12345"))
				return
			}
			_, _ = w.Write([]byte("1234"))
		}),
	)

	atLimit := httptest.NewRequest(http.MethodPost, "/limited", nil)
	atLimit.Header.Set(DefaultHeader, "at-limit")
	atLimitRec := httptest.NewRecorder()
	h.ServeHTTP(atLimitRec, atLimit)
	if atLimitRec.Code != http.StatusOK || atLimitRec.Body.String() != "1234" {
		t.Fatalf("at-limit response = %d/%q", atLimitRec.Code, atLimitRec.Body.String())
	}

	for attempt := 0; attempt < 2; attempt++ {
		over := httptest.NewRequest(http.MethodPost, "/limited?over=true", nil)
		over.Header.Set(DefaultHeader, "over-limit")
		overRec := httptest.NewRecorder()
		h.ServeHTTP(overRec, over)
		assertProblem(t, overRec, http.StatusInternalServerError, classResponseTooLarge)
		if got := overRec.Header().Get("X-Handler-Success"); got != "" {
			t.Fatalf("handler success header leaked: %q", got)
		}
	}
	if hits.Load() != 3 {
		t.Fatalf("handler hits = %d, want 3 (oversized responses must not complete)", hits.Load())
	}
}

func TestNonPositiveLimitsAndCapacityRestoreBoundedDefaults(t *testing.T) {
	cfg := options{}
	WithRequestBodyLimit(0)(&cfg)
	WithResponseBodyLimit(-1)(&cfg)
	if cfg.maxRequestBytes != DefaultMaxRequestBytes || cfg.maxResponseBytes != DefaultMaxResponseBytes {
		t.Fatalf("normalized limits = %d/%d", cfg.maxRequestBytes, cfg.maxResponseBytes)
	}
	if store := NewMemoryStoreWithCapacity(0, 0); store.ttl != DefaultMemoryTTL || store.capacity != DefaultMemoryCapacity {
		t.Fatalf("normalized store config = ttl %v capacity %d", store.ttl, store.capacity)
	}
}

func TestStatusAndRetryAfterMappings(t *testing.T) {
	store := NewMemoryStore(time.Hour)
	started, err := store.BeginLease(context.Background(), "", "busy", Fingerprint(http.MethodPost, "/mapped", []byte("one")))
	if err != nil || started.Result != Started {
		t.Fatalf("seed claim = %#v, %v", started, err)
	}
	h := Middleware(store)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
	}))

	busy := httptest.NewRequest(http.MethodPost, "/mapped", strings.NewReader("one"))
	busy.Header.Set(DefaultHeader, "busy")
	busyRec := httptest.NewRecorder()
	h.ServeHTTP(busyRec, busy)
	assertProblem(t, busyRec, http.StatusConflict, classInFlight)
	if got := busyRec.Header().Get("Retry-After"); got != "1" {
		t.Fatalf("in-flight Retry-After = %q, want 1", got)
	}

	mismatch := httptest.NewRequest(http.MethodPost, "/mapped", strings.NewReader("two"))
	mismatch.Header.Set(DefaultHeader, "busy")
	mismatchRec := httptest.NewRecorder()
	h.ServeHTTP(mismatchRec, mismatch)
	assertProblem(t, mismatchRec, http.StatusUnprocessableEntity, classMismatch)
	if got := mismatchRec.Header().Get("Retry-After"); got != "" {
		t.Fatalf("mismatch Retry-After = %q, want empty", got)
	}
}

func TestMemoryStoreCapacityRejectsWithoutEvictingLiveReplay(t *testing.T) {
	store := NewMemoryStoreWithCapacity(time.Hour, 1)
	ctx := context.Background()
	first, err := store.BeginLease(ctx, "tenant", "first", "fp-first")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CompleteLease(ctx, "tenant", "first", "fp-first", first.Token, StoredResponse{StatusCode: http.StatusCreated, Body: []byte("first")}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.BeginLease(ctx, "tenant", "second", "fp-second"); !errors.Is(err, ErrCapacity) {
		t.Fatalf("capacity error = %v, want ErrCapacity", err)
	}
	replay, err := store.BeginLease(ctx, "tenant", "first", "fp-first")
	if err != nil || replay.Result != Replay || string(replay.Response.Body) != "first" {
		t.Fatalf("live replay was evicted: %#v, %v", replay, err)
	}
}

func TestMiddlewareMapsCapacityToUnavailable(t *testing.T) {
	store := NewMemoryStoreWithCapacity(time.Hour, 1)
	seed, err := store.BeginLease(context.Background(), "", "seed", "fp")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CompleteLease(context.Background(), "", "seed", "fp", seed.Token, StoredResponse{StatusCode: http.StatusOK}); err != nil {
		t.Fatal(err)
	}
	h := Middleware(store)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
	}))
	req := keyedRequest("new-key", "body")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	assertProblem(t, rec, http.StatusServiceUnavailable, classStoreError)
	if got := rec.Header().Get("Retry-After"); got != "1" {
		t.Fatalf("capacity Retry-After = %q, want 1", got)
	}
}

func TestMemoryStoreExpiresRecordsBeforeCapacityCheck(t *testing.T) {
	store := NewMemoryStoreWithCapacity(time.Hour, 1)
	ctx := context.Background()
	claim, err := store.BeginLease(ctx, "", "expired", "old")
	if err != nil {
		t.Fatal(err)
	}
	store.mu.Lock()
	store.records[storeKey{key: "expired"}].createdAt = time.Now().Add(-2 * time.Hour)
	store.mu.Unlock()

	replacement, err := store.BeginLease(ctx, "", "replacement", "new")
	if err != nil || replacement.Result != Started {
		t.Fatalf("replacement = %#v, %v", replacement, err)
	}
	if replacement.Token == claim.Token {
		t.Fatal("replacement reused an opaque lease token")
	}
}

func TestMemoryStoreLeasePreventsStaleOwnerABA(t *testing.T) {
	store := NewMemoryStore(time.Hour)
	ctx := context.Background()
	oldClaim, err := store.BeginLease(ctx, "tenant", "key", "fp")
	if err != nil {
		t.Fatal(err)
	}
	store.mu.Lock()
	store.records[storeKey{tenantID: "tenant", key: "key"}].createdAt = time.Now().Add(-2 * time.Hour)
	store.mu.Unlock()
	newClaim, err := store.BeginLease(ctx, "tenant", "key", "fp")
	if err != nil {
		t.Fatal(err)
	}
	if newClaim.Token == "" || newClaim.Token == oldClaim.Token {
		t.Fatalf("old/new tokens = %q/%q", oldClaim.Token, newClaim.Token)
	}
	if err := store.ReleaseLease(ctx, "tenant", "key", oldClaim.Token); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("stale release error = %v, want ErrLeaseLost", err)
	}
	if err := store.CompleteLease(ctx, "tenant", "key", "fp", oldClaim.Token, StoredResponse{Body: []byte("stale")}); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("stale complete error = %v, want ErrLeaseLost", err)
	}
	stillOwned, err := store.BeginLease(ctx, "tenant", "key", "fp")
	if err != nil || stillOwned.Result != InFlight {
		t.Fatalf("replacement after stale operations = %#v, %v", stillOwned, err)
	}
	if err := store.CompleteLease(ctx, "tenant", "key", "fp", newClaim.Token, StoredResponse{Body: []byte("fresh")}); err != nil {
		t.Fatal(err)
	}
	replay, err := store.BeginLease(ctx, "tenant", "key", "fp")
	if err != nil || replay.Result != Replay || string(replay.Response.Body) != "fresh" {
		t.Fatalf("fresh replay = %#v, %v", replay, err)
	}
}

func TestMemoryStoreCompletedRecordExpires(t *testing.T) {
	store := NewMemoryStore(time.Hour)
	ctx := context.Background()
	claim, err := store.BeginLease(ctx, "", "completed", "fp")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CompleteLease(ctx, "", "completed", "fp", claim.Token, StoredResponse{Body: []byte("stored")}); err != nil {
		t.Fatal(err)
	}
	replay, err := store.BeginLease(ctx, "", "completed", "fp")
	if err != nil || replay.Result != Replay {
		t.Fatalf("live completed claim = %#v, %v", replay, err)
	}
	store.mu.Lock()
	store.records[storeKey{key: "completed"}].createdAt = time.Now().Add(-2 * time.Hour)
	store.mu.Unlock()
	replacement, err := store.BeginLease(ctx, "", "completed", "fp")
	if err != nil || replacement.Result != Started {
		t.Fatalf("expired completed replacement = %#v, %v", replacement, err)
	}
	if replacement.Token == claim.Token {
		t.Fatal("expired completed record reused its lease token")
	}
}

func TestMemoryStoreClonesResponseOwnership(t *testing.T) {
	store := NewMemoryStore(time.Hour)
	ctx := context.Background()
	claim, err := store.BeginLease(ctx, "", "owned", "fp")
	if err != nil {
		t.Fatal(err)
	}
	header := http.Header{"X-Owned": []string{"original"}}
	body := []byte("original")
	if err := store.CompleteLease(ctx, "", "owned", "fp", claim.Token, StoredResponse{Header: header, Body: body}); err != nil {
		t.Fatal(err)
	}
	header.Set("X-Owned", "mutated")
	body[0] = 'X'

	firstReplay, err := store.BeginLease(ctx, "", "owned", "fp")
	if err != nil {
		t.Fatal(err)
	}
	if got := firstReplay.Response.Header.Get("X-Owned"); got != "original" || string(firstReplay.Response.Body) != "original" {
		t.Fatalf("stored response changed = %q/%q", got, firstReplay.Response.Body)
	}
	firstReplay.Response.Header.Set("X-Owned", "replay-mutated")
	firstReplay.Response.Body[0] = 'Y'
	secondReplay, err := store.BeginLease(ctx, "", "owned", "fp")
	if err != nil {
		t.Fatal(err)
	}
	if got := secondReplay.Response.Header.Get("X-Owned"); got != "original" || string(secondReplay.Response.Body) != "original" {
		t.Fatalf("replay ownership leaked = %q/%q", got, secondReplay.Response.Body)
	}
}

func TestLegacyStoreCompatibilityAndAmbiguousComplete(t *testing.T) {
	store := &legacyRetainingStore{completeErr: errors.New("completion outcome unknown")}
	if _, ok := any(store).(LeaseStore); ok {
		t.Fatal("old Store fake unexpectedly implements LeaseStore")
	}
	var mutations atomic.Int32
	h := Middleware(store)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		mutations.Add(1)
		w.Header().Set("X-Handler-Success", "true")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte("business mutation accepted"))
	}))

	firstRec := httptest.NewRecorder()
	h.ServeHTTP(firstRec, keyedRequest("legacy", "body"))
	assertProblem(t, firstRec, http.StatusServiceUnavailable, classStoreError)
	if firstRec.Header().Get("Retry-After") != "1" {
		t.Fatalf("complete failure Retry-After = %q", firstRec.Header().Get("Retry-After"))
	}
	if firstRec.Header().Get("X-Handler-Success") != "" || strings.Contains(firstRec.Body.String(), "business mutation") {
		t.Fatalf("handler success leaked: headers=%v body=%q", firstRec.Header(), firstRec.Body.String())
	}
	if store.releaseCalls.Load() != 0 {
		t.Fatalf("release calls = %d, want 0 after ambiguous Complete", store.releaseCalls.Load())
	}

	secondRec := httptest.NewRecorder()
	h.ServeHTTP(secondRec, keyedRequest("legacy", "body"))
	assertProblem(t, secondRec, http.StatusConflict, classInFlight)
	if mutations.Load() != 1 {
		t.Fatalf("business mutations = %d, want 1", mutations.Load())
	}
}

func TestLeaseCompleteFailureIsFailClosedAndRetained(t *testing.T) {
	store := &failingCompleteLeaseStore{MemoryStore: NewMemoryStore(time.Hour)}
	var hits atomic.Int32
	h := Middleware(store)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte("success"))
	}))
	firstRec := httptest.NewRecorder()
	h.ServeHTTP(firstRec, keyedRequest("lease-fail", "body"))
	assertProblem(t, firstRec, http.StatusServiceUnavailable, classStoreError)
	if firstRec.Header().Get("Retry-After") != "1" || firstRec.Body.String() == "success" {
		t.Fatalf("fail-closed response headers=%v body=%q", firstRec.Header(), firstRec.Body.String())
	}
	if store.releaseCalls.Load() != 0 {
		t.Fatalf("release calls = %d, want 0", store.releaseCalls.Load())
	}
	secondRec := httptest.NewRecorder()
	h.ServeHTTP(secondRec, keyedRequest("lease-fail", "body"))
	assertProblem(t, secondRec, http.StatusConflict, classInFlight)
	if hits.Load() != 1 {
		t.Fatalf("handler hits = %d, want 1", hits.Load())
	}
}

func TestHandlerPanicReleasesLeaseAndRepanics(t *testing.T) {
	store := NewMemoryStore(time.Hour)
	var hits atomic.Int32
	h := Middleware(store)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if hits.Add(1) == 1 {
			panic("handler panic")
		}
		w.WriteHeader(http.StatusCreated)
	}))

	func() {
		defer func() {
			if recovered := recover(); recovered != "handler panic" {
				t.Fatalf("recovered = %#v, want handler panic", recovered)
			}
		}()
		h.ServeHTTP(httptest.NewRecorder(), keyedRequest("panic", "body"))
	}()
	secondRec := httptest.NewRecorder()
	h.ServeHTTP(secondRec, keyedRequest("panic", "body"))
	if secondRec.Code != http.StatusCreated || hits.Load() != 2 {
		t.Fatalf("second response/hits = %d/%d, want 201/2", secondRec.Code, hits.Load())
	}
}

func TestCancelledHandlerPanicReleasesLeaseAndRepanics(t *testing.T) {
	store := NewMemoryStore(time.Hour)
	var hits atomic.Int32
	var cancelFirst context.CancelFunc
	h := Middleware(store)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if hits.Add(1) == 1 {
			cancelFirst()
			panic("cancelled handler panic")
		}
		w.WriteHeader(http.StatusCreated)
	}))

	ctx, cancel := context.WithCancel(context.Background())
	cancelFirst = cancel
	func() {
		defer func() {
			if recovered := recover(); recovered != "cancelled handler panic" {
				t.Fatalf("recovered = %#v, want cancelled handler panic", recovered)
			}
		}()
		h.ServeHTTP(httptest.NewRecorder(), keyedRequest("cancelled-panic", "body").WithContext(ctx))
	}()

	secondRec := httptest.NewRecorder()
	h.ServeHTTP(secondRec, keyedRequest("cancelled-panic", "body"))
	if secondRec.Code != http.StatusCreated || hits.Load() != 2 {
		t.Fatalf("second response/hits = %d/%d, want 201/2", secondRec.Code, hits.Load())
	}
}

func TestCancelledHandlerCleanupUsesDetachedBoundedContext(t *testing.T) {
	tests := []struct {
		name    string
		options []Option
		handle  func(http.ResponseWriter)
	}{
		{
			name: "handler 5xx",
			handle: func(w http.ResponseWriter) {
				w.WriteHeader(http.StatusServiceUnavailable)
			},
		},
		{
			name:    "oversized response",
			options: []Option{WithResponseBodyLimit(4)},
			handle: func(w http.ResponseWriter) {
				_, _ = w.Write([]byte("12345"))
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := &releaseContextObservingStore{MemoryStore: NewMemoryStore(time.Hour)}
			ctx, cancel := context.WithCancel(context.WithValue(context.Background(), releaseContextKey{}, "preserved"))
			h := Middleware(store, tt.options...)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				cancel()
				tt.handle(w)
			}))

			h.ServeHTTP(httptest.NewRecorder(), keyedRequest("cancelled-cleanup", "body").WithContext(ctx))

			called, hasDeadline, value, contextErr := store.releaseObservation()
			if called != 1 || contextErr != nil || !hasDeadline || value != "preserved" {
				t.Fatalf("release observation = called:%d err:%v deadline:%v value:%#v", called, contextErr, hasDeadline, value)
			}
			claim, err := store.BeginLease(context.Background(), "", "cancelled-cleanup", Fingerprint(http.MethodPost, "/operation", []byte("body")))
			if err != nil || claim.Result != Started {
				t.Fatalf("claim after cancelled cleanup = %#v, %v", claim, err)
			}
		})
	}
}

func TestHandlerPanicReleasesLegacyClaimAndRepanics(t *testing.T) {
	store := &legacyRetainingStore{}
	h := Middleware(store)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("legacy handler panic")
	}))
	func() {
		defer func() {
			if recovered := recover(); recovered != "legacy handler panic" {
				t.Fatalf("recovered = %#v, want legacy handler panic", recovered)
			}
		}()
		h.ServeHTTP(httptest.NewRecorder(), keyedRequest("legacy-panic", "body"))
	}()
	if store.releaseCalls.Load() != 1 {
		t.Fatalf("legacy panic release calls = %d, want 1", store.releaseCalls.Load())
	}
	claim, err := store.Begin(context.Background(), "", "legacy-panic", Fingerprint(http.MethodPost, "/operation", []byte("body")))
	if err != nil || claim.Result != Started {
		t.Fatalf("claim after panic release = %#v, %v", claim, err)
	}
}

func TestReleaseFailureLogExcludesSensitiveValues(t *testing.T) {
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })

	logReleaseFailure(errors.New("tenant-secret key-secret body-secret"), "test_release")
	got := logs.String()
	for _, secret := range []string{"tenant-secret", "key-secret", "body-secret"} {
		if strings.Contains(got, secret) {
			t.Fatalf("release log leaked %q: %s", secret, got)
		}
	}
	if !strings.Contains(got, "operation=test_release") || !strings.Contains(got, "error_class=idempotency_store_error") {
		t.Fatalf("release log missing structured fields: %s", got)
	}
}

type legacyRetainingStore struct {
	mu           sync.Mutex
	inFlight     bool
	fingerprint  string
	completeErr  error
	releaseCalls atomic.Int32
}

func (s *legacyRetainingStore) Begin(_ context.Context, _, _, fingerprint string) (Claim, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.inFlight {
		s.inFlight = true
		s.fingerprint = fingerprint
		return Claim{Result: Started}, nil
	}
	if s.fingerprint != fingerprint {
		return Claim{Result: Mismatch}, nil
	}
	return Claim{Result: InFlight}, nil
}

func (s *legacyRetainingStore) Complete(_ context.Context, _, _, _ string, _ StoredResponse) error {
	return s.completeErr
}

func (s *legacyRetainingStore) Release(_ context.Context, _, _ string) error {
	s.releaseCalls.Add(1)
	s.mu.Lock()
	s.inFlight = false
	s.mu.Unlock()
	return nil
}

type failingCompleteLeaseStore struct {
	*MemoryStore
	releaseCalls atomic.Int32
}

type releaseContextKey struct{}

type releaseContextObservingStore struct {
	*MemoryStore
	mu          sync.Mutex
	called      int
	contextErr  error
	hasDeadline bool
	value       any
}

func (s *releaseContextObservingStore) ReleaseLease(ctx context.Context, tenantID, key string, token LeaseToken) error {
	_, hasDeadline := ctx.Deadline()
	s.mu.Lock()
	s.called++
	s.contextErr = ctx.Err()
	s.hasDeadline = hasDeadline
	s.value = ctx.Value(releaseContextKey{})
	s.mu.Unlock()
	return s.MemoryStore.ReleaseLease(ctx, tenantID, key, token)
}

func (s *releaseContextObservingStore) releaseObservation() (int, bool, any, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.called, s.hasDeadline, s.value, s.contextErr
}

func (s *failingCompleteLeaseStore) CompleteLease(context.Context, string, string, string, LeaseToken, StoredResponse) error {
	return ErrLeaseLost
}

func (s *failingCompleteLeaseStore) ReleaseLease(ctx context.Context, tenantID, key string, token LeaseToken) error {
	s.releaseCalls.Add(1)
	return s.MemoryStore.ReleaseLease(ctx, tenantID, key, token)
}

func keyedRequest(key, body string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/operation", strings.NewReader(body))
	req.Header.Set(DefaultHeader, key)
	return req
}

func assertProblem(t *testing.T, recorder *httptest.ResponseRecorder, status int, class string) {
	t.Helper()
	if recorder.Code != status {
		t.Fatalf("status = %d, want %d; body=%s", recorder.Code, status, recorder.Body.String())
	}
	if got := recorder.Header().Get("Content-Type"); got != "application/problem+json" {
		t.Fatalf("Content-Type = %q, want application/problem+json", got)
	}
	var problem struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &problem); err != nil {
		t.Fatalf("decode problem: %v; body=%s", err, recorder.Body.String())
	}
	if problem.Code != class {
		t.Fatalf("problem code = %q, want %q", problem.Code, class)
	}
}
