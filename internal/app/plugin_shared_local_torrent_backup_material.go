package app

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"path/filepath"
	"strings"
	"sync"
	"unicode/utf8"

	"light-media/internal/materialio"
	"light-media/internal/plugins"
)

// This is the existing S02 operation followed by an invocation-owned transport
// spool. It never replaces or removes the source-visible rebuilt hash.torrent.
// Framework candidate06 must supply CheckMaterialHost; no local ticket imitation
// or fake grant factory is provided by this consumer.
func (a *App) prepareOriginalLocalTorrentBackupMaterial(ctx context.Context, scope originalBaseCallScope, input plugins.LocalTorrentBackupRequest) (response json.RawMessage, err error) {
	if err = plugins.CheckMaterialHost(ctx, plugins.LocalTorrentBackupCapability); err != nil {
		return nil, err
	}
	if input.DownloaderID == "" || !utf8.ValidString(input.DownloaderID) || !utf8.ValidString(input.Hash) || input.Hash == "" || input.Hash == "." || input.Hash == ".." || strings.ContainsAny(input.Hash, "/\\\x00") || !importPath(input.Directory) {
		return nil, bad("INVALID_PLUGIN_VALUE", "本机种子材料需要下载器、受管目录和准确文件名")
	}
	selected, err := a.configuredDownloader(input.DownloaderID)
	if err != nil {
		return nil, err
	}
	// Capture the same host-selected config/root authority before source IO.
	// The source adapter independently checks it at every original filesystem op.
	authority := &originalLocalTorrentBackupIO{app: a, scope: scope, request: input, downloader: selected}
	if err = authority.check(ctx); err != nil {
		return nil, err
	}
	result, err := a.prepareOriginalLocalTorrentBackup(ctx, scope, input)
	if current := authority.check(ctx); current != nil {
		return nil, current
	}
	if err != nil {
		return nil, err
	}
	if result.Skipped {
		if result.Temporary || len(result.Content) != 0 {
			return nil, bad("INVALID_PLUGIN_VALUE", "本机种子材料跳过结果无效")
		}
		return json.Marshal(map[string]bool{"skipped": true, "temporary": false})
	}
	// This private transport root is deliberately not the S02 reconstruction
	// sandbox or S12 persistent cache. Only exact created spool files are closed.
	directory := filepath.Join(a.Dir, "temp", "plugin-material-spool")
	parent, err := importDir(directory, true)
	if err != nil {
		return nil, err
	}
	guard := func(callCtx context.Context) error {
		if err := authority.check(callCtx); err != nil {
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
	defer func() {
		if !transferred {
			err = errors.Join(err, closeOwner())
			if err != nil {
				response = nil
			}
		}
	}()
	size, err := spool.CopyFrom(ctx, bytes.NewReader(result.Content))
	if err != nil {
		return nil, err
	}
	summary, err := spool.Snapshot(ctx)
	if err != nil {
		return nil, err
	}
	if size != int64(len(result.Content)) || summary.Size != size {
		return nil, materialio.ErrLength
	}
	reader, err := spool.Claim(ctx, summary.Size, summary.SHA256)
	if err != nil {
		return nil, err
	}
	grant, err := plugins.IssueMaterial(ctx, plugins.MaterialSpec{Capability: plugins.LocalTorrentBackupCapability, Direction: plugins.MaterialToWorker, Kind: "bytes", Total: summary.Size, SHA256: hex.EncodeToString(summary.SHA256[:])}, plugins.MaterialPort{
		Read: func(readCtx context.Context, offset int64, n int) ([]byte, error) {
			if n < 1 || n > plugins.MaterialFrameBytes {
				return nil, bad("INVALID_PLUGIN_VALUE", "材料页长度无效")
			}
			page := make([]byte, n)
			got, err := reader.ReadAtContext(readCtx, page, offset)
			if err != nil {
				return nil, err
			}
			if got != n {
				return nil, io.ErrUnexpectedEOF
			}
			return page, nil
		}, Close: closeOwner,
	}, guard)
	if err != nil {
		return nil, err
	}
	response, err = json.Marshal(struct {
		Skipped   bool                  `json:"skipped"`
		Temporary bool                  `json:"temporary"`
		Material  plugins.MaterialGrant `json:"material"`
	}{false, result.Temporary, grant})
	if err != nil {
		return nil, err
	}
	if len(response) > plugins.MaxPayloadBytes {
		return nil, bad("PLUGIN_VALUE_TOO_LARGE", "本机材料控制结果超过已有传输上限")
	}
	if err = guard(ctx); err != nil {
		return nil, err
	}
	// Finish marks the stream consumed. This exact owner survives only until
	// invocation scope Close; no fourth control RPC or upload lifecycle change.
	transferred = true
	return response, nil
}
