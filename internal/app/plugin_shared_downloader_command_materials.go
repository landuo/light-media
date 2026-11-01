package app

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"path/filepath"
	"sync"

	"light-media/internal/materialio"
	"light-media/internal/plugins"
)

// This state belongs to the EXISTING invocation inbound owner. It is separate
// from legacy small buffers and outbound handles; it contains no worker path.
// Grant IO uses the private spool. Submit hands ordinary []byte to the unchanged
// source leaf/native API, so this is not an O(1) whole-chain memory claim.
type originalDownloaderMaterialUpload struct {
	capability string
	snapshot   *originalDownloaderCommandSnapshot
	total      int64
	digest     [sha256.Size]byte
	spool      *materialio.Spool
	closeOwner func() error
	consumed   bool
}

func (s *originalDownloaderCommandUploads) materialValueLocked(capability, handle string) (*originalDownloaderMaterialUpload, error) {
	if s.closed || s.materials == nil {
		return nil, bad("PLUGIN_CALL_CLOSED", "下载器入站材料调用已结束")
	}
	value := s.materials[handle]
	if !originalDownloaderUploadCapability(capability) || !originalDownloaderUploadHandle(handle) || value == nil || value.capability != capability {
		return nil, bad("PLUGIN_TRANSFER_DENIED", "入站材料不属于当前调用及添加能力")
	}
	if value.consumed {
		return nil, bad("PLUGIN_RESOURCE_CONFLICT", "入站材料已消费，不能再次提交或撤销")
	}
	return value, nil
}
func (s *originalDownloaderCommandUploads) registerMaterial(ctx context.Context, value *originalDownloaderMaterialUpload) (string, error) {
	if err := s.check(ctx); err != nil {
		return "", err
	}
	if value == nil || value.snapshot == nil || value.spool == nil || value.closeOwner == nil || value.total < 0 || !originalDownloaderUploadCapability(value.capability) {
		return "", originalDownloaderUploadInvalid()
	}
	var nonce [32]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return "", err
	}
	handle := "u_" + hex.EncodeToString(nonce[:])
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if s.closed || s.materials == nil {
		return "", bad("PLUGIN_CALL_CLOSED", "下载器入站材料调用已结束")
	}
	if s.values[handle] != nil || s.materials[handle] != nil {
		return "", bad("PLUGIN_RESOURCE_CONFLICT", "入站材料句柄发生冲突")
	}
	value.snapshot = value.snapshot.clone()
	s.materials[handle] = value
	return handle, nil
}
func (s *originalDownloaderCommandUploads) snapshotMaterial(ctx context.Context, capability, handle string) (*originalDownloaderCommandSnapshot, error) {
	if err := s.check(ctx); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	value, err := s.materialValueLocked(capability, handle)
	if err != nil {
		return nil, err
	}
	return value.snapshot.clone(), nil
}
func (s *originalDownloaderCommandUploads) appendMaterial(ctx context.Context, capability, handle string, offset int64, data []byte) error {
	if err := s.check(ctx); err != nil {
		return err
	}
	s.mu.Lock()
	value, err := s.materialValueLocked(capability, handle)
	s.mu.Unlock()
	if err != nil {
		return err
	}
	if offset < 0 || offset > value.total || len(data) < 1 || len(data) > plugins.MaterialFrameBytes || int64(len(data)) > value.total-offset {
		return originalDownloaderUploadInvalid()
	}
	next, err := value.spool.Append(ctx, offset, data)
	if err != nil {
		return err
	}
	if next != offset+int64(len(data)) {
		return materialio.ErrLength
	}
	return nil
}
func (s *originalDownloaderCommandUploads) consumeMaterial(ctx context.Context, capability, handle string) (*originalDownloaderMaterialUpload, *originalDownloaderCommandSnapshot, error) {
	if err := s.check(ctx); err != nil {
		return nil, nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	value, err := s.materialValueLocked(capability, handle)
	if err != nil {
		return nil, nil, err
	}
	// Linearize once BEFORE Claim, IO, native construction or selected dispatch.
	// Any ensuing guard/length/digest/read/transport failure leaves this tombstone.
	value.consumed = true
	snapshot := value.snapshot.clone()
	value.snapshot = nil
	return value, snapshot, nil
}
func (s *originalDownloaderCommandUploads) abortMaterial(ctx context.Context, capability, handle string) error {
	value, _, err := s.consumeMaterial(ctx, capability, handle)
	if err != nil {
		return err
	}
	return value.closeOwner()
}

// Used only for this function's newly registered, not-published owner when
// Issue/marshal fails. It is cleanup, not a worker-facing authority bypass.
func (s *originalDownloaderCommandUploads) discardNewMaterial(handle string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if value := s.materials[handle]; value != nil {
		value.consumed = true
		value.snapshot = nil
	}
}
func (s *originalDownloaderCommandUploads) claimMaterialBound(ctx context.Context, capability, handle string) (snapshot *originalDownloaderCommandSnapshot, content []byte, err error) {
	value, snapshot, err := s.consumeMaterial(ctx, capability, handle)
	if err != nil {
		return nil, nil, err
	}
	defer func() {
		err = errors.Join(err, value.closeOwner())
		if err != nil {
			snapshot = nil
			content = nil
		}
	}()
	// We do not read/trust framework.finished or a worker boolean. This exact
	// owner independently proves complete bytes and digest; premature claims
	// fail terminally, including a last-page/submit race through Spool's mutex.
	reader, err := value.spool.Claim(ctx, value.total, value.digest)
	if err != nil {
		return nil, nil, err
	}
	if uint64(value.total) > uint64(^uint(0)>>1) {
		return nil, nil, bad("PLUGIN_VALUE_TOO_LARGE", "材料超过当前平台字节切片表示范围")
	}
	content = []byte{}
	page := make([]byte, materialio.CopyChunkBytes)
	digest := sha256.New()
	for offset := int64(0); offset < value.total; {
		n := int(min(int64(len(page)), value.total-offset))
		got, readErr := reader.ReadAtContext(ctx, page[:n], offset)
		if readErr != nil {
			return nil, nil, readErr
		}
		if got != n {
			return nil, nil, io.ErrUnexpectedEOF
		}
		content = append(content, page[:got]...)
		_, _ = digest.Write(page[:got])
		offset += int64(got)
	}
	var actual [sha256.Size]byte
	copy(actual[:], digest.Sum(nil))
	if actual != value.digest || int64(len(content)) != value.total {
		return nil, nil, materialio.ErrDigest
	}
	if err = ctx.Err(); err != nil {
		return nil, nil, err
	}
	return snapshot, content, nil
}

func (a *App) beginOriginalDownloaderMaterial(ctx context.Context, scope originalBaseCallScope, method, kind string, fields map[string]json.RawMessage) (response json.RawMessage, err error) {
	if err = plugins.CheckMaterialHost(ctx, method); err != nil {
		return nil, err
	}
	if !originalDownloaderCommandKeys(fields, "op", "downloader_id", "args", "total", "sha256") {
		return nil, originalDownloaderUploadInvalid()
	}
	var key, digestText string
	var total *int64
	if json.Unmarshal(fields["downloader_id"], &key) != nil || json.Unmarshal(fields["total"], &total) != nil || total == nil || *total < 0 || json.Unmarshal(fields["sha256"], &digestText) != nil {
		return nil, originalDownloaderUploadInvalid()
	}
	rawDigest, err := hex.DecodeString(digestText)
	if err != nil || len(rawDigest) != sha256.Size || hex.EncodeToString(rawDigest) != digestText {
		return nil, originalDownloaderUploadInvalid()
	}
	var digest [sha256.Size]byte
	copy(digest[:], rawDigest)
	if *total == 0 && digest != sha256.Sum256(nil) {
		return nil, originalDownloaderUploadInvalid()
	}
	configured, err := a.originalDownloaderCommandSelect(ctx, scope, kind, key)
	if err != nil {
		return nil, err
	}
	args, err := decodeOriginalDownloaderCommandArgs(fields["args"], false)
	if err != nil {
		return nil, err
	}
	snapshot := (&originalDownloaderCommandSnapshot{configured: configured, args: args}).clone()
	current := a.originalDownloaderCommandGuard(scope, kind, snapshot)
	if err = current(ctx); err != nil {
		return nil, err
	}
	directory := filepath.Join(a.Dir, "temp", "plugin-material-spool")
	parent, err := importDir(directory, true)
	if err != nil {
		return nil, err
	}
	guard := func(callCtx context.Context) error {
		if err := current(callCtx); err != nil {
			return err
		}
		return sharedLocalStorageSameDirectory(directory, parent)
	}
	spool, err := materialio.New(ctx, directory, guard)
	if err != nil {
		return nil, errors.Join(err, parent.Close())
	}
	var once sync.Once
	var closeErr error
	closeOwner := func() error {
		once.Do(func() { closeErr = errors.Join(spool.Close(), parent.Close()) })
		return closeErr
	}
	transferred := false
	handle := ""
	defer func() {
		if !transferred {
			if handle != "" {
				scope.downloaderUploads.discardNewMaterial(handle)
			}
			err = errors.Join(err, closeOwner())
			if err != nil {
				response = nil
			}
		}
	}()
	value := &originalDownloaderMaterialUpload{capability: method, snapshot: snapshot, total: *total, digest: digest, spool: spool, closeOwner: closeOwner}
	handle, err = scope.downloaderUploads.registerMaterial(ctx, value)
	if err != nil {
		return nil, err
	}
	grant, err := plugins.IssueMaterial(ctx, plugins.MaterialSpec{Capability: method, Direction: plugins.MaterialToHost, Kind: "bytes", Total: *total, SHA256: digestText}, plugins.MaterialPort{
		Write: func(writeCtx context.Context, offset int64, data []byte) error {
			return scope.downloaderUploads.appendMaterial(writeCtx, method, handle, offset, data)
		}, Close: closeOwner,
	}, guard)
	if err != nil {
		return nil, err
	}
	response, err = json.Marshal(struct {
		Handle   string                `json:"handle"`
		Material plugins.MaterialGrant `json:"material"`
	}{handle, grant})
	if err != nil {
		return nil, err
	}
	if len(response) > plugins.MaxPayloadBytes {
		return nil, bad("PLUGIN_VALUE_TOO_LARGE", "下载器材料控制结果超过既有传输上限")
	}
	if err = guard(ctx); err != nil {
		return nil, err
	}
	transferred = true
	return response, nil
}
