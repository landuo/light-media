package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"light-media/internal/plugins"
	"light-media/internal/plugins/builtin"
	"light-media/internal/plugins/schedule"
)

func TestPublicPluginServiceExecutesActualSDKAndPreservesAutomaticTime(t *testing.T) {
	a := newTestApp(t)
	useBuiltInPluginProcess(t, a)
	cookie := setupTestAdmin(t, a)
	ctx := context.Background()
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		fmt.Fprint(w, "OwnedOld => OwnedNew\n")
	}))
	defer server.Close()
	if w := callTest(t, a.Handler(), "POST", "/api/plugins/install", `{"plugin_id":"RemoteIdentifiers"}`, cookie, ""); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	identifiersExecuteNext(t, a)
	identifiersConfigure(t, a, cookie, 1, builtin.RemoteIdentifiersConfiguration{Enable: true, Flitter: true, Cron: "0 0 * * *", FileURLs: server.URL + "/words?token=owned-service-secret"})
	identifiersExecuteNext(t, a)
	var before string
	if err := a.Store.DB.QueryRow(`SELECT next_due FROM plugin_schedules WHERE service_id='RemoteIdentifiers'`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	listed := callTest(t, a.Handler(), "GET", "/api/plugins/services", "", cookie, "")
	var projection struct {
		Data []PluginServiceInfo `json:"data"`
	}
	if listed.Code != 200 || json.Unmarshal(listed.Body.Bytes(), &projection) != nil || len(projection.Data) != 1 {
		t.Fatal("public service absent", listed.Code, listed.Body.String())
	}
	item := projection.Data[0]
	if item.Provider != "共享识别词" || item.Name != "获取远端识别词" || item.Status != "等待" || !item.CanRun || strings.Contains(listed.Body.String(), "owned-service-secret") || strings.Contains(listed.Body.String(), "handler") {
		t.Fatal("wrong or private public projection", listed.Body.String())
	}
	body := `{"instance_id":"RemoteIdentifiers","service_id":"RemoteIdentifiers"}`
	w := callTest(t, a.Handler(), "POST", "/api/plugins/services/run", body, cookie, "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"accepted":true`) || !strings.Contains(w.Body.String(), `"state":"pending"`) || requests.Load() != 0 {
		t.Fatal("queue acceptance confused with execution", w.Code, w.Body.String(), requests.Load())
	}
	if w := callTest(t, a.Handler(), "POST", "/api/plugins/services/run", body, cookie, ""); w.Code != 400 || !strings.Contains(w.Body.String(), "PLUGIN_SERVICE_BUSY") {
		t.Fatal("duplicate queued execution allowed", w.Code, w.Body.String())
	}
	queued, err := a.pluginStore.publicPluginServices(ctx, time.Now())
	if err != nil || len(queued) != 1 || queued[0].CanRun {
		t.Fatal("queue not projected", queued, err)
	}
	run := identifiersExecuteNext(t, a)
	_, words, err := a.recognitionWords(ctx)
	if err != nil || requests.Load() != 1 || len(words) != 2 || words[1] != "OwnedOld => OwnedNew" || run.Action != "service" {
		t.Fatal("public service did not perform actual SDK business", requests.Load(), words, run.Action, run.State, err)
	}
	var after string
	a.Store.DB.QueryRow(`SELECT next_due FROM plugin_schedules WHERE service_id='RemoteIdentifiers'`).Scan(&after)
	if before != after {
		t.Fatal("manual command moved automatic schedule", before, after)
	}
	compat := callTest(t, a.Handler(), "GET", "/api/v1/dashboard/schedule", "", cookie, "")
	var original []map[string]any
	if compat.Code != 200 || json.Unmarshal(compat.Body.Bytes(), &original) != nil || len(original) != 1 || len(original[0]) != 5 || original[0]["id"] != item.ID {
		t.Fatal("original schedule API mismatch", compat.Code, compat.Body.String())
	}
	if w := callTest(t, a.Handler(), "GET", "/api/v1/system/runscheduler?jobid="+item.ID, "", cookie, ""); w.Code != 200 || strings.TrimSpace(w.Body.String()) != `{"success":true}` {
		t.Fatal("original executor not connected", w.Code, w.Body.String())
	}
	identifiersExecuteNext(t, a)
	if requests.Load() != 2 {
		t.Fatal("compat executor skipped business", requests.Load())
	}
}

func TestPublicPluginServicesAuthAndCrossSiteGETGuard(t *testing.T) {
	a := newTestApp(t)
	h := a.Handler()
	cookie := setupTestAdmin(t, a)
	for _, path := range []string{"/api/plugins/services", "/api/v1/dashboard/schedule", "/api/v1/system/runscheduler?jobid=anything"} {
		if w := callTest(t, h, "GET", path, "", nil, ""); w.Code != 401 {
			t.Fatal("unauthenticated service API", path, w.Code)
		}
	}
	if w := callTest(t, h, "POST", "/api/plugins/services/run", `{}`, cookie, "http://foreign.invalid"); w.Code != 403 {
		t.Fatal("foreign POST accepted", w.Code)
	}
	for _, header := range []string{"Origin", "Sec-Fetch-Site"} {
		r := httptest.NewRequest("GET", "/api/v1/system/runscheduler?jobid=anything", nil)
		r.AddCookie(cookie)
		if header == "Origin" {
			r.Header.Set(header, "http://foreign.invalid")
		} else {
			r.Header.Set(header, "cross-site")
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 403 {
			t.Fatal("cross-site GET executor accepted", header, w.Code, w.Body.String())
		}
	}
	if w := callTest(t, h, "GET", "/api/v1/system/runscheduler?jobid=missing", "", cookie, ""); w.Code != 400 || !strings.Contains(w.Body.String(), "PLUGIN_SERVICE_NOT_FOUND") {
		t.Fatal("arbitrary command admitted", w.Code, w.Body.String())
	}
}

func TestPublicPluginServicesAmbiguousOriginalIDCannotSelectAnotherInstance(t *testing.T) {
	a := newTestApp(t)
	cookie := setupTestAdmin(t, a)
	ctx := context.Background()
	at := time.Now()
	manifest := plugins.Manifest{SchemaVersion: 1, PluginID: "SyntheticPlugin", Name: "owned service", Version: "1.2.3", Runtime: "go-executable", Entry: "plugin", HostAPIMin: 1, HostAPIMax: 1, Methods: []string{"init", "stop"}}
	raw, _ := json.Marshal(manifest)
	for _, pair := range [][2]string{{"foo", "Bar_Check"}, {"foo_Bar", "Check"}} {
		if err := a.pluginStore.Install(ctx, PluginInstallation{InstanceID: pair[0], PluginID: manifest.PluginID, Version: manifest.Version, ManifestSchemaVersion: 1, Manifest: raw}); err != nil {
			t.Fatal(err)
		}
		if _, _, err := a.pluginStore.SaveConfigAndQueue(ctx, pair[0], 0, map[string]any{"enabled": true}); err != nil {
			t.Fatal(err)
		}
		run, err := a.pluginStore.ClaimRun(ctx)
		if err != nil {
			t.Fatal(err)
		}
		definition := pluginScheduleDefinition{ID: pair[1], Name: "owned collision", Handler: "check", Trigger: schedule.Spec{Kind: "interval", Timezone: "UTC", IntervalMS: 30000}}
		if err := a.pluginStore.configurePluginSchedules(ctx, run, "services", []pluginScheduleDefinition{definition}, at); err != nil {
			t.Fatal(err)
		}
		if err := a.pluginStore.FinishRun(ctx, run.ID, "succeeded", "", nil); err != nil {
			t.Fatal(err)
		}
	}
	w := callTest(t, a.Handler(), "GET", "/api/v1/system/runscheduler?jobid=foo_Bar_Check", "", cookie, "")
	if w.Code != 400 || !strings.Contains(w.Body.String(), "PLUGIN_SERVICE_AMBIGUOUS") {
		t.Fatal("guessed a split for ambiguous public ID", w.Code, w.Body.String())
	}
	var count int
	a.Store.DB.QueryRow(`SELECT COUNT(*) FROM plugin_schedule_occurrences`).Scan(&count)
	if count != 0 {
		t.Fatal("ambiguous compatibility request queued work", count)
	}
	// The native UI sends both original coordinates; no split or coincidence.
	w = callTest(t, a.Handler(), "POST", "/api/plugins/services/run", `{"instance_id":"foo_Bar","service_id":"Check"}`, cookie, "")
	if w.Code != 200 {
		t.Fatal("explicit native coordinates rejected", w.Code, w.Body.String())
	}
	var instance, service string
	if err := a.Store.DB.QueryRow(`SELECT instance_id,service_id FROM plugin_schedule_occurrences`).Scan(&instance, &service); err != nil || instance != "foo_Bar" || service != "Check" {
		t.Fatal("native coordinates changed", instance, service, err)
	}
}

func TestPublicPluginServicesConsumedOnceAndUncertainRemainVisible(t *testing.T) {
	ctx := context.Background()
	at := time.Now().UTC()
	a, _, _ := pluginScheduleFixture(t, at, schedule.Spec{Kind: "date", Timezone: "Asia/Shanghai", RunAt: schedule.FormatTime(at.Add(time.Second))}, -1)
	publicServiceValidTimerManifest(t, a)
	if n, err := a.pluginStore.enqueueDuePluginSchedules(ctx, at.Add(time.Second)); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	items, err := a.pluginStore.publicPluginServices(ctx, at)
	if err != nil || len(items) != 1 || items[0].CanRun {
		t.Fatal("consumed queued date hidden", items, err)
	}
	run, err := a.pluginStore.ClaimRun(ctx)
	if err != nil {
		t.Fatal(err)
	}
	items, err = a.pluginStore.publicPluginServices(ctx, at)
	if err != nil || len(items) != 1 || items[0].Status != "正在运行" {
		t.Fatal("running date hidden", items, err)
	}
	if _, err := a.Store.DB.Exec(`UPDATE plugin_runs SET state='reconcile_pending' WHERE id=?`, run.ID); err != nil {
		t.Fatal(err)
	}
	items, err = a.pluginStore.publicPluginServices(ctx, at)
	if err != nil || len(items) != 1 || items[0].Status != "等待核对" || items[0].CanRun {
		t.Fatal("uncertainty advertised as executable", items, err)
	}
	if _, err := a.Store.DB.Exec(`UPDATE plugin_runs SET state='succeeded' WHERE id=?`, run.ID); err != nil {
		t.Fatal(err)
	}
	items, err = a.pluginStore.publicPluginServices(ctx, at)
	if err != nil || len(items) != 0 {
		t.Fatal("exhausted date still advertised", items, err)
	}
}

// The storage-only timer fixture deliberately has a partial manifest. Supply
// real protocol metadata for public API tests, which validate full packages.
func publicServiceValidTimerManifest(t *testing.T, a *App) {
	t.Helper()
	m := plugins.Manifest{SchemaVersion: 1, PluginID: "SyntheticPlugin", Name: "owned service", Version: "1.2.3", Runtime: "go-executable", Entry: "plugin", HostAPIMin: 1, HostAPIMax: 1, Methods: []string{"init", "stop"}}
	raw, err := json.Marshal(m)
	if err != nil || m.Validate() != nil {
		t.Fatal("invalid public fixture manifest", err)
	}
	sealed, err := a.pluginStore.seal("manifest", "timer", "main", raw)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Store.DB.Exec(`UPDATE plugin_installations SET manifest=? WHERE instance_id='timer'`, sealed); err != nil {
		t.Fatal(err)
	}
}

func TestPublicPluginServicesExcludeStaleDisabledAndTamper(t *testing.T) {
	ctx := context.Background()
	at := time.Now().UTC()
	for _, change := range []string{"UPDATE plugin_installations SET desired_enabled=0", "UPDATE plugin_installations SET installed=0", "UPDATE plugin_installations SET generation=generation+1", "UPDATE plugin_values SET revision=revision+1 WHERE kind='config'", "UPDATE plugin_schedules SET active=0"} {
		t.Run(change, func(t *testing.T) {
			a, _, definition := pluginScheduleFixture(t, at, schedule.Spec{Kind: "interval", Timezone: "UTC", IntervalMS: 30000}, -1)
			if _, err := a.Store.DB.Exec(change); err != nil {
				t.Fatal(err)
			}
			items, err := a.pluginStore.publicPluginServices(ctx, at)
			if err != nil || len(items) != 0 {
				t.Fatal("stale declaration visible", items, err)
			}
			if _, err := a.pluginStore.enqueueManualPluginService(ctx, "timer", definition.ID, at); err == nil {
				t.Fatal("stale declaration executable")
			}
		})
	}
	a, _, _ := pluginScheduleFixture(t, at, schedule.Spec{Kind: "interval", Timezone: "UTC", IntervalMS: 30000}, -1)
	if _, err := a.Store.DB.Exec(`UPDATE plugin_schedules SET definition=x'010203'`); err != nil {
		t.Fatal(err)
	}
	if _, err := a.pluginStore.publicPluginServices(ctx, at); err == nil {
		t.Fatal("damaged encrypted declaration projected")
	}
}

func TestPluginServiceTimeDifferenceOriginalUnits(t *testing.T) {
	at := time.Now()
	for _, sample := range []struct {
		delta time.Duration
		want  string
	}{
		{-time.Second, ""}, {500 * time.Millisecond, ""}, {time.Second, "1秒"}, {59 * time.Second, "59秒"}, {61 * time.Second, "1分钟"}, {3601 * time.Second, "1小时"}, {(86400 + 7200 + 180 + 59) * time.Second, "1天2小时3分钟"},
	} {
		if got := pluginServiceTimeDifference(at.Add(sample.delta), at); got != sample.want {
			t.Fatalf("%v: %q != %q", sample.delta, got, sample.want)
		}
	}
}
