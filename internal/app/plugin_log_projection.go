package app

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/sys/unix"
)

// Fixed LogConfigModel + RotatingFileHandler: 5MiB/current and10 backups.
// This projection is written inside the existing sole writer after its normal
// level/queue/redaction boundary. It has no separate queue, daemon or logger.
const pluginProjectionMaxBytes int64 = 5 << 20
const pluginProjectionBackups = 10
const pluginProjectionDownloadFiles = 10

func pluginProjectionName(instance string) (string, error) {
	if !pluginStorageID.MatchString(instance) {
		return "", &APIError{"NOT_FOUND", "插件日志不存在", 404}
	}
	return strings.ToLower(instance) + ".log", nil
}
func (a *App) pluginProjectionDirectory(create bool) (int, error) {
	root, err := unix.Open(filepath.Join(a.Dir, "logs"), unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return -1, err
	}
	defer unix.Close(root)
	if create {
		if err = unix.Mkdirat(root, "plugins", 0700); err != nil && !errors.Is(err, unix.EEXIST) {
			return -1, err
		}
	}
	return unix.Openat(root, "plugins", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
}
func pluginProjectionOpen(dir int, name string, flags int) (*os.File, error) {
	var before unix.Stat_t
	if err := unix.Fstatat(dir, name, &before, unix.AT_SYMLINK_NOFOLLOW); err == nil {
		if before.Mode&unix.S_IFMT != unix.S_IFREG {
			return nil, unix.ELOOP
		}
	} else if !errors.Is(err, unix.ENOENT) || flags&unix.O_CREAT == 0 {
		return nil, err
	}
	fd, err := unix.Openat(dir, name, flags|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0600)
	if err != nil {
		return nil, err
	}
	var st unix.Stat_t
	if unix.Fstat(fd, &st) != nil || st.Mode&unix.S_IFMT != unix.S_IFREG {
		unix.Close(fd)
		return nil, errors.New("plugin log must be regular")
	}
	return os.NewFile(uintptr(fd), "plugin-log-projection"), nil
}
func pluginProjectionText(entry map[string]any) string {
	level, _ := entry["level"].(string)
	if level == "WARN" {
		level = "WARNING"
	}
	stamp, _ := entry["time"].(string)
	at, err := time.Parse(time.RFC3339Nano, stamp)
	if err != nil {
		at = time.Now()
	}
	module, _ := entry["module"].(string)
	message, _ := entry["message"].(string)
	// Source caller_name is adapted to the actual Go module attribution; no
	// Python filename/stack is fabricated. Host timestamp remains authoritative.
	return fmt.Sprintf("【%s】%s - %s - %s", level, at.Local().Format("2006-01-02 15:04:05,000"), module, message)
}
func (l *runtimeLogger) writePluginProjection(raw []byte) error {
	var entry map[string]any
	if json.Unmarshal(raw, &entry) != nil {
		return errors.New("invalid host log")
	}
	instance, _ := entry["plugin_instance_id"].(string)
	if instance == "" {
		return nil
	}
	name, err := pluginProjectionName(instance)
	if err != nil {
		return err
	}
	dir, err := l.app.pluginProjectionDirectory(true)
	if err != nil {
		return err
	}
	defer unix.Close(dir)
	f, err := pluginProjectionOpen(dir, name, unix.O_CREAT|unix.O_APPEND|unix.O_WRONLY)
	if err != nil {
		return err
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return err
	}
	line := pluginProjectionText(entry) + "\n"
	mib, backups := l.app.pluginProjectionBudget()
	maxBytes := new(big.Int).Mul(mib, big.NewInt(1<<20))
	proposed := new(big.Int).Add(big.NewInt(info.Size()), big.NewInt(int64(utf8.RuneCountInString(line))))
	// CPython's handler adds len(formatted_unicode_message) to tell() bytes.
	// Its >= threshold and empty-file exception are retained, including Unicode.
	if maxBytes.Sign() > 0 && info.Size() > 0 && proposed.Cmp(maxBytes) >= 0 {
		f.Close()
		if err := pluginProjectionRollover(dir, name, backups); err != nil {
			return err
		}
		f, err = pluginProjectionOpen(dir, name, unix.O_CREAT|unix.O_APPEND|unix.O_WRONLY)
		if err != nil {
			return err
		}
	}
	defer f.Close()
	n, err := io.WriteString(f, line)
	if err == nil && n != len(line) {
		err = io.ErrShortWrite
	}
	return err
}

func pluginProjectionLeaves(dir int) ([]string, error) {
	fd, err := unix.Dup(dir)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), "plugin-log-directory")
	defer f.Close()
	return f.Readdirnames(-1)
}

func pluginProjectionRollover(dir int, name string, count *big.Int) error {
	// backupCount<=0 closes/reopens the append stream without renaming or
	// truncating it. A source-sized arbitrary integer must not overflow a Go int.
	if count.Sign() <= 0 {
		return nil
	}
	names, err := pluginProjectionLeaves(dir)
	if err != nil {
		return err
	}
	type numbered struct {
		name  string
		index *big.Int
	}
	items := []numbered{}
	for _, leaf := range names {
		if !strings.HasPrefix(leaf, name+".") {
			continue
		}
		suffix := strings.TrimPrefix(leaf, name+".")
		n, ok := new(big.Int).SetString(suffix, 10)
		if ok && n.Sign() > 0 && n.String() == suffix && n.Cmp(count) < 0 {
			items = append(items, numbered{leaf, n})
		}
	}
	// These are exactly the existing files visited by the source descending
	// range. Enumerating their canonical indexes avoids an unbounded empty scan
	// for a valid arbitrary-precision Pydantic backupCount.
	sort.Slice(items, func(i, j int) bool { return items[i].index.Cmp(items[j].index) > 0 })
	for _, item := range items {
		next := new(big.Int).Add(item.index, big.NewInt(1))
		to := name + "." + next.String()
		if err := unix.Unlinkat(dir, to, 0); err != nil && !errors.Is(err, unix.ENOENT) {
			return err
		}
		if err := unix.Renameat(dir, item.name, dir, to); err != nil {
			return err
		}
	}
	if err := unix.Unlinkat(dir, name+".1", 0); err != nil && !errors.Is(err, unix.ENOENT) {
		return err
	}
	return unix.Renameat(dir, name, dir, name+".1")
}

// Startup runs before the existing writer goroutine starts. A small pending
// manifest owns only previously absent per-instance projections; interrupted
// replay removes those owned leaves and deterministically replays retained
// redacted spool records. Existing native projections are authoritative.
func (l *runtimeLogger) bootstrapPluginProjections() error {
	const doneName = ".plugin-projection-v1"
	root := filepath.Join(l.app.Dir, "logs")
	done := filepath.Join(root, doneName)
	pending := done + ".pending"
	if raw, err := os.ReadFile(done); err == nil {
		var receipt struct {
			Version    int
			Unreadable int `json:"unreadable_legacy_lines"`
		}
		if json.Unmarshal(raw, &receipt) != nil || receipt.Version != 1 {
			return errors.New("invalid plugin projection receipt")
		}
		l.pluginProjectionLegacyGap = receipt.Unreadable > 0
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	dir, err := l.app.pluginProjectionDirectory(true)
	if err != nil {
		return err
	}
	defer unix.Close(dir)
	if raw, err := os.ReadFile(pending); err == nil {
		var owned []string
		if json.Unmarshal(raw, &owned) != nil {
			return errors.New("invalid plugin log replay receipt")
		}
		for _, name := range owned {
			if !strings.HasSuffix(name, ".log") {
				return errors.New("invalid plugin log replay name")
			}
			if _, err := pluginProjectionName(strings.TrimSuffix(name, ".log")); err != nil {
				return err
			}
			leaves, err := pluginProjectionLeaves(dir)
			if err != nil {
				return err
			}
			for _, leaf := range leaves {
				if leaf != name {
					if !strings.HasPrefix(leaf, name+".") {
						continue
					}
					suffix := strings.TrimPrefix(leaf, name+".")
					n, ok := new(big.Int).SetString(suffix, 10)
					if !ok || n.Sign() <= 0 || n.String() != suffix {
						continue
					}
				}
				if err := unix.Unlinkat(dir, leaf, 0); err != nil && !errors.Is(err, unix.ENOENT) {
					return err
				}
			}
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	files, err := l.app.logFiles()
	if err != nil {
		return err
	}
	sort.SliceStable(files, func(i, j int) bool {
		if files[i].Name == "app.jsonl" {
			return false
		}
		if files[j].Name == "app.jsonl" {
			return true
		}
		if files[i].Modified.Equal(files[j].Modified) {
			return files[i].Name < files[j].Name
		}
		return files[i].Modified.Before(files[j].Modified)
	})
	targets := map[string]bool{}
	badLines := 0
	scan := func(callback func([]byte, map[string]any) error) error {
		for _, file := range files {
			fd, err := unix.Open(filepath.Join(root, file.Name), unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
			if err != nil {
				return err
			}
			f := os.NewFile(uintptr(fd), "legacy-log-spool")
			reader := bufio.NewReaderSize(f, logRecordLimit+1)
			oversized := false
			for {
				raw, readErr := reader.ReadSlice('\n')
				if errors.Is(readErr, bufio.ErrBufferFull) {
					oversized = true
					continue
				}
				if oversized {
					// Retained legacy corruption cannot prevent the new host from
					// starting or allocate an unbounded line. Persist its gap once.
					badLines++
					oversized = false
				} else if len(raw) > 0 {
					var entry map[string]any
					if json.Unmarshal(raw, &entry) != nil {
						badLines++
					} else if err := callback(raw, entry); err != nil {
						f.Close()
						return err
					}
				}
				if readErr != nil {
					f.Close()
					if !errors.Is(readErr, io.EOF) {
						return readErr
					}
					break
				}
			}
		}
		return nil
	}
	if err = scan(func(_ []byte, entry map[string]any) error {
		instance, _ := entry["plugin_instance_id"].(string)
		name, err := pluginProjectionName(instance)
		if err != nil {
			return nil
		}
		if _, seen := targets[name]; seen {
			return nil
		}
		var st unix.Stat_t
		err = unix.Fstatat(dir, name, &st, unix.AT_SYMLINK_NOFOLLOW)
		if err == nil {
			targets[name] = false
			return nil
		}
		if !errors.Is(err, unix.ENOENT) {
			return err
		}
		targets[name] = true
		return nil
	}); err != nil {
		return err
	}
	// Preserve even an imported native backup-only namespace. No historical
	// spool replay may rotate over pre-existing plugin files.
	entries, err := os.ReadDir(filepath.Join(root, "plugins"))
	if err != nil {
		return err
	}
	for _, entry := range entries {
		for name := range targets {
			if entry.Name() == name || strings.HasPrefix(entry.Name(), name+".") {
				targets[name] = false
			}
		}
	}
	owned := []string{}
	for name, build := range targets {
		if build {
			owned = append(owned, name)
		}
	}
	sort.Strings(owned)
	raw, _ := json.Marshal(owned)
	atomic := func(path string, data []byte) error {
		f, err := os.CreateTemp(root, ".plugin-replay-")
		if err != nil {
			return err
		}
		defer os.Remove(f.Name())
		if _, err = f.Write(data); err != nil {
			f.Close()
			return err
		}
		if err = f.Sync(); err != nil {
			f.Close()
			return err
		}
		if err = f.Close(); err != nil {
			return err
		}
		return os.Rename(f.Name(), path)
	}
	if err = atomic(pending, raw); err != nil {
		return err
	}
	if err = scan(func(_ []byte, entry map[string]any) error {
		instance, _ := entry["plugin_instance_id"].(string)
		name, err := pluginProjectionName(instance)
		if err != nil || !targets[name] {
			return nil
		}
		safe := sanitizeLogEntry(entry, l.app.Settings())
		data, _ := json.Marshal(safe)
		return l.writePluginProjection(data)
	}); err != nil {
		return err
	}
	l.pluginProjectionLegacyGap = badLines > 0
	receipt, _ := json.Marshal(map[string]any{"version": 1, "projected_instances": len(owned), "unreadable_legacy_lines": badLines / 2})
	if err = atomic(done, receipt); err != nil {
		return err
	}
	if err = os.Remove(pending); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func (a *App) readPluginProjectionHistory(ctx context.Context, instance string) (logPage, string, error) {
	out := logPage{Entries: []map[string]any{}, Complete: true, Health: a.logs.health(), Retention: map[string]int{}}
	mib, backups := a.pluginProjectionBudget()
	if mib.IsInt64() {
		out.Retention["rotate_mib"] = int(mib.Int64())
	}
	if backups.IsInt64() {
		out.Retention["backup_files"] = int(backups.Int64())
	}
	flushCtx, cancel := context.WithTimeout(ctx, time.Second)
	flushed := a.logs.flush(flushCtx)
	cancel()
	if !flushed {
		return out, "", bad("PLUGIN_LOG_HISTORY_CHANGED", "日志仍在写入，请重试")
	}
	out.Gap = a.logs.pluginProjectionLegacyGap
	out.Complete = !out.Gap
	name, err := pluginProjectionName(instance)
	if err != nil {
		return out, "", err
	}
	a.logs.fileMu.Lock()
	defer a.logs.fileMu.Unlock()
	dir, err := a.pluginProjectionDirectory(false)
	if err != nil {
		return out, "", pluginProjectionReadError(err)
	}
	defer unix.Close(dir)
	f, err := pluginProjectionOpen(dir, name, unix.O_RDONLY)
	if err != nil {
		return out, "", pluginProjectionReadError(err)
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return out, "", err
	}
	size := info.Size()
	start := int64(0)
	if size > 100<<10 {
		start = size - (100 << 10)
	}
	raw, err := io.ReadAll(io.NewSectionReader(f, start, size-start))
	if err != nil {
		return out, "", err
	}
	if start > 0 {
		if i := bytes.IndexByte(raw, '\n'); i >= 0 {
			start += int64(i + 1)
			raw = raw[i+1:]
		}
	}
	lines := bytes.Split(raw, []byte("\n"))
	offset := start
	var stat unix.Stat_t
	_ = unix.Fstat(int(f.Fd()), &stat)
	for _, line := range lines {
		length := len(line) + 1
		line = bytes.TrimSpace(line)
		if len(line) > 0 {
			clean := strings.ToValidUTF8(string(line), "�")
			sum := sha256.Sum256([]byte(fmt.Sprintf("%d:%d:%d", stat.Dev, stat.Ino, offset)))
			message := clean
			if parts := strings.SplitN(clean, " - ", 3); len(parts) == 3 {
				message = parts[2]
			}
			out.Entries = append(out.Entries, map[string]any{"id": "pl_" + hex.EncodeToString(sum[:12]), "plugin_instance_id": instance, "message": message, "raw": clean})
		}
		offset += int64(length)
	}
	if len(out.Entries) > 50 {
		out.Entries = out.Entries[len(out.Entries)-50:]
	}
	for i, j := 0, len(out.Entries)-1; i < j; i, j = i+1, j-1 {
		out.Entries[i], out.Entries[j] = out.Entries[j], out.Entries[i]
	}
	return out, fmt.Sprintf("%s:%d", a.logs.boot, a.logs.pluginProjectionSequence), nil
}
func pluginProjectionReadError(err error) error {
	if errors.Is(err, os.ErrNotExist) || errors.Is(err, unix.ENOENT) || errors.Is(err, unix.ELOOP) {
		return &APIError{"NOT_FOUND", "插件日志不存在", 404}
	}
	return bad("PLUGIN_LOG_EXPORT_UNAVAILABLE", "插件日志不可读，请检查存储")
}
