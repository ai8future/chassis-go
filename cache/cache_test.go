package cache_test

import (
	"testing"
	"time"

	chassis "github.com/ai8future/chassis-go/v8"
	"github.com/ai8future/chassis-go/v8/cache"
)

func init() { chassis.RequireMajor(8) }

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
