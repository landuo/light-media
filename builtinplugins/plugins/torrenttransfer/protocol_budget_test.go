package torrenttransfer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"light-media/builtinplugins/internal/sdk"
)

// This test-only clock controls the real independently running SDK worker. No
// test clock, snapshot method or state mutation is shipped by cmd/torrent-transfer.
type fixtureClock struct {
	mu      sync.Mutex
	now     time.Time
	waiters map[chan struct{}]time.Time
}

func (c *fixtureClock) wait(ctx context.Context, stop <-chan struct{}, due time.Time) error {
	ch := make(chan struct{})
	c.mu.Lock()
	c.waiters[ch] = due
	c.mu.Unlock()
	defer func() { c.mu.Lock(); delete(c.waiters, ch); c.mu.Unlock() }()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-stop:
		return errSchedulerStopped
	case <-ch:
		return nil
	}
}
func (c *fixtureClock) tick() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	var first chan struct{}
	var due time.Time
	for ch, t := range c.waiters {
		if first == nil || t.Before(due) {
			first, due = ch, t
		}
	}
	if first == nil {
		return false
	}
	c.now = due
	delete(c.waiters, first)
	close(first)
	return true
}
func budgetFixtureManifest() plugins.Manifest {
	m := Manifest()
	m.PluginID = "TorrentTransferBudgetFixture"
	m.Entry = "torrent-transfer-budget-fixture"
	m.Methods = append(m.Methods, "test_tick", "test_snapshot", "test_pending")
	return m
}
func TestBudgetWorkerFixture(t *testing.T) {
	enabled := false
	for _, arg := range os.Args {
		if arg == "tt-sdk-budget-worker" {
			enabled = true
		}
	}
	if !enabled {
		return
	}
	p := New()
	clock := &fixtureClock{now: time.Now(), waiters: map[chan struct{}]time.Time{}}
	p.now = func() time.Time { clock.mu.Lock(); defer clock.mu.Unlock(); return clock.now }
	p.waitUntil = clock.wait
	handle := func(ctx context.Context, m string, r json.RawMessage, h plugins.HostCaller) (json.RawMessage, error) {
		switch m {
		case "test_tick":
			return json.Marshal(clock.tick())
		case "test_pending":
			var ids []string
			if e := json.Unmarshal(r, &ids); e != nil {
				return nil, e
			}
			p.state.mu.Lock()
			p.state.Recheck["Target"] = ids
			p.state.mu.Unlock()
			return json.RawMessage(`null`), nil
		case "test_snapshot":
			p.mu.Lock()
			passes := len(p.passes)
			active := 0
			if p.scheduler != nil {
				active = p.scheduler.active
			}
			p.mu.Unlock()
			p.state.mu.Lock()
			pending := len(p.state.Recheck["Target"])
			running := p.state.recheckRunning
			p.state.mu.Unlock()
			clock.mu.Lock()
			waiting := len(clock.waiters)
			clock.mu.Unlock()
			return json.Marshal(map[string]any{"passes": passes, "active_scheduler": active, "pending": pending, "recheck_running": running, "waiting_timers": waiting})
		}
		return p.Handle(ctx, m, r, h)
	}
	if e := plugins.ServeConcurrent(context.Background(), os.Stdin, os.Stdout, budgetFixtureManifest(), handle); e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(2)
	}
	os.Exit(0)
}

type phaseObservation struct {
	start, end time.Time
	method     string
	result     plugins.RunResult
	err        error
}
type budgetFixture struct {
	t                *testing.T
	ctx              context.Context
	cancel           context.CancelFunc
	client           *plugins.Client
	identity         plugins.Identity
	mu               sync.Mutex
	tasks            []plugins.DownloaderTaskV1
	added            map[string]bool
	markers          map[string]string
	backup           string
	ready            bool
	history          map[string]json.RawMessage
	logs             []plugins.LogEntryV1
	notices          []map[string]any
	phases           []phaseObservation
	totalRPC         int
	starts           [][]string
	events           []map[string]any
	blockMethod      string
	blockOnce        sync.Once
	entered, release chan struct{}
}

func startBudgetFixture(t *testing.T, n int) *budgetFixture {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	exe, e := os.Executable()
	if e != nil {
		t.Fatal(e)
	}
	m := budgetFixtureManifest()
	id := plugins.Identity{PluginID: m.PluginID, InstanceID: "BudgetSynthetic", Version: m.Version}
	client, e := (plugins.Runner{Timeout: 5 * time.Second, ConcurrentCalls: true, RequireConcurrent: true}).Start(ctx, plugins.Command{Path: exe, Args: []string{"-test.run=^TestBudgetWorkerFixture$", "--", "tt-sdk-budget-worker"}}, m, plugins.SessionBinding{Identity: id, Generation: 1})
	if e != nil {
		cancel()
		t.Fatal(e)
	}
	f := &budgetFixture{t: t, ctx: ctx, cancel: cancel, client: client, identity: id, backup: t.TempDir(), added: map[string]bool{}, markers: map[string]string{}, history: map[string]json.RawMessage{}, entered: make(chan struct{}), release: make(chan struct{})}
	for i := 1; i <= n; i++ {
		hash := fmt.Sprintf("%040x", i)
		f.tasks = append(f.tasks, plugins.DownloaderTaskV1{Hash: hash, QBittorrent: &plugins.QBittorrentTaskInfoV1{Tags: ptr("tag"), Category: ptr("Music"), SavePath: ptr("/source/music"), State: ptr("pausedUP")}})
		raw, _ := bencode(map[string]any{"announce": "https://synthetic.invalid/announce", "info": map[string]any{"name": hash, "length": newByteInteger(1)}})
		if e := os.WriteFile(filepath.Join(f.backup, hash+".torrent"), raw, 0600); e != nil {
			t.Fatal(e)
		}
	}
	t.Cleanup(func() {
		stopCtx, x := context.WithTimeout(context.Background(), 4*time.Second)
		defer x()
		_ = client.Stop(stopCtx)
		cancel()
	})
	return f
}
func (f *budgetFixture) config() map[string]any {
	return map[string]any{"enabled": false, "onlyonce": false, "cron": "0 0 * * *", "autostart": false, "fromdownloader": "Source", "todownloader": "Target", "fromtorrentpath": f.backup, "frompath": "/source", "topath": "/dest", "deletesource": true, "deleteduplicate": false, "notify": true, "transferemptylabel": true, "skipverify": false, "remainoldtag": false, "remainoldcat": false, "add_torrent_tags": "transferred"}
}
func (f *budgetFixture) call(method string, in any) plugins.RunResult {
	f.t.Helper()
	raw, e := json.Marshal(in)
	if e != nil {
		f.t.Fatal(e)
	}
	start := time.Now()
	f.event("owner.start", method)
	out, e := f.client.CallWithOptions(f.ctx, method, raw, f.phaseHost("owner:"+method), plugins.CallOptions{CallerDeadlineOnly: true})
	if e != nil {
		f.t.Fatalf("%s: %v", method, e)
	}
	f.mu.Lock()
	f.phases = append(f.phases, phaseObservation{method: method, result: out, start: start, end: time.Now()})
	f.mu.Unlock()
	f.event("owner.complete", method)
	return out
}
func (f *budgetFixture) host(ctx context.Context, id plugins.Identity, m string, raw json.RawMessage) (json.RawMessage, error) {
	if id != f.identity || ctx.Err() != nil {
		return nil, errors.New("fixture callback identity/scope mismatch")
	}
	f.mu.Lock()
	f.totalRPC++
	label, _ := ctx.Value(budgetPhaseKey{}).(string)
	f.events = append(f.events, map[string]any{"at": time.Now().UTC().Format(time.RFC3339Nano), "kind": "rpc", "phase": label, "method": m})
	block := f.blockMethod == m
	f.mu.Unlock()
	if block {
		f.blockOnce.Do(func() {
			close(f.entered)
			select {
			case <-f.release:
			case <-ctx.Done():
			}
		})
		if e := ctx.Err(); e != nil {
			return nil, e
		}
	}
	if m == plugins.BackgroundSubmitCapability {
		var in struct {
			Method  string          `json:"method"`
			Payload json.RawMessage `json:"payload"`
		}
		if e := json.Unmarshal(raw, &in); e != nil {
			return nil, e
		}
		start := time.Now()
		label := in.Method + ":" + string(in.Payload)
		f.event("child.admit", label)
		pending, e := f.client.StartCallWithAdmission(ctx, f.ctx, in.Method, in.Payload, f.phaseHost(label), plugins.CallOptions{CallerDeadlineOnly: true})
		if e != nil {
			return nil, e
		}
		go func() {
			result, e := pending.Wait()
			f.mu.Lock()
			f.phases = append(f.phases, phaseObservation{method: in.Method, result: result, err: e, start: start, end: time.Now()})
			f.mu.Unlock()
			f.event("child.complete", label)
		}()
		return json.RawMessage(`{"accepted":true}`), nil
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	var sel plugins.DownloaderTasksRequestV1
	_ = json.Unmarshal(raw, &sel)
	mutation := func(accepted bool) json.RawMessage {
		outcome := "confirmed"
		if !accepted {
			outcome = "rejected"
		}
		r, _ := json.Marshal(plugins.DownloaderSourceMutationResultV1{DownloaderMutationV1: plugins.DownloaderMutationV1{SchemaVersion: 1, DownloaderID: sel.DownloaderID, Accepted: accepted, Outcome: outcome}})
		return r
	}
	switch m {
	case plugins.DownloaderConfigurationsReadCapability:
		return json.Marshal(plugins.DownloaderConfigurationsV1{SchemaVersion: 1, Downloaders: []plugins.BrushDownloaderReferenceV1{{ID: "source", Name: "Source", Kind: "qbittorrent", Enabled: true}, {ID: "target", Name: "Target", Kind: "qbittorrent", Enabled: true}}})
	case plugins.DownloaderSourceConnectionCapability:
		return json.Marshal(plugins.DownloaderSourceConnectionV1{DownloaderConnectionV1: plugins.DownloaderConnectionV1{SchemaVersion: 1, DownloaderID: sel.DownloaderID, Kind: "qbittorrent"}, Exists: true})
	case plugins.DownloaderSourceCompletedCapability:
		return json.Marshal(plugins.DownloaderSourceTasksResultV1{DownloaderTasksV1: plugins.DownloaderTasksV1{SchemaVersion: 1, DownloaderID: sel.DownloaderID, Kind: "qbittorrent", Tasks: f.tasks}})
	case plugins.DownloaderSourceTasksCapability:
		tasks := []plugins.DownloaderTaskV1{}
		for _, hash := range sel.Hashes {
			if f.added[hash] {
				state := "checkingUP"
				if f.ready {
					state = "pausedUP"
				}
				tasks = append(tasks, plugins.DownloaderTaskV1{Hash: hash, QBittorrent: &plugins.QBittorrentTaskInfoV1{State: ptr(state)}})
			}
		}
		return json.Marshal(plugins.DownloaderSourceTasksResultV1{DownloaderTasksV1: plugins.DownloaderTasksV1{SchemaVersion: 1, DownloaderID: sel.DownloaderID, Kind: "qbittorrent", Tasks: tasks}})
	case plugins.DownloaderSourceAddCapability:
		var in plugins.DownloaderAddRequestV1
		if e := json.Unmarshal(raw, &in); e != nil {
			return nil, e
		}
		value, e := bdecode(in.Torrent)
		if e != nil {
			return nil, e
		}
		hash := value.(map[string]any)["info"].(map[string]any)["name"].(string)
		f.added[hash] = true
		for _, tag := range in.Tags {
			f.markers[tag] = hash
		}
		return mutation(true), nil
	case plugins.DownloaderSourceFindByTagCapability:
		var in plugins.DownloaderFindByTagRequestV1
		_ = json.Unmarshal(raw, &in)
		hash := f.markers[in.Tags[0]]
		return json.Marshal(plugins.DownloaderFindByTagV1{SchemaVersion: 1, DownloaderID: in.DownloaderID, Hash: &hash})
	case plugins.DownloaderSourceControlCapability:
		var in plugins.DownloaderControlRequestV1
		_ = json.Unmarshal(raw, &in)
		if in.Action == "start" {
			f.starts = append(f.starts, append([]string{}, in.Hashes...))
		}
		return mutation(true), nil
	case plugins.BaseSaveData:
		var in struct {
			Key   string
			Value json.RawMessage
		}
		_ = json.Unmarshal(raw, &in)
		f.history[in.Key] = in.Value
		return json.RawMessage(`null`), nil
	case plugins.BaseUpdateConfig:
		return json.RawMessage(`false`), nil
	case plugins.LogWriteCapability:
		var l plugins.LogEntryV1
		_ = json.Unmarshal(raw, &l)
		f.logs = append(f.logs, l)
		if l.Message == "转移做种任务执行完成" {
			f.events = append(f.events, map[string]any{"at": time.Now().UTC().Format(time.RFC3339Nano), "kind": "business.transfer.complete", "phase": label})
		}
		return json.RawMessage(`null`), nil
	case plugins.PostMessageCapability:
		var n map[string]any
		_ = json.Unmarshal(raw, &n)
		f.notices = append(f.notices, n)
		return json.RawMessage(`{"queued":0,"attempted":0,"failed":0,"errors":[]}`), nil
	case plugins.SystemMessagePutCapability:
		return json.RawMessage(`null`), nil
	}
	return nil, errors.New("unexpected budget fixture resource " + m)
}
func (f *budgetFixture) snapshot() map[string]any {
	var out map[string]any
	_ = json.Unmarshal(f.call("test_snapshot", nil).Result, &out)
	return out
}
func (f *budgetFixture) wait(test func() bool) {
	f.t.Helper()
	until := time.Now().Add(5 * time.Second)
	for !test() {
		if time.Now().After(until) {
			f.t.Fatal("fixture state did not settle")
		}
		time.Sleep(time.Millisecond)
	}
}
func (f *budgetFixture) assertBudgets(minRPC int) {
	f.t.Helper()
	f.wait(func() bool { s := f.snapshot(); return s["passes"] == float64(0) })
	f.mu.Lock()
	defer f.mu.Unlock()
	maxCalls := 0
	for _, p := range f.phases {
		if p.err != nil {
			f.t.Errorf("phase %s: %v", p.method, p.err)
		}
		if p.result.HostCalls > maxCalls {
			maxCalls = p.result.HostCalls
		}
		if p.result.HostCalls > 64 {
			f.t.Fatalf("phase exceeded unchanged default budget: %s %d", p.method, p.result.HostCalls)
		}
	}
	if f.totalRPC < minRPC {
		f.t.Fatalf("not enough actual host RPC: %d < %d", f.totalRPC, minRPC)
	}
	f.saveObservationLocked(maxCalls)
	f.t.Logf("unchanged default budget=64, observed maximum=%d, total host RPC=%d, returned SDK phases=%d", maxCalls, f.totalRPC, len(f.phases))
}
func TestSDKHundredTorrentAndFilteredCursorBudgets(t *testing.T) {
	t.Run("120-transfers", func(t *testing.T) {
		f := startBudgetFixture(t, 120)
		f.call("init", f.config())
		f.call("transfer", nil)
		f.mu.Lock()
		if len(f.history) != 120 || len(f.notices) != 1 || f.notices[0]["text"] != "总数：120，成功：120，失败：0，跳过：0，删除重复：0" {
			t.Fatal("large pass effects/summary", len(f.history), f.notices)
		}
		f.mu.Unlock()
		f.assertBudgets(257)
	})
	t.Run("400-filtered-candidates", func(t *testing.T) {
		f := startBudgetFixture(t, 400)
		cfg := f.config()
		cfg["includelabels"] = "missing"
		f.call("init", cfg)
		f.call("transfer", nil)
		f.mu.Lock()
		if len(f.history) != 0 || len(f.notices) != 0 || len(f.added) != 0 {
			t.Fatal("filtered candidates produced side effects")
		}
		filtered := 0
		for _, l := range f.logs {
			if strings.Contains(l.Message, "不含有转移标签 missing") {
				filtered++
			}
		}
		f.mu.Unlock()
		if filtered != 400 {
			t.Fatal("filtered logs dropped", filtered)
		}
		f.assertBudgets(257)
	})
}
func TestSDKLargeRecheckPreservesSingleBulkCalls(t *testing.T) {
	f := startBudgetFixture(t, 0)
	f.call("init", f.config())
	ids := []string{}
	f.mu.Lock()
	for i := 1; i <= 2000; i++ {
		hash := fmt.Sprintf("%040x", i)
		ids = append(ids, hash)
		f.added[hash] = true
	}
	f.mu.Unlock()
	f.call("test_pending", ids)
	first := f.call("check_recheck", nil)
	if first.HostCalls > 64 || f.snapshot()["pending"] != float64(2000) {
		t.Fatal("incomplete large queue changed")
	}
	f.mu.Lock()
	f.ready = true
	f.mu.Unlock()
	last := f.call("check_recheck", nil)
	if last.HostCalls > 64 || f.snapshot()["pending"] != float64(0) {
		t.Fatal("ready large queue not cleared")
	}
	f.mu.Lock()
	if len(f.starts) != 1 || len(f.starts[0]) != 2000 {
		t.Fatal("source bulk start was split", len(f.starts))
	}
	f.mu.Unlock()
	f.assertBudgets(1)
}
func TestSDKMoreThan64PrivateTicksKeepFreshBudgets(t *testing.T) {
	f := startBudgetFixture(t, 0)
	cfg := f.config()
	cfg["enabled"] = true
	cfg["autostart"] = true
	f.call("init", cfg)
	hash := fmt.Sprintf("%040x", 1)
	f.mu.Lock()
	f.added[hash] = true
	f.mu.Unlock()
	f.call("test_pending", []string{hash})
	for i := 0; i < 70; i++ {
		f.wait(func() bool { return f.snapshot()["waiting_timers"] == float64(1) })
		f.mu.Lock()
		before := 0
		for _, p := range f.phases {
			if p.method == "private_scheduler" {
				before++
			}
		}
		f.mu.Unlock()
		if string(f.call("test_tick", nil).Result) != "true" {
			t.Fatal("timer tick not delivered")
		}
		f.wait(func() bool {
			f.mu.Lock()
			defer f.mu.Unlock()
			after := 0
			for _, p := range f.phases {
				if p.method == "private_scheduler" {
					after++
				}
			}
			return after > before
		})
	}
	f.call("stop_service", nil)
	if f.snapshot()["active_scheduler"] != float64(0) || f.snapshot()["waiting_timers"] != float64(0) {
		t.Fatal("private timer leaked after stop")
	}
	f.assertBudgets(257)
}

func (f *budgetFixture) async(method string, in any) <-chan error {
	raw, _ := json.Marshal(in)
	done := make(chan error, 1)
	go func() {
		start := time.Now()
		f.event("owner.start", method)
		out, e := f.client.CallWithOptions(f.ctx, method, raw, f.phaseHost("owner:"+method), plugins.CallOptions{CallerDeadlineOnly: true})
		f.mu.Lock()
		f.phases = append(f.phases, phaseObservation{method: method, result: out, err: e, start: start, end: time.Now()})
		f.mu.Unlock()
		done <- e
	}()
	return done
}
func TestSDKStopAndReinitJoinQueuedAndActiveContinuations(t *testing.T) {
	t.Run("stop-queued-before-selection", func(t *testing.T) {
		f := startBudgetFixture(t, 100)
		cfg := f.config()
		cfg["onlyonce"] = true
		f.call("init", cfg)
		f.wait(func() bool { return f.snapshot()["waiting_timers"] == float64(1) })
		f.mu.Lock()
		f.blockMethod = plugins.BackgroundSubmitCapability
		f.mu.Unlock()
		f.call("test_tick", nil)
		select {
		case <-f.entered:
		case <-time.After(5 * time.Second):
			t.Fatal("continuation admission was not reached")
		}
		stopped := f.async("stop_service", nil)
		select {
		case e := <-stopped:
			t.Fatal("stop returned before queued owner joined", e)
		case <-time.After(30 * time.Millisecond):
		}
		close(f.release)
		select {
		case e := <-stopped:
			if e != nil {
				t.Fatal(e)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("queued stop did not join")
		}
		state := f.snapshot()
		if state["passes"] != float64(0) || state["active_scheduler"] != float64(0) {
			t.Fatal("queued pass leaked", state)
		}
		f.mu.Lock()
		if len(f.history) != 0 || len(f.added) != 0 || len(f.notices) != 0 {
			t.Fatal("stop during selection produced write/summary")
		}
		f.mu.Unlock()
		f.assertBudgets(1)
	})
	for _, reinit := range []bool{false, true} {
		name := "stop-active-side-effects"
		if reinit {
			name = "reinit-active-side-effects"
		}
		t.Run(name, func(t *testing.T) {
			f := startBudgetFixture(t, 100)
			cfg := f.config()
			cfg["onlyonce"] = true
			f.call("init", cfg)
			f.wait(func() bool { return f.snapshot()["waiting_timers"] == float64(1) })
			f.mu.Lock()
			f.blockMethod = plugins.DownloaderSourceAddCapability
			f.mu.Unlock()
			f.call("test_tick", nil)
			select {
			case <-f.entered:
			case <-time.After(5 * time.Second):
				t.Fatal("first add not reached")
			}
			if f.snapshot()["passes"] != float64(1) {
				t.Fatal("owner pass ended while add was blocked")
			}
			method := "stop_service"
			var in any
			if reinit {
				method = "init"
				cfg["onlyonce"] = false
				in = cfg
			}
			stopped := f.async(method, in)
			select {
			case e := <-stopped:
				t.Fatal("lifecycle returned before source side effects joined", e)
			case <-time.After(30 * time.Millisecond):
			}
			close(f.release)
			select {
			case e := <-stopped:
				if e != nil {
					t.Fatal(e)
				}
			case <-time.After(8 * time.Second):
				t.Fatal("active lifecycle did not join")
			}
			state := f.snapshot()
			if state["passes"] != float64(0) || state["active_scheduler"] != float64(0) || state["pending"] != float64(100) {
				t.Fatal("active pass state leaked/reset", state)
			}
			f.mu.Lock()
			if len(f.history) != 100 || len(f.notices) != 1 || f.notices[0]["text"] != "总数：100，成功：100，失败：0，跳过：0，删除重复：0" {
				t.Fatal("source effect-phase stop behavior changed", len(f.history), f.notices)
			}
			before := len(f.history)
			f.mu.Unlock()
			if string(f.call("test_tick", nil).Result) != "false" {
				t.Fatal("removed private clock remained queued")
			}
			f.mu.Lock()
			if len(f.history) != before {
				t.Fatal("completed continuation replayed")
			}
			f.mu.Unlock()
			f.assertBudgets(257)
		})
	}
}

type budgetPhaseKey struct{}

func (f *budgetFixture) phaseHost(label string) plugins.HostHandler {
	return func(ctx context.Context, id plugins.Identity, m string, r json.RawMessage) (json.RawMessage, error) {
		return f.host(context.WithValue(ctx, budgetPhaseKey{}, label), id, m, r)
	}
}
func (f *budgetFixture) event(kind, phase string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = append(f.events, map[string]any{"at": time.Now().UTC().Format(time.RFC3339Nano), "kind": kind, "phase": phase})
}
func (f *budgetFixture) saveObservationLocked(maxCalls int) {
	dir, e := filepath.Abs("../../../work/migration-20261105/torrenttransfer/sdk-observations")
	if e != nil {
		f.t.Fatal(e)
	}
	if e = os.MkdirAll(dir, 0700); e != nil {
		f.t.Fatal(e)
	}
	phases := []map[string]any{}
	for _, p := range f.phases {
		fault := ""
		if p.err != nil {
			fault = p.err.Error()
		}
		phases = append(phases, map[string]any{"method": p.method, "start": p.start.UTC().Format(time.RFC3339Nano), "end": p.end.UTC().Format(time.RFC3339Nano), "host_calls": p.result.HostCalls, "error": fault})
	}
	raw, e := json.MarshalIndent(map[string]any{"kind": "actual-independent-SDK-worker-with-synthetic-host-resources", "test": f.t.Name(), "clock": "test-only-controlled-clock;production-timer-unchanged", "default_max_host_calls": 64, "observed_max_host_calls": maxCalls, "total_rpc": f.totalRPC, "phases": phases, "events": f.events}, "", "  ")
	if e != nil {
		f.t.Fatal(e)
	}
	name := strings.ReplaceAll(f.t.Name(), "/", "-") + ".json"
	if e = os.WriteFile(filepath.Join(dir, name), append(raw, '\n'), 0600); e != nil {
		f.t.Fatal(e)
	}
}
