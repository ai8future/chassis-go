// Package cache provides an in-memory TTL/LRU cache with optional OTel metrics.
package cache

import (
	"container/list"
	"sync"
	"time"

	chassis "github.com/ai8future/chassis-go/v11"
)

type Cache[K comparable, V any] struct {
	mu      sync.RWMutex
	items   map[K]*list.Element
	order   *list.List
	maxSize int
	ttl     time.Duration
	name    string
}

type entry[K comparable, V any] struct {
	key       K
	value     V
	expiresAt time.Time
}

type Option func(*config)

type config struct {
	maxSize int
	ttl     time.Duration
	name    string
}

func MaxSize(n int) Option {
	return func(c *config) { c.maxSize = n }
}

func TTL(d time.Duration) Option {
	return func(c *config) { c.ttl = d }
}

func Name(s string) Option {
	return func(c *config) { c.name = s }
}

func New[K comparable, V any](opts ...Option) *Cache[K, V] {
	chassis.AssertVersionChecked()
	cfg := &config{maxSize: 1000}
	for _, o := range opts {
		o(cfg)
	}
	if cfg.maxSize < 1 {
		cfg.maxSize = 1
	}
	return &Cache[K, V]{
		items:   make(map[K]*list.Element, cfg.maxSize),
		order:   list.New(),
		maxSize: cfg.maxSize,
		ttl:     cfg.ttl,
		name:    cfg.name,
	}
}

func (c *Cache[K, V]) Get(key K) (V, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	elem, ok := c.items[key]
	if !ok {
		var zero V
		return zero, false
	}

	e := elem.Value.(*entry[K, V])
	if !e.expiresAt.IsZero() && time.Now().After(e.expiresAt) {
		c.removeLocked(elem)
		var zero V
		return zero, false
	}

	c.order.MoveToFront(elem)
	return e.value, true
}

func (c *Cache[K, V]) Set(key K, value V) {
	c.mu.Lock()
	defer c.mu.Unlock()

	var expiresAt time.Time
	if c.ttl > 0 {
		expiresAt = time.Now().Add(c.ttl)
	}

	if elem, ok := c.items[key]; ok {
		e := elem.Value.(*entry[K, V])
		e.value = value
		e.expiresAt = expiresAt
		c.order.MoveToFront(elem)
		return
	}

	for len(c.items) >= c.maxSize {
		c.evictLocked()
	}

	e := &entry[K, V]{key: key, value: value, expiresAt: expiresAt}
	elem := c.order.PushFront(e)
	c.items[key] = elem
}

func (c *Cache[K, V]) Delete(key K) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if elem, ok := c.items[key]; ok {
		c.removeLocked(elem)
	}
}

func (c *Cache[K, V]) Len() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.items)
}

func (c *Cache[K, V]) Prune() int {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.ttl == 0 {
		return 0
	}

	now := time.Now()
	removed := 0
	for _, elem := range c.items {
		e := elem.Value.(*entry[K, V])
		if !e.expiresAt.IsZero() && now.After(e.expiresAt) {
			c.removeLocked(elem)
			removed++
		}
	}
	return removed
}

func (c *Cache[K, V]) evictLocked() {
	back := c.order.Back()
	if back == nil {
		return
	}
	c.removeLocked(back)
}

func (c *Cache[K, V]) removeLocked(elem *list.Element) {
	e := elem.Value.(*entry[K, V])
	delete(c.items, e.key)
	c.order.Remove(elem)
}
