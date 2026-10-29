package httpapi

import (
	"context"
	"sync"
	"time"
)

// latestCache remembers the supplier's newest batch so the window view
// never waits for the supplier. If the value is older than maxAge, it is
// refreshed in the background and the last known value is returned
// meanwhile. Only the very first call waits for the supplier.
//
// This matters because the window view also shows which batch is being
// saved: that part must reflect the database now, not the moment before a
// slow supplier call.
type latestCache struct {
	source LatestSource
	maxAge time.Duration
	now    func() time.Time

	mu         sync.Mutex
	value      int64
	err        error
	fetchedAt  time.Time
	refreshing bool
}

func newLatestCache(source LatestSource, maxAge time.Duration) *latestCache {
	return &latestCache{source: source, maxAge: maxAge, now: time.Now}
}

// Get returns the newest batch number known, or the last error.
func (c *latestCache) Get() (int64, error) {
	c.mu.Lock()
	first := c.fetchedAt.IsZero()
	stale := c.now().Sub(c.fetchedAt) > c.maxAge
	if stale && !c.refreshing {
		c.refreshing = true
		if first {
			c.mu.Unlock()
			c.refresh()
			c.mu.Lock()
		} else {
			go c.refresh()
		}
	}
	defer c.mu.Unlock()
	return c.value, c.err
}

func (c *latestCache) refresh() {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	value, err := c.source.Latest(ctx)

	c.mu.Lock()
	defer c.mu.Unlock()
	if err == nil {
		c.value = value
	}
	c.err = err
	c.fetchedAt = c.now()
	c.refreshing = false
}
