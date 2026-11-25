package torrenttransfer

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"light-media/builtinplugins/internal/sdk"
)

// Synthetic SDK resources: these exercise worker/client DTO and business
// sequence, not the production App, network protocol or daemon behavior.
type sdkFixture struct {
	mu                                                                         sync.Mutex
	configs                                                                    []plugins.BrushDownloaderReferenceV1
	inactiveIDs                                                                map[string]bool
	absentIDs                                                                  map[string]bool
	fromKind, toKind                                                           string
	backup, root                                                               string
	calls                                                                      []string
	adds                                                                       []plugins.DownloaderSourceAddRequestV1
	controls                                                                   []plugins.DownloaderControlRequestV1
	notices                                                                    []map[string]any
	history                                                                    map[string]json.RawMessage
	updates                                                                    []map[string]any
	submissions                                                                []json.RawMessage
	added, duplicate, rejectAdd, rejectControl, emptyQuery                     bool
	queryError                                                                 bool
	addUnknown, controlUnknown                                                 bool
	nativeQueryError, nativeCompletedError, nativeAddError, nativeControlError bool
	plugin                                                                     *Plugin
	dispatchSchedulers                                                         bool
	dispatchHost                                                               plugins.HostCaller
	tasks                                                                      []plugins.DownloaderTaskV1
}

func newSDKFixture(t *testing.T, from, to string) *sdkFixture {
	t.Helper()
	dir := t.TempDir()
	backup := filepath.Join(dir, "backup")
	if e := os.Mkdir(backup, 0700); e != nil {
		t.Fatal(e)
	}
	raw, _ := bencode(map[string]any{"announce": "https://synthetic.invalid/announce", "info": map[string]any{"name": "owned synthetic", "length": newByteInteger(1)}})
	if e := os.WriteFile(filepath.Join(backup, sourceHash+".torrent"), raw, 0600); e != nil {
		t.Fatal(e)
	}
	return &sdkFixture{fromKind: from, toKind: to, backup: backup, root: filepath.Join(dir, "instance"), history: map[string]json.RawMessage{}}
}
func (s *sdkFixture) config() map[string]any {
	return map[string]any{"enabled": false, "cron": "0 0 * * *", "fromdownloader": "Source", "todownloader": "Target", "fromtorrentpath": s.backup, "frompath": "/source", "topath": "/dest", "add_torrent_tags": "a,a, b", "remainoldtag": true, "remainoldcat": true, "transferemptylabel": true, "autostart": false, "skipverify": false, "deletesource": true, "deleteduplicate": false, "notify": true}
}
func ptr[T any](v T) *T { return &v }
func (s *sdkFixture) task(kind, hash string) plugins.DownloaderTaskV1 {
	task := plugins.DownloaderTaskV1{Hash: hash}
	if kind == "qbittorrent" {
		task.QBittorrent = &plugins.QBittorrentTaskInfoV1{State: ptr("pausedUP"), Tags: ptr("a, raw "), Category: ptr(" Music "), SavePath: ptr("/source/music")}
	}
	if kind == "transmission" {
		task.Transmission = &plugins.TransmissionTaskInfoV1{Status: ptr(json.Number("0")), PercentDone: ptr(json.Number("1")), Labels: ptr([]string{"a", " raw "}), DownloadDir: ptr("/source/music")}
	}
	return task
}
func (s *sdkFixture) host(ctx context.Context, method string, raw json.RawMessage) (json.RawMessage, error) {
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, method)
	var id struct {
		DownloaderID string `json:"downloader_id"`
	}
	_ = json.Unmarshal(raw, &id)
	kind := s.fromKind
	if id.DownloaderID == "target" {
		kind = s.toKind
	}
	for _, ref := range s.configs {
		if ref.ID == id.DownloaderID {
			kind = canonicalKind(ref.Kind)
		}
	}
	switch method {
	case plugins.DownloaderConfigurationsReadCapability:
		if s.configs != nil {
			return json.Marshal(plugins.DownloaderConfigurationsV1{SchemaVersion: 1, Downloaders: s.configs})
		}
		return json.Marshal(plugins.DownloaderConfigurationsV1{SchemaVersion: 1, Downloaders: []plugins.BrushDownloaderReferenceV1{{ID: "source", Name: "Source", Kind: s.fromKind, Enabled: true}, {ID: "disabled", Name: "Off", Kind: "qbittorrent", Enabled: false}, {ID: "target", Name: "Target", Kind: s.toKind, Enabled: true}}})
	case plugins.DownloaderSourceConnectionCapability:
		return json.Marshal(plugins.DownloaderSourceConnectionV1{DownloaderConnectionV1: plugins.DownloaderConnectionV1{SchemaVersion: 1, DownloaderID: id.DownloaderID, Kind: kind, Inactive: s.inactiveIDs[id.DownloaderID]}, Exists: !s.absentIDs[id.DownloaderID]})
	case plugins.DownloaderSourceCompletedCapability:
		if s.nativeCompletedError {
			return json.Marshal(plugins.DownloaderSourceTasksResultV1{DownloaderTasksV1: plugins.DownloaderTasksV1{SchemaVersion: 1, DownloaderID: id.DownloaderID, Kind: kind, Tasks: []plugins.DownloaderTaskV1{}}, NativeError: true})
		}
		return json.Marshal(plugins.DownloaderSourceTasksResultV1{DownloaderTasksV1: plugins.DownloaderTasksV1{SchemaVersion: 1, DownloaderID: id.DownloaderID, Kind: kind, Tasks: []plugins.DownloaderTaskV1{s.task(kind, sourceHash)}}})
	case plugins.DownloaderSourceTasksCapability:
		if s.queryError {
			return nil, errors.New("synthetic scope or transport failure")
		}
		tasks := []plugins.DownloaderTaskV1{}
		if s.duplicate {
			tasks = append(tasks, s.task(kind, sourceHash))
		} else if s.added && !s.emptyQuery {
			tasks = append(tasks, s.task(kind, addedHash))
		}
		if s.tasks != nil {
			tasks = s.tasks
		}
		if s.nativeQueryError {
			tasks = []plugins.DownloaderTaskV1{}
		}
		return json.Marshal(plugins.DownloaderSourceTasksResultV1{DownloaderTasksV1: plugins.DownloaderTasksV1{SchemaVersion: 1, DownloaderID: id.DownloaderID, Kind: kind, Tasks: tasks}, NativeError: s.nativeQueryError})
	case plugins.DownloaderSourceAddCapability:
		var req plugins.DownloaderSourceAddRequestV1
		if e := json.Unmarshal(raw, &req); e != nil {
			return nil, e
		}
		s.adds = append(s.adds, req)
		s.added = true
		accepted := !s.rejectAdd && !s.nativeAddError
		outcome := "confirmed"
		if !accepted {
			outcome = "rejected"
		}
		if s.addUnknown {
			accepted = false
			outcome = "unknown"
		}
		return json.Marshal(plugins.DownloaderSourceMutationResultV1{DownloaderMutationV1: plugins.DownloaderMutationV1{SchemaVersion: 1, DownloaderID: id.DownloaderID, Accepted: accepted, Outcome: outcome, Hash: addedHash}, NativeError: s.nativeAddError || s.addUnknown})
	case plugins.DownloaderSourceFindByTagCapability:
		return json.Marshal(plugins.DownloaderFindByTagV1{SchemaVersion: 1, DownloaderID: id.DownloaderID, Hash: ptr(addedHash)})
	case plugins.DownloaderSourceControlCapability:
		var req plugins.DownloaderControlRequestV1
		if e := json.Unmarshal(raw, &req); e != nil {
			return nil, e
		}
		s.controls = append(s.controls, req)
		accepted := !s.rejectControl && !s.nativeControlError
		outcome := "confirmed"
		if !accepted {
			outcome = "rejected"
		}
		if s.controlUnknown {
			accepted = false
			outcome = "unknown"
		}
		return json.Marshal(plugins.DownloaderSourceMutationResultV1{DownloaderMutationV1: plugins.DownloaderMutationV1{SchemaVersion: 1, DownloaderID: id.DownloaderID, Accepted: accepted, Outcome: outcome}, NativeError: s.nativeControlError || s.controlUnknown})
	case plugins.BaseSaveData:
		var in struct {
			Key      string          `json:"key"`
			Value    json.RawMessage `json:"value"`
			PluginID string          `json:"plugin_id"`
		}
		if e := json.Unmarshal(raw, &in); e != nil {
			return nil, e
		}
		if in.PluginID != "" {
			return nil, errors.New("unexpected explicit data namespace")
		}
		s.history[in.Key] = in.Value
		return json.RawMessage(`null`), nil
	case plugins.BaseGetDataPath:
		return json.Marshal(s.root)
	case plugins.BaseUpdateConfig:
		var in struct {
			Config map[string]any `json:"config"`
		}
		_ = json.Unmarshal(raw, &in)
		s.updates = append(s.updates, in.Config)
		return json.RawMessage(`false`), nil
	case plugins.BackgroundSubmitCapability:
		var in struct {
			Method  string          `json:"method"`
			Payload json.RawMessage `json:"payload"`
		}
		_ = json.Unmarshal(raw, &in)
		if (in.Method == "transfer_continue" || in.Method == "private_scheduler" && s.dispatchSchedulers) && s.plugin != nil {
			p := s.plugin
			next := s.dispatchHost
			if next == nil {
				next = s.host
			}
			go func() { _, _ = p.Handle(context.Background(), in.Method, in.Payload, next) }()
		}
		s.submissions = append(s.submissions, append(json.RawMessage{}, raw...))
		return json.RawMessage(`{"accepted":true}`), nil
	case plugins.PostMessageCapability:
		var in map[string]any
		_ = json.Unmarshal(raw, &in)
		s.notices = append(s.notices, in)
		return json.RawMessage(`{"queued":0,"attempted":0,"failed":0,"errors":[]}`), nil
	case plugins.LogWriteCapability, plugins.SystemMessagePutCapability:
		return json.RawMessage(`null`), nil
	default:
		return nil, errors.New("unexpected synthetic capability: " + method)
	}
}
func invoke(t *testing.T, p *Plugin, s *sdkFixture, method string, in any) json.RawMessage {
	t.Helper()
	s.mu.Lock()
	s.plugin = p
	s.mu.Unlock()
	raw, e := json.Marshal(in)
	if e != nil {
		t.Fatal(e)
	}
	out, e := p.Handle(context.Background(), method, raw, s.host)
	if e != nil {
		t.Fatalf("%s: %v", method, e)
	}
	return out
}
func TestSDKFourClientPairsAndHistory(t *testing.T) {
	for _, from := range []string{"qbittorrent", "transmission"} {
		for _, to := range []string{"qbittorrent", "transmission"} {
			t.Run(from+"-"+to, func(t *testing.T) {
				s := newSDKFixture(t, from, to)
				p := New()
				invoke(t, p, s, "init", s.config())
				invoke(t, p, s, "transfer", nil)
				if len(s.adds) != 1 || !s.adds[0].Paused || *s.adds[0].SavePath != "/dest/music" {
					t.Fatalf("paused add/mapping: %+v", s.adds)
				}
				add := s.adds[0]
				if !contains(add.Tags, "a") || !contains(add.Tags, " b") {
					t.Fatalf("raw configured tags: %v", add.Tags)
				}
				wantSourceTag := "raw"
				if from == "transmission" {
					wantSourceTag = " raw "
				}
				if !contains(add.Tags, wantSourceTag) {
					t.Fatalf("source labels: %v", add.Tags)
				}
				if to == "qbittorrent" {
					wantCat := "Music"
					if from == "transmission" {
						wantCat = ""
					}
					if add.Category == nil || *add.Category != wantCat || add.SkipCheckingWire == nil || *add.SkipCheckingWire != "False" || add.SkipChecking != nil {
						t.Fatalf("qB flags: %+v", add)
					}
					marker := add.Tags[len(add.Tags)-1]
					if len(marker) != 10 {
						t.Fatalf("marker length: %q", marker)
					}
				} else if add.Category != nil || add.SkipChecking != nil || add.SkipCheckingWire != nil {
					t.Fatal("TR received qB fields")
				}
				history := s.history["Source-"+sourceHash]
				var actual map[string]any
				_ = json.Unmarshal(history, &actual)
				want := map[string]any{"to_download": "Target", "to_download_id": addedHash, "delete_source": true, "delete_duplicate": false}
				if !reflect.DeepEqual(actual, want) {
					t.Fatalf("history: %s", history)
				}
				if len(s.notices) != 1 || s.notices[0]["text"] != "总数：1，成功：1，失败：0，跳过：0，删除重复：0" {
					t.Fatalf("summary: %v", s.notices)
				}
				if len(s.controls) < 1 || s.controls[len(s.controls)-1].Action != "remove_task" || s.controls[len(s.controls)-1].DownloaderID != "source" {
					t.Fatalf("source task only deletion: %v", s.controls)
				}
				p.state.mu.Lock()
				ids := p.state.Recheck["Target"]
				p.state.mu.Unlock()
				if !reflect.DeepEqual(ids, []string{addedHash}) {
					t.Fatalf("pending: %v", ids)
				}
				s.rejectControl = true
				invoke(t, p, s, "check_recheck", nil)
				if s.controls[len(s.controls)-1].Action != "start" {
					t.Fatal("eligible stopped task not started")
				}
				if len(p.state.Recheck["Target"]) != 0 {
					t.Fatal("false start return must still clear eligible ID")
				}
			})
		}
	}
}
func TestSDKFalseQBTupleAndDuplicateIndependence(t *testing.T) {
	s := newSDKFixture(t, "qbittorrent", "qbittorrent")
	s.rejectAdd = true
	p := New()
	invoke(t, p, s, "init", s.config())
	invoke(t, p, s, "transfer", nil)
	if len(s.history) != 1 || !contains(s.calls, plugins.DownloaderSourceFindByTagCapability) {
		t.Fatal("qB rejected tuple stopped marker lookup")
	}
	s = newSDKFixture(t, "qbittorrent", "transmission")
	s.duplicate = true
	s.rejectControl = true
	p = New()
	cfg := s.config()
	cfg["deletesource"] = false
	cfg["deleteduplicate"] = true
	invoke(t, p, s, "init", cfg)
	invoke(t, p, s, "transfer", nil)
	if len(s.adds) != 0 || len(s.history) != 0 || len(s.controls) != 1 || s.controls[0].Action != "remove_task" {
		t.Fatalf("duplicate source-only removal: %v", s.controls)
	}
	if s.notices[0]["text"] != "总数：1，成功：0，失败：0，跳过：0，删除重复：1" {
		t.Fatal(s.notices)
	}
}
func TestSDKBackupExistenceBeforeDuplicate(t *testing.T) {
	s := newSDKFixture(t, "qbittorrent", "qbittorrent")
	s.duplicate = true
	if e := os.Remove(filepath.Join(s.backup, sourceHash+".torrent")); e != nil {
		t.Fatal(e)
	}
	cfg := s.config()
	cfg["deleteduplicate"] = true
	p := New()
	invoke(t, p, s, "init", cfg)
	invoke(t, p, s, "transfer", nil)
	if contains(s.calls, plugins.DownloaderSourceTasksCapability) || len(s.controls) != 0 {
		t.Fatal("duplicate queried/removed without source backup")
	}
	if s.notices[0]["text"] != "总数：1，成功：0，失败：1，跳过：0，删除重复：0" {
		t.Fatal(s.notices)
	}
}
func TestSDKTrackerRepairInstanceNamespace(t *testing.T) {
	a := newSDKFixture(t, "qbittorrent", "transmission")
	b := newSDKFixture(t, "qbittorrent", "transmission")
	for _, s := range []*sdkFixture{a, b} {
		raw, _ := bencode(map[string]any{"info": map[string]any{"name": "owned", "length": newByteInteger(1)}})
		resume, _ := bencode(map[string]any{"trackers": []any{[]any{"https://synthetic.invalid/a", "https://synthetic.invalid/b"}, []any{"https://synthetic.invalid/c"}}})
		_ = os.WriteFile(filepath.Join(s.backup, sourceHash+".torrent"), raw, 0600)
		_ = os.WriteFile(filepath.Join(s.backup, sourceHash+".fastresume"), resume, 0600)
		p := New()
		invoke(t, p, s, "init", s.config())
		invoke(t, p, s, "transfer", nil)
		after, _ := os.ReadFile(filepath.Join(s.backup, sourceHash+".torrent"))
		if !reflect.DeepEqual(after, raw) {
			t.Fatal("source backup modified")
		}
		repaired, e := os.ReadFile(filepath.Join(s.root, "tmp", sourceHash+".torrent"))
		if e != nil {
			t.Fatal(e)
		}
		if !reflect.DeepEqual(repaired, s.adds[0].Torrent) {
			t.Fatal("repaired bytes not submitted")
		}
		data, e := bdecode(repaired)
		if e != nil {
			t.Fatal(e)
		}
		main := data.(map[string]any)
		if main["announce"] != "https://synthetic.invalid/a" || len(main["announce-list"].([]any)) != 2 {
			t.Fatal(main)
		}
	}
	if a.root == b.root {
		t.Fatal("instance file namespaces collide")
	}
}
func TestPersistentConfigDefaultsTypesStateAndForm(t *testing.T) {
	s := newSDKFixture(t, "qbittorrent", "transmission")
	p := New()
	var form []json.RawMessage
	_ = json.Unmarshal(invoke(t, p, s, "get_form", nil), &form)
	var defaults map[string]any
	_ = json.Unmarshal(form[1], &defaults)
	if defaults["autostart"] != true || defaults["add_torrent_tags"] != "已整理,转移做种" {
		t.Fatal(defaults)
	}
	if strings.Contains(string(form[0]), `"value":"Off"`) || !strings.Contains(string(form[0]), `"value":"Source"`) {
		t.Fatal("form downloader eligibility")
	}
	invoke(t, p, s, "init", map[string]any{"enabled": false})
	cfg := p.currentConfig()
	if cfg.values["autostart"] != nil || len(cfg.tags) != 0 {
		t.Fatal("UI defaults incorrectly applied to partial init")
	}
	for _, falsey := range []any{nil, map[string]any{}, []any{}, false, json.Number("0e10"), ""} {
		invoke(t, p, s, "init", falsey)
		if p.currentConfig().values["enabled"] != false {
			t.Fatal("falsey init mutated config")
		}
	}
	cfgMap := s.config()
	cfgMap["enabled"] = true
	cfgMap["autostart"] = false
	invoke(t, p, s, "init", cfgMap)
	if string(invoke(t, p, s, "get_state", nil)) != "true" {
		t.Fatal("enabled state")
	}
	if string(invoke(t, p, s, "get_service", nil)) == "[]" {
		t.Fatal("public cron omitted")
	}
	cfgMap["cron"] = "bad cron"
	invoke(t, p, s, "init", cfgMap)
	if _, e := p.Handle(context.Background(), "get_service", nil, s.host); e == nil {
		t.Fatal("invalid cron accepted")
	}
	for _, method := range []string{"get_api", "get_page", "get_command", "close"} {
		if string(invoke(t, p, s, method, nil)) != "null" {
			t.Fatal(method)
		}
	}
}
func TestSourcePythonWhitespaceCrontabAndPublicDeclaration(t *testing.T) {
	t.Setenv("TZ", "UTC")
	raw, e := os.ReadFile("reference/cron-whitespace-source-oracle.json")
	if e != nil {
		t.Fatal(e)
	}
	var source struct {
		Cases []struct {
			Name       string   `json:"separator_codepoint"`
			Expression string   `json:"expression"`
			Fields     []string `json:"split_fields"`
			Accepted   bool     `json:"accepted"`
		}
	}
	if e = json.Unmarshal(raw, &source); e != nil || len(source.Cases) != 6 {
		t.Fatal("invalid source whitespace oracle", e)
	}
	for _, c := range source.Cases {
		t.Run(c.Name, func(t *testing.T) {
			if got := strings.FieldsFunc(c.Expression, sourcePythonSpace); !reflect.DeepEqual(got, c.Fields) {
				t.Fatal("source str.split fields changed", got, c.Fields)
			}
			if _, e := ParseCrontab(c.Expression, "UTC"); (e == nil) != c.Accepted {
				t.Fatal("source APScheduler compilation changed", e)
			}
			s := newSDKFixture(t, "qbittorrent", "transmission")
			p := New()
			cfg := s.config()
			cfg["enabled"], cfg["cron"] = true, c.Expression
			invoke(t, p, s, "init", cfg)
			var jobs []struct {
				Trigger struct {
					Cron string `json:"cron"`
				} `json:"trigger"`
			}
			if e := json.Unmarshal(invoke(t, p, s, "get_service", nil), &jobs); e != nil || len(jobs) != 1 || jobs[0].Trigger.Cron != c.Expression {
				t.Fatal("public source cron declaration changed", jobs, e)
			}
		})
	}
}
func TestOnlyOnceInvalidValidationAndPrivateStop(t *testing.T) {
	s := newSDKFixture(t, "qbittorrent", "transmission")
	p := New()
	cfg := s.config()
	cfg["onlyonce"] = true
	cfg["autostart"] = true
	invoke(t, p, s, "init", cfg)
	if len(s.updates) != 1 || s.updates[0]["onlyonce"] != false || len(s.submissions) != 1 || p.currentConfig().flag("onlyonce") {
		t.Fatal("one-shot not cleared before admission")
	}
	p.state.Recheck["Target"] = []string{addedHash}
	p.state.recheckRunning = true
	p.mu.Lock()
	job := p.scheduler
	job.start = time.Now().Add(time.Hour)
	p.mu.Unlock()
	s.dispatchSchedulers = true
	done := make(chan error, 1)
	go func() { done <- p.runScheduler(context.Background(), job.id, s.host) }()
	until := time.Now().Add(time.Second)
	for {
		p.mu.Lock()
		started := job.started
		p.mu.Unlock()
		if started {
			break
		}
		if time.Now().After(until) {
			t.Fatal("scheduler not started")
		}
		time.Sleep(time.Millisecond)
	}
	invoke(t, p, s, "stop_service", nil)
	select {
	case e := <-done:
		if e != nil {
			t.Fatal(e)
		}
	case <-time.After(time.Second):
		t.Fatal("stop did not join private scheduler")
	}
	if !p.state.recheckRunning || len(p.state.Recheck["Target"]) != 1 || p.state.stopped.Load() {
		t.Fatal("stop reset pending state or leaked event")
	}
	cfg["fromtorrentpath"] = filepath.Join(s.backup, "missing")
	cfg["enabled"] = true
	invoke(t, p, s, "init", cfg)
	if p.currentConfig().flag("enabled") || p.currentConfig().flag("onlyonce") || len(s.updates) != 2 || len(s.submissions) != 1 {
		t.Fatal("validation did not disable flags")
	}
}
func TestSDKQueryFailurePreservesGuardBoundary(t *testing.T) {
	s := newSDKFixture(t, "qbittorrent", "qbittorrent")
	p := New()
	invoke(t, p, s, "init", s.config())
	p.state.Recheck["Target"] = []string{addedHash}
	s.queryError = true
	if _, e := p.Handle(context.Background(), "check_recheck", nil, s.host); e == nil {
		t.Fatal("opaque host failure swallowed")
	}
	if !p.state.recheckRunning || len(p.state.Recheck["Target"]) != 1 || len(s.controls) != 0 {
		t.Fatal("revoked/failed callback continued effects")
	}
}
func TestSourceFalseyPathAndTags(t *testing.T) {
	for _, c := range []struct {
		in   any
		want bool
	}{{json.Number("0e10"), false}, {json.Number("0.000"), false}, {json.Number("1e-10000"), false}, {"false", true}, {newByteInteger(0), false}} {
		if truth(c.in) != c.want {
			t.Fatalf("truth(%v)", c.in)
		}
	}
	for _, x := range []struct {
		in   string
		want []string
	}{{"a, b", []string{"a", " b"}}, {"  a,b  ", []string{"a", "b"}}, {"   ", []string{""}}, {"\x1ca\x1f", []string{"a"}}} {
		if !reflect.DeepEqual(splitTags(x.in), x.want) {
			t.Fatal(x)
		}
	}
	if got := convertSavePathValue("/source/music", json.Number("0"), "/dest"); got != "/source/music" {
		t.Fatal(got)
	}
	if got := convertSavePathValue("/source/music", json.Number("1"), "/dest"); got != nil {
		t.Fatal(got)
	}
}

func TestSourceNumericEquality(t *testing.T) {
	for _, x := range []struct {
		a, b any
		want bool
	}{
		{true, json.Number("1"), true}, {false, json.Number("0.0"), true}, {json.Number("1"), json.Number("1.0"), true},
		{json.Number("9007199254740993"), json.Number("9007199254740992.0"), false}, {json.Number("1e-10000"), json.Number("0"), true}, {"1", json.Number("1"), false},
	} {
		if pyEqual(x.a, x.b) != x.want {
			t.Fatalf("Python equality %v %v", x.a, x.b)
		}
	}
}

func TestPrivateIntervalAnchorAndStaleOneShot(t *testing.T) {
	s := newSDKFixture(t, "qbittorrent", "transmission")
	p := New()
	cfg := s.config()
	cfg["enabled"] = true
	cfg["autostart"] = true
	invoke(t, p, s, "init", cfg)
	p.state.Recheck["Target"] = []string{addedHash}
	p.mu.Lock()
	job := p.scheduler
	job.start = time.Now().Add(-30*time.Second + 40*time.Millisecond)
	p.mu.Unlock()
	s.dispatchSchedulers = true
	done := make(chan error, 1)
	go func() { done <- p.runScheduler(context.Background(), job.id, s.host) }()
	until := time.Now().Add(time.Second)
	for {
		p.state.mu.Lock()
		empty := len(p.state.Recheck["Target"]) == 0
		p.state.mu.Unlock()
		if empty {
			break
		}
		if time.Now().After(until) {
			t.Fatal("anchored 30-second interval did not execute its due check")
		}
		time.Sleep(time.Millisecond)
	}
	invoke(t, p, s, "stop_service", nil)
	if e := <-done; e != nil {
		t.Fatal(e)
	}
	s = newSDKFixture(t, "qbittorrent", "transmission")
	p = New()
	cfg = s.config()
	cfg["onlyonce"] = true
	invoke(t, p, s, "init", cfg)
	p.mu.Lock()
	job = p.scheduler
	job.start = time.Now().Add(-10 * time.Second)
	p.mu.Unlock()
	go func() { done <- p.runScheduler(context.Background(), job.id, s.host) }()
	time.Sleep(40 * time.Millisecond)
	invoke(t, p, s, "stop_service", nil)
	if e := <-done; e != nil {
		t.Fatal(e)
	}
	if contains(s.calls, plugins.DownloaderSourceCompletedCapability) {
		t.Fatal("missed one-shot was replayed beyond default grace")
	}
}

func TestRepairCannotWriteAcrossInstanceSymlink(t *testing.T) {
	s := newSDKFixture(t, "qbittorrent", "transmission")
	outside := t.TempDir()
	if e := os.MkdirAll(s.root, 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.Symlink(outside, filepath.Join(s.root, "tmp")); e != nil {
		t.Fatal(e)
	}
	if _, e := (sdkHost{caller: s.host}).WriteTemp(context.Background(), sourceHash+".torrent", []byte("owned")); e == nil {
		t.Fatal("repair escaped current instance")
	}
	if _, e := os.Stat(filepath.Join(outside, sourceHash+".torrent")); !os.IsNotExist(e) {
		t.Fatal("outside file created", e)
	}
}

func TestSDKTypedNativeErrorsPreserveSourceBranches(t *testing.T) {
	t.Run("completed-error-returns-without-summary", func(t *testing.T) {
		s := newSDKFixture(t, "qbittorrent", "transmission")
		s.nativeCompletedError = true
		p := New()
		invoke(t, p, s, "init", s.config())
		invoke(t, p, s, "transfer", nil)
		if len(s.adds) != 0 || len(s.notices) != 0 {
			t.Fatal("ordinary completed query failure continued")
		}
	})
	t.Run("duplicate-query-error-proceeds-and-recheck-error-clears", func(t *testing.T) {
		s := newSDKFixture(t, "qbittorrent", "qbittorrent")
		s.nativeQueryError = true
		p := New()
		invoke(t, p, s, "init", s.config())
		invoke(t, p, s, "transfer", nil)
		if len(s.history) != 1 {
			t.Fatal("source discarded []true duplicate query")
		}
		invoke(t, p, s, "check_recheck", nil)
		if len(p.state.Recheck["Target"]) != 0 || p.state.recheckRunning {
			t.Fatal("[]true did not clear queue normally")
		}
	})
	for _, kind := range []string{"qbittorrent", "transmission"} {
		t.Run("native-add-error-"+kind, func(t *testing.T) {
			s := newSDKFixture(t, "qbittorrent", kind)
			s.nativeAddError = true
			p := New()
			invoke(t, p, s, "init", s.config())
			invoke(t, p, s, "transfer", nil)
			if kind == "qbittorrent" {
				if !contains(s.calls, plugins.DownloaderSourceFindByTagCapability) || len(s.history) != 1 {
					t.Fatal("false qB tuple must still lookup and accept observed marker")
				}
			} else if contains(s.calls, plugins.DownloaderSourceFindByTagCapability) || len(s.history) != 0 || s.notices[0]["text"] != "总数：1，成功：0，失败：1，跳过：0，删除重复：0" {
				t.Fatal("TR add exception should be false")
			}
		})
	}
	t.Run("native-control-errors-ignored", func(t *testing.T) {
		s := newSDKFixture(t, "qbittorrent", "qbittorrent")
		s.nativeControlError = true
		p := New()
		invoke(t, p, s, "init", s.config())
		invoke(t, p, s, "transfer", nil)
		if len(s.history) != 1 || len(p.state.Recheck["Target"]) != 1 {
			t.Fatal("source false recheck/delete return changed history")
		}
		invoke(t, p, s, "check_recheck", nil)
		if len(p.state.Recheck["Target"]) != 0 {
			t.Fatal("source false start return retained ready hash")
		}
	})
	t.Run("native-duplicate-delete-error-counts", func(t *testing.T) {
		s := newSDKFixture(t, "qbittorrent", "transmission")
		s.nativeControlError = true
		s.duplicate = true
		p := New()
		cfg := s.config()
		cfg["deleteduplicate"] = true
		cfg["deletesource"] = false
		invoke(t, p, s, "init", cfg)
		invoke(t, p, s, "transfer", nil)
		if s.notices[0]["text"] != "总数：1，成功：0，失败：0，跳过：0，删除重复：1" {
			t.Fatal("source false duplicate delete return changed counts")
		}
	})
}
func TestSDKPostMutationFencesNeverContinue(t *testing.T) {
	for _, tc := range []struct {
		name, outcome, code string
		accepted            bool
	}{{"changed-after-confirmed", "confirmed", "CALL_CHANGED_AFTER_MUTATION", true}, {"partial", "unknown", "PARTIAL_MUTATION", false}} {
		t.Run(tc.name, func(t *testing.T) {
			s := newSDKFixture(t, "qbittorrent", "qbittorrent")
			p := New()
			invoke(t, p, s, "init", s.config())
			host := func(ctx context.Context, m string, r json.RawMessage) (json.RawMessage, error) {
				if m == plugins.DownloaderSourceAddCapability {
					return json.Marshal(plugins.DownloaderSourceMutationResultV1{DownloaderMutationV1: plugins.DownloaderMutationV1{SchemaVersion: 1, DownloaderID: "target", Accepted: tc.accepted, Outcome: tc.outcome, FailureCode: tc.code}, NativeError: true})
				}
				return s.host(ctx, m, r)
			}
			s.dispatchHost = host
			if _, e := p.Handle(context.Background(), "transfer", nil, host); e == nil {
				t.Fatal("unsafe mutation receipt swallowed")
			}
			if len(s.controls) != 0 || len(s.history) != 0 || contains(s.calls, plugins.DownloaderSourceFindByTagCapability) {
				t.Fatal("post-mutation chain continued")
			}
		})
	}
}
func TestSDKNullSkipCheckingAndEmptyTRMaterial(t *testing.T) {
	s := newSDKFixture(t, "transmission", "qbittorrent")
	p := New()
	cfg := s.config()
	cfg["skipverify"] = nil
	cfg["remainoldtag"] = false
	invoke(t, p, s, "init", cfg)
	invoke(t, p, s, "transfer", nil)
	if s.adds[0].SkipChecking != nil {
		t.Fatal("null skip-check flag coerced into false")
	}
	if e := os.WriteFile(filepath.Join(s.backup, sourceHash+".torrent"), []byte{}, 0600); e != nil {
		t.Fatal(e)
	}
	s = newSDKFixture(t, "transmission", "qbittorrent")
	if e := os.WriteFile(filepath.Join(s.backup, sourceHash+".torrent"), []byte{}, 0600); e != nil {
		t.Fatal(e)
	}
	s.rejectAdd = true
	p = New()
	invoke(t, p, s, "init", s.config())
	invoke(t, p, s, "transfer", nil)
	if len(s.adds) != 1 || len(s.adds[0].Torrent) != 0 || !contains(s.calls, plugins.DownloaderSourceFindByTagCapability) {
		t.Fatal("empty TR source was parsed/rejected before source false qB tuple lookup")
	}
}

func TestSDKNativeErrorDoesNotTurnTRReceiptIntoSourceSuccess(t *testing.T) {
	for _, kind := range []string{"qbittorrent", "transmission"} {
		host := func(context.Context, string, json.RawMessage) (json.RawMessage, error) {
			return json.Marshal(plugins.DownloaderSourceMutationResultV1{DownloaderMutationV1: plugins.DownloaderMutationV1{SchemaVersion: 1, DownloaderID: "target", Accepted: true, Outcome: "confirmed", Hash: addedHash}, NativeError: true})
		}
		out, e := (sdkHost{caller: host}).AddPaused(context.Background(), Service{Name: "Target", Ref: "target", Type: kind}, AddSpec{Content: []byte("owned"), SavePath: "/dest", SkipChecking: false})
		if e != nil || !out.Accepted || out.SourceTruthy != (kind == "qbittorrent") {
			t.Fatal("actual receipt and source truthiness conflated", kind, out, e)
		}
	}
}

func TestSDKPlainUnknownKeepsSourceFalseOutcomesWithoutWriteRetry(t *testing.T) {
	for _, kind := range []string{"qbittorrent", "transmission"} {
		t.Run(kind, func(t *testing.T) {
			s := newSDKFixture(t, "qbittorrent", kind)
			s.addUnknown = true
			p := New()
			invoke(t, p, s, "init", s.config())
			invoke(t, p, s, "transfer", nil)
			if len(s.adds) != 1 {
				t.Fatal("selected add was retried", len(s.adds))
			}
			if kind == "qbittorrent" {
				if !contains(s.calls, plugins.DownloaderSourceFindByTagCapability) || len(s.history) != 1 {
					t.Fatal("source QB tuple lookup/success path removed")
				}
			} else {
				if contains(s.calls, plugins.DownloaderSourceFindByTagCapability) || len(s.history) != 0 || len(s.controls) != 0 || s.notices[0]["text"] != "总数：1，成功：0，失败：1，跳过：0，删除重复：0" {
					t.Fatal("source TR false path changed")
				}
			}
		})
	}
	s := newSDKFixture(t, "qbittorrent", "qbittorrent")
	s.controlUnknown = true
	p := New()
	invoke(t, p, s, "init", s.config())
	invoke(t, p, s, "transfer", nil)
	invoke(t, p, s, "check_recheck", nil)
	if len(s.history) != 1 || len(p.state.Recheck["Target"]) != 0 {
		t.Fatal("source false control path changed")
	}
	counts := map[string]int{}
	for _, r := range s.controls {
		counts[r.Action]++
	}
	if counts["recheck"] != 1 || counts["remove_task"] != 1 || counts["start"] != 1 {
		t.Fatal("selected control write retried", counts)
	}
	result, e := (sdkHost{caller: s.host}).effect(context.Background(), Service{Type: "qbittorrent", Ref: "target"}, []string{addedHash}, "start")
	if e != nil || result.Accepted || result.Outcome != "unknown" {
		t.Fatal("unknown native fact rewritten", result, e)
	}
}
