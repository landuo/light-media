package removelink

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"testing"

	plugins "light-media/builtinplugins/internal/sdk"
)

type sourceLogPhase struct {
	Method string
	Calls  int
}
type sourceLogProtocol struct {
	f           *protocolFixture
	mu          sync.Mutex
	logs        [][2]string
	messages    []json.RawMessage
	phases      []*sourceLogPhase
	reject      string
	rejected    bool
	afterReject int
}

func (h *sourceLogProtocol) handler(method string) plugins.HostHandler {
	phase := &sourceLogPhase{Method: method}
	h.mu.Lock()
	h.phases = append(h.phases, phase)
	h.mu.Unlock()
	return func(ctx context.Context, id plugins.Identity, method string, raw json.RawMessage) (json.RawMessage, error) {
		if ctx.Err() != nil || id != h.f.id {
			return nil, errors.New("fixture caller revoked")
		}
		h.mu.Lock()
		phase.Calls++
		if h.rejected {
			h.afterReject++
		}
		h.mu.Unlock()
		if method == plugins.BackgroundSubmitCapability || method == plugins.CleanupSubmitCapability {
			var in struct {
				Method  string
				Payload json.RawMessage
			}
			if e := json.Unmarshal(raw, &in); e != nil {
				return nil, e
			}
			var pending *plugins.PendingCall
			var e error
			if method == plugins.CleanupSubmitCapability {
				pending, e = h.f.client.StartCleanupContinuationWithAdmission(ctx, in.Method, in.Payload, h.handler(in.Method))
			} else {
				pending, e = h.f.client.StartCallWithAdmission(ctx, h.f.ctx, in.Method, in.Payload, h.handler(in.Method), plugins.CallOptions{CallerDeadlineOnly: true})
			}
			if e != nil {
				return nil, e
			}
			go func() { _, _ = pending.Wait() }()
			return json.RawMessage(`{"accepted":true}`), nil
		}
		if method == plugins.LogWriteCapability {
			var in struct{ Level, Message string }
			if e := json.Unmarshal(raw, &in); e != nil {
				return nil, e
			}
			h.mu.Lock()
			h.logs = append(h.logs, [2]string{in.Level, in.Message})
			reject := h.reject != "" && strings.Contains(in.Message, h.reject)
			if reject {
				h.rejected = true
			}
			h.mu.Unlock()
			if reject {
				return nil, errors.New("synthetic logging authority revoked")
			}
			return json.RawMessage(`null`), nil
		}
		if method == plugins.SystemMessagePutCapability {
			h.mu.Lock()
			h.messages = append(h.messages, append(json.RawMessage(nil), raw...))
			h.mu.Unlock()
		}
		return h.f.host(ctx, method, raw)
	}
}

func TestActualWorkerFailedWatchStopSourceLogSequence(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("actual installed source selects Linux InotifyObserver")
	}
	raw, e := os.ReadFile("reference/observer-start-failure-source312.json")
	if e != nil {
		t.Fatal(e)
	}
	var source struct {
		Source  string      `json:"source_sha256"`
		Initial [][2]string `json:"initial_logs"`
		Stop    [][2]string `json:"stop_logs"`
	}
	if json.Unmarshal(raw, &source) != nil || source.Source != "22fb375e531378b3454607c7bca305b637abd60ee8ed69e14e32fbc2ddfc5485" {
		t.Fatal("unattested failed-start source")
	}
	f := startProtocolMode(t, true)
	h := &sourceLogProtocol{f: f}
	h.must(t, "init", map[string]any{"enabled": true, "monitor_dirs": filepath.Join(f.root, "absent")})
	h.mu.Lock()
	initial := append([][2]string{}, h.logs...)
	messages := len(h.messages)
	h.mu.Unlock()
	if messages != 1 {
		t.Fatal("native start failure lacks source systemmessage", messages)
	}
	normalize := func(logs [][2]string) [][2]string {
		for i := range logs {
			logs[i][0] = strings.ToLower(logs[i][0])
			logs[i][1] = strings.ReplaceAll(logs[i][1], f.root, "/fixture")
			logs[i][1] = regexp.MustCompile(`耗时 [0-9]+\.[0-9]+ 秒`).ReplaceAllString(logs[i][1], "耗时 0.00 秒")
			if strings.Contains(logs[i][1], "启动硬链接监控失败：") {
				logs[i][1] = strings.Split(logs[i][1], "启动硬链接监控失败：")[0] + "启动硬链接监控失败：[Errno 2] No such file or directory"
			}
		}
		return logs
	}
	if a, b := fmt.Sprint(normalize(initial)), fmt.Sprint(source.Initial); a != b {
		t.Fatal("source init failed-start logs", a, b)
	}
	h.must(t, "stop_service", nil)
	h.mu.Lock()
	stopped := append([][2]string{}, h.logs[len(initial):]...)
	h.mu.Unlock()
	if a, b := fmt.Sprint(normalize(stopped)), fmt.Sprint(source.Stop); a != b {
		t.Fatal("source stop retained failed observer", a, b)
	}
	h.record(t, "actual-native-ENOENT-retained-unstarted-observer-stop")
}

func TestSourceWatchFailuresAndGuardLogging(t *testing.T) {
	for _, mode := range []string{"hardlink", "strm"} {
		t.Run(mode, func(t *testing.T) {
			for _, message := range []string{"ordinary native start failure", "[Errno 28] inotify watch limit reached"} {
				t.Run(message, func(t *testing.T) {
					type entry struct {
						Method string
						Body   map[string]any
					}
					trace := []entry{}
					recorded := 0
					o := &sdkOps{watch: func(Watch) error { return errors.New(message) }, watchStartFailed: func() { recorded++ }}
					o.bind(context.Background(), func(_ context.Context, m string, r json.RawMessage) (json.RawMessage, error) {
						var b map[string]any
						_ = json.Unmarshal(r, &b)
						trace = append(trace, entry{m, b})
						if m == plugins.StorageExistsCapability {
							return json.RawMessage(`false`), nil
						}
						return json.RawMessage(`null`), nil
					})
					defer o.unbind()
					o.StartWatch(Watch{"/owned/m", mode})
					if recorded != 1 || len(trace) != 3 || trace[1].Method != plugins.LogWriteCapability || trace[2].Method != plugins.SystemMessagePutCapability {
						t.Fatal("source failure logger/systemmessage order", trace)
					}
					kind := "硬链接"
					if mode == "strm" {
						kind = " STRM "
					}
					want := "/owned/m 启动" + kind + "监控失败：" + message
					if trace[2].Body["message"] != want || trace[2].Body["title"] != "媒体文件清理" {
						t.Fatal(trace)
					}
					if strings.Contains(message, "inotify") {
						if trace[1].Body["level"] != "WARNING" || !strings.Contains(trace[1].Body["message"].(string), "echo fs.inotify.max_user_watches=524288") {
							t.Fatal(trace)
						}
					} else if trace[1].Body["level"] != "ERROR" || trace[1].Body["message"] != want {
						t.Fatal(trace)
					}
				})
			}
		})
	}
	for _, cancel := range []bool{false, true} {
		t.Run(fmt.Sprint("guard/", cancel), func(t *testing.T) {
			calls, started, recorded := 0, 0, 0
			o := &sdkOps{watch: func(Watch) error { started++; return context.Canceled }, watchStartFailed: func() { recorded++ }}
			o.bind(context.Background(), func(_ context.Context, m string, _ json.RawMessage) (json.RawMessage, error) {
				calls++
				if !cancel {
					return nil, errors.New("host authority denied")
				}
				return json.RawMessage(`false`), nil
			})
			defer o.unbind()
			var err error
			func() { defer capture(&err); o.StartWatch(Watch{"/owned/m", "hardlink"}) }()
			if err == nil || recorded != 0 || calls != 1 || (!cancel && started != 0) {
				t.Fatal("guard/cancel became native-start source failure", err, calls, started, recorded)
			}
		})
	}
}
func (h *sourceLogProtocol) call(method string, value any) error {
	raw, _ := json.Marshal(value)
	_, e := h.f.client.CallWithOptions(h.f.ctx, method, raw, h.handler("owner:"+method), plugins.CallOptions{CallerDeadlineOnly: true})
	return e
}
func (h *sourceLogProtocol) must(t *testing.T, method string, value any) {
	t.Helper()
	if e := h.call(method, value); e != nil {
		t.Fatal(method, e)
	}
}
func (h *sourceLogProtocol) record(t *testing.T, kind string) {
	t.Helper()
	h.mu.Lock()
	defer h.mu.Unlock()
	max, total := 0, 0
	for _, p := range h.phases {
		total += p.Calls
		if p.Calls > max {
			max = p.Calls
		}
		if p.Calls > phaseHostCalls+1 {
			t.Fatal("logger-inclusive phase budget", p)
		}
	}
	report := map[string]any{"classification": "actual independent SDK worker with synthetic scoped host and owned filesystem; NOT ordinary Main", "case": kind, "total_host_calls": total, "maximum_calls_per_phase": max, "default_host_call_limit": 64, "private_slice_limit": phaseHostCalls, "logs": h.logs, "phases": h.phases}
	if root := os.Getenv("REMOVELINK_LOG_OBSERVATIONS"); root != "" {
		if e := os.MkdirAll(root, 0700); e != nil {
			t.Fatal(e)
		}
		raw, _ := json.MarshalIndent(report, "", "  ")
		if e := os.WriteFile(filepath.Join(root, t.Name()+".json"), append(raw, '\n'), 0600); e != nil {
			t.Fatal(e)
		}
	}
	t.Logf("actual logger-inclusive SDK: phase=%d totalRPC=%d max=%d harddefault=64", len(h.phases), total, max)
}
func TestActualWorkerSourceLoggingBudgetAndStopJoin(t *testing.T) {
	f := startProtocol(t)
	a, links := f.links(120)
	h := &sourceLogProtocol{f: f}
	h.must(t, "init", map[string]any{"enabled": true, "monitor_dirs": filepath.Dir(a), "delayed_deletion": true, "delay_seconds": 86400, "delete_history": true})
	if e := os.Remove(a); e != nil {
		t.Fatal(e)
	}
	h.must(t, "test_event", map[string]any{"kind": "deleted", "path": a, "at": 1760000100})
	h.must(t, "stop_service", nil)
	for _, path := range links {
		if _, e := os.Stat(path); !errors.Is(e, os.ErrNotExist) {
			t.Fatal("stop owner returned before delete", path, e)
		}
	}
	h.mu.Lock()
	first, history, deletes, complete := 0, 0, 0, 0
	for _, l := range h.logs {
		if l == [2]string{"INFO", "初始化媒体文件清理插件"} {
			first++
		}
		if strings.HasPrefix(l[1], "删除转移记录：") {
			history++
		}
		if strings.HasPrefix(l[1], "延迟删除硬链接文件：") {
			deletes++
		}
		if l == [2]string{"DEBUG", "服务停止完成"} {
			complete++
		}
	}
	h.mu.Unlock()
	if first != 1 || history != 121 || deletes != 120 || complete != 2 {
		t.Fatal("missing/duplicate source logs", first, history, deletes, complete)
	}
	raw := f.call("test_snapshot", nil)
	var snapshot struct {
		Passes int
		Queue  []Task
	}
	if json.Unmarshal(raw.Result, &snapshot) != nil || snapshot.Passes != 0 || len(snapshot.Queue) != 0 {
		t.Fatal("stop left pass/task", string(raw.Result))
	}
	h.mu.Lock()
	total := 0
	for _, p := range h.phases {
		total += p.Calls
	}
	h.mu.Unlock()
	if total <= 256 {
		t.Fatal("did not exercise source logs across phase budget", total)
	}
	h.record(t, "120pending-stop-flush-with-every-source-log")
}
func TestActualWorkerInitFailureLogsBeforeConfigOnce(t *testing.T) {
	f := startProtocol(t)
	h := &sourceLogProtocol{f: f}
	if e := h.call("init", []int{1}); e == nil {
		t.Fatal("malformed source config accepted")
	}
	h.mu.Lock()
	logs := append([][2]string{}, h.logs...)
	h.mu.Unlock()
	if len(logs) != 1 || logs[0] != [2]string{"INFO", "初始化媒体文件清理插件"} {
		t.Fatal("source INFO must precede rejected config exactly once", logs)
	}
	h.must(t, "get_state", nil)
	h.record(t, "invalid-config-source-INFO-before-error-same-process-recovers")
}
func TestActualWorkerLoggerGuardStopsBeforeUnlink(t *testing.T) {
	f := startProtocol(t)
	a, links := f.links(2)
	h := &sourceLogProtocol{f: f}
	h.must(t, "init", map[string]any{"enabled": true, "monitor_dirs": filepath.Dir(a), "delayed_deletion": false})
	if e := os.Remove(a); e != nil {
		t.Fatal(e)
	}
	h.mu.Lock()
	h.reject = "立即删除硬链接文件："
	h.mu.Unlock()
	if e := h.call("test_event", map[string]any{"kind": "deleted", "path": a, "at": 1760000100}); e == nil {
		t.Fatal("revoked logger scope swallowed")
	}
	h.mu.Lock()
	if !h.rejected || h.afterReject != 0 {
		t.Fatal("calls after rejected log", h.afterReject)
	}
	h.reject = ""
	h.rejected = false
	h.mu.Unlock()
	for _, p := range links {
		if _, e := os.Stat(p); e != nil {
			t.Fatal("write occurred after failed pre-write source logger", p, e)
		}
	}
	h.must(t, "get_state", nil)
	h.record(t, "logger-guard-terminal-and-fresh-call-recovers")
}
func TestDeleteDoesNotRequireParentListPermission(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "write-search-only")
	if e := os.Mkdir(dir, 0700); e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(dir, "owned.mkv")
	if e := os.WriteFile(path, []byte("owned"), 0600); e != nil {
		t.Fatal(e)
	}
	if e := os.Chmod(dir, 0300); e != nil {
		t.Fatal(e)
	}
	defer os.Chmod(dir, 0700)
	calls := []string{}
	ops := &sdkOps{}
	ops.bind(context.Background(), func(_ context.Context, method string, raw json.RawMessage) (json.RawMessage, error) {
		calls = append(calls, method)
		if method != plugins.StorageDeleteCapability {
			return nil, fmt.Errorf("Path.unlink must not list parent: %s", method)
		}
		var r plugins.StorageFilesRequest
		if json.Unmarshal(raw, &r) != nil || r.FileItem == nil {
			return nil, errors.New("invalid guarded delete target")
		}
		if e := os.Remove(path); e != nil {
			return nil, e
		}
		return json.RawMessage(`true`), nil
	})
	defer ops.unbind()
	if e := ops.Unlink(path); e != nil {
		t.Fatal(e)
	}
	if len(calls) != 1 || calls[0] != plugins.StorageDeleteCapability {
		t.Fatal(calls)
	}
	t.Log("actual OS0300 unlink via SDK Delete callback; scoped host guards are separately owned host/Main tests")
}
