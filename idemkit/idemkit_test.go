package idemkit

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	chassis "github.com/ai8future/chassis-go/v11"
)

func TestMain(m *testing.M) {
	chassis.RequireMajor(11)
	os.Exit(m.Run())
}

func TestReplayAndMismatch(t *testing.T) {
	store := NewMemoryStore(time.Hour)
	var hits atomic.Int32
	h := Middleware(store)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		w.Write([]byte(`{"message_id":"msg_123"}`))
	}))

	first := httptest.NewRequest(http.MethodPost, "/send", strings.NewReader(`{"a":1}`))
	first.Header.Set(DefaultHeader, "idem_v1_example")
	firstRec := httptest.NewRecorder()
	h.ServeHTTP(firstRec, first)
	if firstRec.Code != http.StatusAccepted {
		t.Fatalf("first status = %d", firstRec.Code)
	}

	replayReq := httptest.NewRequest(http.MethodPost, "/send", strings.NewReader(`{"a":1}`))
	replayReq.Header.Set(DefaultHeader, "idem_v1_example")
	replayRec := httptest.NewRecorder()
	h.ServeHTTP(replayRec, replayReq)
	if replayRec.Code != http.StatusAccepted || replayRec.Header().Get(ReplayHeader) != "true" {
		t.Fatalf("replay status/header = %d/%q", replayRec.Code, replayRec.Header().Get(ReplayHeader))
	}
	if replayRec.Body.String() != firstRec.Body.String() {
		t.Fatalf("replay body = %q, want %q", replayRec.Body.String(), firstRec.Body.String())
	}

	mismatchReq := httptest.NewRequest(http.MethodPost, "/send", strings.NewReader(`{"a":2}`))
	mismatchReq.Header.Set(DefaultHeader, "idem_v1_example")
	mismatchRec := httptest.NewRecorder()
	h.ServeHTTP(mismatchRec, mismatchReq)
	if mismatchRec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("mismatch status = %d, want 422", mismatchRec.Code)
	}
	var problem map[string]any
	if err := json.NewDecoder(mismatchRec.Body).Decode(&problem); err != nil {
		t.Fatalf("decode problem: %v", err)
	}
	if problem["code"] != classMismatch {
		t.Fatalf("problem code = %v, want %s", problem["code"], classMismatch)
	}
	if hits.Load() != 1 {
		t.Fatalf("handler hits = %d, want 1", hits.Load())
	}
}

func TestInFlightDuplicate(t *testing.T) {
	store := NewMemoryStore(time.Hour)
	entered := make(chan struct{})
	release := make(chan struct{})
	h := Middleware(store)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-release
		w.WriteHeader(http.StatusAccepted)
	}))
	go func() {
		req := httptest.NewRequest(http.MethodPost, "/send", strings.NewReader(`{"a":1}`))
		req.Header.Set(DefaultHeader, "inflight")
		h.ServeHTTP(httptest.NewRecorder(), req)
	}()
	<-entered
	dup := httptest.NewRequest(http.MethodPost, "/send", strings.NewReader(`{"a":1}`))
	dup.Header.Set(DefaultHeader, "inflight")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, dup)
	close(release)
	if rec.Code != http.StatusConflict {
		t.Fatalf("duplicate status = %d, want 409", rec.Code)
	}
}

func Test5xxReleasesClaim(t *testing.T) {
	store := NewMemoryStore(time.Hour)
	var hits atomic.Int32
	h := Middleware(store)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hits.Add(1) == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusAccepted)
		w.Write([]byte("ok"))
	}))
	for i, want := range []int{http.StatusServiceUnavailable, http.StatusAccepted} {
		req := httptest.NewRequest(http.MethodPost, "/send", strings.NewReader(`{"a":1}`))
		req.Header.Set(DefaultHeader, "release-on-5xx")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != want {
			t.Fatalf("attempt %d status = %d, want %d", i+1, rec.Code, want)
		}
	}
	if hits.Load() != 2 {
		t.Fatalf("hits = %d, want 2", hits.Load())
	}
}

func TestMethodPassThroughAndCrossTenantIsolation(t *testing.T) {
	fixturePath := filepath.Join("..", "testdata", "windmill", "contracts", "fixtures", "idempotency-cross-tenant.json")
	if _, err := os.Stat(fixturePath); err != nil {
		t.Fatalf("missing pinned cross-tenant fixture: %v", err)
	}
	store := NewMemoryStore(time.Hour)
	var hits atomic.Int32
	h := Middleware(store, WithTenantResolver(func(r *http.Request) string { return r.Header.Get("X-Tenant-ID") }))(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hit := hits.Add(1)
		w.WriteHeader(http.StatusAccepted)
		if r.Method == http.MethodGet {
			w.Write([]byte("get"))
			return
		}
		w.Write([]byte(r.Header.Get("X-Tenant-ID") + ":" + string(rune('A'+hit-1))))
	}))

	getReq := httptest.NewRequest(http.MethodGet, "/send", nil)
	getReq.Header.Set(DefaultHeader, "ignored-on-get")
	getRec := httptest.NewRecorder()
	h.ServeHTTP(getRec, getReq)
	if getRec.Header().Get(ReplayHeader) != "" {
		t.Fatalf("GET should pass through without replay header")
	}

	for _, tenant := range []string{"tenant_a", "tenant_b"} {
		req := httptest.NewRequest(http.MethodPost, "/send", strings.NewReader(`{"a":1}`))
		req.Header.Set(DefaultHeader, "shared")
		req.Header.Set("X-Tenant-ID", tenant)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusAccepted || rec.Header().Get(ReplayHeader) != "" {
			t.Fatalf("tenant %s status/replay = %d/%q", tenant, rec.Code, rec.Header().Get(ReplayHeader))
		}
		if !strings.HasPrefix(rec.Body.String(), tenant+":") {
			t.Fatalf("tenant %s body = %q", tenant, rec.Body.String())
		}
	}
}

func TestMemoryStoreLegacyMethodsCompleteReplayAndRelease(t *testing.T) {
	store := NewMemoryStore(time.Hour)
	ctx := context.Background()
	claim, err := store.Begin(ctx, "tenant", "key", "fingerprint")
	if err != nil || claim.Result != Started {
		t.Fatalf("Begin = %#v, %v", claim, err)
	}
	response := StoredResponse{StatusCode: http.StatusCreated, Header: http.Header{"X-Test": {"value"}}, Body: []byte("created")}
	if err := store.Complete(ctx, "tenant", "key", "fingerprint", response); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	claim, err = store.Begin(ctx, "tenant", "key", "fingerprint")
	if err != nil || claim.Result != Replay || claim.Response == nil || string(claim.Response.Body) != "created" {
		t.Fatalf("replay Begin = %#v, %v", claim, err)
	}
	if claim.Response.Fingerprint != "fingerprint" || claim.Response.CreatedAt.IsZero() {
		t.Fatalf("prepared response = %#v", claim.Response)
	}
	if err := store.Release(ctx, "tenant", "key"); err != nil {
		t.Fatalf("Release: %v", err)
	}
	claim, err = store.Begin(ctx, "tenant", "key", "fingerprint")
	if err != nil || claim.Result != Started {
		t.Fatalf("Begin after Release = %#v, %v", claim, err)
	}
}

func TestMemoryStoreLegacyMethodsRespectCancellation(t *testing.T) {
	store := NewMemoryStore(time.Hour)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := store.Begin(ctx, "tenant", "key", "fingerprint"); !errors.Is(err, context.Canceled) {
		t.Fatalf("Begin error = %v", err)
	}
	if err := store.Complete(ctx, "tenant", "key", "fingerprint", StoredResponse{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("Complete error = %v", err)
	}
	if err := store.Release(ctx, "tenant", "key"); !errors.Is(err, context.Canceled) {
		t.Fatalf("Release error = %v", err)
	}
}

func TestMemoryStoreLegacyCompleteHonorsCapacity(t *testing.T) {
	store := NewMemoryStoreWithCapacity(time.Hour, 1)
	if err := store.Complete(context.Background(), "tenant", "first", "fingerprint", StoredResponse{}); err != nil {
		t.Fatal(err)
	}
	if err := store.Complete(context.Background(), "tenant", "second", "fingerprint", StoredResponse{}); !errors.Is(err, ErrCapacity) {
		t.Fatalf("Complete error = %v, want ErrCapacity", err)
	}
}
