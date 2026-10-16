package app

import (
	"archive/zip"
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
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
		// Capture the stream boundary before reading disk. Later events may also
		// occur in the history page; stable log IDs let the client deduplicate.
		a.logs.mu.Lock()
		resume := fmt.Sprintf("%s:%d", a.logs.boot, a.logs.seq)
		a.logs.mu.Unlock()
		filters := logQuery(copyRequest)
		if filters["from"] == "" {
			filters["from"] = "0001-01-01T00:00:00Z"
		}
		if filters["limit"] == "" {
			filters["limit"] = "50"
		}
		page, err := a.queryLogs(r.Context(), filters)
		if err != nil {
			a.writeError(w, r, err)
		} else {
			writeJSON(w, r, map[string]any{"history": page, "resume_id": resume})
		}
		return true
	}
	switch parts[2] {
	case "stream":
		a.streamLogs(w, copyRequest)
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

const pluginLogSnapshotLimit int64 = 1 << 30 // Matches the logging settings' maximum retained bytes.

func (a *App) collectPluginLogSnapshot(ctx context.Context, instance string) (*os.File, error) {
	f, err := a.pluginLogTemp()
	if err != nil {
		return nil, bad("PLUGIN_LOG_EXPORT_UNAVAILABLE", "无法准备插件日志，请检查存储")
	}
	success := false
	defer func() {
		if !success {
			_ = f.Close()
		}
	}()
	q := map[string]string{"plugin_instance_id": instance, "from": "0001-01-01T00:00:00Z", "to": now(), "limit": "500"}
	encoder := json.NewEncoder(f)
	for {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		page, err := a.queryLogs(ctx, q)
		if err != nil {
			return nil, err
		}
		if page.historyGap {
			return nil, &APIError{"PLUGIN_LOG_HISTORY_CHANGED", "日志在读取时发生轮转清理或存在损坏，请重新下载", http.StatusConflict}
		}
		for _, entry := range page.Entries {
			if err := encoder.Encode(entry); err != nil {
				return nil, bad("PLUGIN_LOG_EXPORT_UNAVAILABLE", "插件日志暂存失败，请检查存储")
			}
		}
		stat, err := f.Stat()
		if err != nil || stat.Size() > pluginLogSnapshotLimit {
			return nil, bad("PLUGIN_LOG_EXPORT_TOO_LARGE", "插件日志超过导出限额，未截断返回")
		}
		if page.Complete {
			break
		}
		if page.NextCursor == "" || page.NextCursor == q["cursor"] {
			return nil, bad("PLUGIN_LOG_EXPORT_UNAVAILABLE", "插件日志读取未能继续，请重新下载")
		}
		q["cursor"] = page.NextCursor
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	success = true
	return f, nil
}

func pluginLogText(entry map[string]any) string {
	value := func(key string) string {
		if entry[key] == nil {
			return ""
		}
		return fmt.Sprint(entry[key])
	}
	level := value("level")
	if level == "WARN" {
		level = "WARNING"
	}
	text := fmt.Sprintf("%s: [%s] %s - %s", level, value("time"), value("module"), value("message"))
	if code := value("error_code"); code != "" {
		text += " [" + code + "]"
	}
	return text
}

func (a *App) preparePluginLogFile(ctx context.Context, instance string, compressed bool) (*os.File, string, error) {
	snapshot, err := a.collectPluginLogSnapshot(ctx, instance)
	if err != nil {
		return nil, "", err
	}
	defer snapshot.Close()
	out, err := a.pluginLogTemp()
	if err != nil {
		return nil, "", err
	}
	success := false
	defer func() {
		if !success {
			_ = out.Close()
		}
	}()
	name := strings.ToLower(instance) + ".log"
	var target io.Writer = out
	var archive *zip.Writer
	if compressed {
		archive = zip.NewWriter(out)
		defer func() {
			if archive != nil {
				_ = archive.Close()
			}
		}()
		stem := strings.ToLower(instance) + "-logs-" + time.Now().Format("20060102-150405")
		name = stem + ".zip"
		target, err = archive.Create(stem + "/" + strings.ToLower(instance) + ".log")
		if err != nil {
			return nil, "", err
		}
	}
	write := func(raw []byte) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		var entry map[string]any
		if json.Unmarshal(raw, &entry) != nil {
			return errors.New("invalid prepared log record")
		}
		_, err := fmt.Fprintln(target, pluginLogText(entry))
		return err
	}
	if compressed {
		// History pages are newest first. Reverse the private bounded spool for
		// a chronological log inside the archive, without retaining all rows.
		stat, err := snapshot.Stat()
		if err != nil {
			return nil, "", err
		}
		var scanned int64
		reader := reverseLogReader{file: snapshot, End: stat.Size(), scanned: &scanned, ctx: ctx}
		for {
			scanned = 0 // A single record is bounded; the complete spool already has a byte limit.
			reader.deadline = time.Now().Add(2 * time.Second)
			raw, badLine, err := reader.next()
			if err == io.EOF {
				break
			}
			if err != nil || badLine {
				return nil, "", errors.New("prepared log record unavailable")
			}
			if err := write(raw); err != nil {
				return nil, "", err
			}
		}
		if err := archive.Close(); err != nil {
			return nil, "", err
		}
		archive = nil
	} else {
		// Original full-text window displays the latest line first.
		scanner := bufio.NewScanner(snapshot)
		scanner.Buffer(make([]byte, 4096), logRecordLimit+1)
		for scanner.Scan() {
			if err := write(scanner.Bytes()); err != nil {
				return nil, "", err
			}
		}
		if err := scanner.Err(); err != nil {
			return nil, "", err
		}
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
