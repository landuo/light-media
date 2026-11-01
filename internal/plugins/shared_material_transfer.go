package plugins

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
)

// sharedReadMaterialBytes is private to the typed material SDK consumers. It
// cannot select endpoints/paths or invoke arbitrary RPC. The public []byte
// result still materializes O(n); it grows from actual pages, not declared Total.
func sharedReadMaterialBytes(ctx context.Context, grant MaterialGrant, capability string) ([]byte, error) {
	if ctx == nil {
		return nil, errors.New("material bytes require an invocation")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !validMaterialSpec(grant.MaterialSpec) || grant.Capability != capability || grant.Direction != MaterialToWorker || grant.Kind != "bytes" || grant.Contract != MaterialContract || grant.ChunkBytes < 1 || grant.ChunkBytes > MaterialFrameBytes {
		return nil, errors.New("invalid material byte grant")
	}
	// This is platform slice representability, never a replacement material quota.
	if uint64(grant.Total) > uint64(^uint(0)>>1) {
		return nil, errors.New("material bytes exceed platform slice representation")
	}
	expected, err := hex.DecodeString(grant.SHA256)
	if err != nil {
		return nil, err
	}
	content := []byte{}
	digest := sha256.New()
	var offset int64
	for offset < grant.Total {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		page, err := materialExchange(ctx, grant, "read", offset, nil)
		if err != nil {
			return nil, err
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		n := min(int64(grant.ChunkBytes), grant.Total-offset)
		if page.Token != grant.Token || page.Offset != offset || page.Next != offset+n || page.Finished || int64(len(page.Data)) != n {
			return nil, errors.New("material byte page mismatch")
		}
		content = append(content, page.Data...)
		_, _ = digest.Write(page.Data)
		offset = page.Next
	}
	if !bytes.Equal(digest.Sum(nil), expected) {
		return nil, errors.New("material byte digest mismatch")
	}
	finished, err := materialExchange(ctx, grant, "finish", offset, nil)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if finished.Token != grant.Token || finished.Offset != offset || finished.Next != offset || !finished.Finished || len(finished.Data) != 0 {
		return nil, errors.New("material finish acknowledgement mismatch")
	}
	return content, nil
}

func (c LocalTorrentBackup) prepareMaterial(ctx context.Context, input LocalTorrentBackupRequest) (LocalTorrentBackupResult, error) {
	var zero LocalTorrentBackupResult
	request, err := json.Marshal(struct {
		Operation string                    `json:"op"`
		Input     LocalTorrentBackupRequest `json:"input"`
	}{"prepare_material", input})
	if err != nil {
		return zero, err
	}
	if len(request) > 128<<10 {
		return zero, errors.New("local torrent backup input exceeds host limit")
	}
	raw, err := c.Host(ctx, LocalTorrentBackupCapability, request)
	if err != nil {
		return zero, err
	}
	if err := ctx.Err(); err != nil {
		return zero, err
	}
	var reply struct {
		Skipped   *bool          `json:"skipped"`
		Temporary *bool          `json:"temporary"`
		Material  *MaterialGrant `json:"material,omitempty"`
	}
	if err := torrentMaterialDecode(raw, &reply); err != nil {
		return zero, err
	}
	if materialUniqueJSON(raw) != nil {
		return zero, errors.New("invalid local backup material result")
	}
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(raw, &fields)
	if reply.Skipped == nil || reply.Temporary == nil {
		return zero, errors.New("incomplete local backup material result")
	}
	if *reply.Skipped {
		if *reply.Temporary || len(fields) != 2 {
			return zero, errors.New("mixed skipped local backup material result")
		}
		return LocalTorrentBackupResult{Skipped: true}, nil
	}
	if len(fields) != 3 || reply.Material == nil || !materialExactFields(fields["material"], "contract", "token", "invocation", "chunk_bytes", "capability", "direction", "kind", "total", "sha256") {
		return zero, errors.New("incomplete local backup material grant")
	}
	content, err := sharedReadMaterialBytes(ctx, *reply.Material, LocalTorrentBackupCapability)
	if err != nil {
		return zero, err
	}
	return LocalTorrentBackupResult{Content: content, Temporary: *reply.Temporary}, nil
}
