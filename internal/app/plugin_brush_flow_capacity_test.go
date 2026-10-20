package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"light-media/internal/download"
	"light-media/internal/plugins"
	bf "light-media/internal/plugins/brushflow"
	"light-media/internal/plugins/builtin"
	"light-media/internal/sites"
	"strings"
	"testing"
	"time"
)

func brushCapacityConfig(t *testing.T, a *App, count int) bf.Row {
	t.Helper()
	ctx := context.Background()
	a.cfgMu.Lock()
	a.config.Sites = []SiteConfig{{Config: sites.Config{ID: "capacity-owned", Kind: "nexusphp", Name: "Owned capacity", BaseURL: "http://127.0.0.1:1"}, Enabled: true}}
	a.config.Downloaders = []DownloaderConfig{{Config: download.Config{ID: "capacity-qb", Kind: "qbittorrent", BaseURL: "http://127.0.0.1:1"}, Name: "Owned capacity qB"}}
	a.cfgMu.Unlock()
	if e := a.bootstrapNativeSiteIdentities(ctx); e != nil {
		t.Fatal(e)
	}
	identity, e := a.Store.nativeSiteByAppID(ctx, "capacity-owned")
	if e != nil {
		t.Fatal(e)
	}
	cfg := bf.DefaultConfig()
	cfg["enabled"] = true
	tasks := make([]any, count)
	for i := range tasks {
		tasks[i] = bf.Row{"id": fmt.Sprintf("capacity%08d", i), "name": "Owned capacity", "enabled": true, "site_id": identity.SourceID, "downloader": "Owned capacity qB"}
	}
	cfg["tasks"] = tasks
	return cfg
}

func TestBrushFlowOriginalConfigBeyondHostCellCeilingsUsesActualSDK(t *testing.T) {
	a := newTestApp(t)
	useBuiltInPluginProcess(t, a)
	cfg := brushCapacityConfig(t, a, 1)
	// Original NormalizeTask preserves this string and the source has no field
	// length or configuration-cell bound. The disabled fixture performs no IO.
	cfg["enabled"] = false
	cfg["tasks"].([]any)[0].(bf.Row)["save_path"] = "/owned/" + strings.Repeat("x", (17<<20)+1)
	want, e := json.Marshal(cfg)
	if e != nil || len(want) <= 16<<20 {
		t.Fatal("fixture", e, len(want))
	}
	in, run := brushInstall(t, a, cfg)
	if !bytes.Contains(run.Input, []byte(strings.Repeat("x", 1024))) {
		t.Fatal("original input lost")
	}
	workerInput, e := brushFlowWorkerInput(run)
	if e != nil || len(workerInput) >= 1024 || bytes.Contains(workerInput, []byte(strings.Repeat("x", 1024))) {
		t.Fatal("private complete config entered worker frame", e, len(workerInput))
	}
	brushFinish(t, a, run)
	stored, e := a.pluginStore.Config(context.Background(), in.InstanceID)
	if e != nil || len(stored.Value) <= 16<<20 {
		t.Fatal("large config lost", e, len(stored.Value))
	}
	s, _, e := a.pluginStore.brushFlowRead(context.Background(), a.Store.DB, in.InstanceID, in.Generation)
	if e != nil || !bytes.Equal(s.Config, stored.Value) {
		t.Fatal("applied config proof", e)
	}
	a.pluginBrushFlow.owners = nil
	reload, e := a.pluginStore.EnqueueRun(context.Background(), in.InstanceID, "reload", nil)
	if e != nil {
		t.Fatal("cold queue", e)
	}
	reload, e = a.pluginStore.claimRunForInstance(context.Background(), in.InstanceID)
	if e != nil {
		t.Fatal("cold claim", e)
	}
	brushFinish(t, a, reload)
	cookie := setupTestAdmin(t, a)
	current, e := a.pluginStore.Config(context.Background(), in.InstanceID)
	if e != nil {
		t.Fatal(e)
	}
	body := fmt.Sprintf(`{"revision":%d,"config":%s}`, current.Revision, current.Value)
	response := callTest(t, a.Handler(), "PUT", "/api/plugins/"+in.InstanceID+"/config", body, cookie, "")
	if response.Code != 200 {
		t.Fatal("original large config HTTP save", response.Code)
	}
	savedRun, e := a.pluginStore.claimRunForInstance(context.Background(), in.InstanceID)
	if e != nil {
		t.Fatal(e)
	}
	brushFinish(t, a, savedRun)
	var sealed []byte
	if e = a.Store.DB.QueryRow(`SELECT sealed FROM plugin_brush_flow_state WHERE instance_id=? AND generation=?`, in.InstanceID, in.Generation).Scan(&sealed); e != nil {
		t.Fatal(e)
	}
	raw, e := a.pluginStore.unseal("run_result", in.InstanceID, brushFlowKey(in.Generation), sealed)
	var descriptor brushFlowDescriptor
	if e != nil || json.Unmarshal(raw, &descriptor) != nil || descriptor.ConfigRoot == "" || len(raw) > pluginValueLimit {
		t.Fatal("large applied config was not authenticated in pages", e, len(raw))
	}
	if _, e = a.Store.DB.Exec(`DELETE FROM plugin_brush_flow_ledger_pages WHERE collection='applied-config' AND id=?`, descriptor.ConfigRoot); e != nil {
		t.Fatal(e)
	}
	a.pluginBrushFlow.owners = nil
	if _, e = a.pluginStore.EnqueueRun(context.Background(), in.InstanceID, "reload", nil); e == nil {
		t.Fatal("missing config page authorized cold source reload")
	}
}

func TestBrushFlowOriginalManyServicesUseActualSDK(t *testing.T) {
	for _, count := range []int{501, 5001} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			a := newTestApp(t)
			useBuiltInPluginProcess(t, a)
			cfg := brushCapacityConfig(t, a, count)
			// This is the original schema-v2 path: complete a real initial SDK
			// migration, then save the user's task list. No fake KV descriptor or
			// out-of-band task_data_schema_version bypass is inserted.
			in, initial := brushInstall(t, a, bf.DefaultConfig())
			brushFinish(t, a, initial)
			stored, e := a.pluginStore.Config(context.Background(), in.InstanceID)
			if e != nil {
				t.Fatal(e)
			}
			_, _, e = a.pluginStore.SaveConfigAndQueue(context.Background(), in.InstanceID, stored.Revision, cfg)
			if e != nil {
				t.Fatal("source-valid config save", e)
			}
			run, e := a.pluginStore.claimRunForInstance(context.Background(), in.InstanceID)
			if e != nil {
				t.Fatal(e)
			}
			brushFinish(t, a, run)
			var active int
			e = a.Store.DB.QueryRow(`SELECT COUNT(*) FROM plugin_schedules WHERE instance_id=? AND active=1`, in.InstanceID).Scan(&active)
			if e != nil || active != count*2 {
				t.Fatal("source schedules truncated", e, active, count*2)
			}
		})
	}
}

func TestBrushFlowLargeSnapshotsKeepNativeScopeAndOtherPluginLimits(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	value := json.RawMessage(`{"owned":"` + strings.Repeat("x", pluginValueLimit) + `"}`)
	for _, owner := range []struct {
		plugin, version string
		allowed         bool
	}{{bf.PluginID, bf.Version, true}, {bf.PluginID, "other", false}, {"SystemProcess", bf.Version, false}} {
		_, e := pluginSnapshotJSON(owner.plugin, owner.version, value, true)
		if (e == nil) != owner.allowed {
			t.Fatal("large snapshot policy", owner.plugin, owner.version, e)
		}
	}
	sealed, e := a.pluginStore.sealPluginSnapshot("config", "owned-brush", "main", bf.PluginID, bf.Version, value)
	if e != nil || !bytes.HasPrefix(sealed, []byte(brushFlowSnapshotPrefix)) {
		t.Fatal(e)
	}
	if got, e := a.pluginStore.unseal("config", "owned-brush", "main", sealed); e != nil || !bytes.Equal(got, value) {
		t.Fatal("native roundtrip", e)
	}
	for _, attack := range []struct {
		kind, instance, key string
		sealed              []byte
	}{{"run", "owned-brush", "main", sealed}, {"config", "other", "main", sealed}, {"config", "owned-brush", "other", sealed}, {"config", "owned-brush", "main", sealed[len(brushFlowSnapshotPrefix):]}} {
		if _, e = a.pluginStore.unseal(attack.kind, attack.instance, attack.key, attack.sealed); e == nil {
			t.Fatal("snapshot escaped AEAD scope")
		}
	}
	ordinary, e := a.pluginStore.seal("config", "owned-brush", "main", []byte(`{}`))
	if e != nil {
		t.Fatal(e)
	}
	if _, e = a.pluginStore.unseal("config", "owned-brush", "main", append([]byte(brushFlowSnapshotPrefix), ordinary...)); e == nil {
		t.Fatal("ordinary seal promoted by prepending marker")
	}
	uninstallInstall(t, a, "ordinary", builtin.SystemProcessManifest(), map[string]any{}, false)
	if _, e = a.pluginStore.SaveConfig(ctx, "ordinary", 1, value); e == nil {
		t.Fatal("other plugin default ceiling changed")
	}
	tx, e := a.Store.DB.BeginTx(ctx, nil)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = a.pluginStore.saveValueTx(ctx, tx, "ordinary", "config", "main", 1, value); e == nil {
		tx.Rollback()
		t.Fatal("in-transaction current owner did not retain ordinary ceiling")
	}
	tx.Rollback()
	response := plugins.HTTPResponse{Status: 200, Body: value}
	if response.Validate() == nil || validateBrushFlowHTTPResponse(response) != nil {
		t.Fatal("native body policy changed common response behavior")
	}
	for _, invalid := range []plugins.HTTPResponse{{Status: 199, Body: json.RawMessage(`{}`)}, {Status: 200, Body: json.RawMessage(`not-json`)}, {Status: 200, Body: json.RawMessage(`{}`), Headers: map[string]string{"Set-Cookie": "forbidden"}}} {
		if validateBrushFlowHTTPResponse(invalid) == nil {
			t.Fatal("native large response bypassed metadata/content validation")
		}
	}
}

func TestBrushFlowSourceTaskBodyBeyondGenericHTTPSizeUsesActualSDK(t *testing.T) {
	a := newTestApp(t)
	useBuiltInPluginProcess(t, a)
	cfg := brushCapacityConfig(t, a, 1)
	in, run := brushInstall(t, a, bf.DefaultConfig())
	brushFinish(t, a, run)
	cookie := setupTestAdmin(t, a)
	task := cfg["tasks"].([]any)[0].(bf.Row)
	delete(task, "id")
	task["save_path"] = "/owned/" + strings.Repeat("x", 600<<10)
	response := callTest(t, a.Handler(), "POST", "/api/plugins/"+in.InstanceID+"/api/tasks", string(mustCleanSeedJSON(task)), cookie, "")
	var result bf.Response
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &result) != nil || !result.Success {
		t.Fatal("original task API", response.Code)
	}
	stored, e := a.pluginStore.Config(context.Background(), in.InstanceID)
	if e != nil || !bytes.Contains(stored.Value, []byte(strings.Repeat("x", 600<<10))) {
		t.Fatal("source task field truncated", e)
	}
}

func TestBrushFlowCompletionKeepsCallerCancellationAndOnlyBoundsCleanup(t *testing.T) {
	type traceKey struct{}
	parent, cancel := context.WithCancel(context.WithValue(context.Background(), traceKey{}, "owned-trace"))
	active, release := brushFlowCompletionContext(parent)
	if _, bounded := active.Deadline(); bounded || active.Value(traceKey{}) != "owned-trace" {
		t.Fatal("normal durable completion acquired cleanup budget or lost source trace")
	}
	cancel()
	select {
	case <-active.Done():
	case <-time.After(time.Second):
		t.Fatal("normal completion detached caller cancellation")
	}
	release()
	cleanup, release := brushFlowCompletionContext(parent)
	defer release()
	deadline, bounded := cleanup.Deadline()
	if !bounded || time.Until(deadline) > 3*time.Second || cleanup.Err() != nil || cleanup.Value(traceKey{}) != "owned-trace" {
		t.Fatal("canceled source cleanup lost its existing bounded detached semantics")
	}
}
