package plugins

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"
)

// Only the existing typed bytes Add path may use this negotiated bridge.
// Arguments and bytes are frozen before allocating the invocation-owned handle;
// public AddTorrent results and URL/magnet/ordinary argument behavior stay old.
func downloaderCommandAddMaterial(ctx context.Context, host HostCaller, id, capability string, args map[string]any, payload []byte) (out json.RawMessage, err error) {
	content := append([]byte{}, payload...)
	sum := sha256.Sum256(content)
	digest := hex.EncodeToString(sum[:])
	begin, err := json.Marshal(struct {
		Op           string         `json:"op"`
		DownloaderID string         `json:"downloader_id"`
		Args         map[string]any `json:"args"`
		Total        int64          `json:"total"`
		SHA256       string         `json:"sha256"`
	}{"upload_material_begin", id, args, int64(len(content)), digest})
	if err != nil {
		return nil, err
	}
	raw, err := downloaderCommandCall(ctx, host, capability, begin, downloaderCommandRequestMaxBytes)
	if err != nil {
		return nil, err
	}
	fields, err := downloaderCommandObject(raw, "handle", "material")
	if err != nil || materialUniqueJSON(raw) != nil {
		return nil, errors.New("invalid downloader material receipt")
	}
	var handle string
	var grant MaterialGrant
	if json.Unmarshal(fields["handle"], &handle) != nil || !downloaderCommandHandle(handle) || !materialExactFields(fields["material"], "contract", "token", "invocation", "chunk_bytes", "capability", "direction", "kind", "total", "sha256") || strictJSON(fields["material"], &grant) != nil {
		return nil, errors.New("invalid downloader material grant")
	}
	submitted := false
	defer func() {
		if submitted {
			return
		}
		// Reclamation is never selected-effect rollback. Scope Close is the final
		// owner even if cancellation makes this ordinary abort impossible.
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
		defer cancel()
		packet, _ := json.Marshal(struct {
			Op     string `json:"op"`
			Handle string `json:"handle"`
		}{"upload_material_abort", handle})
		_, _ = host(cleanup, capability, packet)
	}()
	if !validMaterialSpec(grant.MaterialSpec) || grant.Contract != MaterialContract || grant.Capability != capability || grant.Direction != MaterialToHost || grant.Kind != "bytes" || grant.Total != int64(len(content)) || grant.SHA256 != digest || grant.ChunkBytes < 1 || grant.ChunkBytes > MaterialFrameBytes {
		return nil, errors.New("downloader material grant mismatch")
	}
	for offset := int64(0); offset < grant.Total; {
		if err = ctx.Err(); err != nil {
			return nil, err
		}
		next := offset + min(int64(grant.ChunkBytes), grant.Total-offset)
		reply, e := materialExchange(ctx, grant, "write", offset, content[int(offset):int(next)])
		if e != nil {
			return nil, e
		}
		if reply.Token != grant.Token || reply.Offset != offset || reply.Next != next || reply.Finished || len(reply.Data) != 0 {
			return nil, errors.New("downloader material page mismatch")
		}
		offset = next
	}
	finished, err := materialExchange(ctx, grant, "finish", grant.Total, nil)
	if err != nil {
		return nil, err
	}
	if finished.Token != grant.Token || finished.Offset != grant.Total || finished.Next != grant.Total || !finished.Finished || len(finished.Data) != 0 {
		return nil, errors.New("downloader material finish mismatch")
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	submit, _ := json.Marshal(struct {
		Op     string `json:"op"`
		Handle string `json:"handle"`
	}{"upload_material_submit", handle})
	// Once any submit is attempted, an unknown or malformed result cannot replay
	// submit/invoke or trigger abort. The host independently claims size+SHA once.
	submitted = true
	out, err = host(ctx, capability, submit)
	if err != nil {
		return nil, err
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	return out, nil
}
