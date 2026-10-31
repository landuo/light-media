package app

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"unicode/utf8"

	"golang.org/x/sys/unix"

	"light-media/internal/plugins"
	"light-media/internal/plugins/torrenttransfer"
)

// This adapter supplies only the six purpose-bound IO operations used by the
// source material component. It grants no worker pathname/read/write capability.
type originalLocalTorrentBackupIO struct {
	app          *App
	scope        originalBaseCallScope
	request      plugins.LocalTorrentBackupRequest
	downloader   DownloaderConfig
	temporary    string
	releaseTemp  func()
	authorityErr error
}

type originalLocalBackupPathLock struct {
	mu    sync.Mutex
	users int
}

var originalLocalBackupPathLocks struct {
	sync.Mutex
	paths map[string]*originalLocalBackupPathLock
}

// Serialize only a shared material pathname's write-through-final-read. The
// registry has no persistent entries once all current users have left. Waiting
// for a path never holds App.pluginMu (the next reader needs that short lock).
func lockOriginalLocalBackupPath(path string) func() {
	locks := &originalLocalBackupPathLocks
	locks.Lock()
	if locks.paths == nil {
		locks.paths = make(map[string]*originalLocalBackupPathLock)
	}
	lock := locks.paths[path]
	if lock == nil {
		lock = &originalLocalBackupPathLock{}
		locks.paths[path] = lock
	}
	lock.users++
	locks.Unlock()
	lock.mu.Lock()
	return func() {
		lock.mu.Unlock()
		locks.Lock()
		lock.users--
		if lock.users == 0 {
			delete(locks.paths, path)
		}
		locks.Unlock()
	}
}

func (a *App) prepareOriginalLocalTorrentBackup(ctx context.Context, scope originalBaseCallScope, request plugins.LocalTorrentBackupRequest) (torrenttransfer.LocalBackupMaterial, error) {
	if ctx == nil || scope.validate == nil {
		return torrenttransfer.LocalBackupMaterial{}, bad("PLUGIN_CAPABILITY_DENIED", "本机种子材料缺少实际调用范围")
	}
	if request.DownloaderID == "" || !utf8.ValidString(request.DownloaderID) || !utf8.ValidString(request.Hash) || request.Hash == "" || request.Hash == "." || request.Hash == ".." || strings.ContainsAny(request.Hash, "/\\\x00") || !importPath(request.Directory) {
		return torrenttransfer.LocalBackupMaterial{}, bad("INVALID_PLUGIN_VALUE", "本机种子材料需要下载器、受管目录和准确文件名")
	}
	if err := scope.validate(ctx); err != nil {
		return torrenttransfer.LocalBackupMaterial{}, err
	}
	configured, err := a.configuredDownloader(request.DownloaderID)
	if err != nil {
		return torrenttransfer.LocalBackupMaterial{}, err
	}
	qb := false
	switch configured.Kind {
	case "qb", "qbittorrent":
		qb = true
	case "tr", "transmission":
	default:
		return torrenttransfer.LocalBackupMaterial{}, bad("DOWNLOADER_UNSUPPORTED", "下载器类型不支持本机备份材料")
	}
	port := &originalLocalTorrentBackupIO{app: a, scope: scope, request: request, downloader: configured}
	defer func() {
		if port.releaseTemp != nil {
			port.releaseTemp()
		}
	}()
	result, err := torrenttransfer.PrepareLocalBackupMaterial(ctx, torrenttransfer.LocalBackupMaterialInput{
		QBittorrent: qb, Hash: request.Hash,
		TorrentLabel: port.backupPath(".torrent"), ResumeLabel: port.backupPath(".fastresume"),
	}, port, func(entry torrenttransfer.Log) {
		a.logOriginalPluginContext(ctx, entry.Level, "local_torrent_backup", entry.Message)
	})
	// The source catches ordinary resume IO failures. Revoked authority, stale
	// configuration, cancellation and existing filesystem guards are never turned
	// into a successful source skip by that catch. No bytes escape a stale call.
	if port.authorityErr != nil {
		return torrenttransfer.LocalBackupMaterial{}, port.authorityErr
	}
	if checkErr := port.check(ctx); checkErr != nil {
		return torrenttransfer.LocalBackupMaterial{}, checkErr
	}
	return result, err
}

func (p *originalLocalTorrentBackupIO) backupPath(extension string) string {
	return filepath.Join(p.request.Directory, p.request.Hash+extension)
}

func (p *originalLocalTorrentBackupIO) check(ctx context.Context) error {
	err := ctx.Err()
	if err == nil {
		err = p.scope.validate(ctx)
	}
	if err == nil {
		var current DownloaderConfig
		current, err = p.app.configuredDownloader(p.request.DownloaderID)
		if err == nil && current.Config != p.downloader.Config {
			err = bad("DOWNLOADER_CHANGED", "下载器配置已变化，请重新读取本机材料")
		}
	}
	if err == nil {
		for _, path := range []string{p.request.Directory, p.backupPath(".torrent"), p.backupPath(".fastresume")} {
			if err = p.app.sharedLocalStorageAllowed(path); err != nil {
				break
			}
		}
	}
	if err != nil && p.authorityErr == nil {
		p.authorityErr = err
	}
	return err
}

func (p *originalLocalTorrentBackupIO) ioError(err error) error {
	if errors.Is(err, unix.ELOOP) {
		err = bad("INVALID_FILE", "种子材料不能包含符号链接")
	}
	if err != nil && sharedLocalStorageGuard(err) && p.authorityErr == nil {
		p.authorityErr = err
	}
	return err
}

func (p *originalLocalTorrentBackupIO) exists(ctx context.Context, path string) (bool, error) {
	p.app.pluginMu.Lock()
	defer p.app.pluginMu.Unlock()
	if err := p.check(ctx); err != nil {
		return false, err
	}
	parent, info, err := sharedLocalStorageStat(path)
	if parent != nil {
		defer parent.Close()
	}
	if checkErr := p.check(ctx); checkErr != nil {
		return false, checkErr
	}
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, p.ioError(err)
	}
	if !info.Mode().IsRegular() {
		return false, p.ioError(bad("INVALID_FILE", "备份材料必须是普通文件"))
	}
	return true, nil
}

func (p *originalLocalTorrentBackupIO) read(ctx context.Context, path string) ([]byte, error) {
	p.app.pluginMu.Lock()
	defer p.app.pluginMu.Unlock()
	if err := p.check(ctx); err != nil {
		return nil, err
	}
	parent, info, err := sharedLocalStorageStat(path)
	if parent != nil {
		defer parent.Close()
	}
	if err != nil {
		return nil, p.ioError(err)
	}
	if !info.Mode().IsRegular() {
		return nil, p.ioError(bad("INVALID_FILE", "种子材料必须是普通文件"))
	}
	if info.Size() > plugins.TorrentMaterialMaxBytes {
		return nil, p.ioError(bad("PLUGIN_VALUE_TOO_LARGE", "种子材料超过 16 MiB 上限"))
	}
	if err := sharedLocalStorageSameDirectory(filepath.Dir(path), parent); err != nil {
		return nil, p.ioError(err)
	}
	fd, err := unix.Openat(int(parent.Fd()), filepath.Base(path), unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, p.ioError(err)
	}
	file := os.NewFile(uintptr(fd), path)
	defer file.Close()
	opened, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !opened.Mode().IsRegular() || !os.SameFile(info, opened) {
		return nil, p.ioError(bad("FILE_CHANGED", "种子材料在打开时已变化"))
	}
	content, err := io.ReadAll(io.LimitReader(file, plugins.TorrentMaterialMaxBytes+1))
	if err != nil {
		return nil, err
	}
	if len(content) > plugins.TorrentMaterialMaxBytes {
		return nil, p.ioError(bad("PLUGIN_VALUE_TOO_LARGE", "种子材料超过 16 MiB 上限"))
	}
	if err := sharedLocalStorageSameDirectory(filepath.Dir(path), parent); err != nil {
		return nil, p.ioError(err)
	}
	if err := p.check(ctx); err != nil {
		return nil, err
	}
	return content, nil
}

func (p *originalLocalTorrentBackupIO) TorrentExists(ctx context.Context) (bool, error) {
	return p.exists(ctx, p.backupPath(".torrent"))
}
func (p *originalLocalTorrentBackupIO) ReadTorrent(ctx context.Context) ([]byte, error) {
	return p.read(ctx, p.backupPath(".torrent"))
}
func (p *originalLocalTorrentBackupIO) ResumeExists(ctx context.Context) (bool, error) {
	return p.exists(ctx, p.backupPath(".fastresume"))
}
func (p *originalLocalTorrentBackupIO) ReadResume(ctx context.Context) ([]byte, error) {
	return p.read(ctx, p.backupPath(".fastresume"))
}

func (p *originalLocalTorrentBackupIO) WriteTemporary(ctx context.Context, content []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if p.temporary != "" {
		return errors.New("local backup temporary material was already selected")
	}
	base := filepath.Join(p.app.Dir, "temp", "plugin-torrent-backup")
	p.temporary = filepath.Join(base, p.request.Hash+".torrent")
	p.releaseTemp = lockOriginalLocalBackupPath(p.temporary)
	p.app.pluginMu.Lock()
	defer p.app.pluginMu.Unlock()
	if err := p.check(ctx); err != nil {
		return err
	}
	if len(content) > plugins.TorrentMaterialMaxBytes {
		return p.ioError(bad("PLUGIN_VALUE_TOO_LARGE", "重建种子材料超过 16 MiB 上限"))
	}
	parent, err := importDir(base, true)
	if err != nil {
		return p.ioError(err)
	}
	defer parent.Close()
	if err := sharedLocalStorageSameDirectory(base, parent); err != nil {
		return p.ioError(err)
	}
	if err := p.check(ctx); err != nil {
		return err
	}
	// Match the original deterministic <temp>/<hash>.torrent replacement, but
	// within this App-owned S02 sandbox. Check the opened type before truncation;
	// no following links, FIFO blocking, or arbitrary worker writes are allowed.
	fd, err := unix.Openat(int(parent.Fd()), filepath.Base(p.temporary), unix.O_WRONLY|unix.O_CREAT|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0600)
	if err != nil {
		return p.ioError(err)
	}
	file := os.NewFile(uintptr(fd), p.temporary)
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		file.Close()
		if err != nil {
			return err
		}
		return p.ioError(bad("INVALID_FILE", "重建材料必须是普通文件"))
	}
	if err := file.Truncate(0); err != nil {
		file.Close()
		return err
	}
	// Failures may now leave a truncated/partial owned material. Never unlink it
	// or equate a source skip with a guarantee that no temporary file exists.
	_, writeErr := importCopy(ctx, bytes.NewReader(content), file, func() error { return p.check(ctx) })
	if writeErr == nil {
		writeErr = file.Sync()
	}
	closeErr := file.Close()
	if writeErr == nil {
		writeErr = closeErr
	}
	if writeErr == nil {
		writeErr = parent.Sync()
	}
	if err := p.check(ctx); err != nil {
		return err
	}
	return p.ioError(writeErr)
}

func (p *originalLocalTorrentBackupIO) ReadTemporary(ctx context.Context) ([]byte, error) {
	if p.temporary == "" {
		return nil, errors.New("local backup temporary material was not selected")
	}
	return p.read(ctx, p.temporary)
}
