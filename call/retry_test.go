package call

import (
	"context"
	"errors"
	"io"
	"net/http"
	"sync/atomic"
	"testing"
	"time"
)

type trackingBody struct {
	closed *atomic.Int32
}

func (tb *trackingBody) Read(p []byte) (int, error) {
	return 0, io.EOF
}

func (tb *trackingBody) Close() error {
	tb.closed.Add(1)
	return nil
}

func TestRetrier_RetriesOnNetworkErrorAndClosesBody(t *testing.T) {
	var attempts atomic.Int32
	var closed atomic.Int32

	r := &Retrier{MaxAttempts: 3, BaseDelay: 1 * time.Millisecond}
	_, err := r.Do(context.Background(), func() (*http.Response, error) {
		attempts.Add(1)
		resp := &http.Response{
			StatusCode: http.StatusBadGateway,
			Body:       &trackingBody{closed: &closed},
		}
		return resp, errors.New("network down")
	})

	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if attempts.Load() != 3 {
		t.Fatalf("attempts = %d, want 3", attempts.Load())
	}
	// All response bodies from failed attempts should be closed.
	if closed.Load() != attempts.Load() {
		t.Fatalf("closed = %d, want %d", closed.Load(), attempts.Load())
	}
}

func TestRetrier_BackoffHonorsContextCancel(t *testing.T) {
	r := &Retrier{BaseDelay: 200 * time.Millisecond}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	start := time.Now()
	err := r.backoff(ctx, 0)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
	if time.Since(start) > 100*time.Millisecond {
		t.Fatalf("backoff returned too slowly after cancel")
	}
}
