package postgres

import (
	"sync"
	"time"
)

// ttlCache keeps a value for a short time. The menu and the service areas change
// a few times a day but are read on every app launch, so answering them from
// memory spares the database nearly all of that traffic. A replica that has not
// expired yet serves the old value: staleness is bounded by the ttl.
type ttlCache[K comparable, V any] struct {
	ttl  time.Duration
	mu   sync.Mutex
	data map[K]ttlEntry[V]
	now  func() time.Time
}

type ttlEntry[V any] struct {
	value   V
	expires time.Time
}

func newTTLCache[K comparable, V any](ttl time.Duration) *ttlCache[K, V] {
	return &ttlCache[K, V]{ttl: ttl, data: map[K]ttlEntry[V]{}, now: time.Now}
}

func (c *ttlCache[K, V]) get(k K) (V, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.data[k]
	if !ok || !c.now().Before(e.expires) {
		delete(c.data, k)
		var zero V
		return zero, false
	}
	return e.value, true
}

func (c *ttlCache[K, V]) set(k K, v V) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.data) > 1024 { // bounded: drop everything rather than track recency
		c.data = map[K]ttlEntry[V]{}
	}
	c.data[k] = ttlEntry[V]{value: v, expires: c.now().Add(c.ttl)}
}
