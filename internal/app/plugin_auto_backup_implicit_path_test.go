package app

import (
	"bytes"
	"context"
	"encoding/json"
	"light-media/internal/plugins"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPluginAutoBackupFreshInstallFormCloneImplicitArchiveIsolation(t *testing.T) {
	autoBackupFreshFormClone(t, false)
}
func TestAutoBackupActualServerFreshInstallFormCloneImplicitArchiveIsolation(t *testing.T) {
	autoBackupFreshFormClone(t, true)
}
func autoBackupFreshFormClone(t *testing.T, realServer bool) {
	a := newTestApp(t)
	useBuiltInPluginProcess(t, a)
	if realServer {
		transferServerBinary.once.Do(func() { transferServerBinary.path, transferServerBinary.err = buildTransferFixture(true) })
		if transferServerBinary.err != nil {
			t.Fatal(transferServerBinary.err)
		}
		a.pluginCommand = func(id string) (plugins.Command, error) {
			return plugins.Command{Path: transferServerBinary.path, Args: []string{"--plugin-worker", id}}, nil
		}
	}
	cookie := setupTestAdmin(t, a)
	w := callTest(t, a.Handler(), "POST", "/api/plugins/install", `{"plugin_id":"AutoBackup"}`, cookie, "")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	autoTestSuccess(t, a, autoTestClaim(t, a))
	ctx := context.Background()
	settings := a.Settings()
	settings.APIToken = "owned-autobackup-instance-synthetic-api"
	if err := a.saveSettings(ctx, settings); err != nil {
		t.Fatal(err)
	}
	// Save the actual displayed full model, just as the UI does. Never clear or
	// omit back_path in the test to manufacture an implicit default.
	saveForm := func(id string) {
		t.Helper()
		form := callTest(t, a.Handler(), "GET", "/api/plugins/"+id+"/form", "", cookie, "")
		var response struct {
			Data struct {
				Model    map[string]any `json:"model"`
				Revision int64          `json:"revision"`
			} `json:"data"`
		}
		if form.Code != 200 || json.Unmarshal(form.Body.Bytes(), &response) != nil {
			t.Fatal("form", form.Code, form.Body.String())
		}
		if value := response.Data.Model["back_path"]; value != "" {
			t.Errorf("derived default persisted as an explicit path for %s: %v", id, value)
		}
		root := filepath.Join(a.Dir, "plugin-private", id, "backups")
		if !strings.Contains(form.Body.String(), root) {
			t.Errorf("form lacks current instance default hint: %s", id)
		}
		response.Data.Model["cnt"] = 1
		raw, _ := json.Marshal(map[string]any{"revision": response.Data.Revision, "config": response.Data.Model})
		saved := callTest(t, a.Handler(), "PUT", "/api/plugins/"+id+"/config", string(raw), cookie, "")
		if saved.Code != 200 {
			t.Fatal("save actual form", saved.Code, saved.Body.String())
		}
		run, e := a.pluginStore.claimRunForInstance(ctx, id)
		if e != nil {
			t.Fatal(e)
		}
		autoTestSuccess(t, a, run)
	}
	saveForm("AutoBackup")
	w = callTest(t, a.Handler(), "POST", "/api/plugins/AutoBackup/clone", `{"suffix":"copy"}`, cookie, "")
	if w.Code != 200 {
		t.Fatal("official clone", w.Code, w.Body.String())
	}
	clone := "AutoBackupcopy"
	saveForm(clone)
	instant := time.Date(2026, 10, 23, 9, 0, 0, 0, time.UTC)
	a.pluginAutoBackup.now = func() time.Time { return instant }
	autoTestRun(t, a)
	source := autoTestFiles(t, a)
	if len(source) != 1 {
		t.Fatal(source)
	}
	original, e := os.ReadFile(source[0])
	if e != nil {
		t.Fatal(e)
	}
	if e = a.Store.Put(ctx, "owned-clone-backup", "marker", map[string]string{"value": "clone snapshot has distinct owned content"}); e != nil {
		t.Fatal(e)
	}
	runClone := func() {
		t.Helper()
		if _, e := a.pluginStore.EnqueueRun(ctx, clone, "run", nil); e != nil {
			t.Fatal(e)
		}
		r, e := a.pluginStore.claimRunForInstance(ctx, clone)
		if e != nil {
			t.Fatal(e)
		}
		autoTestSuccess(t, a, r)
	}
	runClone()
	root := filepath.Join(a.Dir, "plugin-private", clone, "backups")
	files, e := filepath.Glob(filepath.Join(root, "bk*.zip"))
	if e != nil || len(files) != 1 {
		t.Fatal("fresh-form clone archive missing", files, e)
	}
	content, e := os.ReadFile(files[0])
	if e != nil || bytes.Equal(content, original) {
		t.Fatal("clone archive must be distinct", e)
	}
	instant = instant.Add(time.Second)
	runClone()
	got, e := os.ReadFile(source[0])
	if e != nil || !bytes.Equal(got, original) {
		t.Fatal("clone write/retention changed source", e)
	}
	files, e = filepath.Glob(filepath.Join(root, "bk*.zip"))
	if e != nil || len(files) != 1 {
		t.Fatal("clone retention", files, e)
	}
	if realServer {
		autoBackupVerifyOwnedArchiveRestore(t, files[0])
	}
}

func TestPluginAutoBackupExplicitCurrentDefaultPathRemainsExplicit(t *testing.T) {
	a := newTestApp(t)
	cookie := autoTestInstall(t, a)
	ctx := context.Background()
	path := filepath.Join(a.Dir, "plugin-private", "AutoBackup", "backups")
	autoTestConfig(t, a, cookie, map[string]any{"back_path": path, "cnt": 2})
	w := callTest(t, a.Handler(), "POST", "/api/plugins/AutoBackup/clone", `{"suffix":"explicitdefault"}`, cookie, "")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	for _, id := range []string{"AutoBackup", "AutoBackupexplicitdefault"} {
		c, e := a.pluginStore.Config(ctx, id)
		if e != nil {
			t.Fatal(e)
		}
		var config map[string]any
		if json.Unmarshal(c.Value, &config) != nil || config["back_path"] != path {
			t.Fatal("explicit exact-default intent rewritten", id)
		}
		form := callTest(t, a.Handler(), "GET", "/api/plugins/"+id+"/form", "", cookie, "")
		var response struct {
			Data struct {
				Model map[string]any `json:"model"`
			} `json:"data"`
		}
		if form.Code != 200 || json.Unmarshal(form.Body.Bytes(), &response) != nil || response.Data.Model["back_path"] != path {
			t.Fatal("explicit form path rewritten", id, form.Code)
		}
	}
}
