package app

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"fmt"
	"golang.org/x/sys/unix"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Plugin logs are selected by the host's concrete instance, never a supplied
// filename. The underlying logger remains the sole writer and redactor.
func (a *App) pluginLogHTTP(w http.ResponseWriter, r *http.Request, path string) bool {
	parts := strings.Split(path, "/")
	if len(parts) < 2 || parts[1] != "logs" {
		return false
	}
	if len(parts) > 3 || !pluginStorageID.MatchString(parts[0]) {
		a.writeError(w, r, &APIError{"NOT_FOUND", "插件日志不存在", http.StatusNotFound})
		return true
	}
	if r.Method != http.MethodGet {
		a.writeError(w, r, &APIError{"METHOD_NOT_ALLOWED", "插件日志只允许读取", http.StatusMethodNotAllowed})
		return true
	}
	if _, err := a.pluginStore.Installation(r.Context(), parts[0]); err != nil {
		a.writeError(w, r, &APIError{"NOT_FOUND", "插件日志不存在", http.StatusNotFound})
		return true
	}
	instance := parts[0]
	q := r.URL.Query()
	if selected := q.Get("plugin_instance_id"); selected != "" && selected != instance {
		a.writeError(w, r, bad("PLUGIN_LOG_SCOPE_MISMATCH", "日志筛选与当前插件不一致"))
		return true
	}
	q.Set("plugin_instance_id", instance)
	// Scope comes from the URL, not an alternate implementation/run selector.
	q.Del("plugin_id")
	q.Del("plugin_run_id")
	copyRequest := r.Clone(r.Context())
	copyURL := *r.URL
	copyURL.RawQuery = q.Encode()
	copyRequest.URL = &copyURL
	if len(parts) == 2 {
		page, resume, err := a.readPluginProjectionHistory(r.Context(), instance)
		if err != nil {
			a.writeError(w, r, err)
		} else {
			writeJSON(w, r, map[string]any{"history": page, "resume_id": resume})
		}
		return true
	}

	switch parts[2] {
	case "stream":
		if _, _, err := a.readPluginProjectionHistory(r.Context(), instance); err != nil {
			a.writeError(w, r, err)
		} else {
			a.streamLogs(w, copyRequest)
		}
	case "file", "download":
		a.servePluginLogFile(w, r, instance, parts[2] == "download")
	default:
		a.writeError(w, r, &APIError{"NOT_FOUND", "插件日志操作不存在", http.StatusNotFound})
	}
	return true
}

// Open, then unlink a new private file. Open descriptors survive until close,
// including after rotation; cancellation or process exit leaves no export file.
func (a *App) pluginLogTemp() (*os.File, error) {
	f, err := os.CreateTemp(a.Dir, ".plugin-log-")
	if err != nil {
		return nil, err
	}
	if err := os.Remove(f.Name()); err != nil {
		_ = f.Close()
		return nil, err
	}
	return f, nil
}

const pluginLogSnapshotLimit int64 = 1 << 30

// Retained source files remain independent ZIP entries. Selection is current
// first, then backup modification time descending, limited to ten in total.
func (a *App) preparePluginLogFile(ctx context.Context, instance string, compressed bool) (*os.File, string, error) {
	if err := ctx.Err(); err != nil {
		return nil, "", err
	}
	flushCtx, cancel := context.WithTimeout(ctx, time.Second)
	flushed := a.logs.flush(flushCtx)
	cancel()
	if !flushed {
		return nil, "", bad("PLUGIN_LOG_HISTORY_CHANGED", "日志仍在写入，请重试")
	}
	leaf, err := pluginProjectionName(instance)
	if err != nil {
		return nil, "", err
	}
	type snapshot struct {
		name  string
		file  *os.File
		size  int64
		mtime time.Time
		mode  os.FileMode
	}
	selected := []snapshot{}
	defer func() {
		for _, item := range selected {
			item.file.Close()
		}
	}()
	capture := func() error {
		a.logs.fileMu.Lock()
		defer a.logs.fileMu.Unlock()
		dir, err := a.pluginProjectionDirectory(false)
		if err != nil {
			return pluginProjectionReadError(err)
		}
		defer unix.Close(dir)
		current, err := pluginProjectionOpen(dir, leaf, unix.O_RDONLY)
		if err == nil {
			info, e := current.Stat()
			if e != nil {
				current.Close()
				return e
			}
			selected = append(selected, snapshot{leaf, current, info.Size(), info.ModTime(), info.Mode()})
		} else if !compressed || !errors.Is(err, unix.ENOENT) {
			return pluginProjectionReadError(err)
		}
		if !compressed {
			return nil
		}
		entries, err := os.ReadDir(filepath.Join(a.Dir, "logs", "plugins"))
		if err != nil {
			return pluginProjectionReadError(err)
		}
		backups := []snapshot{}
		defer func() {
			for _, item := range backups {
				item.file.Close()
			}
		}()
		for _, entry := range entries {
			if !strings.HasPrefix(entry.Name(), leaf+".") {
				continue
			}
			var st unix.Stat_t
			if err := unix.Fstatat(dir, entry.Name(), &st, unix.AT_SYMLINK_NOFOLLOW); err != nil {
				return err
			}
			if st.Mode&unix.S_IFMT == unix.S_IFLNK {
				return pluginProjectionReadError(unix.ELOOP)
			}
			if st.Mode&unix.S_IFMT != unix.S_IFREG {
				continue
			}
			f, err := pluginProjectionOpen(dir, entry.Name(), unix.O_RDONLY)
			if err != nil {
				return pluginProjectionReadError(err)
			}
			info, err := f.Stat()
			if err != nil {
				f.Close()
				return err
			}
			backups = append(backups, snapshot{entry.Name(), f, info.Size(), info.ModTime(), info.Mode()})
		}
		sort.SliceStable(backups, func(i, j int) bool { return backups[i].mtime.After(backups[j].mtime) })
		remaining := pluginProjectionDownloadFiles - len(selected)
		if len(backups) > remaining {
			for _, b := range backups[remaining:] {
				b.file.Close()
			}
			backups = backups[:remaining]
		}
		selected = append(selected, backups...)
		backups = nil
		if len(selected) == 0 {
			return &APIError{"NOT_FOUND", "插件日志不存在", 404}
		}
		return nil
	}
	if err = capture(); err != nil {
		return nil, "", err
	}
	out, err := a.pluginLogTemp()
	if err != nil {
		return nil, "", err
	}
	success := false
	defer func() {
		if !success {
			out.Close()
		}
	}()
	name := leaf
	if compressed {
		name = strings.TrimSuffix(leaf, ".log") + "-logs-" + time.Now().Format("20060102-150405") + ".zip"
		stem := strings.TrimSuffix(name, ".zip")
		archive := zip.NewWriter(out)
		for _, item := range selected {
			if err := ctx.Err(); err != nil {
				archive.Close()
				return nil, "", err
			}
			at := item.mtime.Local()
			if at.Year() < 1980 || at.Year() > 2107 {
				archive.Close()
				return nil, "", errors.New("plugin log ZIP timestamp outside supported range")
			}
			header := &zip.FileHeader{Name: stem + "/" + item.name, Method: zip.Deflate, Modified: at.Truncate(2 * time.Second)}
			header.SetMode(item.mode)
			writer, err := archive.CreateHeader(header)
			if err != nil {
				archive.Close()
				return nil, "", err
			}
			n, err := io.Copy(writer, io.NewSectionReader(item.file, 0, item.size))
			if err != nil || n != item.size {
				archive.Close()
				return nil, "", bad("PLUGIN_LOG_HISTORY_CHANGED", "日志在读取时变化，请重试")
			}
		}
		if err := archive.Close(); err != nil {
			return nil, "", err
		}
	} else {
		if selected[0].size > pluginLogSnapshotLimit {
			return nil, "", bad("PLUGIN_LOG_EXPORT_TOO_LARGE", "插件日志超过导出限额")
		}
		raw, err := io.ReadAll(io.NewSectionReader(selected[0].file, 0, selected[0].size))
		if err != nil || int64(len(raw)) != selected[0].size {
			return nil, "", bad("PLUGIN_LOG_HISTORY_CHANGED", "日志在读取时变化，请重试")
		}
		lines := bytes.Split(raw, []byte("\n"))
		for i, j := 0, len(lines)-1; i < j; i, j = i+1, j-1 {
			lines[i], lines[j] = lines[j], lines[i]
		}
		if _, err = out.Write(bytes.Join(lines, []byte("\n"))); err != nil {
			return nil, "", err
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, "", err
	}
	if _, err := out.Seek(0, io.SeekStart); err != nil {
		return nil, "", err
	}
	success = true
	return out, name, nil
}

func (a *App) servePluginLogFile(w http.ResponseWriter, r *http.Request, instance string, compressed bool) {
	select {
	case a.logs.exportSlots <- struct{}{}:
		defer func() { <-a.logs.exportSlots }()
	default:
		a.writeError(w, r, &APIError{"PLUGIN_LOG_EXPORT_BUSY", "正在导出日志，请稍后重试", http.StatusTooManyRequests})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	f, name, err := a.preparePluginLogFile(ctx, instance, compressed)
	if err != nil {
		var api *APIError
		if !errors.As(err, &api) {
			err = bad("PLUGIN_LOG_EXPORT_UNAVAILABLE", "无法准备完整插件日志，请重试并检查存储")
		}
		a.writeError(w, r, err)
		return
	}
	defer f.Close()
	w.Header().Set("Cache-Control", "no-store")
	if a.logs.pluginProjectionLegacyGap {
		w.Header().Set("X-Plugin-Log-Legacy-Gap", "true")
	}
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")
	if compressed {
		w.Header().Set("Content-Type", "application/zip")
		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, name))
	} else {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	}
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(2 * time.Minute))
	http.ServeContent(w, r.WithContext(ctx), name, time.Time{}, f)
}
