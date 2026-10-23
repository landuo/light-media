package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"

	"light-media/internal/download"
	bf "light-media/internal/plugins/brushflow"
	"light-media/internal/sites"
)

// Original remote CRUD remains available after loading even while business
// enabled=false. Explicit reload isolates this contract; it is not a proposed
// mandatory first-configuration step. No worker pumps queued cleanup here.
func TestPluginLoadedDisabledCloneUsesOriginalRemoteCRUD(t *testing.T) {
	pluginLoadedRemoteCRUD(t, false)
}

// The ordinary first screen reads status and submits its original create API.
// There is no user reload request or empty configuration save in this path.
func TestPluginNewDisabledCloneFirstRemoteCRUDLoadsWithoutManualReload(t *testing.T) {
	pluginLoadedRemoteCRUD(t, true)
}

func pluginLoadedRemoteCRUD(t *testing.T, automatic bool) {
	a := newTestApp(t)
	useBuiltInPluginProcess(t, a)
	cookie := setupTestAdmin(t, a)
	ctx := context.Background()
	p := a.pluginStore
	var calls atomic.Int32
	trap := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		http.Error(w, "unexpected dependency access", 503)
	}))
	defer trap.Close()
	disabled := false
	a.cfgMu.Lock()
	a.config.Sites = []SiteConfig{{Config: sites.Config{ID: "loaded-remote-site", Kind: "nexusphp", Name: "Owned remote site", BaseURL: trap.URL}, Enabled: true}}
	a.config.Downloaders = []DownloaderConfig{{Config: download.Config{ID: "loaded-remote-downloader", Kind: "qbittorrent", BaseURL: trap.URL}, Name: "Owned inactive remote downloader", Enabled: &disabled}}
	a.cfgMu.Unlock()
	if err := a.bootstrapNativeSiteIdentities(ctx); err != nil {
		t.Fatal(err)
	}
	site, err := a.Store.nativeSiteByAppID(ctx, "loaded-remote-site")
	if err != nil {
		t.Fatal(err)
	}
	installCloneSource(t, p, bf.NativeManifest())
	cfg := bf.DefaultConfig()
	cfg["enabled"] = false
	if _, err = p.SaveConfig(ctx, bf.PluginID, 0, cfg); err != nil {
		t.Fatal(err)
	}
	clone, err := p.Clone(ctx, bf.PluginID, PluginCloneRequest{Suffix: "LoadedRemote"})
	if err != nil {
		t.Fatal(err)
	}
	sourceBefore := reloadParityRows(t, a, bf.PluginID)
	cold, err := p.Installation(ctx, clone.InstanceID)
	if err != nil || cold.DesiredEnabled {
		t.Fatalf("expected fresh unloaded clone: %+v %v", cold, err)
	}
	stopWorker := func() {}
	if automatic {
		workerCtx, cancel := context.WithCancel(ctx)
		done := make(chan struct{})
		go func() { defer close(done); a.pluginWorker(workerCtx) }()
		var once sync.Once
		stopWorker = func() { once.Do(func() { cancel(); <-done }) }
		defer stopWorker()
	} else {
		reloaded := callTest(t, a.Handler(), "POST", "/api/plugins/"+clone.InstanceID+"/reload", `{}`, cookie, "")
		if reloaded.Code != 200 {
			t.Fatalf("reload: %d %s", reloaded.Code, reloaded.Body.String())
		}
		run := reloadParityResponseRun(t, reloaded.Body.Bytes())
		reloadParityComplete(t, a, clone.InstanceID, run.ID, "reload")
		loaded, err := p.Installation(ctx, clone.InstanceID)
		if err != nil || !loaded.DesiredEnabled {
			t.Fatalf("reload did not load instance: %+v %v", loaded, err)
		}

	}
	api := func(method, path string, body any) map[string]any {
		t.Helper()
		raw := ""
		if body != nil {
			raw = string(mustCleanSeedJSON(body))
		}
		w := callTest(t, a.Handler(), method, "/api/plugins/"+clone.InstanceID+"/api/"+path, raw, cookie, "")
		if w.Code != 200 {
			t.Fatalf("original %s %s: %d %s", method, path, w.Code, w.Body.String())
		}
		var out map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	checkConfig := func(wantTasks int) {
		t.Helper()
		stored, err := p.Config(ctx, clone.InstanceID)
		if err != nil {
			t.Fatal(err)
		}
		var got struct {
			Enabled bool             `json:"enabled"`
			Tasks   []map[string]any `json:"tasks"`
		}
		if err = json.Unmarshal(stored.Value, &got); err != nil || got.Enabled || len(got.Tasks) != wantTasks {
			t.Fatalf("business flag/task count changed: enabled=%v tasks=%d err=%v", got.Enabled, len(got.Tasks), err)
		}
		var active int
		if err = a.Store.DB.QueryRow(`SELECT COUNT(*) FROM plugin_schedules WHERE instance_id=? AND active=1`, clone.InstanceID).Scan(&active); err != nil || active != 0 {
			t.Fatalf("disabled instance scheduled business work: %d %v", active, err)
		}
		if !reflect.DeepEqual(sourceBefore, reloadParityRows(t, a, bf.PluginID)) {
			t.Fatal("source installation/config/data/runs/registrations changed")
		}
	}
	detailTask := func(out map[string]any) map[string]any {
		t.Helper()
		if out["success"] != true {
			t.Fatalf("original response failed: %v", out)
		}
		data, ok := out["data"].(map[string]any)
		if !ok {
			t.Fatal("missing detail data")
		}
		task, ok := data["task"].(map[string]any)
		if !ok {
			t.Fatal("missing original detail.task")
		}
		return task
	}
	checkConfig(0)
	if automatic {
		status := api("GET", "status", nil)
		data, ok := status["data"].(map[string]any)
		if status["success"] != true || !ok || data["enabled"] != false {
			t.Fatalf("first original status: %v", status)
		}
	}
	payload := map[string]any{"name": "Owned remote task", "enabled": false, "notify": false, "site_id": site.SourceID, "downloader": "Owned inactive remote downloader"}
	created := detailTask(api("POST", "tasks", payload))
	taskID, ok := created["id"].(string)
	if !ok || taskID == "" || created["name"] != payload["name"] {
		t.Fatalf("create identity/name: %v", created)
	}
	checkConfig(1)
	if automatic {
		loaded, err := p.Installation(ctx, clone.InstanceID)
		if err != nil || !loaded.DesiredEnabled {
			t.Fatalf("original create did not load instance: %+v %v", loaded, err)
		}
		var count, succeeded int
		if err = a.Store.DB.QueryRow(`SELECT COUNT(*),COALESCE(SUM(state='succeeded'),0) FROM plugin_runs WHERE instance_id=? AND action IN ('init','reload')`, clone.InstanceID).Scan(&count, &succeeded); err != nil || count != 1 || succeeded != 1 {
			t.Fatalf("expected exactly one completed framework load, got %d/%d %v", succeeded, count, err)
		}
	}
	payload["name"] = "Owned remote task revised"
	updated := detailTask(api("PUT", "tasks/"+taskID, payload))
	if updated["id"] != taskID || updated["name"] != payload["name"] {
		t.Fatalf("update changed identity/lost value: %v", updated)
	}
	checkConfig(1)
	toggled := detailTask(api("POST", "tasks/"+taskID+"/state", map[string]any{"enabled": true}))
	if toggled["id"] != taskID || toggled["enabled"] != true {
		t.Fatalf("task state not updated: %v", toggled)
	}
	checkConfig(1)
	detail := detailTask(api("GET", "tasks/"+taskID, nil))
	if detail["id"] != taskID || detail["name"] != payload["name"] || detail["enabled"] != true {
		t.Fatalf("readback lost persisted CRUD values: %v", detail)
	}
	// The real API runs synchronously; stop the normal worker before DELETE
	// solely to observe its real pending cleanup intent without consuming it.
	stopWorker()
	removed := api("DELETE", "tasks/"+taskID, nil)
	if removed["success"] != true {
		t.Fatalf("delete failed: %v", removed)
	}
	data, ok := removed["data"].(map[string]any)
	if !ok {
		t.Fatal("delete missing original status")
	}
	tasks, ok := data["tasks"].([]any)
	if !ok || len(tasks) != 0 || data["enabled"] != false {
		t.Fatalf("delete did not return disabled empty status: %v", data)
	}
	checkConfig(0)
	missing := api("GET", "tasks/"+taskID, nil)
	if missing["success"] != false || missing["message"] != "刷流任务不存在" {
		t.Fatalf("deleted detail contract: %v", missing)
	}
	state, _, err := p.brushFlowReadCurrent(ctx, a.Store.DB, clone.InstanceID, clone.Generation)
	if err != nil {
		t.Fatal(err)
	}
	queued := 0
	for token, source := range state.pendingSources(ctx, a.Store.DB) {
		if err = p.brushFlowValidateSource(ctx, a.Store.DB, state, token, source); err != nil {
			t.Fatal(err)
		}
		if source.TaskID != taskID || source.Operation != "cleanup_task_tag" {
			t.Fatalf("unexpected queued operation: %s", source.Operation)
		}
		cleanup, err := p.Run(ctx, source.Run)
		if err != nil || cleanup.State != "pending" || cleanup.Action != "task" {
			t.Fatalf("cleanup must remain queued, not executed: %+v %v", cleanup, err)
		}
		queued++
	}
	if err = state.check(); err != nil {
		t.Fatal(err)
	}
	if queued != 1 {
		t.Fatalf("expected one authenticated asynchronous tag-cleanup intent, got %d", queued)
	}
	var completedTasks int
	if err = a.Store.DB.QueryRow(`SELECT COUNT(*) FROM plugin_runs WHERE instance_id=? AND action IN ('task','service') AND state<>'pending'`, clone.InstanceID).Scan(&completedTasks); err != nil || completedTasks != 0 {
		t.Fatalf("business/cleanup execution occurred: %d %v", completedTasks, err)
	}
	if calls.Load() != 0 {
		t.Fatalf("CRUD accessed external dependency: %d", calls.Load())
	}
}
