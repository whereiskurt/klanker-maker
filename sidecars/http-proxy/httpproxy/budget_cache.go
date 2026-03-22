// Package httpproxy — budget_cache.go
// In-process budget limit cache with configurable TTL (default 10s).
// Prevents a DynamoDB read on every Bedrock request by caching the last-known
// budget state and optimistically tracking proxy-side spend increments.
package httpproxy

import (
	"sync"
	"time"
)

const defaultBudgetCacheTTL = 10 * time.Second

// BudgetEntry holds the cached budget state for a single sandbox.
type BudgetEntry struct {
	ComputeLimit float64
	AILimit      float64
	AISpent      float64
	fetchedAt    time.Time
}

// budgetCache is the unexported singleton used by the proxy. External callers
// use NewBudgetCache / NewBudgetCacheWithTTL for test isolation.
type budgetCache struct {
	mu      sync.Mutex
	entries map[string]*BudgetEntry
	ttl     time.Duration
}

// NewBudgetCache returns a new budget cache with the default 10s TTL.
func NewBudgetCache() *budgetCache {
	return NewBudgetCacheWithTTL(defaultBudgetCacheTTL)
}

// NewBudgetCacheWithTTL returns a new budget cache with a custom TTL.
// Useful in tests where a short TTL avoids long sleeps.
func NewBudgetCacheWithTTL(ttl time.Duration) *budgetCache {
	return &budgetCache{
		entries: make(map[string]*BudgetEntry),
		ttl:     ttl,
	}
}

// Get returns the cached BudgetEntry for sandboxID if it is within the TTL,
// otherwise returns nil (cache miss).
func (c *budgetCache) Get(sandboxID string) *BudgetEntry {
	c.mu.Lock()
	defer c.mu.Unlock()

	entry, ok := c.entries[sandboxID]
	if !ok {
		return nil
	}
	if time.Since(entry.fetchedAt) > c.ttl {
		delete(c.entries, sandboxID)
		return nil
	}
	// Return a copy to prevent callers from mutating cached state.
	copy := *entry
	return &copy
}

// Set stores or replaces the BudgetEntry for sandboxID, resetting the TTL clock.
func (c *budgetCache) Set(sandboxID string, entry *BudgetEntry) {
	c.mu.Lock()
	defer c.mu.Unlock()

	stored := *entry
	stored.fetchedAt = time.Now()
	c.entries[sandboxID] = &stored
}

// UpdateLocalSpend adds additionalCost to the cached AISpent for sandboxID.
// This is an optimistic update: it tracks proxy-side increments between
// DynamoDB refreshes so the proxy can detect budget exhaustion locally
// without waiting for the next TTL expiry. No-op if the entry is missing or
// has expired (the next request will trigger a fresh DynamoDB read anyway).
func (c *budgetCache) UpdateLocalSpend(sandboxID string, additionalCost float64) {
	c.mu.Lock()
	defer c.mu.Unlock()

	entry, ok := c.entries[sandboxID]
	if !ok {
		return
	}
	if time.Since(entry.fetchedAt) > c.ttl {
		delete(c.entries, sandboxID)
		return
	}
	entry.AISpent += additionalCost
}
