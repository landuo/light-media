package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"sync/atomic"
	"testing"

	"light-media/internal/plugins"
	"light-media/internal/plugins/autosignin"
)

// This exercises the actual ordinary cmd/server worker and framework Clone API,
// not two hand-installed names, imported receipts, a fake worker or a browser.
// The only business endpoint is this test's synthetic loopback HTTP server.
func TestAutoSignInNativeFrameworkCloneRealServer(t *testing.T) {
	ctx := context.Background()
	a := newTestApp(t)
	worker := os.Getenv("AUTOSIGNIN_NATIVE_UI_WORKER")
	if worker == "" {
		transferServerBinary.once.Do(func() { transferServerBinary.path, transferServerBinary.err = buildTransferFixture(true) })
		if transferServerBinary.err != nil {
			t.Fatal(transferServerBinary.err)
		}
		worker = transferServerBinary.path
	} else {
		raw, err := os.ReadFile(worker)
		if !filepath.IsAbs(worker) || err != nil || fmt.Sprintf("%x", sha256.Sum256(raw)) != os.Getenv("AUTOSIGNIN_NATIVE_UI_WORKER_SHA256") {
			t.Fatal("explicit actual server worker identity", err)
		}
	}
	a.pluginCommand = func(id string) (plugins.Command, error) {
		if id != "AutoSignIn" {
			return plugins.Command{}, fmt.Errorf("owned fixture rejects other plugin")
		}
		return plugins.Command{Path: worker, Args: []string{"--plugin-worker", id}}, nil
	}
	admin := setupTestAdmin(t, a)
	var requests, wrongHeaders atomic.Int32
	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Header.Get("Cookie") != "sid=synthetic-native" {
			wrongHeaders.Add(1)
		}
		fmt.Fprint(w, `<a href="logout.php">退出</a><p>这是您的首次签到，本次签到获得10个魔力值。</p>`)
	}))
	defer site.Close()
	settings := a.Settings()
	settings.Sites = []SiteConfig{signinSitesNative(site.URL)}
	signinSitesPublish(t, a, settings)
	call := func(method, path, body string) {
		t.Helper()
		w := callTest(t, a.Handler(), method, path, body, admin, "")
		if w.Code != http.StatusOK {
			t.Fatalf("%s %s: %d %s", method, path, w.Code, w.Body.String())
		}
	}
	save := func(instance, cron string) {
		t.Helper()
		stored, err := a.pluginStore.Config(ctx, instance)
		if err != nil {
			t.Fatal(err)
		}
		config := map[string]any{"enabled": true, "onlyonce": false, "notify": false, "cron": cron, "sign_sites": []any{"all"}, "login_sites": []any{}, "queue_cnt": 1}
		body, err := json.Marshal(map[string]any{"revision": stored.Revision, "config": config})
		if err != nil {
			t.Fatal(err)
		}
		call("PUT", "/api/plugins/"+instance+"/config", string(body))
		autoSignInSDKExecute(t, a, instance)
	}
	command := func(instance string) {
		t.Helper()
		if _, err := a.queueAutoSignInCommand(ctx, instance, pluginAPIAuthority{Kind: "command"}, "飞书", "owned-user"); err != nil {
			t.Fatal(err)
		}
		autoSignInSDKExecute(t, a, instance)
	}
	call("POST", "/api/plugins/install", `{"plugin_id":"AutoSignIn"}`)
	autoSignInSDKExecute(t, a, "AutoSignIn")
	save("AutoSignIn", "0 0 * * *")
	if requests.Load() != 0 {
		t.Fatal("fresh install/configuration ran site business")
	}
	command("AutoSignIn")
	firstCount := requests.Load()
	first := autoSignInSDKState(t, a, "AutoSignIn")
	if firstCount == 0 || len(first.History) == 0 {
		t.Fatal("ordinary server did not perform source business")
	}
	// Verify the actual normal server -> App HTTP -> Dialog response, not the
	// in-memory SDK fixture which previously hid the host envelope discrepancy.
	pageResponse := callTest(t, a.Handler(), "GET", "/api/plugins/AutoSignIn/page", "", admin, "")
	var response struct {
		Data struct {
			RenderMode string            `json:"render_mode"`
			Page       []json.RawMessage `json:"page"`
		} `json:"data"`
	}
	if pageResponse.Code != http.StatusOK || json.Unmarshal(pageResponse.Body.Bytes(), &response) != nil || response.Data.RenderMode != "vuetify" || len(response.Data.Page) == 0 {
		t.Fatalf("actual Dialog page contract: %d %s", pageResponse.Code, pageResponse.Body.String())
	}
	if requests.Load() != firstCount {
		t.Fatal("read-only page performed site business")
	}
	sourceConfig, err := a.pluginStore.Config(ctx, "AutoSignIn")
	if err != nil {
		t.Fatal(err)
	}
	call("POST", "/api/plugins/AutoSignIn/clone", `{"suffix":"owned","name":"Owned native clone"}`)
	const clone = "AutoSignInowned"
	if _, err = a.pluginStore.ClaimRun(ctx); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("clone creation queued execution", err)
	}
	cloneInstall, err := a.pluginStore.Installation(ctx, clone)
	if err != nil || cloneInstall.DesiredEnabled {
		t.Fatal("fresh clone not staged", err)
	}
	call("GET", "/api/plugins/"+clone+"/form", "")
	t.Run("cold clone page is empty without initialization", func(t *testing.T) {
		before, err := a.pluginStore.Config(ctx, clone)
		if err != nil {
			t.Fatal(err)
		}
		page := callTest(t, a.Handler(), "GET", "/api/plugins/"+clone+"/page", "", admin, "")
		if page.Code != http.StatusOK || !bytes.Contains(page.Body.Bytes(), []byte("暂无签到数据")) {
			t.Errorf("cold clone actual page must render genuine empty history: %d %s", page.Code, page.Body.String())
		}
		after, err := a.pluginStore.Config(ctx, clone)
		if err != nil || after.Revision != before.Revision || !bytes.Equal(after.Value, before.Value) {
			t.Fatal("cold page changed config", err)
		}
		if requests.Load() != firstCount {
			t.Fatal("cold page sent business HTTP")
		}
		if _, err := a.pluginStore.ClaimRun(ctx); !errors.Is(err, sql.ErrNoRows) {
			t.Fatal("cold page enqueued work", err)
		}
		// These are deliberately forged host identities, not platform pause APIs.
		current, err := a.pluginStore.Installation(ctx, clone)
		if err != nil {
			t.Fatal(err)
		}
		for name, mutate := range map[string]func(*PluginInstallation){
			"generation":       func(i *PluginInstallation) { i.Generation++ },
			"enabled snapshot": func(i *PluginInstallation) { i.DesiredEnabled = !i.DesiredEnabled },
			"manifest":         func(i *PluginInstallation) { i.Manifest = json.RawMessage(`{}`) },
			"uninstalled":      func(i *PluginInstallation) { i.Installed = false },
		} {
			t.Run("reject forged "+name, func(t *testing.T) {
				forged := current
				mutate(&forged)
				if _, err := a.pluginAutoSignInHost(ctx, forged, nil, autosignin.SnapshotCapability, json.RawMessage(`{}`)); err == nil {
					t.Fatal("forged page identity accepted")
				}
			})
		}
		if _, err := a.pluginAutoSignInHost(ctx, current, &PluginRun{}, autosignin.SnapshotCapability, json.RawMessage(`{}`)); err == nil {
			t.Fatal("page accepted execution run")
		}
		if _, err := a.pluginAutoSignInHost(ctx, current, nil, autosignin.RunCapability, json.RawMessage(`{}`)); err == nil {
			t.Fatal("cold readonly admission granted execution")
		}
		t.Run("cold residual data is not hidden", func(t *testing.T) {
			if _, err := a.Store.DB.Exec(`INSERT INTO plugin_signin_meta(instance_id,generation,sealed,digest) VALUES(?,?,x'00',x'00')`, clone, current.Generation); err != nil {
				t.Fatal(err)
			}
			defer func() {
				if _, err := a.Store.DB.Exec(`DELETE FROM plugin_signin_meta WHERE instance_id=? AND generation=?`, clone, current.Generation); err != nil {
					t.Error(err)
				}
			}()
			if _, err := a.pluginAutoSignInHost(ctx, current, nil, autosignin.SnapshotCapability, json.RawMessage(`{}`)); err == nil {
				t.Fatal("cold residual data became an empty page")
			}
		})
		var owners int
		if err := a.Store.DB.QueryRow(`SELECT COUNT(*) FROM plugin_signin_owners WHERE instance_id=?`, clone).Scan(&owners); err != nil || owners != 0 {
			t.Fatal("cold page claimed owner", owners, err)
		}
	})
	if requests.Load() != firstCount {
		t.Fatal("clone creation/form caused HTTP")
	}
	save(clone, "0 1 * * *")
	cloneBefore := autoSignInSDKState(t, a, clone)
	if len(cloneBefore.History) != 0 || requests.Load() != firstCount {
		t.Fatal("clone inherited history or configuration triggered HTTP")
	}
	for id := range first.Runs {
		if _, copied := cloneBefore.Runs[id]; copied {
			t.Fatal("clone inherited a source execution receipt")
		}
	}
	command(clone)
	cloneAfter := autoSignInSDKState(t, a, clone)
	if requests.Load() <= firstCount || len(cloneAfter.History) == 0 || wrongHeaders.Load() != 0 {
		t.Fatal("actual clone HTTP/history/header contract failed")
	}
	sourceAfter := autoSignInSDKState(t, a, "AutoSignIn")
	if !reflect.DeepEqual(sourceAfter.History, first.History) {
		t.Fatal("clone command changed source history")
	}
	if !bytes.Equal(sourceAfter.Services, first.Services) || !reflect.DeepEqual(sourceAfter.Jobs, first.Jobs) {
		t.Fatal("clone configuration or execution changed source scheduling")
	}
	currentConfig, err := a.pluginStore.Config(ctx, "AutoSignIn")
	if err != nil || currentConfig.Revision != sourceConfig.Revision || !bytes.Equal(currentConfig.Value, sourceConfig.Value) {
		t.Fatal("clone configuration changed source", err)
	}
	if _, err = a.pluginStore.ClaimRun(ctx); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("unexpected residual execution", err)
	}
	t.Run("config disabled page retains authenticated history", func(t *testing.T) {
		stored, err := a.pluginStore.Config(ctx, clone)
		if err != nil {
			t.Fatal(err)
		}
		var config map[string]any
		if err := json.Unmarshal(stored.Value, &config); err != nil {
			t.Fatal(err)
		}
		config["enabled"] = false
		body, err := json.Marshal(map[string]any{"revision": stored.Revision, "config": config})
		if err != nil {
			t.Fatal(err)
		}
		call("PUT", "/api/plugins/"+clone+"/config", string(body))
		before, err := a.pluginStore.Config(ctx, clone)
		if err != nil {
			t.Fatal(err)
		}
		count := requests.Load()
		page := callTest(t, a.Handler(), "GET", "/api/plugins/"+clone+"/page", "", admin, "")
		if page.Code != http.StatusOK || !bytes.Contains(page.Body.Bytes(), []byte("签到成功")) || !bytes.Contains(page.Body.Bytes(), []byte("登录成功")) || bytes.Contains(page.Body.Bytes(), []byte("暂无签到数据")) {
			t.Errorf("paused actual page lost authenticated history: %d %s", page.Code, page.Body.String())
		}
		after, err := a.pluginStore.Config(ctx, clone)
		if err != nil || after.Revision != before.Revision || !bytes.Equal(after.Value, before.Value) || requests.Load() != count {
			t.Fatal("paused page mutated config or sent HTTP", err)
		}
		installation, err := a.pluginStore.Installation(ctx, clone)
		if err != nil || !installation.Installed {
			t.Fatal("page changed installed identity", err)
		}
		var finalConfig map[string]any
		if json.Unmarshal(after.Value, &finalConfig) != nil || finalConfig["enabled"] != false {
			t.Fatal("page enabled paused config")
		}
		// Fault injection is not represented as a supported user pause operation.
		for _, damage := range []string{"corrupt owner", "missing owner", "corrupt config"} {
			t.Run(damage, func(t *testing.T) {
				var sealed []byte
				table, column := "plugin_signin_owners", "instance_id"
				query := `SELECT sealed FROM plugin_signin_owners WHERE instance_id=?`
				if damage == "corrupt config" {
					table = "plugin_values"
					query = `SELECT sealed FROM plugin_values WHERE instance_id=? AND kind='config' AND item_key='main'`
				}
				if err := a.Store.DB.QueryRow(query, clone).Scan(&sealed); err != nil {
					t.Fatal(err)
				}
				where := column + "=?"
				if table == "plugin_values" {
					where += " AND kind='config' AND item_key='main'"
				}
				if damage == "missing owner" {
					if _, err := a.Store.DB.Exec(`DELETE FROM plugin_signin_owners WHERE instance_id=?`, clone); err != nil {
						t.Fatal(err)
					}
					defer func() {
						if _, err := a.Store.DB.Exec(`INSERT INTO plugin_signin_owners(instance_id,generation,sealed) VALUES(?,?,?)`, clone, installation.Generation, sealed); err != nil {
							t.Error(err)
						}
					}()
				} else {
					if _, err := a.Store.DB.Exec("UPDATE "+table+" SET sealed=x'00' WHERE "+where, clone); err != nil {
						t.Fatal(err)
					}
					defer func() {
						if _, err := a.Store.DB.Exec("UPDATE "+table+" SET sealed=? WHERE "+where, sealed, clone); err != nil {
							t.Error(err)
						}
					}()
				}
				if _, err := a.pluginAutoSignInHost(ctx, installation, nil, autosignin.SnapshotCapability, json.RawMessage(`{}`)); err == nil {
					t.Fatal("damaged started state produced a page")
				}
			})
		}

	})
}
