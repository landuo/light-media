package app

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sort"
	"sync/atomic"
	"testing"

	"light-media/internal/plugins/builtin"
)

func reloadParityRows(t *testing.T, a *App, instance string) map[string][]string {
	t.Helper()
	out := map[string][]string{}
	for _, table := range []string{"plugin_installations", "plugin_values", "plugin_runs", "plugin_schedules", "plugin_event_subscriptions"} {
		rows, err := a.Store.DB.Query(`SELECT * FROM `+table+` WHERE instance_id=?`, instance)
		if err != nil {
			t.Fatal(err)
		}
		cols, err := rows.Columns()
		if err != nil {
			rows.Close()
			t.Fatal(err)
		}
		for rows.Next() {
			values := make([]any, len(cols))
			ptrs := make([]any, len(cols))
			for i := range values {
				ptrs[i] = &values[i]
			}
			if err = rows.Scan(ptrs...); err != nil {
				rows.Close()
				t.Fatal(err)
			}
			raw, err := json.Marshal(values)
			if err != nil {
				rows.Close()
				t.Fatal(err)
			}
			out[table] = append(out[table], string(raw))
		}
		if err = rows.Err(); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		rows.Close()
		sort.Strings(out[table])
	}
	return out
}
func reloadParityComplete(t *testing.T, a *App, id, runID, action string) PluginRun {
	t.Helper()
	ctx := context.Background()
	queued, err := a.pluginStore.Run(ctx, runID)
	if err != nil || queued.InstanceID != id || queued.Action != action || queued.State != "pending" {
		t.Fatalf("wrong queued run: %+v %v", queued, err)
	}
	run, err := a.pluginStore.claimRunForInstance(ctx, id)
	if err != nil || run.ID != runID {
		t.Fatalf("wrong claimed run: %+v %v", run, err)
	}
	a.executePluginRun(ctx, run)
	done, err := a.pluginStore.Run(ctx, runID)
	if err != nil || done.InstanceID != id || done.Action != action || done.State != "succeeded" {
		t.Fatalf("real compiled completion: %+v %v", done, err)
	}
	return done
}
func reloadParityResponseRun(t *testing.T, raw []byte) PluginRun {
	t.Helper()
	var out struct {
		Data struct {
			Run PluginRun `json:"run"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &out); err != nil || out.Data.Run.ID == "" {
		t.Fatalf("actual management run envelope: %s %v", raw, err)
	}
	return out.Data.Run
}

// Original core reload always stops/starts a loaded clone even when its copied
// business flags are false. A preceding empty save is not part of that contract.
func TestPluginOriginalDisabledCloneReloadDoesNotRequireEmptySave(t *testing.T) {
	a := newTestApp(t)
	useBuiltInPluginProcess(t, a)
	cookie := setupTestAdmin(t, a)
	ctx := context.Background()
	p := a.pluginStore
	m := builtin.MediaServerRefreshManifest()
	init := uninstallInstall(t, a, m.PluginID, m, map[string]any{"enabled": true, "delay": 0, "mediaservers": []string{"fnos"}}, true)
	a.executePluginRun(ctx, init)
	if done, err := p.Run(ctx, init.ID); err != nil || done.State != "succeeded" {
		t.Fatalf("source init: %+v %v", done, err)
	}
	clone, err := p.Clone(ctx, m.PluginID, PluginCloneRequest{Suffix: "DirectReload"})
	if err != nil {
		t.Fatal(err)
	}
	copied, err := p.Config(ctx, clone.InstanceID)
	if err != nil {
		t.Fatal(err)
	}
	sourceBefore := reloadParityRows(t, a, m.PluginID)
	response := callTest(t, a.Handler(), "POST", "/api/plugins/"+clone.InstanceID+"/reload", `{}`, cookie, "")
	if response.Code != 200 {
		t.Fatalf("original disabled-clone reload must queue without empty save: %d %s", response.Code, response.Body.String())
	}
	queued := reloadParityResponseRun(t, response.Body.Bytes())
	reloadParityComplete(t, a, clone.InstanceID, queued.ID, "reload")
	after, err := p.Config(ctx, clone.InstanceID)
	if err != nil || !bytes.Equal(copied.Value, after.Value) || copied.Revision != after.Revision {
		t.Fatalf("reload implicitly saved clone config: %v", err)
	}
	var active int
	if err = a.Store.DB.QueryRow(`SELECT COUNT(*) FROM plugin_event_subscriptions WHERE instance_id=? AND active=1`, clone.InstanceID).Scan(&active); err != nil || active != 0 {
		t.Fatalf("disabled clone enabled events: %d %v", active, err)
	}
	if got := reloadParityRows(t, a, m.PluginID); !reflect.DeepEqual(sourceBefore, got) {
		t.Fatal("clone reload changed source config/run/subscription/service")
	}
}

func TestPluginOriginalTwoInstanceSaveToggleReloadRegistrationIsolation(t *testing.T) {
	for _, kind := range []string{"event", "schedule"} {
		t.Run(kind, func(t *testing.T) {
			a := newTestApp(t)
			useBuiltInPluginProcess(t, a)
			cookie := setupTestAdmin(t, a)
			ctx := context.Background()
			p := a.pluginStore
			var external atomic.Int32
			trap := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				external.Add(1)
				http.Error(w, "unexpected fixture IO", 503)
			}))
			defer trap.Close()
			m := builtin.MediaServerRefreshManifest()
			table := "plugin_event_subscriptions"
			config := func(enabled bool) map[string]any {
				return map[string]any{"enabled": enabled, "delay": 0, "mediaservers": []string{"fnos"}}
			}
			if kind == "schedule" {
				m = builtin.RemoteIdentifiersManifest()
				// Existing owned synthetic runtime/network fixture, no source import or external access.
				identifiersSeedSource(t, a)
				table = "plugin_schedules"
				config = func(enabled bool) map[string]any {
					return map[string]any{"enable": enabled, "onlyonce": false, "flitter": false, "cron": "30 4 * * *", "file_urls": trap.URL + "/words"}
				}
			}
			init := uninstallInstall(t, a, m.PluginID, m, config(true), true)
			a.executePluginRun(ctx, init)
			if done, err := p.Run(ctx, init.ID); err != nil || done.State != "succeeded" {
				t.Fatalf("source init: %+v %v", done, err)
			}
			clone, err := p.Clone(ctx, m.PluginID, PluginCloneRequest{Suffix: "ToggleOwned"})
			if err != nil {
				t.Fatal(err)
			}
			sourceBefore := reloadParityRows(t, a, m.PluginID)
			count := func(id string) int {
				t.Helper()
				var n int
				if e := a.Store.DB.QueryRow(`SELECT COUNT(*) FROM `+table+` WHERE instance_id=? AND active=1`, id).Scan(&n); e != nil {
					t.Fatal(e)
				}
				return n
			}
			if count(m.PluginID) != 1 || count(clone.InstanceID) != 0 {
				t.Fatal("wrong starting source/clone registration")
			}
			for _, enabled := range []bool{true, false, true} {
				current, e := p.Config(ctx, clone.InstanceID)
				if e != nil {
					t.Fatal(e)
				}
				body, e := json.Marshal(map[string]any{"revision": current.Revision, "config": config(enabled)})
				if e != nil {
					t.Fatal(e)
				}
				response := callTest(t, a.Handler(), "PUT", "/api/plugins/"+clone.InstanceID+"/config", string(body), cookie, "")
				if response.Code != 200 {
					t.Fatalf("explicit save: %d %s", response.Code, response.Body.String())
				}
				queued := reloadParityResponseRun(t, response.Body.Bytes())
				reloadParityComplete(t, a, clone.InstanceID, queued.ID, "init")
				want := 0
				if enabled {
					want = 1
				}
				if n := count(clone.InstanceID); n != want {
					t.Fatalf("toggle registration enabled=%v got=%d", enabled, n)
				}
				saved, e := p.Config(ctx, clone.InstanceID)
				if e != nil {
					t.Fatal(e)
				}
				response = callTest(t, a.Handler(), "POST", "/api/plugins/"+clone.InstanceID+"/reload", `{}`, cookie, "")
				if response.Code != 200 {
					t.Fatalf("reload after explicit save: %d %s", response.Code, response.Body.String())
				}
				queued = reloadParityResponseRun(t, response.Body.Bytes())
				reloadParityComplete(t, a, clone.InstanceID, queued.ID, "reload")
				after, e := p.Config(ctx, clone.InstanceID)
				if e != nil || !bytes.Equal(saved.Value, after.Value) || saved.Revision != after.Revision {
					t.Fatalf("reload changed saved config: %v", e)
				}
				if n := count(clone.InstanceID); n != want {
					t.Fatalf("reload changed/duplicated registration enabled=%v got=%d", enabled, n)
				}
				if got := reloadParityRows(t, a, m.PluginID); !reflect.DeepEqual(sourceBefore, got) {
					t.Fatal("clone save/toggle/reload modified source rows")
				}
				form := callTest(t, a.Handler(), "GET", "/api/plugins/"+clone.InstanceID+"/form", "", cookie, "")
				if form.Code != 200 {
					t.Fatalf("business disabled lost Form: %d %s", form.Code, form.Body.String())
				}
			}
			if external.Load() != 0 {
				t.Fatalf("init/reload unexpectedly executed external action: %d", external.Load())
			}
		})
	}
}
