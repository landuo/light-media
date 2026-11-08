package app

import (
	"slices"
	"sync"
	"time"
)

const (
	originalChatRSSInvalidTTL      = 24 * time.Hour
	originalChatRSSInvalidCapacity = 128
)

type originalChatRSSInvalidEntry struct {
	resourceID string
	expires    time.Time
}

// TorrentHelper shares this TTL region for the lifetime of the application.
// Keep only the enclosure hash. Presence checks do not refresh TTL or order.
type originalChatRSSInvalidEnclosures struct {
	mu      sync.Mutex
	entries []originalChatRSSInvalidEntry
}

func (c *originalChatRSSInvalidEnclosures) expire(now time.Time) {
	first := 0
	for first < len(c.entries) && !now.Before(c.entries[first].expires) {
		first++
	}
	c.entries = slices.Delete(c.entries, 0, first)
}

func (a *App) originalChatRSSInvalidSnapshot() map[string]struct{} {
	c := &a.originalChatRSSInvalid
	c.mu.Lock()
	defer c.mu.Unlock()
	c.expire(time.Now())
	ids := make(map[string]struct{}, len(c.entries))
	for _, entry := range c.entries {
		ids[entry.resourceID] = struct{}{}
	}
	return ids
}

func (a *App) rememberOriginalChatRSSInvalidEnclosure(enclosure string) {
	if enclosure == "" {
		return
	}
	resourceID := "rss_" + tokenHash(enclosure)
	c := &a.originalChatRSSInvalid
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now()
	c.expire(now)
	if slices.ContainsFunc(c.entries, func(entry originalChatRSSInvalidEntry) bool {
		return entry.resourceID == resourceID
	}) {
		return
	}
	if len(c.entries) == originalChatRSSInvalidCapacity {
		c.entries = slices.Delete(c.entries, 0, 1)
	}
	c.entries = append(c.entries, originalChatRSSInvalidEntry{resourceID: resourceID, expires: now.Add(originalChatRSSInvalidTTL)})
}
