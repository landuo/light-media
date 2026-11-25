// Package cloudflarespeedtest ports the installed CloudflareSpeedTest 1.5.1
// control flow. Host owns configuration, scheduling, authority and messages;
// Runtime owns only the new instance's files and the explicitly approved CLI.
package cloudflarespeedtest

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type Object map[string]any

const (
	PluginID      = "CloudflareSpeedTest"
	Version       = "1.0.0"
	Action        = "cloudflare_speedtest"
	ReleaseAPI    = "https://api.github.com/repos/XIU2/CloudflareSpeedTest/releases/latest"
	ReleasePrefix = "https://github.com/XIU2/CloudflareSpeedTest/releases/download"
)

// Host calls are semantic effects, not an authority granted by worker input.
// Implementations must bind them to a live instance/generation/config revision.
// A write whose acknowledgement is lost must not be retried automatically.
// ScheduleOnce adds an inactive job; StartOnce activates it after config save.
// StopOnce removes/cancels jobs without joining a callback that could be
// blocked on this Machine. Lifecycle draining/joining happens outside these
// methods, with the running invocation's context cancelled first.
type Host interface {
	Guard(context.Context) error
	CustomHosts(context.Context) (Object, error)
	SaveCustomHosts(context.Context, Object) error
	SaveConfig(context.Context, Object) error
	ReloadCustomHosts(context.Context) error
	PostMessage(context.Context, Message) error
	SystemMessage(context.Context, string, string) error
	ScheduleOnce(context.Context, time.Time) error
	StartOnce(context.Context) error
	StopOnce(context.Context) error
	Now() time.Time
	Log(ctx context.Context, level, message string)
}

type Message struct {
	Type    string `json:"mtype,omitempty"`
	Title   string `json:"title"`
	Text    string `json:"text,omitempty"`
	Channel any    `json:"channel,omitempty"`
	User    any    `json:"userid,omitempty"`
}

type Event struct{ Data Object }
type Platform struct {
	OS      string `json:"os"`
	Machine string `json:"machine"`
}

// Backslash preserves the original Path(data + "\\binary") fallback test.
// On Unix native storage refuses to inspect the sibling path outside its root.
type FileRef struct {
	Name      string `json:"name"`
	Backslash bool   `json:"backslash"`
}
type ReleaseResponse struct {
	Available bool `json:"available"`
	Tag       any  `json:"tag"`
}
type InstallRequest struct {
	URL      string   `json:"url"`
	Archive  string   `json:"archive"`
	Version  string   `json:"version"`
	Platform Platform `json:"platform"`
}
type RunSpec struct {
	Binary string   `json:"binary"`
	Args   []string `json:"args"`
	Result string   `json:"result"`
	// Original Windows wrapper waits the full interval even if the child exits.
	WindowsWait time.Duration `json:"windows_wait"`
}
type RunResult struct {
	ExitCode   int  `json:"exit_code"`
	Completed  bool `json:"completed"`
	OwnTimeout bool `json:"own_timeout"`
}

type Runtime interface {
	Platform(context.Context) (Platform, error)
	Exists(context.Context, FileRef) (bool, error)
	Ensure(context.Context) error
	Reset(context.Context) error
	Latest(context.Context, bool) (ReleaseResponse, error)
	Download(context.Context, InstallRequest) error
	Extract(context.Context, InstallRequest) error
	Remove(context.Context, string) error
	Run(context.Context, RunSpec) (RunResult, error)
	ReadResult(context.Context) ([]byte, error)
}

// SourceError exposes a source Python exception class without exporting raw
// arguments, credentials, paths, CLI output or transport error strings.
type SourceError struct{ Class, Operation string }

func (e *SourceError) Error() string { return e.Class + ": " + e.Operation }
func ErrorClass(err error) string {
	var source *SourceError
	if errors.As(err, &source) {
		return source.Class
	}
	return ""
}
func sourceError(class, operation string) error { return &SourceError{class, operation} }

type Outcome struct{ Status, OldIP, NewIP string }
type Machine struct {
	mu     sync.Mutex
	h      Host
	r      Runtime
	config Object
	view   atomic.Pointer[Object]
}

func New(host Host, runtime Runtime) (*Machine, error) {
	if host == nil || runtime == nil {
		return nil, errors.New("CloudflareSpeedTest host/runtime required")
	}
	// These are class defaults, deliberately different from get_form defaults.
	m := &Machine{h: host, r: runtime, config: Object{
		"onlyonce": false, "cron": nil, "cf_ip": nil, "version": nil, "ipv4": false,
		"ipv6": false, "re_install": false, "additional_args": nil, "notify": false, "check": false,
	}}
	m.publishConfig()
	return m, nil
}

var configKeys = []string{"onlyonce", "cron", "cf_ip", "version", "ipv4", "ipv6", "re_install", "additional_args", "notify", "check"}

// Init corresponds to init_plugin, including consuming onlyonce before starting
// the +3 second job. The trusted host must suppress imported one-shot flags;
// replaying migration snapshots is not an authorized invocation of this method.
func (m *Machine) Init(ctx context.Context, config Object) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.guard(ctx); err != nil {
		return err
	}
	// Source stop_service catches scheduler errors and continues initialization.
	m.stop(ctx)
	if len(config) != 0 {
		for _, k := range configKeys {
			m.config[k] = config[k]
		}
		m.publishConfig()
	}
	if truth(m.config["ipv4"]) || truth(m.config["ipv6"]) {
		if truth(m.config["onlyonce"]) {
			m.h.Log(ctx, "info", "Cloudflare CDN优选服务启动，立即运行一次")
			if err := m.guard(ctx); err != nil {
				return err
			}
			if err := m.h.ScheduleOnce(ctx, m.h.Now().Add(3*time.Second)); err != nil {
				return m.schedulerFailure(ctx)
			}
			m.config["onlyonce"] = false
			if err := m.save(ctx); err != nil {
				m.stop(ctx)
				return errors.Join(err, m.schedulerFailure(ctx))
			}
			if err := m.guard(ctx); err != nil {
				return err
			}
			if err := m.h.StartOnce(ctx); err != nil {
				m.stop(ctx)
				return m.schedulerFailure(ctx)
			}
		}
	}
	return nil
}
func (m *Machine) schedulerFailure(ctx context.Context) error {
	m.h.Log(ctx, "error", "Cloudflare CDN优选服务出错")
	if err := m.guard(ctx); err != nil {
		return err
	}
	return m.h.SystemMessage(ctx, "Cloudflare IP优选", "Cloudflare CDN优选服务出错")
}
func (m *Machine) Stop(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.guard(ctx); err != nil {
		return err
	}
	m.stop(ctx)
	return nil
}
func (m *Machine) stop(ctx context.Context) {
	if err := m.h.StopOnce(ctx); err != nil {
		m.h.Log(ctx, "error", "停止Cloudflare IP优选服务失败")
	}
}
func (m *Machine) publishConfig() {
	snapshot := clone(m.config)
	m.view.Store(&snapshot)
}
func (m *Machine) State() bool { return configState(*m.view.Load()) }
func configState(config Object) bool {
	return truth(config["cf_ip"]) && truth(config["cron"]) && (truth(config["ipv4"]) || truth(config["ipv6"]))
}
func (m *Machine) guard(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return m.h.Guard(ctx)
}
func (m *Machine) save(ctx context.Context) error {
	m.publishConfig()
	if err := m.guard(ctx); err != nil {
		return err
	}
	c := clone(m.config)
	c["onlyonce"] = false
	return m.h.SaveConfig(ctx, c)
}

// Main deliberately preserves the source's order: CustomHosts then environment,
// correction, CLI, CustomHosts save, own config save, reload and notification.
// It serializes only this machine; implementations must not call it under an
// App global lock, SQL transaction or the scheduler's shutdown lock.
func (m *Machine) Main(ctx context.Context, event *Event) (Outcome, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if event != nil && (!truth(event.Data) || event.Data["action"] != Action) {
		return Outcome{Status: "ignored_event"}, nil
	}
	if err := m.guard(ctx); err != nil {
		return Outcome{}, err
	}
	custom, err := m.h.CustomHosts(ctx)
	if err != nil {
		return Outcome{}, err
	}
	if truth(m.config["cf_ip"]) && !truth(custom) {
		m.h.Log(ctx, "error", "Cloudflare CDN优选依赖于自定义Hosts，请先维护hosts")
		return Outcome{Status: "missing_hosts"}, nil
	}
	// Preserve the source's boolean precedence: None.get fails if cf_ip is false.
	if custom == nil {
		return Outcome{}, sourceError("AttributeError", "CustomHosts.get")
	}
	if !truth(custom["hosts"]) {
		m.h.Log(ctx, "error", "Cloudflare CDN优选依赖于自定义Hosts，请先维护hosts")
		return Outcome{Status: "missing_hosts"}, nil
	}
	if !truth(m.config["cf_ip"]) {
		m.h.Log(ctx, "error", "CloudflareSpeedTest加载成功，首次运行，需要配置优选ip")
		return Outcome{Status: "missing_ip"}, nil
	}
	if event != nil {
		m.h.Log(ctx, "info", "收到命令，开始Cloudflare IP优选 ...")
		if err = m.guard(ctx); err != nil {
			return Outcome{}, err
		}
		if err = m.h.PostMessage(ctx, Message{Title: "开始Cloudflare IP优选 ...", Channel: event.Data["channel"], User: event.Data["user"]}); err != nil {
			return Outcome{}, err
		}
	}
	if !truth(m.config["ipv4"]) && !truth(m.config["ipv6"]) {
		m.config["ipv4"] = true
		if err = m.save(ctx); err != nil {
			return Outcome{}, err
		}
		m.h.Log(ctx, "warn", "Cloudflare CDN优选未指定ip类型，默认ipv4")
	}
	ok, release, err := m.checkEnvironment(ctx)
	if err != nil {
		return Outcome{}, err
	}
	if ok && release != nil && truth(release) {
		m.config["version"] = release
		if err = m.save(ctx); err != nil {
			return Outcome{}, err
		}
	}
	hosts, err := hostLines(custom["hosts"])
	if err != nil {
		return Outcome{}, err
	}
	if truth(m.config["check"]) {
		if err = m.correctIP(ctx, hosts); err != nil {
			return Outcome{}, err
		}
	}
	if !ok {
		m.h.Log(ctx, "error", "获取到最优ip格式错误，请重试")
		m.config["onlyonce"] = false
		if err = m.save(ctx); err != nil {
			return Outcome{}, err
		}
		m.stop(ctx)
		return Outcome{Status: "environment_failed"}, nil
	}
	args, err := SplitArgs(pyString(m.config["additional_args"]))
	if err != nil {
		return Outcome{}, err
	}
	args = append(args, "-o", "result_hosts.txt")
	if truth(m.config["ipv4"]) {
		args = append(args, "-f", "ip.txt")
	}
	if truth(m.config["ipv6"]) {
		args = append(args, "-f", "ipv6.txt")
	}
	binary := "cfst"
	suffix := ""
	platform, err := m.r.Platform(ctx)
	if err != nil {
		return Outcome{}, err
	}
	if platform.OS == "windows" {
		suffix = ".exe"
	}
	present, err := m.exists(ctx, "cfst"+suffix, false)
	if err != nil {
		return Outcome{}, err
	}
	if !present {
		old, e := m.exists(ctx, "CloudflareST"+suffix, false)
		if e != nil {
			return Outcome{}, e
		}
		if old {
			binary = "CloudflareST"
		}
	}
	spec := RunSpec{Binary: binary + suffix, Args: args, Result: "result_hosts.txt"}
	if platform.OS == "windows" {
		spec.WindowsWait = 600 * time.Second
		if strings.Contains(pyString(m.config["additional_args"]), "-dd") {
			spec.WindowsWait = 120 * time.Second
		}
	}
	if err = m.guard(ctx); err != nil {
		return Outcome{}, err
	}
	m.h.Log(ctx, "info", "正在进行Cloudflare CDN优选，请耐心等待")
	// Source ignores completed exit codes. An unacknowledged/unknown dispatch is
	// different: return it and do not read a stale CSV or repeat the process.
	run, err := m.r.Run(ctx, spec)
	if err != nil {
		return Outcome{}, err
	}
	if !run.Completed && !run.OwnTimeout {
		return Outcome{}, errors.New("CloudflareSpeedTest CLI outcome unknown")
	}
	if err = m.guard(ctx); err != nil {
		return Outcome{}, err
	}
	data, err := m.r.ReadResult(ctx)
	if err != nil {
		return Outcome{}, err
	}
	best := BestIP(data, platform.OS == "windows")
	if !validIP(best) {
		m.h.Log(ctx, "info", "未获取到有效的最优 IP")
		return Outcome{Status: "invalid_result"}, nil
	}
	m.h.Log(ctx, "info", "获取到最优ip==>["+best+"]")
	if m.config["cf_ip"] == best {
		m.h.Log(ctx, "info", "CloudflareSpeedTest CDN优选ip未变，不做处理")
		return Outcome{Status: "unchanged", OldIP: best, NewIP: best}, nil
	}
	old, ok := m.config["cf_ip"].(string)
	if !ok {
		return Outcome{}, sourceError("TypeError", "hosts.replace")
	}
	var newHosts strings.Builder
	for _, line := range hosts {
		if !truth(line) || line == "\n" {
			continue
		}
		fields := pyFields(pyString(line))
		if len(fields) == 0 {
			return Outcome{}, sourceError("IndexError", "hosts first token")
		}
		text, ok := line.(string)
		if !ok {
			return Outcome{}, sourceError("AttributeError", "hosts.replace")
		}
		if fields[0] == old {
			text = strings.ReplaceAll(text, old, best)
		}
		newHosts.WriteString(strings.ReplaceAll(text, "\n", ""))
		newHosts.WriteByte('\n')
	}
	if err = m.guard(ctx); err != nil {
		return Outcome{}, err
	}
	if err = m.h.SaveCustomHosts(ctx, Object{"hosts": newHosts.String(), "err_hosts": custom["err_hosts"], "enabled": true}); err != nil {
		return Outcome{}, err
	}
	m.config["cf_ip"] = best
	if err = m.save(ctx); err != nil {
		return Outcome{}, err
	}
	if err = m.guard(ctx); err != nil {
		return Outcome{}, err
	}
	if err = m.h.ReloadCustomHosts(ctx); err != nil {
		return Outcome{}, err
	}
	m.h.Log(ctx, "info", "Cloudflare CDN优选ip已替换自定义Hosts插件，已通知重载")
	if truth(m.config["notify"]) {
		if err = m.guard(ctx); err != nil {
			return Outcome{}, err
		}
		if err = m.h.PostMessage(ctx, Message{Type: "SiteMessage", Title: "【Cloudflare优选任务完成】", Text: "原ip：" + old + "\n新ip：" + best}); err != nil {
			return Outcome{}, err
		}
	}
	return Outcome{Status: "updated", OldIP: old, NewIP: best}, nil
}

func (m *Machine) correctIP(ctx context.Context, hosts []any) error {
	counts := map[string]int{}
	order := []string{}
	for _, line := range hosts {
		if !truth(line) {
			continue
		}
		s, ok := line.(string)
		if !ok {
			return sourceError("AttributeError", "hosts.split")
		}
		fields := pyFields(s)
		if len(fields) == 0 {
			return sourceError("IndexError", "hosts first token")
		}
		ip := fields[0]
		if counts[ip] == 0 {
			order = append(order, ip)
		}
		counts[ip]++
	}
	max, count, unique := "", 0, false
	for _, ip := range order {
		if counts[ip] > count {
			max, count, unique = ip, counts[ip], true
		} else if counts[ip] == count {
			unique = false
		}
	}
	if unique && m.config["cf_ip"] != max {
		m.config["cf_ip"] = max
		m.publishConfig()
		if validIP(max) {
			m.h.Log(ctx, "info", "获取到自定义hosts插件中ip "+max+" 出现次数最多，已自动校正优选ip")
		}
	}
	return nil
}
func (m *Machine) exists(ctx context.Context, name string, backslash bool) (bool, error) {
	if err := m.guard(ctx); err != nil {
		return false, err
	}
	return m.r.Exists(ctx, FileRef{name, backslash})
}
func (m *Machine) anyExists(ctx context.Context, files ...FileRef) (bool, error) {
	for _, f := range files {
		ok, err := m.exists(ctx, f.Name, f.Backslash)
		if err != nil {
			return false, err
		}
		if ok {
			return true, nil
		}
	}
	return false, nil
}
func (m *Machine) checkEnvironment(ctx context.Context) (bool, any, error) {
	install := false
	if truth(m.config["re_install"]) {
		install = true
		if err := m.guard(ctx); err != nil {
			return false, nil, err
		}
		if err := m.r.Reset(ctx); err != nil {
			return false, nil, err
		}
		m.h.Log(ctx, "info", "已删除当前实例的 CloudflareSpeedTest 数据，开始重新安装")
	}
	if err := m.guard(ctx); err != nil {
		return false, nil, err
	}
	if err := m.r.Ensure(ctx); err != nil {
		return false, nil, err
	}
	response, err := m.r.Latest(ctx, false)
	if err != nil {
		return false, nil, err
	}
	if !response.Available {
		if err = m.guard(ctx); err != nil {
			return false, nil, err
		}
		response, err = m.r.Latest(ctx, true)
		if err != nil {
			return false, nil, err
		}
	}
	var release any
	if response.Available {
		release = pyString(response.Tag)
	}
	if !truth(release) {
		exists, e := m.exists(ctx, "cfst", false)
		if e != nil {
			return false, nil, e
		}
		if exists {
			m.h.Log(ctx, "warn", "获取CloudflareSpeedTest版本失败，存在可执行版本，继续运行")
			return true, nil, nil
		}
		if truth(m.config["version"]) {
			m.h.Log(ctx, "error", "获取CloudflareSpeedTest版本失败，开始安装上次运行版本")
			install = true
		} else {
			release = "v2.2.2"
			m.config["version"] = release
			m.h.Log(ctx, "error", "获取CloudflareSpeedTest版本失败，开始安装默认版本 v2.2.2")
			install = true
		}
	}
	if !install && !reflect.DeepEqual(release, m.config["version"]) {
		m.h.Log(ctx, "info", "检测到CloudflareSpeedTest有版本更新，开始安装")
		install = true
	}
	if !install && reflect.DeepEqual(release, m.config["version"]) {
		exists, e := m.anyExists(ctx, FileRef{Name: "cfst"}, FileRef{Name: "CloudflareST"}, FileRef{Name: "cfst.exe"})
		if e != nil {
			return false, nil, e
		}
		if !exists {
			m.h.Log(ctx, "warn", "未检测到CloudflareSpeedTest本地版本，重新安装")
			install = true
		}
	}
	if !install {
		m.h.Log(ctx, "info", "CloudflareSpeedTest无新版本，存在可执行版本，继续运行")
		return true, nil, nil
	}
	p, err := m.r.Platform(ctx)
	if err != nil {
		return false, nil, err
	}
	arch := "arm64"
	if p.Machine == "x86_64" {
		arch = "amd64"
	}
	asset := "cfst_linux_" + arch + ".tar.gz"
	if p.OS == "darwin" {
		asset = "cfst_darwin_" + arch + ".zip"
	}
	if p.OS == "windows" {
		asset = "cfst_windows_amd64.zip"
	}
	req := InstallRequest{URL: ReleasePrefix + "/" + pyString(release) + "/" + asset, Archive: asset, Version: pyString(release), Platform: p}
	ok, version, e := m.install(ctx, req, release)
	return ok, version, e
}
func (m *Machine) install(ctx context.Context, req InstallRequest, release any) (bool, any, error) {
	archive, err := m.exists(ctx, req.Archive, false)
	if err != nil {
		return false, nil, err
	}
	if !archive {
		if err = m.guard(ctx); err != nil {
			return false, nil, err
		}
		if err = m.r.Download(ctx, req); err != nil {
			return false, nil, err
		}
		archive, err = m.exists(ctx, req.Archive, false)
		if err != nil {
			return false, nil, err
		}
	}
	if !archive {
		ok, e := m.anyExists(ctx, FileRef{Name: "cfst"}, FileRef{Name: "cfst.exe", Backslash: true}, FileRef{Name: "CloudflareST"}, FileRef{Name: "CloudflareST.exe", Backslash: true})
		if e != nil {
			return false, nil, e
		}
		if ok {
			m.h.Log(ctx, "warn", "CloudflareSpeedTest安装失败，存在可执行版本，继续运行")
			return true, nil, nil
		}
		if err = m.guard(ctx); err != nil {
			return false, nil, err
		}
		if err = m.r.Reset(ctx); err != nil {
			return false, nil, err
		}
		return false, nil, nil
	}
	if err = m.guard(ctx); err != nil {
		return false, nil, err
	}
	if err = m.r.Extract(ctx, req); err != nil {
		// Only an explicit completed extraction failure maps to the source's
		// try/except fallback. Unknown effects/owner cancellation always stop.
		var extraction *ExtractionError
		if !errors.As(err, &extraction) {
			return false, nil, err
		}
		ok, e := m.anyExists(ctx, FileRef{Name: "cfst"}, FileRef{Name: "CloudflareST.exe", Backslash: true})
		if e != nil {
			return false, nil, e
		}
		if ok {
			m.h.Log(ctx, "error", "CloudflareSpeedTest解压失败，继续使用现版本运行")
			return true, nil, nil
		}
		if e = m.guard(ctx); e != nil {
			return false, nil, e
		}
		if e = m.r.Reset(ctx); e != nil {
			return false, nil, e
		}
		return false, nil, nil
	}
	files := []FileRef{{Name: "cfst"}, {Name: "CloudflareST"}}
	if req.Platform.OS == "windows" {
		files = []FileRef{{Name: "cfst.exe", Backslash: true}, {Name: "CloudflareST.exe", Backslash: true}}
	} else {
		if err = m.guard(ctx); err != nil {
			return false, nil, err
		}
		if err = m.r.Remove(ctx, req.Archive); err != nil {
			return false, nil, err
		}
	}
	ok, err := m.anyExists(ctx, files...)
	if err != nil {
		return false, nil, err
	}
	if ok {
		m.h.Log(ctx, "info", "CloudflareSpeedTest安装成功")
		return true, release, nil
	}
	if err = m.guard(ctx); err != nil {
		return false, nil, err
	}
	if err = m.r.Reset(ctx); err != nil {
		return false, nil, err
	}
	return false, nil, nil
}

type ExtractionError struct{}

func (*ExtractionError) Error() string { return "CloudflareSpeedTest archive extraction failed" }

// BestIP mirrors execute().readline().strip(): source Unix awk outputs the
// first field then strips it; Windows strips the whole row before splitting.
func BestIP(data []byte, windows bool) string {
	rows := strings.SplitN(string(data), "\n", 3)
	if len(rows) < 2 {
		return ""
	}
	row := rows[1]
	if windows {
		row = pyStrip(row)
	}
	first := strings.SplitN(row, ",", 2)[0]
	if !windows {
		first = pyStrip(first)
	}
	return first
}
func validIP(s string) bool { a, e := netip.ParseAddr(s); return e == nil && a.Zone() == "" }
func hostLines(v any) ([]any, error) {
	switch v := v.(type) {
	case string:
		parts := strings.Split(v, "\n")
		out := make([]any, len(parts))
		for i, s := range parts {
			out[i] = s
		}
		return out, nil
	case []any:
		return v, nil
	case []string:
		out := make([]any, len(v))
		for i, s := range v {
			out[i] = s
		}
		return out, nil
	default:
		return nil, sourceError("TypeError", "hosts iterable")
	}
}
func truth(v any) bool {
	if v == nil {
		return false
	}
	r := reflect.ValueOf(v)
	switch r.Kind() {
	case reflect.Bool:
		return r.Bool()
	case reflect.String, reflect.Array, reflect.Slice, reflect.Map:
		return r.Len() != 0
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return r.Int() != 0
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return r.Uint() != 0
	case reflect.Float32, reflect.Float64:
		return r.Float() != 0
	}
	return true
}
func pyString(v any) string {
	switch v := v.(type) {
	case nil:
		return "None"
	case string:
		return v
	case bool:
		if v {
			return "True"
		}
		return "False"
	default:
		return fmt.Sprint(v)
	}
}
func pySpace(r rune) bool {
	return r >= 9 && r <= 13 || r >= 0x1c && r <= 0x20 || r == 0x85 || r == 0xa0 || r == 0x1680 || r >= 0x2000 && r <= 0x200a || r == 0x2028 || r == 0x2029 || r == 0x202f || r == 0x205f || r == 0x3000
}
func pyFields(s string) []string { return strings.FieldsFunc(s, pySpace) }
func pyStrip(s string) string    { return strings.TrimFunc(s, pySpace) }
func clone(m Object) Object {
	if m == nil {
		return nil
	}
	out := Object{}
	for k, v := range m {
		out[k] = cloneValue(v)
	}
	return out
}
func cloneValue(v any) any {
	switch v := v.(type) {
	case Object:
		return clone(v)
	case map[string]any:
		return clone(Object(v))
	case []any:
		out := make([]any, len(v))
		for i, x := range v {
			out[i] = cloneValue(x)
		}
		return out
	case []string:
		return append([]string(nil), v...)
	default:
		return v
	}
}
