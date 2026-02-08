package guard

import (
	"context"
	"log/slog"
	"net/http"
	"runtime/debug"
	"sync"
	"time"

	chassis "github.com/ai8future/chassis-go/v5"
	"github.com/ai8future/chassis-go/v5/errors"
)

// Timeout returns middleware that sets a context deadline on the request and
// actively returns 504 Gateway Timeout if the handler does not complete before
// the deadline fires. If the caller already set a tighter deadline, the tighter
// deadline wins and no new deadline is applied.
func Timeout(d time.Duration) func(http.Handler) http.Handler {
	chassis.AssertVersionChecked()
	if d <= 0 {
		panic("guard: Timeout duration must be > 0")
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := r.Context()
			if _, ok := ctx.Deadline(); ok {
				// Caller already set a deadline — respect it, don't override.
				next.ServeHTTP(w, r)
				return
			}

			ctx, cancel := context.WithTimeout(ctx, d)
			defer cancel()
			r = r.WithContext(ctx)

			done := make(chan struct{})
			panicChan := make(chan any, 1)
			tw := &timeoutWriter{w: w, req: r}
			go func() {
				defer func() {
					if p := recover(); p != nil {
						slog.Error("guard: panic in handler behind Timeout middleware",
							"error", p,
							"stack", string(debug.Stack()),
						)
						panicChan <- p
					}
				}()
				next.ServeHTTP(tw, r)
				close(done)
			}()

			select {
			case p := <-panicChan:
				// Re-panic on the original goroutine so Recovery middleware can catch it.
				panic(p)
			case <-done:
				// Handler finished in time — flush any buffered response.
				tw.flush()
			case <-ctx.Done():
				// Deadline exceeded — write 504 if handler hasn't started writing.
				// The goroutine may still be running but its context is cancelled;
				// well-behaved handlers will return promptly. This matches the
				// behavior of Go's stdlib http.TimeoutHandler.
				tw.timeout()
			}
		})
	}
}

// timeoutWriter buffers the response until we know whether the handler
// finished in time or the deadline fired. This prevents partial writes.
type timeoutWriter struct {
	w   http.ResponseWriter
	req *http.Request

	mu      sync.Mutex
	code    int
	headers http.Header
	buf     []byte
	written bool // true once flush() or timeout() has been called
	started bool // true once handler called WriteHeader or Write
}

func (tw *timeoutWriter) Header() http.Header {
	tw.mu.Lock()
	defer tw.mu.Unlock()
	if tw.headers == nil {
		tw.headers = make(http.Header)
	}
	return tw.headers
}

func (tw *timeoutWriter) WriteHeader(code int) {
	tw.mu.Lock()
	defer tw.mu.Unlock()
	if tw.started {
		return
	}
	tw.started = true
	tw.code = code
}

func (tw *timeoutWriter) Write(b []byte) (int, error) {
	tw.mu.Lock()
	defer tw.mu.Unlock()
	if !tw.started {
		tw.started = true
		tw.code = http.StatusOK
	}
	tw.buf = append(tw.buf, b...)
	return len(b), nil
}

// flush writes the buffered response to the real ResponseWriter.
func (tw *timeoutWriter) flush() {
	tw.mu.Lock()
	defer tw.mu.Unlock()
	if tw.written {
		return
	}
	tw.written = true
	for k, vs := range tw.headers {
		for _, v := range vs {
			tw.w.Header().Add(k, v)
		}
	}
	if tw.code > 0 {
		tw.w.WriteHeader(tw.code)
	}
	if len(tw.buf) > 0 {
		if _, err := tw.w.Write(tw.buf); err != nil {
			slog.Error("guard: timeout flush write failed", "error", err)
		}
	}
}

// timeout writes 504 with RFC 9457 Problem Details if the handler hasn't started writing yet.
func (tw *timeoutWriter) timeout() {
	tw.mu.Lock()
	defer tw.mu.Unlock()
	if tw.written || tw.started {
		return
	}
	tw.written = true
	writeProblem(tw.w, tw.req, errors.TimeoutError("request timed out"))
}
