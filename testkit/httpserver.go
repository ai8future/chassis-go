package testkit

import (
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

// RecordedRequest captures the essential parts of an incoming HTTP request
// for later assertion in tests.
type RecordedRequest struct {
	Method  string
	Path    string
	Headers http.Header
	Body    []byte
}

// MockServer wraps httptest.Server with automatic request recording.
type MockServer struct {
	*httptest.Server
	mu       sync.Mutex
	requests []RecordedRequest
}

// Requests returns a snapshot of all requests received so far.
func (s *MockServer) Requests() []RecordedRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := make([]RecordedRequest, len(s.requests))
	copy(cp, s.requests)
	return cp
}

// NewHTTPServer starts an httptest.Server that records every request before
// delegating to handler. The server is automatically closed via t.Cleanup.
func NewHTTPServer(t testing.TB, handler http.Handler) *MockServer {
	t.Helper()
	ms := &MockServer{}
	ms.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		ms.mu.Lock()
		ms.requests = append(ms.requests, RecordedRequest{
			Method:  r.Method,
			Path:    r.URL.Path,
			Headers: r.Header.Clone(),
			Body:    body,
		})
		ms.mu.Unlock()
		handler.ServeHTTP(w, r)
	}))
	t.Cleanup(ms.Close)
	return ms
}

// Respond returns an http.Handler that always replies with the given status
// code and body string, setting Content-Type to application/json.
func Respond(status int, body string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		w.Write([]byte(body))
	})
}

// Sequence returns an http.Handler that serves responses from a list of
// handlers in order, repeating the last handler for any extra requests.
func Sequence(handlers ...http.Handler) http.Handler {
	var mu sync.Mutex
	idx := 0
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		h := handlers[idx]
		if idx < len(handlers)-1 {
			idx++
		}
		mu.Unlock()
		h.ServeHTTP(w, r)
	})
}
