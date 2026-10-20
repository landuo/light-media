package larkmessager

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sync"
)

// OwnerTokenCache preserves the source LarkClient's in-memory authentication
// cache across short SDK calls. It is deliberately opaque and never durable or
// serializable. Reset belongs to the original Init/Stop lifecycle, not a worker.
// This bridge leaves Adapter's original token/expiry algorithm unchanged.
type OwnerTokenCache struct {
	mu         sync.Mutex
	key, token string
	expires    int64
}

func (c *OwnerTokenCache) Reset() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.key, c.token, c.expires = "", "", 0
}
func ownerTokenKey(a *Adapter) string {
	raw, _ := json.Marshal([]any{a.origin, a.binding.Owner, a.binding.Generation, a.binding.Revision, a.binding.AppID, a.binding.AppSecret})
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
func (c *OwnerTokenCache) Bind(a *Adapter) error {
	if c == nil || a == nil {
		return ErrDenied
	}
	key := ownerTokenKey(a)
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.key != key {
		c.key, c.token, c.expires = key, "", 0
	}
	a.tokenMu.Lock()
	a.token, a.expires = c.token, c.expires
	a.tokenMu.Unlock()
	return nil
}
func (c *OwnerTokenCache) Capture(a *Adapter) {
	if c == nil || a == nil {
		return
	}
	key := ownerTokenKey(a)
	a.tokenMu.Lock()
	token, expires := a.token, a.expires
	a.tokenMu.Unlock()
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.key == key {
		c.token, c.expires = token, expires
	}
}
