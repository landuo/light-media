package plugins

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode/utf8"
)

// LocalTorrentBackupCapability prepares one exact local backup material, not an
// arbitrary ReadFile/path RPC or authority to add/delete downloader tasks.
const LocalTorrentBackupCapability = "downloader.backup.material"

// Directory is only a selector inside the host's current managed local roots.
// DownloaderID selects the current host configuration (which determines qB/TR).
// Hash retains case and spelling; the adapter accepts a safe single basename,
// not a normalized hash or path. Neither selector grants filesystem access.
type LocalTorrentBackupRequest struct {
	DownloaderID string `json:"downloader_id"`
	Directory    string `json:"directory"`
	Hash         string `json:"hash"`
}

// Successful empty bytes are nonnil; Skipped has nil Content. Temporary only
// identifies successfully selected rebuilt material, never a guarantee that a
// failed source write left no temporary file. Release is not disk deletion.
type LocalTorrentBackupResult struct {
	Content            []byte
	Skipped, Temporary bool
}

type LocalTorrentBackup struct{ Host HostCaller }

// Prepare is one public logical operation. Private prepare/read/release frames
// reuse the existing 1MiB chunks, 16MiB material bound, strict decoder and opaque
// token shape. No TorrentInfo, folder/files, or reusable host pathname is exposed.
func (c LocalTorrentBackup) Prepare(ctx context.Context, input LocalTorrentBackupRequest) (out LocalTorrentBackupResult, err error) {
	if ctx == nil || c.Host == nil {
		return out, errors.New("local torrent backup requires an active context and host caller")
	}
	if err = ctx.Err(); err != nil {
		return out, err
	}
	if !utf8.ValidString(input.DownloaderID) || !utf8.ValidString(input.Directory) || !utf8.ValidString(input.Hash) {
		return out, errors.New("local torrent backup requires valid Unicode strings")
	}
	if input.Hash == "" || input.Hash == "." || input.Hash == ".." || strings.ContainsAny(input.Hash, "/\\\x00") {
		return out, errors.New("local torrent backup requires one safe hash basename")
	}
	request, err := json.Marshal(struct {
		Operation string                    `json:"op"`
		Input     LocalTorrentBackupRequest `json:"input"`
	}{"prepare", input})
	if err != nil {
		return out, err
	}
	if len(request) > 128<<10 {
		return out, errors.New("local torrent backup input exceeds host limit")
	}
	raw, err := c.Host(ctx, LocalTorrentBackupCapability, request)
	if err != nil {
		return out, err
	}
	// A malformed prepare may still allocate a valid invocation-owned handle.
	// Best-effort release uses only this same callback; scope close is the final
	// owner of reclamation and cleanup cannot restore expired authority.
	var cleanup struct {
		Handle string `json:"handle"`
	}
	if len(raw) <= MaxPayloadBytes && utf8.Valid(raw) && json.Unmarshal(raw, &cleanup) == nil && torrentMaterialHandlePattern.MatchString(cleanup.Handle) {
		defer func() {
			cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
			defer cancel()
			payload, _ := json.Marshal(struct {
				Operation string `json:"op"`
				Handle    string `json:"handle"`
			}{"release", cleanup.Handle})
			released, releaseErr := c.Host(cleanupCtx, LocalTorrentBackupCapability, payload)
			if releaseErr == nil {
				var receipt struct {
					Released *bool `json:"released"`
				}
				releaseErr = torrentMaterialDecode(released, &receipt)
				if releaseErr == nil && (receipt.Released == nil || !*receipt.Released) {
					releaseErr = errors.New("local torrent backup release was not acknowledged")
				}
			}
			if err == nil && ctx.Err() != nil {
				err = ctx.Err()
			}
			if err == nil {
				err = releaseErr
			}
			if err != nil {
				out = LocalTorrentBackupResult{}
			}
		}()
	}
	if err = ctx.Err(); err != nil {
		return out, err
	}
	var prepared struct {
		Skipped   *bool   `json:"skipped"`
		Temporary *bool   `json:"temporary"`
		Handle    *string `json:"handle,omitempty"`
		Total     *int    `json:"total,omitempty"`
		SHA256    *string `json:"sha256,omitempty"`
	}
	if err = torrentMaterialDecode(raw, &prepared); err != nil {
		return out, err
	}
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(raw, &fields)
	if prepared.Skipped == nil || prepared.Temporary == nil {
		return out, errors.New("incomplete local torrent backup result")
	}
	if *prepared.Skipped {
		if *prepared.Temporary || len(fields) != 2 {
			return out, errors.New("mixed skipped local torrent backup result")
		}
		return LocalTorrentBackupResult{Skipped: true}, nil
	}
	if len(fields) != 5 || prepared.Handle == nil || !torrentMaterialHandlePattern.MatchString(*prepared.Handle) || prepared.Total == nil || *prepared.Total < 0 || *prepared.Total > TorrentMaterialMaxBytes || prepared.SHA256 == nil {
		return out, errors.New("invalid local torrent backup byte transfer")
	}
	digest, decodeErr := hex.DecodeString(*prepared.SHA256)
	if decodeErr != nil || len(digest) != sha256.Size || strings.ToLower(*prepared.SHA256) != *prepared.SHA256 {
		return out, errors.New("invalid local torrent backup digest")
	}
	content := make([]byte, 0, *prepared.Total)
	for len(content) < *prepared.Total {
		if err = ctx.Err(); err != nil {
			return out, err
		}
		read, _ := json.Marshal(struct {
			Operation string `json:"op"`
			Handle    string `json:"handle"`
			Offset    int    `json:"offset"`
		}{"read", *prepared.Handle, len(content)})
		raw, err = c.Host(ctx, LocalTorrentBackupCapability, read)
		if err != nil {
			return out, err
		}
		if err = ctx.Err(); err != nil {
			return out, err
		}
		var chunk struct {
			Handle *string         `json:"handle"`
			Offset *int            `json:"offset"`
			Data   json.RawMessage `json:"data"`
		}
		if err = torrentMaterialDecode(raw, &chunk); err != nil {
			return out, err
		}
		if chunk.Handle == nil || *chunk.Handle != *prepared.Handle || chunk.Offset == nil || *chunk.Offset != len(content) || len(chunk.Data) == 0 || chunk.Data[0] != '"' {
			return out, errors.New("local torrent backup chunk identity mismatch")
		}
		var data []byte
		if err = json.Unmarshal(chunk.Data, &data); err != nil {
			return out, err
		}
		if len(data) != min(TorrentMaterialChunkBytes, *prepared.Total-len(content)) {
			return out, errors.New("local torrent backup chunk length mismatch")
		}
		content = append(content, data...)
	}
	actual := sha256.Sum256(content)
	if !bytes.Equal(actual[:], digest) {
		return out, errors.New("local torrent backup digest mismatch")
	}
	return LocalTorrentBackupResult{Content: content, Temporary: *prepared.Temporary}, nil
}
