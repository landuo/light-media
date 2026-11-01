package app

import (
	"container/list"
	"context"
	"light-media/internal/catalog"
	"sync"
	"time"
)

// originalRecognitionRequestCache 是新主动识别的进程内 response-snapshot 缓存。
// 不复用 UI 24h full-model cache，也不是 TmdbCache 的 meta→识别身份持久层。
type originalRecognitionRequestCache struct {
	mu    sync.Mutex
	items map[string]*list.Element
	order list.List
}

type originalRecognitionRequestEntry struct {
	key      string
	expires  time.Time
	snapshot *catalog.SourceRecognitionSnapshot
}

// load 不跨 I/O 持锁；source decorator 同样不合并并发 miss。fresh 只跳过读，
// 取得有效 response 后仍替换同一 key，失败不能移除之前有效的缓存事实。
func (c *originalRecognitionRequestCache) load(ctx context.Context, key string, fresh bool, maxEntries int, ttl time.Duration, at func() time.Time, guard func() error, fetch func() (*catalog.SourceRecognitionSnapshot, error)) (*catalog.SourceRecognitionSnapshot, error) {
	if ctx == nil || at == nil || guard == nil || fetch == nil || maxEntries < 1 {
		return nil, bad("SOURCE_RECOGNITION_CACHE_UNSUPPORTED", "识别缓存配置不可用")
	}
	validate := func() error {
		if err := ctx.Err(); err != nil {
			return err
		}
		return guard()
	}
	if err := validate(); err != nil {
		return nil, err
	}
	if !fresh {
		now := at()
		c.mu.Lock()
		if element := c.items[key]; element != nil {
			entry := element.Value.(*originalRecognitionRequestEntry)
			if now.Before(entry.expires) {
				c.order.MoveToBack(element)
				snapshot := entry.snapshot.Clone()
				c.mu.Unlock()
				if err := validate(); err != nil {
					return nil, err
				}
				return snapshot, nil
			}
			delete(c.items, key)
			c.order.Remove(element)
		}
		c.mu.Unlock()
	}
	snapshot, err := fetch()
	if err != nil {
		return nil, err
	}
	if err := validate(); err != nil {
		return nil, err
	}
	if snapshot == nil {
		return nil, nil
	}
	frozen := snapshot.Clone()
	now := at()
	c.mu.Lock()
	if c.items == nil {
		c.items = map[string]*list.Element{}
	}
	for e := c.order.Front(); e != nil; {
		next := e.Next()
		entry := e.Value.(*originalRecognitionRequestEntry)
		if !now.Before(entry.expires) {
			delete(c.items, entry.key)
			c.order.Remove(e)
		}
		e = next
	}
	if old := c.items[key]; old != nil {
		delete(c.items, key)
		c.order.Remove(old)
	}
	if ttl > 0 {
		for len(c.items) >= maxEntries {
			old := c.order.Front()
			entry := old.Value.(*originalRecognitionRequestEntry)
			delete(c.items, entry.key)
			c.order.Remove(old)
		}
		entry := &originalRecognitionRequestEntry{key: key, expires: now.Add(ttl), snapshot: frozen}
		c.items[key] = c.order.PushBack(entry)
	}
	c.mu.Unlock()
	if err := validate(); err != nil {
		return nil, err
	}
	return snapshot.Clone(), nil
}
