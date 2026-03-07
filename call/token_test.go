package call_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ai8future/chassis-go/v8/call"
)

func TestWithTokenSource(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(200)
	}))
	defer srv.Close()

	source := call.NewCachedToken(func(ctx context.Context) (string, time.Time, error) {
		return "test-token-123", time.Now().Add(1 * time.Hour), nil
	})

	client := call.New(call.WithTokenSource(source))
	req, _ := http.NewRequest("GET", srv.URL, nil)
	_, err := client.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	if gotAuth != "Bearer test-token-123" {
		t.Fatalf("expected Bearer test-token-123, got %q", gotAuth)
	}
}

func TestCachedTokenRefresh(t *testing.T) {
	var fetchCount atomic.Int32
	source := call.NewCachedToken(func(ctx context.Context) (string, time.Time, error) {
		n := fetchCount.Add(1)
		return fmt.Sprintf("token-%d", n), time.Now().Add(50 * time.Millisecond), nil
	}, call.Leeway(40*time.Millisecond))

	tok1, _ := source.Token(context.Background())
	if fetchCount.Load() != 1 {
		t.Fatalf("expected 1 fetch, got %d", fetchCount.Load())
	}

	tok2, _ := source.Token(context.Background())
	if fetchCount.Load() != 1 {
		t.Fatalf("expected still 1 fetch, got %d", fetchCount.Load())
	}
	if tok1 != tok2 {
		t.Fatal("expected same token from cache")
	}

	time.Sleep(20 * time.Millisecond)
	tok3, _ := source.Token(context.Background())
	if fetchCount.Load() != 2 {
		t.Fatalf("expected 2 fetches after leeway, got %d", fetchCount.Load())
	}
	if tok3 == tok1 {
		t.Fatal("expected new token after refresh")
	}
}
