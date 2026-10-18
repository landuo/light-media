package cloudflarespeedtest

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

type effect struct {
	Kind string `json:"kind"`
	Data any    `json:"data"`
}
type fixture struct {
	Name        string            `json:"name"`
	Config      Object            `json:"config"`
	Custom      Object            `json:"custom"`
	Files       []string          `json:"files"`
	Latest      []ReleaseResponse `json:"latest"`
	Platform    Platform          `json:"platform"`
	Download    bool              `json:"download"`
	Extract     []string          `json:"extract"`
	ExtractFail bool              `json:"extract_fail"`
	Result      string            `json:"result"`
	Event       *Event            `json:"event"`
	OnlyInit    bool              `json:"only_init"`
	Expected    oracleResult      `json:"expected"`
}
type oracleResult struct {
	Config  Object   `json:"config"`
	Effects []effect `json:"effects"`
	Error   string   `json:"error"`
	State   bool     `json:"state"`
}
type fake struct {
	f                             fixture
	files                         map[string]bool
	effects                       []effect
	latest                        int
	guardErr, errorSave, errorRun error
	runResult                     RunResult
	custom                        Object
	denyAfter                     string
	block, entered                chan struct{}
}

func newFake(f fixture) *fake {
	files := map[string]bool{}
	for _, s := range f.Files {
		files[s] = true
	}
	if f.Platform.OS == "" {
		f.Platform = Platform{"linux", "x86_64"}
	}
	return &fake{f: f, files: files, custom: clone(f.Custom), effects: []effect{}, runResult: RunResult{Completed: true}}
}
func (f *fake) add(k string, d any) {
	f.effects = append(f.effects, effect{k, d})
	if f.denyAfter == k {
		f.guardErr = errors.New("owner retired")
	}
}
func (f *fake) Guard(context.Context) error                 { return f.guardErr }
func (f *fake) CustomHosts(context.Context) (Object, error) { return clone(f.custom), nil }
func (f *fake) SaveCustomHosts(_ context.Context, o Object) error {
	f.add("custom", clone(o))
	if f.errorSave != nil {
		return f.errorSave
	}
	f.custom = clone(o)
	return nil
}
func (f *fake) SaveConfig(_ context.Context, o Object) error   { f.add("config", clone(o)); return nil }
func (f *fake) ReloadCustomHosts(context.Context) error        { f.add("reload", "CustomHosts"); return nil }
func (f *fake) PostMessage(_ context.Context, m Message) error { f.add("message", m); return nil }
func (f *fake) SystemMessage(_ context.Context, title, text string) error {
	f.add("system", title)
	return nil
}
func (f *fake) ScheduleOnce(_ context.Context, when time.Time) error {
	f.add("schedule", when.Sub(f.Now()).Seconds())
	return nil
}
func (f *fake) StartOnce(context.Context) error { f.add("start", nil); return nil }
func (f *fake) StopOnce(context.Context) error  { f.add("stop", nil); return nil }
func (f *fake) Now() time.Time                  { return time.Unix(1700000000, 0) }
func (f *fake) Log(string, string)              {}
func (f *fake) Platform() Platform              { return f.f.Platform }
func (f *fake) Exists(_ context.Context, r FileRef) (bool, error) {
	if r.Backslash && f.f.Platform.OS != "windows" {
		return false, nil
	}
	return f.files[r.Name], nil
}
func (f *fake) Ensure(context.Context) error { return nil }
func (f *fake) Reset(context.Context) error {
	f.add("reset", nil)
	f.files = map[string]bool{}
	return nil
}
func (f *fake) Latest(_ context.Context, proxy bool) (ReleaseResponse, error) {
	f.add("latest", proxy)
	i := f.latest
	f.latest++
	if i >= len(f.f.Latest) {
		return ReleaseResponse{}, nil
	}
	return f.f.Latest[i], nil
}
func (f *fake) Download(_ context.Context, r InstallRequest) error {
	f.add("download", r)
	if f.f.Download {
		f.files[r.Archive] = true
	}
	return nil
}
func (f *fake) Extract(_ context.Context, r InstallRequest) error {
	f.add("extract", r)
	for _, s := range f.f.Extract {
		f.files[s] = true
	}
	if f.f.ExtractFail {
		return &ExtractionError{}
	}
	return nil
}
func (f *fake) Remove(_ context.Context, name string) error {
	f.add("remove", name)
	delete(f.files, name)
	return nil
}
func (f *fake) Run(_ context.Context, s RunSpec) (RunResult, error) {
	f.add("run", s)
	if f.entered != nil {
		f.entered <- struct{}{}
		<-f.block
	}
	return f.runResult, f.errorRun
}
func (f *fake) ReadResult(context.Context) ([]byte, error) { return []byte(f.f.Result), nil }
func basic() fixture {
	return fixture{Config: Object{"cf_ip": "1.1.1.1", "version": "v2.3.0", "ipv4": true, "ipv6": false, "additional_args": "-dd -t 2", "notify": true, "cron": "0 7 * * *"}, Custom: Object{"hosts": "1.1.1.1 a.example\n9.9.9.9 b.example", "enabled": false, "err_hosts": []any{"retained"}}, Files: []string{"cfst"}, Latest: []ReleaseResponse{{Available: true, Tag: "v2.3.0"}}, Result: "IP,latency\n2.2.2.2,10\n", Platform: Platform{"linux", "x86_64"}}
}
func exercise(t *testing.T, f fixture) (*Machine, *fake, Outcome, error) {
	t.Helper()
	host := newFake(f)
	m, err := New(host, host)
	if err != nil {
		t.Fatal(err)
	}
	if err = m.Init(context.Background(), f.Config); err != nil {
		return m, host, Outcome{}, err
	}
	if f.OnlyInit {
		return m, host, Outcome{}, nil
	}
	out, err := m.Main(context.Background(), f.Event)
	return m, host, out, err
}

func TestSourceOracle(t *testing.T) {
	data, err := os.ReadFile("testdata/oracle.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixtures []fixture
	if err = json.Unmarshal(data, &fixtures); err != nil {
		t.Fatal(err)
	}
	if len(fixtures) < 50 {
		t.Fatal("source cases missing")
	}
	for _, f := range fixtures {
		t.Run(f.Name, func(t *testing.T) {
			m, h, _, err := exercise(t, f)
			actual := oracleResult{m.Config(), h.effects, ErrorClass(err), m.State()}
			a, _ := json.Marshal(actual)
			e, _ := json.Marshal(f.Expected)
			var av, ev any
			_ = json.Unmarshal(a, &av)
			_ = json.Unmarshal(e, &ev)
			if !reflect.DeepEqual(av, ev) {
				t.Fatalf("source mismatch\nactual %s\nsource %s\nerr %v", a, e, err)
			}
		})
	}
}

func TestUpdateOrderAndUnknownNeverRetry(t *testing.T) {
	f := basic()
	m, h, out, err := exercise(t, f)
	if err != nil || out.Status != "updated" {
		t.Fatalf("%+v %v", out, err)
	}
	want := []string{"stop", "latest", "run", "custom", "config", "reload", "message"}
	var kinds []string
	for _, e := range h.effects {
		kinds = append(kinds, e.Kind)
	}
	if !reflect.DeepEqual(kinds, want) {
		t.Fatal(kinds)
	}
	if m.Config()["cf_ip"] != "2.2.2.2" || h.custom["enabled"] != true {
		t.Fatal(m.Config(), h.custom)
	}
	for _, key := range []string{"run", "custom", "config", "reload"} {
		t.Run(key, func(t *testing.T) {
			host := newFake(f)
			machine, _ := New(host, host)
			_ = machine.Init(context.Background(), f.Config)
			host.effects = nil
			switch key {
			case "run":
				host.errorRun = &CLIError{Dispatched: true, Operation: "wait"}
			case "custom":
				host.errorSave = errors.New("write acknowledgement lost")
			default:
				host.denyAfter = key
			}
			_, err := machine.Main(context.Background(), nil)
			if err == nil {
				t.Fatal("unknown/retired operation accepted")
			}
			runs, custom := 0, 0
			for _, e := range host.effects {
				if e.Kind == "run" {
					runs++
				}
				if e.Kind == "custom" {
					custom++
				}
			}
			if runs != 1 || custom > 1 {
				t.Fatal(host.effects)
			}
		})
	}
}

func TestEventsAPIAndService(t *testing.T) {
	f := basic()
	f.Event = &Event{Object{"action": "other"}}
	_, h, out, err := exercise(t, f)
	if err != nil || out.Status != "ignored_event" || len(h.effects) != 1 {
		t.Fatal(out, err, h.effects)
	}
	host := newFake(basic())
	m, _ := New(host, host)
	_ = m.Init(context.Background(), basic().Config)
	before := len(host.effects)
	res, err := m.API(context.Background(), false)
	if err != nil || res.Success || res.Message != "API密钥错误" || len(host.effects) != before {
		t.Fatal(res, err)
	}
	services, err := m.Services(func(s string) error {
		if s != "0 7 * * *" {
			t.Fatal(s)
		}
		return nil
	})
	if err != nil || len(services) != 1 || services[0].ID != PluginID {
		t.Fatal(services, err)
	}
	if _, err = m.Services(nil); err == nil {
		t.Fatal("unconfigured cron accepted")
	}
	host.custom = Object{}
	res, err = m.API(context.Background(), true)
	if err != nil || !res.Success {
		t.Fatal("source early return still API success", res, err)
	}
}

func TestConcurrentMachineSerializesSourceEffects(t *testing.T) {
	f := basic()
	f.Result = "IP,x\n1.1.1.1,1\n"
	h := newFake(f)
	m, _ := New(h, h)
	_ = m.Init(context.Background(), f.Config)
	var wg sync.WaitGroup
	for range 12 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if out, err := m.Main(context.Background(), nil); err != nil || out.Status != "unchanged" {
				t.Errorf("%+v %v", out, err)
			}
		}()
	}
	wg.Wait()
	count := 0
	for _, e := range h.effects {
		if e.Kind == "run" {
			count++
		}
	}
	if count != 12 {
		t.Fatal(count)
	}
}

func TestStopOwnerCancellationAndConfigIsolation(t *testing.T) {
	f := basic()
	h := newFake(f)
	m, _ := New(h, h)
	if err := m.Init(context.Background(), f.Config); err != nil {
		t.Fatal(err)
	}
	snapshot := m.Config()
	snapshot["cf_ip"] = "evil"
	if m.Config()["cf_ip"] != "1.1.1.1" {
		t.Fatal("mutable config alias")
	}
	h.guardErr = errors.New("retired")
	before := len(h.effects)
	if _, err := m.Main(context.Background(), nil); err == nil || len(h.effects) != before {
		t.Fatal("retired owner dispatched")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	h.guardErr = nil
	if _, err := m.Main(ctx, nil); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestArgumentsAndCSV(t *testing.T) {
	args, err := SplitArgs(`-dd -url "https://example.test/a b" -t '4' ""`)
	if err != nil || !reflect.DeepEqual(args, []string{"-dd", "-url", "https://example.test/a b", "-t", "4", ""}) {
		t.Fatal(args, err)
	}
	for _, s := range []string{"x;rm file", "x && id", "$(id)", "`id`", "$HOME", `"$HOME"`, "a\nb", "'missing", "tail\\"} {
		if _, err := SplitArgs(s); !errors.Is(err, ErrShellSyntax) {
			t.Fatalf("unsafe args accepted %q %v", s, err)
		}
	}
	if got := BestIP([]byte("ip,x\n 2.2.2.2 ,1\r\n"), false); got != "2.2.2.2" {
		t.Fatal(got)
	}
	if got := BestIP([]byte("ip,x\n 2.2.2.2 ,1\r\n"), true); got != "2.2.2.2 " {
		t.Fatal(got)
	}
	for _, s := range []string{"001.2.3.4", "::1%zone", "2.2.2.2:80", "", "[::1]"} {
		if validIP(s) {
			t.Fatal(s)
		}
	}
	if !validIP("::ffff:192.0.2.1") {
		t.Fatal("valid inet_pton IPv6")
	}
}

func TestHandleNoWorkerSelectedAuthority(t *testing.T) {
	var calls []string
	host := func(_ context.Context, method string, raw json.RawMessage) (json.RawMessage, error) {
		calls = append(calls, method+" "+string(raw))
		return json.RawMessage(`{}`), nil
	}
	for _, input := range []string{`{"action":"event","input":{"event_id":"e"}}`, `{"action":"schedule","input":{"job_id":"j"}}`, `{"action":"http","input":{"request_id":"r"}}`, `{"action":"reload","config":{"cf_ip":"attacker"}}`} {
		if _, err := Handle(context.Background(), "invoke", json.RawMessage(input), host); err != nil {
			t.Fatal(err)
		}
	}
	for _, input := range []string{`{"action":"event","input":{"event_id":"e","config":"evil"}}`, `{"action":"event","input":{}}`, `{"action":"http","input":{"apikey":"secret"}}`, `{"action":"schedule","input":{"job_id":"j"} } true`} {
		if _, err := Handle(context.Background(), "invoke", json.RawMessage(input), host); err == nil {
			t.Fatal("unbound accepted", input)
		}
	}
	if !strings.HasSuffix(calls[3], "{}") || len(calls) != 4 {
		t.Fatal(calls)
	}
	raw, err := Handle(context.Background(), "config_form", nil, nil)
	if err != nil || !json.Valid(raw) {
		t.Fatal(err)
	}
}
