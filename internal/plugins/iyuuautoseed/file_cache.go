package iyuuautoseed

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"regexp"
	"sync"
	"time"
)

var scopedTorrentName = regexp.MustCompile(`^[0-9a-f]{32}\.torrent$`)

type FileTorrentCache struct {
	root    *os.Root
	mu      sync.Mutex
	invalid map[string]time.Time
	order   []string
	now     func() time.Time
}

func OpenFileTorrentCache(directory string) (*FileTorrentCache, error) {
	r, e := os.OpenRoot(directory)
	if e != nil {
		return nil, e
	}
	return &FileTorrentCache{root: r, invalid: map[string]time.Time{}, now: time.Now}, nil
}
func (c *FileTorrentCache) Close() error { return c.root.Close() }
func (c *FileTorrentCache) GetTorrent(ctx context.Context, name string) ([]byte, error) {
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	if !scopedTorrentName.MatchString(name) {
		return nil, errors.New("torrent cache key outside scope")
	}
	f, e := c.root.Open(name)
	if os.IsNotExist(e) {
		return nil, nil
	}
	if e != nil {
		return nil, e
	}
	defer f.Close()
	b, e := io.ReadAll(io.LimitReader(f, maxAPIResponse+1))
	if len(b) > maxAPIResponse {
		return nil, errors.New("torrent cache object exceeds limit")
	}
	return b, e
}
func (c *FileTorrentCache) PutTorrent(ctx context.Context, name string, raw []byte) error {
	if e := ctx.Err(); e != nil {
		return e
	}
	if !scopedTorrentName.MatchString(name) || len(raw) > maxAPIResponse {
		return errors.New("torrent cache write outside scope")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	// A crash may leave the old temporary object behind. A fresh unpredictable
	// scoped name cannot let that orphan permanently block this cache key.
	nonce := make([]byte, 16)
	if _, e := rand.Read(nonce); e != nil {
		return e
	}
	temp := name + ".pending-" + hex.EncodeToString(nonce)
	f, e := c.root.OpenFile(temp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return e
	}
	defer c.root.Remove(temp)
	_, e = f.Write(raw)
	if e == nil {
		e = f.Sync()
	}
	closeErr := f.Close()
	if e != nil {
		return e
	}
	if closeErr != nil {
		return closeErr
	}
	if e = c.root.Rename(temp, name); e != nil {
		return e
	}
	dir, e := c.root.Open(".")
	if e != nil {
		return e
	}
	defer dir.Close()
	return dir.Sync()
}
func (c *FileTorrentCache) AddInvalid(ctx context.Context, address string) error {
	if e := ctx.Err(); e != nil {
		return e
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	for len(c.order) > 0 {
		key := c.order[0]
		if expiry := c.invalid[key]; expiry.After(now) {
			break
		}
		delete(c.invalid, key)
		c.order = c.order[1:]
	}
	if _, ok := c.invalid[address]; ok {
		for i, k := range c.order {
			if k == address {
				c.order = append(c.order[:i], c.order[i+1:]...)
				break
			}
		}
	}
	c.invalid[address] = now.Add(24 * time.Hour)
	c.order = append(c.order, address)
	for len(c.order) > 128 {
		delete(c.invalid, c.order[0])
		c.order = c.order[1:]
	}
	return nil
}
