package cache_test

import (
	"fmt"
	"sync"
	"testing"
	"time"

	chassis "github.com/ai8future/chassis-go/v10"
	"github.com/ai8future/chassis-go/v10/cache"
)

func init() { chassis.RequireMajor(10) }

func TestSetGet(t *testing.T) {
	c := cache.New[string, string](cache.MaxSize(10))
	c.Set("key", "value")
	got, ok := c.Get("key")
	if !ok || got != "value" {
		t.Fatalf("expected (value, true), got (%q, %v)", got, ok)
	}
}

func TestGetMiss(t *testing.T) {
	c := cache.New[string, int](cache.MaxSize(10))
	_, ok := c.Get("missing")
	if ok {
		t.Fatal("expected miss")
	}
}

func TestDelete(t *testing.T) {
	c := cache.New[string, string](cache.MaxSize(10))
	c.Set("key", "value")
	c.Delete("key")
	_, ok := c.Get("key")
	if ok {
		t.Fatal("expected miss after delete")
	}
}

func TestLRUEviction(t *testing.T) {
	c := cache.New[string, int](cache.MaxSize(3))
	c.Set("a", 1)
	c.Set("b", 2)
	c.Set("c", 3)
	c.Set("d", 4)
	_, ok := c.Get("a")
	if ok {
		t.Fatal("expected 'a' evicted")
	}
	if c.Len() != 3 {
		t.Fatalf("expected len=3, got %d", c.Len())
	}
}

func TestLRUPromotion(t *testing.T) {
	c := cache.New[string, int](cache.MaxSize(3))
	c.Set("a", 1)
	c.Set("b", 2)
	c.Set("c", 3)
	c.Get("a")
	c.Set("d", 4)
	_, ok := c.Get("b")
	if ok {
		t.Fatal("expected 'b' evicted after 'a' was promoted")
	}
	_, ok = c.Get("a")
	if !ok {
		t.Fatal("expected 'a' still present")
	}
}

func TestTTLExpiry(t *testing.T) {
	c := cache.New[string, string](cache.MaxSize(10), cache.TTL(50*time.Millisecond))
	c.Set("key", "value")
	got, ok := c.Get("key")
	if !ok || got != "value" {
		t.Fatal("expected hit before TTL")
	}
	time.Sleep(60 * time.Millisecond)
	_, ok = c.Get("key")
	if ok {
		t.Fatal("expected miss after TTL")
	}
}

func TestPrune(t *testing.T) {
	c := cache.New[string, string](cache.MaxSize(10), cache.TTL(10*time.Millisecond))
	c.Set("a", "1")
	c.Set("b", "2")
	time.Sleep(20 * time.Millisecond)
	removed := c.Prune()
	if removed != 2 {
		t.Fatalf("expected 2 pruned, got %d", removed)
	}
	if c.Len() != 0 {
		t.Fatalf("expected empty cache after prune, got %d", c.Len())
	}
}

func TestLen(t *testing.T) {
	c := cache.New[string, int](cache.MaxSize(10))
	if c.Len() != 0 {
		t.Fatal("expected len=0")
	}
	c.Set("a", 1)
	c.Set("b", 2)
	if c.Len() != 2 {
		t.Fatalf("expected len=2, got %d", c.Len())
	}
}

func TestSetOverwrite(t *testing.T) {
	c := cache.New[string, string](cache.MaxSize(10))
	c.Set("key", "v1")
	c.Set("key", "v2")
	got, _ := c.Get("key")
	if got != "v2" {
		t.Fatalf("expected v2, got %q", got)
	}
	if c.Len() != 1 {
		t.Fatalf("expected len=1, got %d", c.Len())
	}
}

func TestCacheConcurrentAccess(t *testing.T) {
	c := cache.New[string, int](cache.MaxSize(100), cache.TTL(time.Minute))

	var wg sync.WaitGroup
	// Writers
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				key := fmt.Sprintf("key-%d-%d", n, j)
				c.Set(key, n*100+j)
			}
		}(i)
	}
	// Readers
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				key := fmt.Sprintf("key-%d-%d", n, j%50)
				c.Get(key)
			}
		}(i)
	}
	// Deleters
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				key := fmt.Sprintf("key-%d-%d", n, j)
				c.Delete(key)
			}
		}(i)
	}
	// Len + Prune
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 20; i++ {
			c.Len()
			c.Prune()
		}
	}()

	wg.Wait()
	// No assertion needed -- test passes if no race detector violation.
}
