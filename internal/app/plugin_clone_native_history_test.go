package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"light-media/internal/download"
	bf "light-media/internal/plugins/brushflow"
	"light-media/internal/sites"
)

// Real native configuration + task execution creates the old-generation history
// and receipt. No owner/state/receipt rows are fabricated by this fixture.
func TestPluginCloneRecreatedNativeGenerationDoesNotReviveHistory(t *testing.T) {
	a, source := brushDefault(t)
	cookie := setupTestAdmin(t, a)
	p := a.pluginStore
	ctx := context.Background()
	var calls atomic.Int32
	trap := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		http.Error(w, "unexpected synthetic dependency call", http.StatusServiceUnavailable)
	}))
	defer trap.Close()
	disabled := false
	a.cfgMu.Lock()
	a.config.Sites = []SiteConfig{{Config: sites.Config{ID: "clone-history-site", Kind: "nexusphp", Name: "Synthetic history site", BaseURL: trap.URL}, Enabled: true}}
	a.config.Downloaders = []DownloaderConfig{{Config: download.Config{ID: "clone-history-downloader", Kind: "qbittorrent", BaseURL: trap.URL}, Name: "Inactive synthetic downloader", Enabled: &disabled}}
	a.cfgMu.Unlock()
	if err := a.bootstrapNativeSiteIdentities(ctx); err != nil {
		t.Fatal(err)
	}
	identity, err := a.Store.nativeSiteByAppID(ctx, "clone-history-site")
	if err != nil {
		t.Fatal(err)
	}
	sourceConfig, err := p.Config(ctx, source.InstanceID)
	if err != nil {
		t.Fatal(err)
	}
	req := PluginCloneRequest{Suffix: "NativeHistory"}
	clone, err := p.Clone(ctx, source.InstanceID, req)
	if err != nil {
		t.Fatal(err)
	}
	copied, err := p.Config(ctx, clone.InstanceID)
	if err != nil {
		t.Fatal(err)
	}
	const taskID = "owned-old-generation-task"
	config := bf.DefaultConfig()
	config["enabled"] = true
	config["tasks"] = []any{map[string]any{"id": taskID, "name": "Old clone generation task", "enabled": true, "notify": false, "site_id": identity.SourceID, "downloader": "Inactive synthetic downloader", "except_subscribe": false}}
	_, queued, err := p.SaveConfigAndQueue(ctx, clone.InstanceID, copied.Revision, config)
	if err != nil {
		t.Fatal(err)
	}
	init, err := p.claimRunForInstance(ctx, clone.InstanceID)
	if err != nil || init.ID != queued.ID {
		t.Fatalf("claim real clone initialization: %+v %v", init, err)
	}
	brushFinish(t, a, init)
	// Real initialization may enqueue its authenticated tag-cleanup task. Complete
	// only this owned namespace's bounded queue before the explicit brush request.
	drain := func() {
		t.Helper()
		for n := 0; n < 8; n++ {
			run, e := p.claimRunForInstance(ctx, clone.InstanceID)
			if errors.Is(e, sql.ErrNoRows) {
				return
			}
			if e != nil {
				t.Fatal(e)
			}
			brushFinish(t, a, run)
		}
		t.Fatal("unexpected unbounded owned task queue")
	}
	drain()
	api := func(method, path string) map[string]json.RawMessage {
		t.Helper()
		response := callTest(t, a.Handler(), method, "/api/plugins/"+clone.InstanceID+"/api/"+path, "", cookie, "")
		if response.Code != 200 {
			t.Fatalf("%s %s: %d %s", method, path, response.Code, response.Body.String())
		}
		var out map[string]json.RawMessage
		if err := json.Unmarshal(response.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	if got := api("POST", "tasks/"+taskID+"/run"); string(got["success"]) != "true" {
		t.Fatalf("native task request rejected: %v", got)
	}
	task, err := p.claimRunForInstance(ctx, clone.InstanceID)
	if err != nil {
		t.Fatal(err)
	}
	brushFinish(t, a, task)
	detail := api("GET", "tasks/"+taskID)
	var oldDetail struct {
		Runs []json.RawMessage `json:"runs"`
	}
	if err = json.Unmarshal(detail["data"], &oldDetail); err != nil || string(detail["success"]) != "true" || len(oldDetail.Runs) == 0 {
		t.Fatalf("real native history missing: %v %v", detail, err)
	}
	if !bytes.Contains(detail["data"], []byte("downloader_unavailable")) {
		t.Fatalf("unexpected native task result: %s", detail["data"])
	}
	if calls.Load() != 0 {
		t.Fatalf("inactive downloader path performed network: %d", calls.Load())
	}
	oldInstallation, err := p.Installation(ctx, clone.InstanceID)
	if err != nil {
		t.Fatal(err)
	}
	// Read-only audit snapshots are taken only after genuine successful execution.
	var oldState, oldRoot, oldReceipt []byte
	if err = a.Store.DB.QueryRow(`SELECT sealed FROM plugin_brush_flow_state WHERE instance_id=? AND generation=?`, clone.InstanceID, oldInstallation.Generation).Scan(&oldState); err != nil {
		t.Fatal(err)
	}
	if err = a.Store.DB.QueryRow(`SELECT sealed FROM plugin_brush_flow_roots WHERE instance_id=? AND generation=?`, clone.InstanceID, oldInstallation.Generation).Scan(&oldRoot); err != nil {
		t.Fatal(err)
	}
	if err = a.Store.DB.QueryRow(`SELECT sealed FROM plugin_deferred_operations WHERE run_id=?`, task.ID).Scan(&oldReceipt); err != nil {
		t.Fatal(err)
	}
	if len(oldState) == 0 || len(oldRoot) == 0 || len(oldReceipt) == 0 {
		t.Fatal("missing authenticated audit evidence")
	}
	removed := callTest(t, a.Handler(), "POST", "/api/plugins/"+clone.InstanceID+"/uninstall", `{}`, cookie, "")
	if removed.Code != 200 {
		t.Fatalf("native uninstall: %d %s", removed.Code, removed.Body.String())
	}
	recreated, err := p.Clone(ctx, source.InstanceID, req)
	if err != nil {
		t.Fatal("native same-ID recreation", err)
	}
	if recreated.InstanceID != clone.InstanceID || recreated.Generation <= oldInstallation.Generation {
		t.Fatalf("not a new generation: %+v", recreated)
	}
	status := api("GET", "status")
	if string(status["success"]) != "true" || bytes.Contains(status["data"], []byte(taskID)) || bytes.Contains(status["data"], []byte("downloader_unavailable")) {
		t.Fatalf("status revived old history: %v", status)
	}
	missing := api("GET", "tasks/"+taskID)
	if string(missing["success"]) != "false" || !strings.Contains(string(missing["message"]), "任务不存在") {
		t.Fatalf("old task detail survived new source configuration: %v", missing)
	}
	var newState, newRoot, newReceipt []byte
	if err = a.Store.DB.QueryRow(`SELECT sealed FROM plugin_brush_flow_state WHERE instance_id=? AND generation=?`, clone.InstanceID, oldInstallation.Generation).Scan(&newState); err != nil {
		t.Fatal(err)
	}
	if err = a.Store.DB.QueryRow(`SELECT sealed FROM plugin_brush_flow_roots WHERE instance_id=? AND generation=?`, clone.InstanceID, oldInstallation.Generation).Scan(&newRoot); err != nil {
		t.Fatal(err)
	}
	if err = a.Store.DB.QueryRow(`SELECT sealed FROM plugin_deferred_operations WHERE run_id=?`, task.ID).Scan(&newReceipt); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(oldState, newState) || !bytes.Equal(oldRoot, newRoot) || !bytes.Equal(oldReceipt, newReceipt) {
		t.Fatal("recreation erased or rewrote historical sealed audit evidence")
	}
	currentSource, err := p.Config(ctx, source.InstanceID)
	if err != nil || !bytes.Equal(sourceConfig.Value, currentSource.Value) || sourceConfig.Revision != currentSource.Revision {
		t.Fatalf("source configuration changed: %v", err)
	}
	if calls.Load() != 0 {
		t.Fatalf("lifecycle issued dependency network: %d", calls.Load())
	}
}
