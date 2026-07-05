// Package idemkit provides tenant-scoped HTTP idempotency middleware.
package idemkit

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"sync"
	"time"

	chassis "github.com/ai8future/chassis-go/v11"
	chassiserrors "github.com/ai8future/chassis-go/v11/errors"
	"google.golang.org/grpc/codes"
)

const (
	DefaultHeader    = "Idempotency-Key"
	ReplayHeader     = "Idempotency-Replayed"
	DefaultMemoryTTL = 24 * time.Hour
	classMismatch    = "idempotency_fingerprint_mismatch"
	classInFlight    = "idempotency_in_flight"
	classStoreError  = "idempotency_store_error"
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

// Store is the tenant-aware idempotency storage contract.
type Store interface {
	Begin(ctx context.Context, tenantID, key, fingerprint string) (Claim, error)
	Complete(ctx context.Context, tenantID, key, fingerprint string, response StoredResponse) error
	Release(ctx context.Context, tenantID, key string) error
}

type options struct {
	header         string
	tenantResolver TenantResolver
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
	return func(o *options) { o.tenantResolver = resolver }
}

// Middleware returns HTTP middleware that provides idempotency replay for
// mutating requests carrying an idempotency key.
func Middleware(store Store, opts ...Option) func(http.Handler) http.Handler {
	chassis.AssertVersionChecked()
	cfg := options{header: DefaultHeader, tenantResolver: func(*http.Request) string { return "" }}
	for _, opt := range opts {
		opt(&cfg)
	}
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
				writeProblem(w, r, http.StatusInternalServerError, codes.Internal, "idempotency store is not configured", classStoreError)
				return
			}
			body, err := readBody(r)
			if err != nil {
				writeProblem(w, r, http.StatusBadRequest, codes.InvalidArgument, "failed to read request body", chassiserrors.ClassValidation)
				return
			}
			fingerprint := Fingerprint(r.Method, r.URL.RequestURI(), body)
			tenantID := cfg.tenantResolver(r)
			claim, err := store.Begin(r.Context(), tenantID, key, fingerprint)
			if err != nil {
				writeProblem(w, r, http.StatusInternalServerError, codes.Internal, "idempotency store error", classStoreError)
				return
			}
			switch claim.Result {
			case Replay:
				replay(w, claim.Response)
				return
			case InFlight:
				writeProblem(w, r, http.StatusConflict, codes.Aborted, "idempotency key is already in flight", classInFlight)
				return
			case Mismatch:
				writeProblem(w, r, http.StatusUnprocessableEntity, codes.InvalidArgument, "idempotency key fingerprint mismatch", classMismatch)
				return
			}

			rec := newCaptureResponse(w)
			next.ServeHTTP(rec, r)
			status := rec.statusCode()
			if status >= 500 {
				_ = store.Release(r.Context(), tenantID, key)
				return
			}
			_ = store.Complete(r.Context(), tenantID, key, fingerprint, StoredResponse{
				StatusCode:  status,
				Header:      rec.headerSnapshot(),
				Body:        rec.body.Bytes(),
				Fingerprint: fingerprint,
				CreatedAt:   time.Now().UTC(),
			})
		})
	}
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

// MemoryStore is an in-memory tenant-aware Store implementation.
type MemoryStore struct {
	mu      sync.Mutex
	ttl     time.Duration
	records map[storeKey]*memoryRecord
}

type storeKey struct {
	tenantID string
	key      string
}

type memoryRecord struct {
	fingerprint string
	inFlight    bool
	response    *StoredResponse
	createdAt   time.Time
}

// NewMemoryStore creates a tenant-scoped in-memory idempotency store.
func NewMemoryStore(ttl time.Duration) *MemoryStore {
	chassis.AssertVersionChecked()
	if ttl <= 0 {
		ttl = DefaultMemoryTTL
	}
	return &MemoryStore{ttl: ttl, records: map[storeKey]*memoryRecord{}}
}

func (s *MemoryStore) Begin(ctx context.Context, tenantID, key, fingerprint string) (Claim, error) {
	chassis.AssertVersionChecked()
	_ = ctx
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC()
	sk := storeKey{tenantID: tenantID, key: key}
	if rec, ok := s.records[sk]; ok {
		if rec.response != nil && now.Sub(rec.createdAt) > s.ttl {
			delete(s.records, sk)
		} else if rec.fingerprint != fingerprint {
			return Claim{Result: Mismatch}, nil
		} else if rec.inFlight {
			return Claim{Result: InFlight}, nil
		} else if rec.response != nil {
			resp := *rec.response
			resp.Body = append([]byte(nil), rec.response.Body...)
			resp.Header = rec.response.Header.Clone()
			return Claim{Result: Replay, Response: &resp}, nil
		}
	}
	s.records[sk] = &memoryRecord{fingerprint: fingerprint, inFlight: true, createdAt: now}
	return Claim{Result: Started}, nil
}

func (s *MemoryStore) Complete(ctx context.Context, tenantID, key, fingerprint string, response StoredResponse) error {
	chassis.AssertVersionChecked()
	_ = ctx
	s.mu.Lock()
	defer s.mu.Unlock()
	response.Fingerprint = fingerprint
	if response.CreatedAt.IsZero() {
		response.CreatedAt = time.Now().UTC()
	}
	response.Header = response.Header.Clone()
	response.Body = append([]byte(nil), response.Body...)
	s.records[storeKey{tenantID: tenantID, key: key}] = &memoryRecord{fingerprint: fingerprint, response: &response, createdAt: response.CreatedAt}
	return nil
}

func (s *MemoryStore) Release(ctx context.Context, tenantID, key string) error {
	chassis.AssertVersionChecked()
	_ = ctx
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.records, storeKey{tenantID: tenantID, key: key})
	return nil
}

func readBody(r *http.Request) ([]byte, error) {
	if r.Body == nil {
		return nil, nil
	}
	body := new(bytes.Buffer)
	_, err := body.ReadFrom(r.Body)
	if err != nil {
		return nil, err
	}
	r.Body.Close()
	r.Body = ioNopCloser{bytes.NewReader(body.Bytes())}
	return body.Bytes(), nil
}

type ioNopCloser struct{ *bytes.Reader }

func (c ioNopCloser) Close() error { return nil }

func isMutating(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	default:
		return false
	}
}

func replay(w http.ResponseWriter, resp *StoredResponse) {
	if resp == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	for key, values := range resp.Header {
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}
	w.Header().Set(ReplayHeader, "true")
	status := resp.StatusCode
	if status == 0 {
		status = http.StatusOK
	}
	w.WriteHeader(status)
	w.Write(resp.Body)
}

func writeProblem(w http.ResponseWriter, r *http.Request, status int, grpcCode codes.Code, message, class string) {
	chassiserrors.WriteProblem(w, r, &chassiserrors.ServiceError{Message: message, HTTPCode: status, GRPCCode: grpcCode, Class: class}, "")
}

type captureResponse struct {
	http.ResponseWriter
	body   bytes.Buffer
	status int
}

func newCaptureResponse(w http.ResponseWriter) *captureResponse {
	return &captureResponse{ResponseWriter: w}
}

func (w *captureResponse) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
		w.ResponseWriter.WriteHeader(status)
	}
}

func (w *captureResponse) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	w.body.Write(b)
	return w.ResponseWriter.Write(b)
}

func (w *captureResponse) statusCode() int {
	if w.status == 0 {
		return http.StatusOK
	}
	return w.status
}

func (w *captureResponse) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

func (w *captureResponse) headerSnapshot() http.Header {
	return w.ResponseWriter.Header().Clone()
}
