package app

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

const logScanBudget int64 = 32 << 20

var logASCIIQuery = regexp.MustCompile(`^[a-zA-Z0-9 _.-]+$`)
var errLogBudget = errors.New("log query budget reached")

type logRef struct {
	Dev  uint64 `json:"d"`
	Ino  uint64 `json:"i"`
	Size int64  `json:"s"`
}
type logCursor struct {
	Files   []logRef          `json:"f"`
	Index   int               `json:"i"`
	Offset  int64             `json:"o"`
	Filters map[string]string `json:"q"`
	Created int64             `json:"t"`
	Gap     bool              `json:"g,omitempty"`
}
type logPage struct {
	Entries      []map[string]any `json:"entries"`
	NextCursor   string           `json:"next_cursor,omitempty"`
	Complete     bool             `json:"complete"`
	Truncated    bool             `json:"truncated"`
	Gap          bool             `json:"gap"`
	ScannedBytes int64            `json:"scanned_bytes"`
	OldestTime   string           `json:"oldest_time,omitempty"`
	Health       logHealth        `json:"health"`
	Retention    map[string]int   `json:"retention"`
}
type namedLog struct {
	logRef
	Name     string
	Modified time.Time
}

func logQuery(r *http.Request) map[string]string {
	q := map[string]string{}
	for _, k := range []string{"level", "module", "job_id", "workflow_id", "site_id", "q", "from", "to", "limit", "cursor"} {
		q[k] = r.URL.Query().Get(k)
	}
	return q
}
func (a *App) logFiles() ([]namedLog, error) {
	entries, e := os.ReadDir(filepath.Join(a.Dir, "logs"))
	if e != nil {
		return nil, e
	}
	out := []namedLog{}
	for _, ent := range entries {
		if !logFileName.MatchString(ent.Name()) || ent.Type()&os.ModeSymlink != 0 {
			continue
		}
		var st unix.Stat_t
		if unix.Lstat(filepath.Join(a.Dir, "logs", ent.Name()), &st) != nil || st.Mode&unix.S_IFMT != unix.S_IFREG {
			continue
		}
		info, e := ent.Info()
		if e != nil {
			continue
		}
		out = append(out, namedLog{logRef: logRef{uint64(st.Dev), st.Ino, st.Size}, Name: ent.Name(), Modified: info.ModTime()})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Modified.Equal(out[j].Modified) {
			return out[i].Name > out[j].Name
		}
		return out[i].Modified.After(out[j].Modified)
	})
	return out, nil
}
func (a *App) signLogCursor(c logCursor) string {
	raw, _ := json.Marshal(c)
	var buf bytes.Buffer
	z := gzip.NewWriter(&buf)
	_, _ = z.Write(raw)
	_ = z.Close()
	body := buf.Bytes()
	mac := hmac.New(sha256.New, a.Store.key)
	_, _ = mac.Write(body)
	return base64.RawURLEncoding.EncodeToString(body) + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}
func (a *App) parseLogCursor(s string) (logCursor, error) {
	var c logCursor
	invalid := func() (logCursor, error) {
		return c, bad("INVALID_LOG_CURSOR", "日志游标无效或已过期，请重新查询")
	}
	if len(s) > 180000 {
		return invalid()
	}
	parts := strings.Split(s, ".")
	if len(parts) != 2 {
		return invalid()
	}
	body, e := base64.RawURLEncoding.DecodeString(parts[0])
	if e != nil {
		return invalid()
	}
	sig, e := base64.RawURLEncoding.DecodeString(parts[1])
	if e != nil {
		return invalid()
	}
	mac := hmac.New(sha256.New, a.Store.key)
	_, _ = mac.Write(body)
	if !hmac.Equal(mac.Sum(nil), sig) {
		return invalid()
	}
	z, e := gzip.NewReader(bytes.NewReader(body))
	if e != nil {
		return invalid()
	}
	raw, e := io.ReadAll(io.LimitReader(z, 1<<20))
	_ = z.Close()
	if e != nil || len(raw) >= 1<<20 || json.Unmarshal(raw, &c) != nil {
		return invalid()
	}
	if len(c.Files) > 1024 || c.Index < 0 || c.Index > len(c.Files) || c.Offset < 0 || c.Created < time.Now().Add(-24*time.Hour).Unix() || c.Created > time.Now().Add(time.Minute).Unix() {
		return invalid()
	}
	if c.Index < len(c.Files) && c.Offset > c.Files[c.Index].Size {
		return invalid()
	}
	return c, nil
}
func validLogFilters(q map[string]string) (map[string]string, error) {
	out := map[string]string{}
	for _, k := range []string{"level", "module", "job_id", "workflow_id", "site_id", "q", "from", "to"} {
		v := strings.TrimSpace(q[k])
		if len(v) > 512 || strings.ContainsAny(v, "\x00\r\n") {
			return nil, bad("INVALID_LOG_FILTER", "日志筛选内容无效")
		}
		out[k] = v
	}
	if out["level"] != "" && out["level"] != "DEBUG" && out["level"] != "INFO" && out["level"] != "WARN" && out["level"] != "ERROR" {
		return nil, bad("INVALID_LOG_FILTER", "日志级别无效")
	}
	for _, k := range []string{"from", "to"} {
		if out[k] != "" {
			t, e := time.Parse(time.RFC3339Nano, out[k])
			if e != nil {
				return nil, bad("INVALID_LOG_FILTER", "日志时间格式无效")
			}
			out[k] = t.UTC().Format(time.RFC3339Nano)
		}
	}
	if out["from"] != "" && out["to"] != "" {
		f, _ := time.Parse(time.RFC3339Nano, out["from"])
		t, _ := time.Parse(time.RFC3339Nano, out["to"])
		if f.After(t) {
			return nil, bad("INVALID_LOG_FILTER", "开始时间晚于结束时间")
		}
	}
	return out, nil
}
func logMatches(v map[string]any, q map[string]string) bool {
	for _, k := range []string{"level", "job_id", "workflow_id", "site_id"} {
		if q[k] != "" && fmt.Sprint(v[k]) != q[k] {
			return false
		}
	}
	if q["module"] != "" {
		m, _ := v["module"].(string)
		if m != q["module"] && !strings.HasPrefix(m, q["module"]+".") {
			return false
		}
	}
	if q["q"] != "" {
		found := false
		needle := strings.ToLower(q["q"])
		for _, key := range []string{"message", "request_id", "workflow_id", "job_id", "error_code"} {
			if strings.Contains(strings.ToLower(fmt.Sprint(v[key])), needle) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	t, e := time.Parse(time.RFC3339Nano, fmt.Sprint(v["time"]))
	if e != nil {
		return false
	}
	if q["from"] != "" {
		f, _ := time.Parse(time.RFC3339Nano, q["from"])
		if t.Before(f) {
			return false
		}
	}
	if q["to"] != "" {
		end, _ := time.Parse(time.RFC3339Nano, q["to"])
		if t.After(end) {
			return false
		}
	}
	return true
}

// reverseLogReader keeps at most a 64 KiB disk chunk plus one bounded record.
// End always denotes the next unconsumed byte, so page limits never skip a line.
type reverseLogReader struct {
	file     *os.File
	End      int64
	data     []byte
	base     int64
	scanned  *int64
	deadline time.Time
	ctx      context.Context
}

func (r *reverseLogReader) next() ([]byte, bool, error) {
	var tail []byte
	oversize := false
	lineEnd := r.End
	for {
		if r.ctx.Err() != nil {
			return nil, false, r.ctx.Err()
		}
		if len(r.data) == 0 {
			if r.End == 0 {
				if len(tail) > 0 {
					return tail, oversize, nil
				}
				return nil, oversize, io.EOF
			}
			if *r.scanned >= logScanBudget || time.Now().After(r.deadline) {
				if !oversize {
					r.End = lineEnd
				}
				return nil, oversize, errLogBudget
			}
			n := int64(64 << 10)
			if n > r.End {
				n = r.End
			}
			if n > logScanBudget-*r.scanned {
				n = logScanBudget - *r.scanned
			}
			r.base = r.End - n
			r.data = make([]byte, n)
			got, e := r.file.ReadAt(r.data, r.base)
			*r.scanned += int64(got)
			r.data = r.data[:got]
			if e != nil && e != io.EOF {
				return nil, false, e
			}
			if got == 0 {
				return nil, false, io.EOF
			}
		}
		i := bytes.LastIndexByte(r.data, '\n')
		if i >= 0 {
			part := r.data[i+1:]
			r.End = r.base + int64(i)
			r.data = r.data[:i]
			if len(part)+len(tail) > logRecordLimit {
				oversize = true
				tail = nil
			} else if !oversize {
				tail = append(append([]byte{}, part...), tail...)
			}
			if len(tail) == 0 && !oversize {
				lineEnd = r.End
				continue
			}
			return tail, oversize, nil
		}
		if len(r.data)+len(tail) > logRecordLimit {
			oversize = true
			tail = nil
		} else if !oversize {
			tail = append(append([]byte{}, r.data...), tail...)
		}
		r.End = r.base
		r.data = nil
		if r.End == 0 {
			if len(tail) == 0 && !oversize {
				return nil, false, io.EOF
			}
			return tail, oversize, nil
		}
	}
}
func (a *App) queryLogs(ctx context.Context, q map[string]string) (logPage, error) {
	page := logPage{Entries: []map[string]any{}, Health: a.logs.health()}
	cfg := a.Settings()
	days, total, rotate := loggingBudgets(cfg)
	page.Retention = map[string]int{"retain_days": days, "total_mib": total, "rotate_mib": rotate}
	filters, e := validLogFilters(q)
	if e != nil {
		return page, e
	}
	limit := 100
	if q["limit"] != "" {
		limit, e = strconv.Atoi(q["limit"])
		if e != nil || limit < 1 || limit > 500 {
			return page, bad("INVALID_LOG_LIMIT", "日志每页数量必须为1至500")
		}
	}
	deadline := time.Now().Add(2 * time.Second)
	var cursor logCursor
	if q["cursor"] != "" {
		cursor, e = a.parseLogCursor(q["cursor"])
		if e != nil {
			return page, e
		}
		for k, v := range filters {
			if v != "" && v != cursor.Filters[k] {
				return page, bad("LOG_CURSOR_FILTER_CHANGED", "更改筛选后请重新查询日志")
			}
		}
		filters = cursor.Filters
	} else {
		flushCtx, cancel := context.WithTimeout(ctx, 300*time.Millisecond)
		flushed := a.logs.flush(flushCtx)
		cancel()
		if !flushed {
			page.Gap = true
		}
		if filters["from"] == "" {
			filters["from"] = time.Now().Add(-15 * time.Minute).UTC().Format(time.RFC3339Nano)
		}
		if filters["to"] == "" {
			filters["to"] = now()
		}
		files, e := a.logFiles()
		if e != nil {
			return page, bad("LOG_HISTORY_UNAVAILABLE", "日志历史不可读，请检查存储权限")
		}
		cursor = logCursor{Files: []logRef{}, Filters: filters, Created: time.Now().Unix()}
		if len(files) > 1024 {
			files = files[:1024]
			cursor.Gap = true
		}
		for _, f := range files {
			cursor.Files = append(cursor.Files, f.logRef)
		}
		if len(cursor.Files) > 0 {
			cursor.Offset = cursor.Files[0].Size
		}
	}
	var fastNeedle []byte
	if filters["q"] != "" && logASCIIQuery.MatchString(filters["q"]) {
		fastNeedle = []byte(strings.ToLower(filters["q"]))
	}
	files, e := a.logFiles()
	if e != nil {
		return page, bad("LOG_HISTORY_UNAVAILABLE", "日志历史不可读，请检查存储权限")
	}
	inventory := map[[2]uint64]namedLog{}
	for _, f := range files {
		inventory[[2]uint64{f.Dev, f.Ino}] = f
	}
	page.Gap = page.Gap || cursor.Gap
	for cursor.Index < len(cursor.Files) && len(page.Entries) < limit {
		if ctx.Err() != nil {
			return page, ctx.Err()
		}
		if page.ScannedBytes >= logScanBudget || time.Now().After(deadline) {
			page.Truncated = true
			break
		}
		ref := cursor.Files[cursor.Index]
		file, ok := inventory[[2]uint64{ref.Dev, ref.Ino}]
		if !ok || file.Size < cursor.Offset {
			page.Gap = true
			cursor.Index++
			if cursor.Index < len(cursor.Files) {
				cursor.Offset = cursor.Files[cursor.Index].Size
			}
			continue
		}
		fd, err := unix.Open(filepath.Join(a.Dir, "logs", file.Name), unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
		if err != nil {
			page.Gap = true
			cursor.Index++
			if cursor.Index < len(cursor.Files) {
				cursor.Offset = cursor.Files[cursor.Index].Size
			}
			continue
		}
		f := os.NewFile(uintptr(fd), "log-history")
		var st unix.Stat_t
		if unix.Fstat(fd, &st) != nil || uint64(st.Dev) != ref.Dev || st.Ino != ref.Ino || st.Size < cursor.Offset {
			f.Close()
			page.Gap = true
			cursor.Index++
			if cursor.Index < len(cursor.Files) {
				cursor.Offset = cursor.Files[cursor.Index].Size
			}
			continue
		}
		reader := reverseLogReader{file: f, End: cursor.Offset, scanned: &page.ScannedBytes, deadline: deadline, ctx: ctx}
		for len(page.Entries) < limit {
			b, badLine, err := reader.next()
			if badLine {
				page.Gap = true
			}
			cursor.Offset = reader.End
			if err == io.EOF {
				cursor.Offset = 0
				break
			}
			if err == errLogBudget {
				page.Truncated = true
				break
			}
			if err != nil {
				f.Close()
				return page, bad("LOG_HISTORY_UNAVAILABLE", "部分日志读取失败，请检查存储")
			}
			if badLine {
				page.Gap = true
				continue
			}
			if len(fastNeedle) > 0 && !bytes.Contains(b, []byte(`\u`)) && !bytes.Contains(bytes.ToLower(b), fastNeedle) {
				continue
			}
			var v map[string]any
			if json.Unmarshal(b, &v) != nil {
				page.Gap = true
				continue
			}
			t, _ := v["time"].(string)
			if page.OldestTime == "" || t < page.OldestTime {
				page.OldestTime = t
			}
			if logMatches(v, filters) {
				v = sanitizeLogEntry(v, cfg)
				if logMatches(v, filters) {
					page.Entries = append(page.Entries, v)
				}
			}
		}
		f.Close()
		if cursor.Offset == 0 {
			cursor.Index++
			if cursor.Index < len(cursor.Files) {
				cursor.Offset = cursor.Files[cursor.Index].Size
			}
		}
		if page.Truncated {
			break
		}
	}
	page.Complete = cursor.Index >= len(cursor.Files)
	if !page.Complete {
		cursor.Gap = page.Gap
		page.NextCursor = a.signLogCursor(cursor)
	}
	page.Health = a.logs.health()
	if page.Health.WriteError != "" || page.Health.DroppedDebug+page.Health.DroppedInfo+page.Health.DroppedWarn+page.Health.DroppedError > 0 {
		page.Gap = true
	}
	return page, nil
}
