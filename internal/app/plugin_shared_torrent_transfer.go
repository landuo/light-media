package app

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"light-media/internal/plugins"
	"sync"
)

// originalTorrentTransfers belongs to ONE actual invocation. Handles never
// identify filesystem paths or objects in another invocation/worker. Its lock
// guards only local immutable byte buffers; no plugin callback runs under it.
type originalTorrentTransfers struct {
	mu     sync.Mutex
	closed bool
	used   int
	values map[string]*originalTorrentTransfer
}
type originalTorrentTransfer struct {
	content []byte
	next    int
}
type originalTorrentPrepared struct {
	Handle string `json:"handle"`
	Total  int    `json:"total"`
	SHA256 string `json:"sha256"`
}
type originalTorrentChunk struct {
	Handle string `json:"handle"`
	Offset int    `json:"offset"`
	Data   []byte `json:"data"`
}

func newOriginalTorrentTransfers() *originalTorrentTransfers {
	return &originalTorrentTransfers{values: map[string]*originalTorrentTransfer{}}
}
func originalTorrentTransferClosed() error {
	return bad("PLUGIN_CALL_CLOSED", "种子材料调用已结束")
}
func (s *originalTorrentTransfers) publish(ctx context.Context, raw []byte) (originalTorrentPrepared, error) {
	out := originalTorrentPrepared{}
	if ctx == nil || s == nil {
		return out, originalTorrentTransferClosed()
	}
	if err := ctx.Err(); err != nil {
		return out, err
	}
	if len(raw) > plugins.TorrentMaterialMaxBytes {
		return out, bad("PLUGIN_VALUE_TOO_LARGE", "种子材料超过已有资源上限")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return out, err
	}
	if s.closed {
		return out, originalTorrentTransferClosed()
	}
	// Admission is already charged to the actual invocation host-call budget.
	// Do not introduce an extra aggregate material threshold here: it would
	// reject otherwise valid parallel public DownloadTorrent calls.
	var nonce [32]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return out, err
	}
	handle := hex.EncodeToString(nonce[:])
	if _, exists := s.values[handle]; exists {
		return out, bad("PLUGIN_RESOURCE_CONFLICT", "种子材料句柄发生冲突")
	}
	data := bytes.Clone(raw)
	if data == nil {
		data = []byte{}
	}
	digest := sha256.Sum256(data)
	s.values[handle] = &originalTorrentTransfer{content: data}
	s.used += len(data)
	return originalTorrentPrepared{Handle: handle, Total: len(data), SHA256: hex.EncodeToString(digest[:])}, nil
}
func (s *originalTorrentTransfers) read(ctx context.Context, handle string, offset int) (originalTorrentChunk, error) {
	out := originalTorrentChunk{}
	if ctx == nil || s == nil {
		return out, originalTorrentTransferClosed()
	}
	if err := ctx.Err(); err != nil {
		return out, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return out, err
	}
	if s.closed {
		return out, originalTorrentTransferClosed()
	}
	value, ok := s.values[handle]
	if !ok {
		return out, bad("PLUGIN_TRANSFER_DENIED", "种子材料句柄不属于当前调用")
	}
	if offset < 0 || offset != value.next || offset >= len(value.content) {
		return out, bad("INVALID_PLUGIN_VALUE", "种子材料读取偏移无效")
	}
	end := min(offset+plugins.TorrentMaterialChunkBytes, len(value.content))
	out = originalTorrentChunk{Handle: handle, Offset: offset, Data: bytes.Clone(value.content[offset:end])}
	value.next = end
	return out, nil
}
func (s *originalTorrentTransfers) release(ctx context.Context, handle string) error {
	if ctx == nil || s == nil {
		return originalTorrentTransferClosed()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.closed {
		return originalTorrentTransferClosed()
	}
	value, ok := s.values[handle]
	if !ok {
		return bad("PLUGIN_TRANSFER_DENIED", "种子材料句柄不属于当前调用")
	}
	s.used -= len(value.content)
	delete(s.values, handle)
	return nil
}
func (s *originalTorrentTransfers) close() {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	s.closed = true
	s.values = nil
	s.used = 0
}
