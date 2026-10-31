package app

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"sync"
	"unicode/utf8"

	"light-media/internal/plugins"
)

const (
	originalDownloaderUploadQBAdd = "downloader.qbittorrent.add"
	originalDownloaderUploadTRAdd = "downloader.transmission.add"
	// Only the fixed private append packet uses this bound. Generic resource
	// parameters remain 128KiB; global payload/transport/callback limits do not change.
	originalDownloaderUploadAppendMaxBytes = 4*((plugins.TorrentMaterialChunkBytes+2)/3) + 512
)

type originalDownloaderUploadAppend struct {
	Handle string
	Offset int
	Data   []byte
}

func originalDownloaderUploadCapability(capability string) bool {
	return capability == originalDownloaderUploadQBAdd || capability == originalDownloaderUploadTRAdd
}

func originalDownloaderUploadHandle(handle string) bool {
	if len(handle) != 66 || handle[:2] != "u_" {
		return false
	}
	for _, c := range handle[2:] {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

func originalDownloaderUploadInvalid() error {
	return bad("INVALID_PLUGIN_VALUE", "下载器入站材料参数无效")
}

// decodeOriginalDownloaderUploadAppend is not a permissive replacement for
// originalResourceDecode. Only an already-authorized add route may select it.
// Token decoding rejects duplicate/unknown keys, including case aliases; all
// four fields are required and null is not a substitute for offset or data.
func decodeOriginalDownloaderUploadAppend(raw json.RawMessage) (originalDownloaderUploadAppend, error) {
	var out originalDownloaderUploadAppend
	if len(raw) == 0 || len(raw) > originalDownloaderUploadAppendMaxBytes || !utf8.Valid(raw) {
		return out, originalDownloaderUploadInvalid()
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	opening, err := d.Token()
	if err != nil || opening != json.Delim('{') {
		return out, originalDownloaderUploadInvalid()
	}
	fields := make(map[string]json.RawMessage, 4)
	for d.More() {
		key, err := d.Token()
		name, ok := key.(string)
		if err != nil || !ok || (name != "op" && name != "handle" && name != "offset" && name != "data") {
			return out, originalDownloaderUploadInvalid()
		}
		if _, duplicate := fields[name]; duplicate {
			return out, originalDownloaderUploadInvalid()
		}
		var value json.RawMessage
		if d.Decode(&value) != nil || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return out, originalDownloaderUploadInvalid()
		}
		fields[name] = value
	}
	closing, err := d.Token()
	if err != nil || closing != json.Delim('}') || len(fields) != 4 || !errors.Is(d.Decode(new(any)), io.EOF) {
		return out, originalDownloaderUploadInvalid()
	}
	var operation, encoded string
	if json.Unmarshal(fields["op"], &operation) != nil || operation != "upload_append" ||
		json.Unmarshal(fields["handle"], &out.Handle) != nil || !originalDownloaderUploadHandle(out.Handle) ||
		json.Unmarshal(fields["offset"], &out.Offset) != nil || out.Offset < 0 ||
		json.Unmarshal(fields["data"], &encoded) != nil || len(encoded) == 0 || len(encoded) > 4*((plugins.TorrentMaterialChunkBytes+2)/3) {
		return originalDownloaderUploadAppend{}, originalDownloaderUploadInvalid()
	}
	data, err := base64.StdEncoding.Strict().DecodeString(encoded)
	if err != nil || len(data) == 0 || len(data) > plugins.TorrentMaterialChunkBytes || base64.StdEncoding.EncodeToString(data) != encoded {
		return originalDownloaderUploadAppend{}, originalDownloaderUploadInvalid()
	}
	out.Data = data
	return out, nil
}

// originalDownloaderCommandUploads belongs to one invocation, not an object,
// plugin registry, filesystem or downloader. Callers must separately validate
// current capability/identity/scope and typed add parameters before every use.
// This pure owner never authorizes or dispatches any downloader operation.
type originalDownloaderCommandUploads struct {
	mu     sync.Mutex
	closed bool
	values map[string]*originalDownloaderCommandUpload
}

type originalDownloaderCommandUpload struct {
	capability string
	snapshot   *originalDownloaderCommandSnapshot // Immutable, host-selected; nil only in pure store use.
	total      int
	digest     [sha256.Size]byte
	content    []byte
	consumed   bool
	failed     bool
}

func newOriginalDownloaderCommandUploads() *originalDownloaderCommandUploads {
	return &originalDownloaderCommandUploads{values: make(map[string]*originalDownloaderCommandUpload)}
}

func (s *originalDownloaderCommandUploads) check(ctx context.Context) error {
	if ctx == nil || s == nil {
		return bad("PLUGIN_CALL_CLOSED", "下载器入站材料调用已结束")
	}
	return ctx.Err()
}

func (s *originalDownloaderCommandUploads) value(capability, handle string) (*originalDownloaderCommandUpload, error) {
	if s.closed || s.values == nil {
		return nil, bad("PLUGIN_CALL_CLOSED", "下载器入站材料调用已结束")
	}
	value := s.values[handle]
	if !originalDownloaderUploadCapability(capability) || !originalDownloaderUploadHandle(handle) || value == nil || value.capability != capability {
		return nil, bad("PLUGIN_TRANSFER_DENIED", "入站材料不属于当前调用及添加能力")
	}
	if value.consumed {
		return nil, bad("PLUGIN_RESOURCE_CONFLICT", "入站材料已消费，不能再次提交或撤销")
	}
	if value.failed {
		return nil, originalDownloaderUploadInvalid()
	}
	return value, nil
}

func (s *originalDownloaderCommandUploads) begin(ctx context.Context, capability string, total int, digest string) (string, error) {
	return s.beginBound(ctx, capability, total, digest, nil)
}

func (s *originalDownloaderCommandUploads) beginBound(ctx context.Context, capability string, total int, digest string, snapshot *originalDownloaderCommandSnapshot) (string, error) {
	if err := s.check(ctx); err != nil {
		return "", err
	}
	decoded, err := hex.DecodeString(digest)
	if !originalDownloaderUploadCapability(capability) || total < 0 || total > plugins.TorrentMaterialMaxBytes || err != nil || len(decoded) != sha256.Size || hex.EncodeToString(decoded) != digest {
		return "", originalDownloaderUploadInvalid()
	}
	var expected [sha256.Size]byte
	copy(expected[:], decoded)
	if total == 0 && expected != sha256.Sum256(nil) {
		return "", originalDownloaderUploadInvalid()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if s.closed || s.values == nil {
		return "", bad("PLUGIN_CALL_CLOSED", "下载器入站材料调用已结束")
	}
	var nonce [32]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return "", err
	}
	handle := "u_" + hex.EncodeToString(nonce[:])
	if _, exists := s.values[handle]; exists {
		return "", bad("PLUGIN_RESOURCE_CONFLICT", "入站材料句柄发生冲突")
	}
	// Do not allocate the declared total at begin: only received bytes consume
	// memory. Existing invocation callback/wire budgets remain the admission cap.
	s.values[handle] = &originalDownloaderCommandUpload{capability: capability, total: total, digest: expected, snapshot: snapshot.clone()}
	return handle, nil
}

func (s *originalDownloaderCommandUploads) append(ctx context.Context, capability string, chunk originalDownloaderUploadAppend) (int, error) {
	if err := s.check(ctx); err != nil {
		return 0, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	value, err := s.value(capability, chunk.Handle)
	if err != nil {
		return 0, err
	}
	next := len(value.content)
	if chunk.Offset != next || next >= value.total || len(chunk.Data) != min(plugins.TorrentMaterialChunkBytes, value.total-next) {
		return 0, originalDownloaderUploadInvalid()
	}
	value.content = append(value.content, chunk.Data...)
	if len(value.content) == value.total && sha256.Sum256(value.content) != value.digest {
		// A failed integrity check is terminal for this token; never rewind or
		// make a partially overwritten buffer available to a later submit.
		value.content, value.failed = nil, true
		return 0, originalDownloaderUploadInvalid()
	}
	return len(value.content), nil
}

func (s *originalDownloaderCommandUploads) claim(ctx context.Context, capability, handle string) ([]byte, error) {
	_, content, err := s.claimBound(ctx, capability, handle)
	return content, err
}

func (s *originalDownloaderCommandUploads) claimBound(ctx context.Context, capability, handle string) (*originalDownloaderCommandSnapshot, []byte, error) {
	if err := s.check(ctx); err != nil {
		return nil, nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	value, err := s.value(capability, handle)
	if err != nil {
		return nil, nil, err
	}
	if len(value.content) != value.total || sha256.Sum256(value.content) != value.digest {
		return nil, nil, originalDownloaderUploadInvalid()
	}
	content := value.content
	if content == nil {
		content = []byte{}
	}
	// Claim is an atomic one-time handoff, NOT proof that any external add was
	// sent or accepted. Keep the consumed tombstone until phase close; dispatch
	// failures/unknown outcomes never restore the token to a retryable state.
	snapshot := value.snapshot.clone()
	value.content, value.snapshot, value.consumed = nil, nil, true
	return snapshot, content, nil
}

// Inspect only immutable host metadata. No payload bytes escape before claim.
// A failed-integrity token still exposes its binding so an authorized abort can
// reclaim it; append/claim retain their existing failed-state rejection.
func (s *originalDownloaderCommandUploads) snapshot(ctx context.Context, capability, handle string) (*originalDownloaderCommandSnapshot, error) {
	if err := s.check(ctx); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if s.closed {
		return nil, bad("PLUGIN_CALL_CLOSED", "下载器入站材料调用已结束")
	}
	v := s.values[handle]
	if !originalDownloaderUploadCapability(capability) || !originalDownloaderUploadHandle(handle) || v == nil || v.capability != capability {
		return nil, bad("PLUGIN_TRANSFER_DENIED", "入站材料不属于当前调用及添加能力")
	}
	if v.consumed {
		return nil, bad("PLUGIN_RESOURCE_CONFLICT", "入站材料已消费")
	}
	if v.snapshot == nil {
		return nil, bad("PLUGIN_TRANSFER_DENIED", "入站材料缺少当前宿主绑定")
	}
	return v.snapshot.clone(), nil
}

func (s *originalDownloaderCommandUploads) abort(ctx context.Context, capability, handle string) error {
	if err := s.check(ctx); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	// Integrity-failed buffers may be reclaimed too, but a consumed token must
	// never yield a fake rollback receipt. Unknown/foreign tokens remain denied.
	value, err := s.value(capability, handle)
	if err != nil {
		candidate := s.values[handle]
		if s.closed || candidate == nil || candidate.capability != capability || !candidate.failed || candidate.consumed {
			return err
		}
		value = candidate
	}
	value.content = nil
	delete(s.values, handle)
	return nil
}

func (s *originalDownloaderCommandUploads) close() {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	s.closed, s.values = true, nil
	// Never zero backing arrays: a previously claimed slice is now owned by an
	// actual in-flight host callback whose Drained receipt may still be pending.
}
