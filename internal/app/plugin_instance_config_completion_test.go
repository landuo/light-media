package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync/atomic"
	"testing"

	"light-media/internal/download"
	bf "light-media/internal/plugins/brushflow"
	"light-media/internal/sites"
)

// The real core normalizes sparse task defaults during init and calls SaveConfig.
// BrushFlow records its effective durable revision in authenticated own_config
// effects; its common run/attempt revision remains the original admission.
func TestPluginInstanceSettingsAcceptsAuthenticatedInitRevisionAndFinalStatus(t *testing.T) {
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
	a.config.Sites = []SiteConfig{{Config: sites.Config{ID: "completion-site", Kind: "nexusphp", Name: "Owned completion site", BaseURL: trap.URL}, Enabled: true}}
	a.config.Downloaders = []DownloaderConfig{{Config: download.Config{ID: "completion-downloader", Kind: "qbittorrent", BaseURL: trap.URL}, Name: "Owned inactive completion downloader", Enabled: &disabled}}
	a.cfgMu.Unlock()
	if err := a.bootstrapNativeSiteIdentities(ctx); err != nil {
		t.Fatal(err)
	}
	site, err := a.Store.nativeSiteByAppID(ctx, "completion-site")
	if err != nil {
		t.Fatal(err)
	}
	installCloneSource(t, p, bf.NativeManifest())
	config := bf.DefaultConfig()
	const taskID = "owned-sparse-completion-task"
	// Valid references, stable ID, and no missing user credentials. Sparse display
	// defaults are intentionally not pre-normalized by the test.
	config["tasks"] = []any{map[string]any{"id": taskID, "enabled": true, "notify": false, "site_id": site.SourceID, "downloader": "Owned inactive completion downloader"}}
	source, err := p.SaveConfig(ctx, bf.PluginID, 0, config)
	if err != nil {
		t.Fatal(err)
	}
	clone, err := p.Clone(ctx, bf.PluginID, PluginCloneRequest{Suffix: "CompletionOwned"})
	if err != nil {
		t.Fatal(err)
	}
	workerCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { defer close(done); a.pluginWorker(workerCtx) }()
	defer func() { cancel(); <-done }()
	response := callTest(t, a.Handler(), "POST", "/api/plugins/"+clone.InstanceID+"/api/settings", `{"enabled":false,"show_sidebar_nav":false,"global_proxy_delete":false}`, cookie, "")
	if response.Code != 200 {
		t.Fatalf("legitimate init revision change rejected: %d %s", response.Code, response.Body.String())
	}
	var out struct {
		Success bool `json:"success"`
		Data    struct {
			Enabled bool `json:"enabled"`
			Tasks   []struct {
				ID            string `json:"id"`
				Name          string `json:"name"`
				BrushInterval int    `json:"brush_interval"`
				CheckInterval int    `json:"check_interval"`
			} `json:"tasks"`
		} `json:"data"`
	}
	if err = json.Unmarshal(response.Body.Bytes(), &out); err != nil || !out.Success || out.Data.Enabled || len(out.Data.Tasks) != 1 {
		t.Fatalf("original settings result: %s %v", response.Body.String(), err)
	}
	task := out.Data.Tasks[0]
	if task.ID != taskID || task.Name != "刷流任务" || task.BrushInterval != 10 || task.CheckInterval != 5 {
		t.Fatalf("returned prepare-time rather than final normalized status: %+v", task)
	}
	var runID string
	if err = a.Store.DB.QueryRow(`SELECT id FROM plugin_runs WHERE instance_id=? AND action='init'`, clone.InstanceID).Scan(&runID); err != nil {
		t.Fatal(err)
	}
	var initCount, taskCount, activeSchedules int
	if err = a.Store.DB.QueryRow(`SELECT COUNT(*) FROM plugin_runs WHERE instance_id=? AND action='init'`, clone.InstanceID).Scan(&initCount); err != nil || initCount != 1 {
		t.Fatalf("wrong init count %d %v", initCount, err)
	}
	if err = a.Store.DB.QueryRow(`SELECT COUNT(*) FROM plugin_runs WHERE instance_id=? AND action<>'init'`, clone.InstanceID).Scan(&taskCount); err != nil || taskCount != 0 {
		t.Fatalf("disabled configuration executed business work %d %v", taskCount, err)
	}
	if err = a.Store.DB.QueryRow(`SELECT COUNT(*) FROM plugin_schedules WHERE instance_id=? AND active=1`, clone.InstanceID).Scan(&activeSchedules); err != nil || activeSchedules != 0 {
		t.Fatalf("disabled configuration scheduled work %d %v", activeSchedules, err)
	}
	run, err := p.Run(ctx, runID)
	if err != nil || run.State != "succeeded" {
		t.Fatalf("init not complete: %+v %v", run, err)
	}
	tx, err := a.Store.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	head, err := p.runJournal.loadHead(ctx, tx, clone.InstanceID, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	completion, err := p.runJournal.loadCompletion(head)
	if err != nil {
		t.Fatal(err)
	}
	if head.claim.Admission.InstanceID != clone.InstanceID || head.claim.Admission.RunID != run.ID || head.claim.Admission.Action != "init" || head.claim.Admission.AdmissionRevision != completion.FinalRevision || run.ConfigRevision != completion.FinalRevision {
		t.Fatalf("common admission/run/completion identity changed: admission=%+v final=%+v run=%+v", head.claim.Admission, completion, run)
	}
	actual, err := newPluginRunAttemptResult(run.State, run.ErrorCode, run.Result)
	if err != nil || actual != completion.Result {
		t.Fatalf("completion result mismatch: %v", err)
	}
	if err = tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	stored, err := p.Config(ctx, clone.InstanceID)
	if err != nil || stored.Revision <= completion.FinalRevision {
		t.Fatalf("native durable revision did not advance beyond the common attempt: %v", err)
	}

	proofTx, err := a.Store.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	// Reuse the actual native verifier. Do not duplicate or loosen own_config
	// effect chaining to force the generic journal into another revision model.
	proofErr := a.brushFlowHTTPRevisionTx(ctx, proofTx, run)
	_ = proofTx.Rollback()
	if proofErr != nil {
		t.Fatalf("native effective revision lacks own_config proof: %v", proofErr)
	}
	var saved struct {
		Tasks []map[string]any `json:"tasks"`
	}
	if err = json.Unmarshal(stored.Value, &saved); err != nil || len(saved.Tasks) != 1 || saved.Tasks[0]["name"] != "刷流任务" || saved.Tasks[0]["brush_interval"] != float64(10) {
		t.Fatalf("actual normalized config missing: %s %v", stored.Value, err)
	}
	status := callTest(t, a.Handler(), "GET", "/api/plugins/"+clone.InstanceID+"/api/status", "", cookie, "")
	if status.Code != 200 {
		t.Fatalf("status after save: %d %s", status.Code, status.Body.String())
	}
	var fresh, returned map[string]any
	if json.Unmarshal(status.Body.Bytes(), &fresh) != nil || json.Unmarshal(response.Body.Bytes(), &returned) != nil || !reflect.DeepEqual(fresh, returned) {
		t.Fatal("settings response is not the final read status")
	}
	after, err := p.Config(ctx, bf.PluginID)
	if err != nil || source.Revision != after.Revision || !bytes.Equal(source.Value, after.Value) {
		t.Fatalf("source config changed: %v", err)
	}
	if calls.Load() != 0 {
		t.Fatalf("normalization performed dependency network: %d", calls.Load())
	}
}
