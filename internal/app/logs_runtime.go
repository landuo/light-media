package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"golang.org/x/sys/unix"
)

const (
	logRecordLimit = 16 << 10
	logQueueLimit  = 3 << 20
	logWarnReserve = 512 << 10
	logRingLimit   = 768 << 10 // Reserve 256 KiB for eight bounded SSE in-flight records.
)

type logBudget struct{ Days, Total, Rotate int }
type encodedLog struct {
	Budget logBudget
	Seq    uint64
	Data   []byte
	Level  string
}
type logBarrier struct{ done chan struct{} }
type debugOverride struct {
	Scope     string    `json:"scope"`
	ID        string    `json:"id"`
	ExpiresAt time.Time `json:"expires_at"`
}
type logHealth struct {
	QueuedBytes    int    `json:"queued_bytes"`
	RingBytes      int    `json:"ring_bytes"`
	DroppedDebug   uint64 `json:"dropped_debug"`
	DroppedInfo    uint64 `json:"dropped_info"`
	DroppedWarn    uint64 `json:"dropped_warn"`
	DroppedError   uint64 `json:"dropped_error"`
	WriteError     string `json:"write_error"`
	LastWriteAt    string `json:"last_write_at"`
	StdoutError    string `json:"stdout_error"`
	RetentionError string `json:"retention_error"`
	CleanupError   string `json:"cleanup_error"`
	Sequence       uint64 `json:"sequence"`
}
type runtimeLogger struct {
	app                                                              *App
	mu                                                               sync.Mutex
	queue                                                            []encodedLog
	queueBytes                                                       int
	ring                                                             []encodedLog
	ringBytes                                                        int
	seq                                                              uint64
	boot                                                             string
	dropped                                                          [4]uint64
	writeError, stdoutError, cleanupError, retentionError, lastWrite string
	overrides                                                        map[string]debugOverride
	wake                                                             chan struct{}
	barriers                                                         chan logBarrier
	stop                                                             chan struct{}
	done                                                             chan struct{}
	closeOnce                                                        sync.Once
	closed                                                           bool
	fileMu                                                           sync.Mutex // Only rotation/open/retention; never held by history reads.
	born                                                             time.Time
	file                                                             *os.File
	totalBytes                                                       int64
	bytes                                                            int64
	stdout                                                           io.Writer
	frontend                                                         map[string][]time.Time
	streamSlots                                                      chan struct{}
	exportSlots                                                      chan struct{}
}

func newRuntimeLogger(a *App) (*runtimeLogger, error) {
	l := &runtimeLogger{app: a, boot: id("instance_"), wake: make(chan struct{}, 1), barriers: make(chan logBarrier, 16), stop: make(chan struct{}), done: make(chan struct{}), overrides: map[string]debugOverride{}, stdout: os.Stdout, frontend: map[string][]time.Time{}, streamSlots: make(chan struct{}, 8), exportSlots: make(chan struct{}, 1)}
	if err := l.openFile(); err != nil {
		return nil, err
	}
	l.pruneFiles() // New has not returned; no background read races with initial fixtures.
	go l.run()
	return l, nil
}
func logPriority(level string) int {
	switch level {
	case "DEBUG":
		return 0
	case "WARN":
		return 2
	case "ERROR":
		return 3
	default:
		return 1
	}
}
func loggingBudgets(c Settings) (days, total, rotate int) {
	days, total, rotate = c.LogRetainDays, c.LogTotalMiB, c.LogMaxMiB
	if days <= 0 {
		days = 7
	}
	if total <= 0 {
		total = 200
	}
	if rotate <= 0 {
		rotate = 20
	}
	if rotate > total {
		rotate = total
	}
	return
}
func (l *runtimeLogger) health() logHealth {
	l.mu.Lock()
	defer l.mu.Unlock()
	return logHealth{QueuedBytes: l.queueBytes, RingBytes: l.ringBytes, DroppedDebug: l.dropped[0], DroppedInfo: l.dropped[1], DroppedWarn: l.dropped[2], DroppedError: l.dropped[3], WriteError: l.writeError, StdoutError: l.stdoutError, CleanupError: l.cleanupError, RetentionError: l.retentionError, LastWriteAt: l.lastWrite, Sequence: l.seq}
}
func (l *runtimeLogger) enabled(entry map[string]any, c Settings) bool {
	level, _ := entry["level"].(string)
	min := 1
	if c.LogLevel != "" {
		min = logPriority(c.LogLevel)
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	for k, d := range l.overrides {
		if !d.ExpiresAt.After(now) {
			delete(l.overrides, k)
			continue
		}
		module, _ := entry["module"].(string)
		site, _ := entry["site_id"].(string)
		if (d.Scope == "module" && (module == d.ID || strings.HasPrefix(module, d.ID+"."))) || (d.Scope == "site" && site == d.ID) {
			min = 0
		}
	}
	return logPriority(level) >= min
}

var logSecretPattern = regexp.MustCompile(`(?i)(bearer\s+)[^\s,;]+|(password|passwd|cookie|authorization|api[_-]?key|access[_-]?token|session[_-]?token|token)\s*[:=]\s*[^\s,;]+`)
var logFields = map[string]bool{}

func init() {
	for _, k := range strings.Fields("time id level module event message app_version instance_id workflow_id job_id request_id parent_request_id parent_job_id site_id media_id download_id import_id attempt duration_ms error_code retryable next_retry_at run_id target_id target_kind operation method http_status status stack page code build truncated plugin_id plugin_version plugin_instance_id plugin_run_id") {
		logFields[k] = true
	}
}
func boundedLogString(s string, n int) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) && r != '\n' && r != '\t' {
			return ' '
		}
		return r
	}, s)
	if len(s) > n {
		s = s[:n]
		for !utf8.ValidString(s) {
			s = s[:len(s)-1]
		}
		s += "…"
	}
	return s
}
func sanitizeLogEntry(entry map[string]any, cfg Settings) map[string]any {
	safe := map[string]any{}
	for k, v := range entry {
		if !logFields[k] {
			continue
		}
		switch x := v.(type) {
		case string:
			// Sanitize every field, including external trace attributes and stack traces.
			x = cleanWithSettings(x, cfg)
			x = logSecretPattern.ReplaceAllString(x, "[REDACTED]")
			n := 512
			if k == "message" {
				n = 2048
			}
			if k == "stack" {
				n = 6144
			}
			safe[k] = boundedLogString(x, n)
		case float64:
			if !math.IsNaN(x) && !math.IsInf(x, 0) {
				safe[k] = v
			}
		case bool, int, int32, int64, uint, uint32, uint64:
			safe[k] = v
		}
	}
	return safe
}
func (l *runtimeLogger) encode(entry map[string]any, cfg Settings) encodedLog {
	safe := sanitizeLogEntry(entry, cfg)
	if safe["id"] == nil {
		safe["id"] = id("log_")
	}
	if safe["time"] == nil {
		safe["time"] = now()
	}
	level, _ := safe["level"].(string)
	if level != "DEBUG" && level != "INFO" && level != "WARN" && level != "ERROR" {
		level = "INFO"
		safe["level"] = level
	}
	safe["instance_id"] = l.boot
	b, _ := json.Marshal(safe)
	if len(b)+1 > logRecordLimit {
		safe["truncated"] = true
		for k, v := range safe {
			if x, ok := v.(string); ok && k != "id" && k != "time" && k != "instance_id" {
				safe[k] = boundedLogString(x, 256)
			}
		}
		b, _ = json.Marshal(safe)
	}
	days, total, rotate := loggingBudgets(cfg)
	return encodedLog{Data: append(b, '\n'), Level: level, Budget: logBudget{days, total, rotate}}
}
func (l *runtimeLogger) enqueue(entry map[string]any) {
	cfg := l.app.Settings()
	if !l.enabled(entry, cfg) {
		return
	}
	v := l.encode(entry, cfg)
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return
	}
	// Ring and queue share one explicit 4 MiB retention budget. No per-client queues.
	limit := logQueueLimit
	if logPriority(v.Level) < 2 {
		limit -= logWarnReserve
	}
	if l.queueBytes+len(v.Data) > limit {
		l.dropped[logPriority(v.Level)]++
		return
	}
	l.seq++
	v.Seq = l.seq
	l.queue = append(l.queue, v)
	l.queueBytes += len(v.Data)
	for l.ringBytes+len(v.Data) > logRingLimit && len(l.ring) > 0 {
		l.ringBytes -= len(l.ring[0].Data)
		l.ring[0] = encodedLog{}
		l.ring = l.ring[1:]
	}
	l.ring = append(l.ring, v)
	l.ringBytes += len(v.Data)
	select {
	case l.wake <- struct{}{}:
	default:
	}
}
func (l *runtimeLogger) pop() (encodedLog, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.queue) == 0 {
		return encodedLog{}, false
	}
	v := l.queue[0]
	l.queue[0] = encodedLog{}
	l.queue = l.queue[1:]
	return v, true
}
func (l *runtimeLogger) flush(ctx context.Context) bool {
	b := logBarrier{done: make(chan struct{})}
	select {
	case l.barriers <- b:
	case <-ctx.Done():
		return false
	case <-l.done:
		return true
	}
	select {
	case <-b.done:
		return true
	case <-ctx.Done():
		return false
	case <-l.done:
		return true
	}
}
func (l *runtimeLogger) close() {
	l.closeOnce.Do(func() { l.mu.Lock(); l.closed = true; l.mu.Unlock(); close(l.stop) })
	select {
	case <-l.done:
	case <-time.After(3 * time.Second):
	}
}
func (l *runtimeLogger) run() {
	defer close(l.done)
	cleanup := time.NewTicker(time.Minute)
	defer cleanup.Stop()
	dbCleanup := time.NewTicker(time.Hour)
	defer dbCleanup.Stop()
	drain := func() {
		l.mu.Lock()
		count := len(l.queue)
		l.mu.Unlock()
		for i := 0; i < count; i++ {
			v, ok := l.pop()
			if !ok {
				return
			}
			l.write(v)
			l.mu.Lock()
			l.queueBytes -= len(v.Data)
			l.mu.Unlock()
		}
	}
	for {
		select {
		case <-l.wake:
			drain()
		case b := <-l.barriers:
			drain()
			close(b.done)
		case <-cleanup.C:
			l.pruneFiles()
		case <-dbCleanup.C:
			l.cleanupHistory()
		case <-l.stop:
			drain()
			l.fileMu.Lock()
			if l.file != nil {
				_ = l.file.Sync()
				_ = l.file.Close()
				l.file = nil
			}
			l.fileMu.Unlock()
			return
		}
	}
}
func (l *runtimeLogger) openFile() error {
	path := filepath.Join(l.app.Dir, "logs", "app.jsonl")
	st, err := os.Lstat(path)
	if err == nil && !st.Mode().IsRegular() {
		return fmt.Errorf("日志目标必须是普通文件")
	}
	fd, err := unix.Open(path, unix.O_CREAT|unix.O_APPEND|unix.O_RDWR|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0600)
	if err != nil {
		return err
	}
	f := os.NewFile(uintptr(fd), "runtime-log")
	var stat unix.Stat_t
	if unix.Fstat(fd, &stat) != nil || stat.Mode&unix.S_IFMT != unix.S_IFREG {
		f.Close()
		return fmt.Errorf("日志目标必须是普通文件")
	}
	_ = f.Chmod(0600)
	l.file = f
	l.born = time.Now()
	if st, e := f.Stat(); e == nil {
		l.bytes = st.Size()
		if st.Size() > 0 {
			l.born = st.ModTime()
			buf := make([]byte, logRecordLimit)
			n, _ := f.ReadAt(buf, 0)
			if i := bytes.IndexByte(buf[:n], '\n'); i >= 0 {
				var first struct {
					Time string `json:"time"`
				}
				if json.Unmarshal(buf[:i], &first) == nil {
					if t, e := time.Parse(time.RFC3339Nano, first.Time); e == nil {
						l.born = t
					}
				}
			}
		}
	}
	return nil
}
func (l *runtimeLogger) setWriteError(code string) {
	l.mu.Lock()
	l.writeError = code
	l.mu.Unlock()
	l.app.logMu.Lock()
	l.app.logError = code
	l.app.logMu.Unlock()
}
func (l *runtimeLogger) write(v encodedLog) {
	rotate := v.Budget.Rotate
	if rotate <= 0 {
		rotate = 20
	}
	l.fileMu.Lock()
	var err error
	if l.file == nil {
		err = l.openFile()
	}
	if err == nil && l.bytes > 0 && (l.bytes+int64(len(v.Data)) > int64(rotate)<<20 || l.born.UTC().Format("2006-01-02") != time.Now().UTC().Format("2006-01-02")) {
		err = l.file.Sync()
		_ = l.file.Close()
		l.file = nil
		if err == nil {
			err = os.Rename(filepath.Join(l.app.Dir, "logs", "app.jsonl"), filepath.Join(l.app.Dir, "logs", fmt.Sprintf("app-%020d-%s.jsonl", time.Now().UnixNano(), id(""))))
		}
		if err == nil {
			l.bytes = 0
			err = l.openFile()
		}
	}
	if err == nil {
		var n int
		n, err = l.file.Write(v.Data)
		l.bytes += int64(n)
		l.totalBytes += int64(n)
		if err == nil && n != len(v.Data) {
			err = io.ErrShortWrite
		}
	}
	needPrune := v.Budget.Total > 0 && l.totalBytes > int64(v.Budget.Total)<<20
	l.fileMu.Unlock()
	if needPrune {
		l.pruneFilesBudget(v.Budget.Days, v.Budget.Total)
	}
	if err != nil {
		l.setWriteError("日志文件写入失败，请检查磁盘空间和目录权限")
	} else {
		l.setWriteError("")
		l.mu.Lock()
		l.lastWrite = now()
		l.mu.Unlock()
	}
	if l.stdout != nil {
		n, e := l.stdout.Write(v.Data)
		l.mu.Lock()
		if e != nil || n != len(v.Data) {
			l.stdoutError = "标准输出不可写"
		} else {
			l.stdoutError = ""
		}
		l.mu.Unlock()
	}
}

// A stream retains one record at a time, with no unbounded client buffers.
func (l *runtimeLogger) nextAfter(after uint64) (encodedLog, uint64, bool, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	oldest := l.seq + 1
	if len(l.ring) > 0 {
		oldest = l.ring[0].Seq
	}
	if after > l.seq || (after > 0 && after+1 < oldest) {
		return encodedLog{}, l.seq, true, false
	}
	for _, v := range l.ring {
		if v.Seq > after {
			return v, l.seq, false, true
		}
	}
	return encodedLog{}, l.seq, false, false
}
func (l *runtimeLogger) snapshot(after uint64) ([]encodedLog, uint64, uint64, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	oldest := l.seq + 1
	if len(l.ring) > 0 {
		oldest = l.ring[0].Seq
	}
	gap := after > 0 && after+1 < oldest
	out := make([]encodedLog, 0, 128)
	for _, v := range l.ring {
		if v.Seq > after {
			out = append(out, v)
		}
	}
	return out, oldest, l.seq, gap
}

var logFileName = regexp.MustCompile(`^(app\.jsonl(?:\.[0-9]{1,4})?|app-[0-9]{20}-[a-zA-Z0-9_-]+\.jsonl)$`)

func (l *runtimeLogger) pruneFiles() {
	days, total, _ := loggingBudgets(l.app.Settings())
	l.pruneFilesBudget(days, total)
}
func (l *runtimeLogger) pruneFilesBudget(days, total int) {
	cutoff := time.Now().Add(-time.Duration(days) * 24 * time.Hour)
	l.fileMu.Lock()
	defer l.fileMu.Unlock()
	dir := filepath.Join(l.app.Dir, "logs")
	entries, e := os.ReadDir(dir)
	if e != nil {
		l.mu.Lock()
		l.retentionError = "日志目录无法读取"
		l.mu.Unlock()
		return
	}
	l.mu.Lock()
	l.retentionError = ""
	l.mu.Unlock()
	type oldFile struct {
		name  string
		size  int64
		mtime time.Time
	}
	files := []oldFile{}
	var sum int64
	for _, ent := range entries {
		if !logFileName.MatchString(ent.Name()) || ent.Type()&os.ModeSymlink != 0 {
			continue
		}
		st, e := ent.Info()
		if e != nil || !st.Mode().IsRegular() {
			continue
		}
		sum += st.Size()
		if ent.Name() != "app.jsonl" {
			files = append(files, oldFile{ent.Name(), st.Size(), st.ModTime()})
		}
	}
	sort.Slice(files, func(i, j int) bool { return files[i].mtime.Before(files[j].mtime) })
	for _, f := range files {
		if f.mtime.Before(cutoff) || sum > int64(total)<<20 {
			if e := os.Remove(filepath.Join(dir, f.name)); e == nil {
				sum -= f.size
			} else {
				l.mu.Lock()
				l.retentionError = "旧日志清理失败，请检查目录权限"
				l.mu.Unlock()
			}
		}
	}
	l.totalBytes = sum
}

func (l *runtimeLogger) cleanupHistory() {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	// Only final successful/cancelled tasks are eligible. Failed/review/running tasks
	// retain their diagnostic events until explicitly resolved; recovery state is never deleted.
	_, e := l.app.Store.DB.ExecContext(ctx, `DELETE FROM events WHERE seq IN (SELECT e.seq FROM events e JOIN jobs j ON j.id=e.job_id WHERE j.state IN ('succeeded','cancelled') AND j.updated_at<? AND e.time<? ORDER BY e.seq LIMIT 500)`, time.Now().Add(-90*24*time.Hour).UTC().Format(time.RFC3339Nano), time.Now().Add(-90*24*time.Hour).UTC().Format(time.RFC3339Nano))
	if e == nil {
		_, e = l.app.Store.DB.ExecContext(ctx, `DELETE FROM records WHERE kind='audit' AND id IN (SELECT id FROM records WHERE kind='audit' AND updated_at<? ORDER BY updated_at LIMIT 500)`, time.Now().Add(-180*24*time.Hour).UTC().Format(time.RFC3339Nano))
	}
	l.mu.Lock()
	if e != nil {
		l.cleanupError = "历史任务或操作记录清理尚未完成"
	} else {
		l.cleanupError = ""
	}
	l.mu.Unlock()
}
