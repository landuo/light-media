package app

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	ab "light-media/internal/plugins/autobackup"
)

func TestPluginCloneAutoBackupDoesNotInheritOneShotExecution(t *testing.T) {
	pluginCloneAutoBackupIntent(t, false)
}
func TestPluginCloneAutoBackupFreshExplicitOneShotStillSchedules(t *testing.T) {
	pluginCloneAutoBackupIntent(t, true)
}

func pluginCloneAutoBackupIntent(t *testing.T, explicit bool) {
	a := newTestApp(t)
	useBuiltInPluginProcess(t, a)
	cookie := setupTestAdmin(t, a)
	ctx := context.Background()
	p := a.pluginStore
	installCloneSource(t, p, ab.Manifest())
	config := map[string]any{"enabled": false, "onlyonce": true, "webdav_enabled": false, "cnt": 0, "notify": false, "cron": "", "custom_false": false, "custom_zero": 0, "custom_empty": "", "custom_list": []any{}, "custom_object": map[string]any{"onlyonce": true}, "unknown_onlyonce": true}
	source, err := p.SaveConfig(ctx, "AutoBackup", 0, config)
	if err != nil {
		t.Fatal(err)
	}
	clone, err := p.Clone(ctx, "AutoBackup", PluginCloneRequest{Suffix: "IntentOwned"})
	if err != nil {
		t.Fatal(err)
	}
	sourceRows := reloadParityRows(t, a, "AutoBackup")
	copied, err := p.Config(ctx, clone.InstanceID)
	if err != nil {
		t.Fatal(err)
	}
	var got, want map[string]any
	if json.Unmarshal(copied.Value, &got) != nil || json.Unmarshal(source.Value, &want) != nil {
		t.Fatal("decode cloned/source configuration")
	}
	if got["onlyonce"] != false || got["enabled"] != false {
		t.Fatalf("clone retained source execution intent: %v", got["onlyonce"])
	}
	for _, key := range []string{"cnt", "notify", "cron", "custom_false", "custom_zero", "custom_empty", "custom_list", "custom_object", "unknown_onlyonce"} {
		actual, exists := got[key]
		if !exists || !reflect.DeepEqual(actual, want[key]) {
			t.Fatalf("ordinary/unknown value changed: %s", key)
		}
	}
	beforeForm := reloadParityRows(t, a, clone.InstanceID)
	form := callTest(t, a.Handler(), "GET", "/api/plugins/"+clone.InstanceID+"/form", "", cookie, "")
	if form.Code != 200 {
		t.Fatalf("clone Form: %d %s", form.Code, form.Body.String())
	}
	if !reflect.DeepEqual(beforeForm, reloadParityRows(t, a, clone.InstanceID)) {
		t.Fatal("Form/read queued work or changed cloned config")
	}
	response := callTest(t, a.Handler(), "POST", "/api/plugins/"+clone.InstanceID+"/reload", `{}`, cookie, "")
	if response.Code != 200 {
		t.Fatalf("clone reload: %d %s", response.Code, response.Body.String())
	}
	run := reloadParityResponseRun(t, response.Body.Bytes())
	reloadParityComplete(t, a, clone.InstanceID, run.ID, "reload")
	var active int
	if err = a.Store.DB.QueryRow(`SELECT COUNT(*) FROM plugin_schedules WHERE instance_id=? AND active=1`, clone.InstanceID).Scan(&active); err != nil || active != 0 {
		t.Fatalf("copied one-shot scheduled backup: %d %v", active, err)
	}
	if explicit {
		instant := time.Date(2026, 10, 23, 14, 0, 0, 0, time.UTC)
		a.pluginAutoBackup.now = func() time.Time { return instant }
		current, err := p.Config(ctx, clone.InstanceID)
		if err != nil {
			t.Fatal(err)
		}
		_, queued, err := p.SaveConfigAndQueue(ctx, clone.InstanceID, current.Revision, map[string]any{"enabled": false, "onlyonce": true, "webdav_enabled": false})
		if err != nil {
			t.Fatal(err)
		}
		reloadParityComplete(t, a, clone.InstanceID, queued.ID, "init")
		saved, err := p.Config(ctx, clone.InstanceID)
		if err != nil {
			t.Fatal(err)
		}
		var final map[string]any
		if json.Unmarshal(saved.Value, &final) != nil || final["onlyonce"] != false || final["enabled"] != false {
			t.Fatal("fresh explicit one-shot not consumed with business disabled")
		}
		var count int
		var due string
		if err = a.Store.DB.QueryRow(`SELECT COUNT(*) FROM plugin_schedules WHERE instance_id=? AND service_id='AutoBackup.once' AND active=1`, clone.InstanceID).Scan(&count); err != nil || count != 1 {
			t.Fatalf("explicit once scheduling count: %d %v", count, err)
		}
		if err = a.Store.DB.QueryRow(`SELECT next_due FROM plugin_schedules WHERE instance_id=? AND service_id='AutoBackup.once' AND active=1`, clone.InstanceID).Scan(&due); err != nil {
			t.Fatal(err)
		}
		at, err := time.Parse(time.RFC3339Nano, due)
		if err != nil || !at.Equal(instant.Add(3*time.Second)) {
			t.Fatalf("original +3s schedule changed: %s %v", due, err)
		}
	}
	after, err := p.Config(ctx, "AutoBackup")
	if err != nil || after.Revision != source.Revision || !bytes.Equal(after.Value, source.Value) || !reflect.DeepEqual(sourceRows, reloadParityRows(t, a, "AutoBackup")) {
		t.Fatalf("source was mutated: %v", err)
	}
	var operations int
	if err = a.Store.DB.QueryRow(`SELECT COUNT(*) FROM plugin_runs WHERE instance_id=? AND action NOT IN ('init','reload')`, clone.InstanceID).Scan(&operations); err != nil || operations != 0 {
		t.Fatalf("unexpected backup execution: %d %v", operations, err)
	}
	for _, id := range []string{"AutoBackup", clone.InstanceID} {
		files, err := filepath.Glob(filepath.Join(a.Dir, "plugin-private", id, "backups", "bk*.zip"))
		if err != nil || len(files) != 0 {
			t.Fatalf("initialization created archive: %d %v", len(files), err)
		}
	}
}
