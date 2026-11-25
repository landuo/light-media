package app

import (
	"bytes"
	"io"
	"net/http"
	"os"
	"sync"
	"time"

	"golang.org/x/sys/unix"
)

type posterStamp struct {
	device, inode uint64
	size          int64
	ctime, mtime  unix.Timespec
}

func posterFileStamp(f *os.File) (posterStamp, bool) {
	var s unix.Stat_t
	if unix.Fstat(int(f.Fd()), &s) != nil {
		return posterStamp{}, false
	}
	return posterStamp{uint64(s.Dev), s.Ino, s.Size, s.Ctim, s.Mtim}, true
}

type posterEntry struct {
	stamp posterStamp
	mime  string
	data  []byte
}
type posterCache struct {
	mu      sync.Mutex
	entries map[string]posterEntry
	order   []string
	bytes   int
	loading int
}

// Keep small, frequently requested posters on the host, across browser tabs.
// Always open the actual file first. Replacement, same-size edits and restores
// of mtime invalidate the entry. Large images and ranges continue to stream.
func (c *posterCache) content(f *os.File, r *http.Request) (io.ReadSeeker, string, error) {
	stamp, ok := posterFileStamp(f)
	cacheable := ok && stamp.size > 0 && stamp.size <= 384<<10 && time.Since(time.Unix(stamp.ctime.Unix())) > 20*time.Millisecond
	if cacheable {
		c.mu.Lock()
		entry, hit := c.entries[f.Name()]
		if hit && entry.stamp == stamp {
			c.mu.Unlock()
			return bytes.NewReader(entry.data), entry.mime, nil
		}
		cacheable = c.loading < 2 && r.Method == "GET" && r.Header.Get("Range") == "" && r.Header.Get("If-Modified-Since") == ""
		if cacheable {
			c.loading++
		}
		c.mu.Unlock()
	}
	if cacheable {
		defer func() { c.mu.Lock(); c.loading--; c.mu.Unlock() }()
	}
	mime, err := catalogImageContentType(f)
	if err != nil || !cacheable {
		return f, mime, err
	}
	data := make([]byte, int(stamp.size))
	if _, err = io.ReadFull(f, data); err != nil {
		return nil, "", err
	}
	after, ok := posterFileStamp(f)
	if !ok || after != stamp {
		return nil, "", os.ErrInvalid
	}
	key := f.Name()
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries == nil {
		c.entries = make(map[string]posterEntry)
	}
	if old, exists := c.entries[key]; exists {
		if old.stamp == stamp {
			return bytes.NewReader(old.data), old.mime, nil
		}
		c.bytes -= len(old.data)
		for i, k := range c.order {
			if k == key {
				c.order = append(c.order[:i], c.order[i+1:]...)
				break
			}
		}
	}
	for len(c.order) > 0 && (c.bytes+len(data) > 3<<20 || len(c.order) >= 96) {
		k := c.order[0]
		c.order = c.order[1:]
		c.bytes -= len(c.entries[k].data)
		delete(c.entries, k)
	}
	c.entries[key] = posterEntry{stamp, mime, data}
	c.order = append(c.order, key)
	c.bytes += len(data)
	return bytes.NewReader(data), mime, nil
}
