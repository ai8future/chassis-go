// Package idemkit provides tenant-scoped HTTP idempotency middleware.
//
// The middleware suppresses duplicate HTTP responses only after the configured
// Store confirms persistence. It cannot make a handler's business side effects
// exactly once by itself. Stronger guarantees require service-owned
// transactional storage or an outbox that atomically couples the business
// mutation to the idempotency record.
package idemkit

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	stderrors "errors"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"time"

	chassis "github.com/ai8future/chassis-go/v11"
	chassiserrors "github.com/ai8future/chassis-go/v11/errors"
	"google.golang.org/grpc/codes"
)

const (
	DefaultHeader           = "Idempotency-Key"
	ReplayHeader            = "Idempotency-Replayed"
	DefaultMemoryTTL        = 24 * time.Hour
	DefaultMemoryCapacity   = 10_000
	DefaultMaxRequestBytes  = int64(1 << 20)
	DefaultMaxResponseBytes = int64(1 << 20)
	claimReleaseTimeout     = 2 * time.Second
	classMismatch           = "idempotency_fingerprint_mismatch"
	classInFlight           = "idempotency_in_flight"
	classStoreError         = "idempotency_store_error"
	classResponseTooLarge   = "idempotency_response_too_large"
)

var (
	// ErrLeaseLost means a token-aware operation no longer owns the claim.
	ErrLeaseLost = stderrors.New("idemkit: lease ownership lost")
	// ErrCapacity means the store cannot admit another live claim.
	ErrCapacity = stderrors.New("idemkit: store capacity reached")
)

// TenantResolver maps a request to its tenant namespace. The default resolver
// returns an empty string for single-tenant services.
type TenantResolver func(*http.Request) string

// StoredResponse is replayed for matching completed idempotency claims.
type StoredResponse struct {
	StatusCode  int
	Header      http.Header
	Body        []byte
	Fingerprint string
	CreatedAt   time.Time
}

// BeginResult describes the state returned by Store.Begin.
type BeginResult int

const (
	Started BeginResult = iota
	Replay
	InFlight
	Mismatch
)

// Claim is returned by Store.Begin.
type Claim struct {
	Result   BeginResult
	Response *StoredResponse
}

// Store is the source-compatible, tenant-aware idempotency storage contract.
//
// If Complete returns an error, the result is ambiguous. Implementations that
// require duplicate suppression must retain the claim until store-owned expiry
// or operator reconciliation; middleware deliberately does not call Release.
type Store interface {
	Begin(ctx context.Context, tenantID, key, fingerprint string) (Claim, error)
	Complete(ctx context.Context, tenantID, key, fingerprint string, response StoredResponse) error
	Release(ctx context.Context, tenantID, key string) error
}

// LeaseToken is an opaque claim-ownership token.
type LeaseToken string

// LeaseClaim augments a Claim with ownership for a newly started request.
type LeaseClaim struct {
	Claim
	Token LeaseToken
}

// LeaseStore is an optional token-aware extension to Store. It lets middleware
// release or complete only the claim it owns, preventing stale-owner ABA
// mutations after expiry and replacement.
type LeaseStore interface {
	Store
	BeginLease(context.Context, string, string, string) (LeaseClaim, error)
	CompleteLease(context.Context, string, string, string, LeaseToken, StoredResponse) error
	ReleaseLease(context.Context, string, string, LeaseToken) error
}

type options struct {
	header           string
	tenantResolver   TenantResolver
	maxRequestBytes  int64
	maxResponseBytes int64
}

// Option configures middleware.
type Option func(*options)

// WithHeader changes the request header used for the idempotency key.
func WithHeader(name string) Option {
	chassis.AssertVersionChecked()
	return func(o *options) {
		if name != "" {
			o.header = name
		}
	}
}

// WithTenantResolver configures tenant extraction. Nil restores single-tenant mode.
func WithTenantResolver(resolver TenantResolver) Option {
	chassis.AssertVersionChecked()
	return func(o *options) {
		if resolver == nil {
			o.tenantResolver = defaultTenantResolver
			return
		}
		o.tenantResolver = resolver
	}
}

// WithRequestBodyLimit bounds keyed request bodies retained for fingerprinting.
// Non-positive values restore DefaultMaxRequestBytes.
func WithRequestBodyLimit(maxBytes int64) Option {
	chassis.AssertVersionChecked()
	return func(o *options) {
		o.maxRequestBytes = normalizedLimit(maxBytes, DefaultMaxRequestBytes)
	}
}

// WithResponseBodyLimit bounds keyed handler responses buffered before Store
// persistence. Keyed endpoints are therefore non-streaming. Non-positive values
// restore DefaultMaxResponseBytes.
func WithResponseBodyLimit(maxBytes int64) Option {
	chassis.AssertVersionChecked()
	return func(o *options) {
		o.maxResponseBytes = normalizedLimit(maxBytes, DefaultMaxResponseBytes)
	}
}

// Middleware returns HTTP middleware that provides idempotency replay for
// mutating requests carrying an idempotency key. A successful keyed handler
// response is buffered and becomes visible only after Store persistence.
func Middleware(store Store, opts ...Option) func(http.Handler) http.Handler {
	chassis.AssertVersionChecked()
	cfg := options{
		header:           DefaultHeader,
		tenantResolver:   defaultTenantResolver,
		maxRequestBytes:  DefaultMaxRequestBytes,
		maxResponseBytes: DefaultMaxResponseBytes,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(&cfg)
		}
	}
	if cfg.tenantResolver == nil {
		cfg.tenantResolver = defaultTenantResolver
	}
	cfg.maxRequestBytes = normalizedLimit(cfg.maxRequestBytes, DefaultMaxRequestBytes)
	cfg.maxResponseBytes = normalizedLimit(cfg.maxResponseBytes, DefaultMaxResponseBytes)

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !isMutating(r.Method) {
				next.ServeHTTP(w, r)
				return
			}
			key := r.Header.Get(cfg.header)
			if key == "" {
				next.ServeHTTP(w, r)
				return
			}
			if store == nil {
				writeProblem(w, r, http.StatusInternalServerError, codes.Internal, "idempotency store is not configured", classStoreError, 0)
				return
			}

			body, err := readBody(r, cfg.maxRequestBytes)
			if stderrors.Is(err, errBodyTooLarge) {
				writeProblem(w, r, http.StatusRequestEntityTooLarge, codes.InvalidArgument, "idempotency request body is too large", chassiserrors.ClassPayloadTooLarge, 0)
				return
			}
			if err != nil {
				writeProblem(w, r, http.StatusBadRequest, codes.InvalidArgument, "failed to read request body", chassiserrors.ClassValidation, 0)
				return
			}

			fingerprint := Fingerprint(r.Method, r.URL.RequestURI(), body)
			tenantID := cfg.tenantResolver(r)
			claim, owner, leaseStore, err := beginClaim(r.Context(), store, tenantID, key, fingerprint)
			if err != nil {
				writeProblem(w, r, http.StatusServiceUnavailable, codes.Unavailable, "idempotency store error", classStoreError, time.Second)
				return
			}
			switch claim.Result {
			case Replay:
				replay(w, claim.Response)
				return
			case InFlight:
				writeProblem(w, r, http.StatusConflict, codes.Aborted, "idempotency key is already in flight", classInFlight, time.Second)
				return
			case Mismatch:
				writeProblem(w, r, http.StatusUnprocessableEntity, codes.InvalidArgument, "idempotency key fingerprint mismatch", classMismatch, 0)
				return
			case Started:
				// Continue below.
			default:
				writeProblem(w, r, http.StatusServiceUnavailable, codes.Unavailable, "idempotency store returned an invalid claim", classStoreError, time.Second)
				return
			}

			rec := newCaptureResponse(w.Header(), cfg.maxResponseBytes)
			func() {
				defer func() {
					if recovered := recover(); recovered != nil {
						if releaseErr := releaseClaim(r.Context(), store, leaseStore, tenantID, key, owner); releaseErr != nil {
							logReleaseFailure(releaseErr, "panic_release")
						}
						panic(recovered)
					}
				}()
				next.ServeHTTP(rec, r)
			}()

			if rec.overflowed() {
				if releaseErr := releaseClaim(r.Context(), store, leaseStore, tenantID, key, owner); releaseErr != nil {
					logReleaseFailure(releaseErr, "oversize_release")
				}
				writeProblem(w, r, http.StatusInternalServerError, codes.Internal, "idempotency response body is too large", classResponseTooLarge, 0)
				return
			}

			status := rec.statusCode()
			if status >= http.StatusInternalServerError {
				if releaseErr := releaseClaim(r.Context(), store, leaseStore, tenantID, key, owner); releaseErr != nil {
					logReleaseFailure(releaseErr, "handler_error_release")
				}
				rec.flushTo(w)
				return
			}

			response := StoredResponse{
				StatusCode:  status,
				Header:      rec.headerSnapshot(),
				Body:        rec.bodySnapshot(),
				Fingerprint: fingerprint,
				CreatedAt:   time.Now().UTC(),
			}
			if err := completeClaim(r.Context(), store, leaseStore, tenantID, key, fingerprint, owner, response); err != nil {
				writeProblem(w, r, http.StatusServiceUnavailable, codes.Unavailable, "idempotency store error", classStoreError, time.Second)
				return
			}
			rec.flushTo(w)
		})
	}
}

func defaultTenantResolver(*http.Request) string { return "" }

func normalizedLimit(value, fallback int64) int64 {
	if value <= 0 || value == int64(^uint64(0)>>1) {
		return fallback
	}
	return value
}

func beginClaim(ctx context.Context, store Store, tenantID, key, fingerprint string) (Claim, LeaseToken, LeaseStore, error) {
	leaseStore, ok := store.(LeaseStore)
	if !ok {
		claim, err := store.Begin(ctx, tenantID, key, fingerprint)
		return claim, "", nil, err
	}
	claim, err := leaseStore.BeginLease(ctx, tenantID, key, fingerprint)
	return claim.Claim, claim.Token, leaseStore, err
}

func completeClaim(ctx context.Context, store Store, leaseStore LeaseStore, tenantID, key, fingerprint string, token LeaseToken, response StoredResponse) error {
	if leaseStore != nil {
		return leaseStore.CompleteLease(ctx, tenantID, key, fingerprint, token, response)
	}
	return store.Complete(ctx, tenantID, key, fingerprint, response)
}

func releaseClaim(ctx context.Context, store Store, leaseStore LeaseStore, tenantID, key string, token LeaseToken) error {
	releaseCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), claimReleaseTimeout)
	defer cancel()
	if leaseStore != nil {
		return leaseStore.ReleaseLease(releaseCtx, tenantID, key, token)
	}
	return store.Release(releaseCtx, tenantID, key)
}

func logReleaseFailure(err error, operation string) {
	errorClass := classStoreError
	if stderrors.Is(err, ErrLeaseLost) {
		errorClass = "idempotency_lease_lost"
	}
	slog.Error("idemkit: claim release failed", "operation", operation, "error_class", errorClass)
}

// Fingerprint returns a stable hash over method, target, and body bytes.
func Fingerprint(method, target string, body []byte) string {
	chassis.AssertVersionChecked()
	h := sha256.New()
	h.Write([]byte(method))
	h.Write([]byte("\n"))
	h.Write([]byte(target))
	h.Write([]byte("\n"))
	h.Write(body)
	return hex.EncodeToString(h.Sum(nil))
}

// MemoryStore is an in-memory tenant-aware LeaseStore implementation. Its
// capacity is global across tenant/key pairs and it never evicts live records.
type MemoryStore struct {
	mu       sync.Mutex
	ttl      time.Duration
	capacity int
	records  map[storeKey]*memoryRecord
}

type storeKey struct {
	tenantID string
	key      string
}

type memoryRecord struct {
	fingerprint string
	token       LeaseToken
	inFlight    bool
	response    *StoredResponse
	createdAt   time.Time
}

// NewMemoryStore creates a bounded tenant-scoped in-memory idempotency store.
func NewMemoryStore(ttl time.Duration) *MemoryStore {
	chassis.AssertVersionChecked()
	return newMemoryStore(ttl, DefaultMemoryCapacity)
}

// NewMemoryStoreWithCapacity creates a tenant-scoped store with a live-record
// bound. Non-positive capacity restores DefaultMemoryCapacity.
func NewMemoryStoreWithCapacity(ttl time.Duration, capacity int) *MemoryStore {
	chassis.AssertVersionChecked()
	if capacity <= 0 {
		capacity = DefaultMemoryCapacity
	}
	return newMemoryStore(ttl, capacity)
}

func newMemoryStore(ttl time.Duration, capacity int) *MemoryStore {
	if ttl <= 0 {
		ttl = DefaultMemoryTTL
	}
	return &MemoryStore{ttl: ttl, capacity: capacity, records: map[storeKey]*memoryRecord{}}
}

// Begin implements the legacy Store contract. Middleware uses BeginLease when
// the dynamic store implements LeaseStore.
func (s *MemoryStore) Begin(ctx context.Context, tenantID, key, fingerprint string) (Claim, error) {
	chassis.AssertVersionChecked()
	claim, err := s.BeginLease(ctx, tenantID, key, fingerprint)
	return claim.Claim, err
}

// BeginLease starts or observes a token-owned claim.
func (s *MemoryStore) BeginLease(ctx context.Context, tenantID, key, fingerprint string) (LeaseClaim, error) {
	chassis.AssertVersionChecked()
	if err := ctx.Err(); err != nil {
		return LeaseClaim{}, err
	}
	now := time.Now().UTC()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.purgeExpiredLocked(now)

	sk := storeKey{tenantID: tenantID, key: key}
	if rec, ok := s.records[sk]; ok {
		if rec.fingerprint != fingerprint {
			return LeaseClaim{Claim: Claim{Result: Mismatch}}, nil
		}
		if rec.inFlight {
			return LeaseClaim{Claim: Claim{Result: InFlight}}, nil
		}
		if rec.response != nil {
			response := cloneStoredResponse(*rec.response)
			return LeaseClaim{Claim: Claim{Result: Replay, Response: &response}}, nil
		}
	}
	if len(s.records) >= s.capacity {
		return LeaseClaim{}, ErrCapacity
	}
	token, err := randomLeaseToken()
	if err != nil {
		return LeaseClaim{}, err
	}
	s.records[sk] = &memoryRecord{fingerprint: fingerprint, token: token, inFlight: true, createdAt: now}
	return LeaseClaim{Claim: Claim{Result: Started}, Token: token}, nil
}

// Complete implements the legacy Store contract. It preserves key-only
// compatibility; token-aware callers should use CompleteLease.
func (s *MemoryStore) Complete(ctx context.Context, tenantID, key, fingerprint string, response StoredResponse) error {
	chassis.AssertVersionChecked()
	if err := ctx.Err(); err != nil {
		return err
	}
	now := time.Now().UTC()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.purgeExpiredLocked(now)
	sk := storeKey{tenantID: tenantID, key: key}
	if _, exists := s.records[sk]; !exists && len(s.records) >= s.capacity {
		return ErrCapacity
	}
	response = prepareStoredResponse(response, fingerprint, now)
	s.records[sk] = &memoryRecord{fingerprint: fingerprint, response: &response, createdAt: response.CreatedAt}
	return nil
}

// CompleteLease persists only if token still owns the live in-flight claim.
func (s *MemoryStore) CompleteLease(ctx context.Context, tenantID, key, fingerprint string, token LeaseToken, response StoredResponse) error {
	chassis.AssertVersionChecked()
	if err := ctx.Err(); err != nil {
		return err
	}
	now := time.Now().UTC()
	s.mu.Lock()
	defer s.mu.Unlock()
	sk := storeKey{tenantID: tenantID, key: key}
	rec, ok := s.records[sk]
	if !ok || s.expiredLocked(rec, now) {
		if ok {
			delete(s.records, sk)
		}
		return ErrLeaseLost
	}
	if !rec.inFlight || rec.token == "" || rec.token != token || rec.fingerprint != fingerprint {
		return ErrLeaseLost
	}
	response = prepareStoredResponse(response, fingerprint, now)
	s.records[sk] = &memoryRecord{fingerprint: fingerprint, response: &response, createdAt: response.CreatedAt}
	return nil
}

// Release implements the legacy key-only Store contract.
func (s *MemoryStore) Release(ctx context.Context, tenantID, key string) error {
	chassis.AssertVersionChecked()
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.records, storeKey{tenantID: tenantID, key: key})
	return nil
}

// ReleaseLease removes only the live claim owned by token.
func (s *MemoryStore) ReleaseLease(ctx context.Context, tenantID, key string, token LeaseToken) error {
	chassis.AssertVersionChecked()
	if err := ctx.Err(); err != nil {
		return err
	}
	now := time.Now().UTC()
	s.mu.Lock()
	defer s.mu.Unlock()
	sk := storeKey{tenantID: tenantID, key: key}
	rec, ok := s.records[sk]
	if !ok || s.expiredLocked(rec, now) {
		if ok {
			delete(s.records, sk)
		}
		return ErrLeaseLost
	}
	if !rec.inFlight || rec.token == "" || rec.token != token {
		return ErrLeaseLost
	}
	delete(s.records, sk)
	return nil
}

func (s *MemoryStore) purgeExpiredLocked(now time.Time) {
	for key, rec := range s.records {
		if s.expiredLocked(rec, now) {
			delete(s.records, key)
		}
	}
}

func (s *MemoryStore) expiredLocked(rec *memoryRecord, now time.Time) bool {
	return rec == nil || !now.Before(rec.createdAt.Add(s.ttl))
}

func randomLeaseToken() (LeaseToken, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return LeaseToken(base64.RawURLEncoding.EncodeToString(raw)), nil
}

func prepareStoredResponse(response StoredResponse, fingerprint string, now time.Time) StoredResponse {
	response.Fingerprint = fingerprint
	if response.CreatedAt.IsZero() {
		response.CreatedAt = now
	}
	return cloneStoredResponse(response)
}

func cloneStoredResponse(response StoredResponse) StoredResponse {
	response.Header = response.Header.Clone()
	response.Body = append([]byte(nil), response.Body...)
	return response
}

var errBodyTooLarge = stderrors.New("idemkit: body too large")

func readBody(r *http.Request, maxBytes int64) ([]byte, error) {
	if r.Body == nil {
		return nil, nil
	}
	defer r.Body.Close()
	body, err := io.ReadAll(io.LimitReader(r.Body, maxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > maxBytes {
		return nil, errBodyTooLarge
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	return body, nil
}

func isMutating(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	default:
		return false
	}
}

func replay(w http.ResponseWriter, response *StoredResponse) {
	if response == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	owned := cloneStoredResponse(*response)
	copyHeader(w.Header(), owned.Header)
	w.Header().Set(ReplayHeader, "true")
	status := owned.StatusCode
	if status == 0 {
		status = http.StatusOK
	}
	w.WriteHeader(status)
	_, _ = w.Write(owned.Body)
}

func writeProblem(w http.ResponseWriter, r *http.Request, status int, grpcCode codes.Code, message, class string, retryAfter time.Duration) {
	err := &chassiserrors.ServiceError{Message: message, HTTPCode: status, GRPCCode: grpcCode, Class: class}
	if retryAfter > 0 {
		err = err.WithRetryAfter(retryAfter)
	}
	chassiserrors.WriteProblem(w, r, err, "")
}

type captureResponse struct {
	header   http.Header
	body     bytes.Buffer
	status   int
	maxBytes int64
	overflow bool
}

func newCaptureResponse(initial http.Header, maxBytes int64) *captureResponse {
	return &captureResponse{header: initial.Clone(), maxBytes: maxBytes}
}

func (w *captureResponse) Header() http.Header { return w.header }

func (w *captureResponse) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
}

func (w *captureResponse) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	remaining := w.maxBytes - int64(w.body.Len())
	if remaining < int64(len(p)) {
		w.overflow = true
		if remaining > 0 {
			_, _ = w.body.Write(p[:int(remaining)])
		}
		return len(p), nil
	}
	_, _ = w.body.Write(p)
	return len(p), nil
}

func (w *captureResponse) statusCode() int {
	if w.status == 0 {
		return http.StatusOK
	}
	return w.status
}

func (w *captureResponse) overflowed() bool { return w.overflow }

func (w *captureResponse) headerSnapshot() http.Header { return w.header.Clone() }

func (w *captureResponse) bodySnapshot() []byte { return append([]byte(nil), w.body.Bytes()...) }

func (w *captureResponse) flushTo(dst http.ResponseWriter) {
	copyHeader(dst.Header(), w.header)
	dst.WriteHeader(w.statusCode())
	_, _ = dst.Write(w.body.Bytes())
}

func copyHeader(dst, src http.Header) {
	for key := range dst {
		delete(dst, key)
	}
	for key, values := range src {
		dst[key] = append([]string(nil), values...)
	}
}
