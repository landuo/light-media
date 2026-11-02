package systemcommands

import (
	"bytes"
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"path"
	"reflect"
	"sort"
	"strconv"
	"testing"
)

//go:embed testdata/source-system23.json
var source23 []byte

const source23SHA = "9804f571e83e2d78fbb897c34751b60f858d768f2638c9bf798ee6375df10d45"

type object = map[string]any

func obj(v any) object   { return v.(map[string]any) }
func arr(v any) []any    { return v.([]any) }
func str(v any) string   { return v.(string) }
func boolean(v any) bool { return v.(bool) }
func number(v any) int {
	n, e := v.(json.Number).Int64()
	if e != nil {
		panic(e)
	}
	return int(n)
}
func decode(raw []byte) any {
	var v any
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	if err := d.Decode(&v); err != nil {
		panic(err)
	}
	return v
}
func clone(v any) any {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return decode(b)
}
func same(t *testing.T, label string, got, want any) {
	t.Helper()
	g, w := clone(got), clone(want)
	if !reflect.DeepEqual(g, w) {
		gb, _ := json.MarshalIndent(g, "", " ")
		wb, _ := json.MarshalIndent(w, "", " ")
		t.Fatalf("%s differs\ngot %s\nwant %s", label, gb, wb)
	}
}
func sourceCases(t *testing.T) []any {
	t.Helper()
	sum := sha256.Sum256(source23)
	if hex.EncodeToString(sum[:]) != source23SHA {
		t.Fatal("original raw changed")
	}
	root := obj(decode(source23))
	if root["schema"] != "command40-system-source/v1" || number(root["input_count"]) != 87 || number(root["selected_node_count"]) != 16 {
		t.Fatal("original source provenance")
	}
	cases := arr(root["cases"])
	if len(cases) != 23 {
		t.Fatal("source case count")
	}
	return cases
}
func injected(spec any) error {
	if spec == nil {
		return nil
	}
	s := obj(spec)
	return sourceError(str(s["class"]), str(s["message"]))
}
func channelData(c *Channel) any {
	if c == nil {
		return nil
	}
	return object{"enum": "MessageChannel", "name": c.Name, "value": c.Value}
}
func noticeData(n Notice) object {
	m := object{"channel": channelData(n.Channel), "userid": n.UserID, "title": n.Title, "save_history": n.SaveHistory}
	if n.IncludeSource {
		if n.Source == nil {
			m["source"] = nil
		} else {
			m["source"] = *n.Source
		}
	}
	return m
}

var channels = []Channel{{"Wechat", "微信"}, {"Feishu", "飞书"}, {"WechatClawBot", "微信ClawBot"}, {"Telegram", "Telegram"}, {"Slack", "Slack"}, {"Discord", "Discord"}, {"SynologyChat", "SynologyChat"}, {"VoceChat", "VoceChat"}, {"Web", "Web"}, {"WebAgent", "WebAgent"}, {"WebPush", "WebPush"}, {"QQ", "QQ"}}

func channelNamed(v any) *Channel {
	if v == nil {
		return nil
	}
	for _, c := range channels {
		if c.Name == str(v) {
			x := c
			return &x
		}
	}
	panic("unbound channel input")
}

// The following fixtures implement only dependency inputs. They never consult
// original observed outputs. The complete trace is produced by target calls.
type recording struct {
	input            object
	trace            []any
	notices          []any
	cache            object
	existing         map[string]bool
	requests, notice int
	remoteError      error
	guard            func(context.Context) error
	onOpen           func()
	closed           int
}

func newRecording(input object) *recording {
	p := &recording{input: input, trace: []any{}, notices: []any{}, cache: object{}, existing: map[string]bool{}}
	c := obj(input["cache"])
	if boolean(c["seed_present"]) {
		p.cache["__system_restart__"] = object{"recording_serialized_value": clone(c["seed"]), "decode_error": clone(c["decode_error"])}
	}
	for _, x := range arr(obj(input["backup"])["existing_targets"]) {
		p.existing[str(x)] = true
	}
	return p
}
func (p *recording) log(event string, fields object) {
	m := object{"event": event}
	for k, v := range fields {
		m[k] = clone(v)
	}
	p.trace = append(p.trace, m)
}
func (p *recording) ports() Ports {
	return Ports{
		Guard: p.guard, FS: p, Cache: p, Codec: recordingCodec{p},
		Remote: func(ctx context.Context, side Side) (Response, error) {
			p.log("RequestUtils.init", object{"kwargs": object{"proxies": object{"https": "recording://owned-proxy"}, "headers": object{"User-Agent": "owned-original-contract"}}})
			urls := map[Side]string{Backend: "https://api.github.com/repos/landuo/MoviePilot/releases", Frontend: "https://api.github.com/repos/jxxghp/MoviePilot-Frontend/releases"}
			if p.requests >= len(arr(p.input["requests"])) || (p.requests%2 == 0) != (side == Backend) {
				return nil, boundary("unexpected dependency request order/count")
			}
			spec := obj(arr(p.input["requests"])[p.requests])
			p.requests++
			p.log("RequestUtils.get_res", object{"url": urls[side]})
			if p.remoteError != nil {
				return nil, p.remoteError
			}
			if err := injected(spec["request_error"]); err != nil {
				return nil, err
			}
			if boolean(spec["none"]) {
				return nil, nil
			}
			return recordingResponse{p, spec}, nil
		},
		LocalBackend: func(context.Context) (string, error) { return "v2.15.7", nil },
		FrontendPath: func(context.Context) (string, error) {
			p.log("SystemUtils.is_frozen", object{"value": false})
			return "/owned/frontend/version.txt", nil
		},
		FrontendFallback: func(context.Context) (string, error) { return "v2.15.6", nil },
		IsDocker: func(context.Context) (bool, error) {
			v := boolean(obj(p.input["backup"])["docker"])
			p.log("SystemUtils.is_docker", object{"value": v})
			return v, nil
		},
		Restart: func(context.Context) (any, error) {
			s := obj(p.input["restart"])
			p.log("SystemHelper.restart", object{"outcome": s})
			if err := injected(s["error"]); err != nil {
				return nil, err
			}
			return clone(s["return"]), nil
		},
		Notify: func(_ context.Context, n Notice) error {
			v := noticeData(n)
			p.log("Notification", object{"kwargs": v})
			p.log("post_message", object{"notice": v})
			p.notice++
			s := obj(p.input["notice"])
			if p.notice == number(s["error_on"]) {
				if err := injected(s["error"]); err != nil {
					return err
				}
			}
			p.notices = append(p.notices, clone(v))
			return nil
		},
		Log: func(_ context.Context, level, message string) error {
			p.log("logger."+level, object{"message": message})
			return nil
		},
	}
}
func (p *recording) engine(t *testing.T) *Engine {
	t.Helper()
	e, err := New(Configuration{MarkerKey: "__system_restart__", PluginsDirectory: "/owned/root/app/plugins", BackupDirectory: "/owned/config/plugins_backup", Channels: channels}, p.ports())
	if err != nil {
		t.Fatal(err)
	}
	return e
}

type recordingResponse struct {
	p    *recording
	spec object
}

func (r recordingResponse) Truth(context.Context) (bool, error) {
	v := boolean(r.spec["truthy"])
	r.p.log("Response.bool", object{"value": v})
	return v, nil
}
func (r recordingResponse) JSON(context.Context) (any, error) {
	r.p.log("Response.json", nil)
	if err := injected(r.spec["json_error"]); err != nil {
		return nil, err
	}
	return clone(r.spec["json"]), nil
}

type recordingReader struct{ p *recording }

func (r recordingReader) Read(context.Context) (string, error) {
	r.p.log("file.read", nil)
	s := obj(r.p.input["frontend"])
	if err := injected(s["read_error"]); err != nil {
		return "", err
	}
	return str(s["text"]), nil
}
func (r recordingReader) Close() error { r.p.closed++; r.p.log("file.close", nil); return nil }
func (p *recording) Exists(_ context.Context, name string) (bool, error) {
	p.log("path.exists", object{"path": name})
	switch name {
	case "/owned/frontend/version.txt":
		return boolean(obj(p.input["frontend"])["exists"]), nil
	case "/owned/root/app/plugins":
		return boolean(obj(p.input["backup"])["plugins_exist"]), nil
	}
	return p.existing[name], nil
}
func (p *recording) OpenFrontend(_ context.Context, name string) (Reader, error) {
	if name != "/owned/frontend/version.txt" {
		return nil, boundary("unowned frontend path")
	}
	p.log("open", object{"path": name, "mode": "r", "kwargs": object{"encoding": "utf-8", "errors": "replace"}})
	if err := injected(obj(p.input["frontend"])["open_error"]); err != nil {
		return nil, err
	}
	if p.onOpen != nil {
		p.onOpen()
	}
	return recordingReader{p}, nil
}
func (p *recording) Mkdir(_ context.Context, name string) error {
	p.log("path.mkdir", object{"path": name, "kwargs": object{"parents": true, "exist_ok": true}})
	p.existing[name] = true
	return nil
}
func (p *recording) Entries(_ context.Context, name string) ([]Entry, error) {
	p.log("path.iterdir", object{"path": name})
	items := []Entry{}
	for _, x := range arr(obj(p.input["backup"])["items"]) {
		items = append(items, Entry{Name: str(arr(x)[0])})
	}
	return items, nil
}
func (p *recording) kind(name string) string {
	for _, x := range arr(obj(p.input["backup"])["items"]) {
		if str(arr(x)[0]) == path.Base(name) {
			return str(arr(x)[1])
		}
	}
	return ""
}
func (p *recording) IsDir(_ context.Context, name string) (bool, error) {
	p.log("path.is_dir", object{"name": path.Base(name)})
	return p.kind(name) == "dir", nil
}
func (p *recording) IsFile(_ context.Context, name string) (bool, error) {
	p.log("path.is_file", object{"name": path.Base(name)})
	return p.kind(name) == "file", nil
}
func (p *recording) copy(event, from, to string) error {
	p.log(event, object{"source": from, "target": to})
	if err := injected(obj(p.input["backup"])["copy_error"]); err != nil {
		return err
	}
	p.existing[to] = true
	return nil
}
func (p *recording) CopyTree(_ context.Context, from, to string) error {
	return p.copy("shutil.copytree", from, to)
}
func (p *recording) CopyFile(_ context.Context, from, to string) error {
	return p.copy("shutil.copy2", from, to)
}
func (p *recording) Get(_ context.Context, key string) (any, bool, error) {
	p.log("FileCache.get", object{"key": key})
	if err := injected(obj(p.input["cache"])["get_error"]); err != nil {
		return nil, false, err
	}
	v, ok := p.cache[key]
	return v, ok, nil
}
func (p *recording) Set(_ context.Context, key string, value any) error {
	p.log("FileCache.set", object{"key": key, "value": value})
	if err := injected(obj(p.input["cache"])["set_error"]); err != nil {
		return err
	}
	p.cache[key] = clone(value)
	return nil
}
func (p *recording) Delete(_ context.Context, key string) error {
	p.log("FileCache.delete", object{"key": key})
	if err := injected(obj(p.input["cache"])["delete_error"]); err != nil {
		return err
	}
	delete(p.cache, key)
	return nil
}

type recordingCodec struct{ p *recording }

func (c recordingCodec) Encode(_ context.Context, value any) (any, error) {
	c.p.log("recording_pickle.dumps", object{"value": value})
	return object{"recording_serialized_value": clone(value), "decode_error": nil}, nil
}
func (c recordingCodec) Decode(_ context.Context, value any) (any, error) {
	c.p.log("recording_pickle.loads", object{"value": value})
	v := obj(value)
	if err := injected(v["decode_error"]); err != nil {
		return nil, err
	}
	return clone(v["recording_serialized_value"]), nil
}
func errorData(err error) any {
	s, ok := err.(*SourceError)
	if !ok {
		return object{"unexpected_platform_error": err.Error()}
	}
	return object{"class": s.Class, "module": s.Module, "message": s.Message, "args": object{"tuple": []any{s.Message}}}
}
func runCase(t *testing.T, original object) {
	t.Helper()
	input := obj(original["input"])
	p := newRecording(obj(input["ports"]))
	e := p.engine(t)
	same(t, "cache before", p.cache, original["cache_before"])
	calls := []any{}
	for _, v := range arr(input["calls"]) {
		call := obj(v)
		method := str(call["method"])
		args := []any{}
		var err error
		got := object{"method": method, "trace_start": len(p.trace)}
		if method == "restart_finish" {
			err = e.RestartFinish(context.Background())
		} else {
			channel := channelNamed(call["channel"])
			var source *string
			if call["source"] != nil {
				x := str(call["source"])
				source = &x
			}
			args = []any{channelData(channel), call["userid"], call["source"]}
			switch method {
			case "version":
				err = e.Version(context.Background(), channel, call["userid"], source)
			case "restart":
				err = e.Restart(context.Background(), channel, call["userid"], source)
			default:
				t.Fatal("unknown method")
			}
		}
		got["args"] = args
		got["trace_end"] = len(p.trace)
		got["cache_after"] = clone(p.cache)
		if err == nil {
			got["return"] = nil
		} else {
			got["exception"] = errorData(err)
		}
		calls = append(calls, got)
	}
	same(t, "all original call results/errors/type/args/trace boundaries/cache", calls, original["calls"])
	same(t, "full ordered effect/logger trace", p.trace, original["trace"])
	same(t, "notices", p.notices, original["notices"])
	same(t, "final cache", p.cache, original["cache_after"])
	same(t, "request count", p.requests, original["recording_requests"])
	targets := []string{}
	for name := range p.existing {
		targets = append(targets, name)
	}
	sort.Strings(targets)
	same(t, "backup effects", targets, original["existing_recording_targets"])
}
func TestSystemCommandsOriginalSource23(t *testing.T) {
	calls := 0
	for _, v := range sourceCases(t) {
		c := obj(v)
		calls += len(arr(c["calls"]))
		t.Run(str(c["id"]), func(t *testing.T) { runCase(t, c) })
	}
	if calls != 29 {
		t.Fatal("wrong original call count")
	}
}
func TestSystemCommandsPlatformBoundariesAndIsolation(t *testing.T) {
	base := obj(obj(sourceCases(t)[0])["input"])
	t.Run("unknown_not_source_caught", func(t *testing.T) {
		p := newRecording(obj(clone(base["ports"])))
		sentinel := errors.New("owned unknown transport error")
		p.remoteError = sentinel
		e := p.engine(t)
		if err := e.Version(context.Background(), nil, nil, nil); err != sentinel {
			t.Fatalf("identity: %v", err)
		}
		if p.requests != 1 || len(p.notices) != 0 || len(p.trace) != 2 {
			t.Fatal("unknown error swallowed/replayed")
		}
	})
	t.Run("guard_source_error_is_out_of_band", func(t *testing.T) {
		p := newRecording(obj(clone(base["ports"])))
		denied := sourceError("RuntimeError", "owned authority")
		p.guard = func(context.Context) error { return denied }
		e := p.engine(t)
		err := e.Restart(context.Background(), nil, nil, nil)
		if !errors.Is(err, denied) {
			t.Fatalf("guard lost: %v", err)
		}
		if _, ok := err.(*SourceError); ok || len(p.trace) != 0 {
			t.Fatal("guard became source-catchable or dispatched")
		}
	})
	t.Run("pre_cancel_no_effect", func(t *testing.T) {
		p := newRecording(obj(clone(base["ports"])))
		e := p.engine(t)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if err := e.RestartFinish(ctx); err != context.Canceled || len(p.trace) != 0 {
			t.Fatal("pre-cancel dispatched")
		}
	})
	t.Run("open_then_cancel_closes_without_notice", func(t *testing.T) {
		p := newRecording(obj(clone(base["ports"])))
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		p.onOpen = cancel
		e := p.engine(t)
		if err := e.Version(ctx, nil, nil, nil); err != context.Canceled {
			t.Fatalf("cancel: %v", err)
		}
		if p.closed != 1 || len(p.notices) != 0 || obj(p.trace[len(p.trace)-1])["event"] != "file.close" {
			t.Fatal("owned reader leaked or notification dispatched")
		}
	})
	t.Run("identity_injected_not_product_constant", func(t *testing.T) {
		p := newRecording(obj(clone(base["ports"])))
		ports := p.ports()
		ports.LocalBackend = func(context.Context) (string, error) { return "owned-target-identity", nil }
		e, err := New(Configuration{MarkerKey: "owned-marker", PluginsDirectory: "/owned/root/app/plugins", BackupDirectory: "/owned/config/plugins_backup", Channels: channels}, ports)
		if err != nil {
			t.Fatal(err)
		}
		if err = e.Version(context.Background(), nil, nil, nil); err != nil {
			t.Fatal(err)
		}
		want := "当前后端版本：owned-target-identity，远程版本：v2.15.7\n当前前端版本：v2.15.6，已是最新版本"
		if obj(p.notices[0])["title"] != want {
			t.Fatal("identity not injected")
		}
	})
	t.Run("concurrent_independent_engines", func(t *testing.T) {
		for i := 0; i < 4; i++ {
			t.Run(strconv.Itoa(i), func(t *testing.T) {
				t.Parallel()
				for _, v := range sourceCases(t) {
					runCase(t, obj(v))
				}
			})
		}
	})
}

// This only verifies fixture JSON structure; normal/race execution is delegated
// to the coordinator. No test launches subprocesses, sockets or native code.
