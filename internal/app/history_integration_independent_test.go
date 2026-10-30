package app

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"light-media/internal/plugins"
	"light-media/internal/plugins/artifacts"
)

// 独立集成验收使用真实可安装 UI worker，不把 HTTP 200 或静态页面当成查询成功。
func installIndependentHistoryUI(t *testing.T, a *App, cookie *http.Cookie) {
	t.Helper()
	binary := os.Getenv("HISTORY_INDEPENDENT_UI_BINARY")
	if binary == "" {
		t.Fatal("HISTORY_INDEPENDENT_UI_BINARY required")
	}
	payload, err := os.ReadFile(binary)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := exec.Command(binary, "--manifest").Output()
	if err != nil {
		t.Fatal(err)
	}
	var manifest plugins.Manifest
	if err = json.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	if err = manifest.Validate(); err != nil {
		t.Fatal(err)
	}
	wantCapabilities := append(plugins.BaseMethods(), plugins.TransferHistoryBySourceCapability, plugins.TransferHistoryByDestinationCapability)
	if manifest.PluginID != "OwnedHistoryUI" || !reflect.DeepEqual(manifest.Capabilities, wantCapabilities) || len(manifest.APIs) != 2 {
		t.Fatal("unexpected UI authority")
	}
	for _, api := range manifest.APIs {
		if api.Auth != "bear" || !reflect.DeepEqual(api.Methods, []string{"POST"}) || api.Body != "json" || !api.BodyRequired {
			t.Fatal("unexpected UI API contract")
		}
	}
	var buffer bytes.Buffer
	z := zip.NewWriter(&buffer)
	header := &zip.FileHeader{Name: manifest.Entry, Method: zip.Deflate}
	header.SetMode(0700)
	w, err := z.CreateHeader(header)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = w.Write(payload); err != nil {
		t.Fatal(err)
	}
	if err = z.Close(); err != nil {
		t.Fatal(err)
	}
	programSHA, zipSHA := sha256.Sum256(payload), sha256.Sum256(buffer.Bytes())
	index := artifacts.Index{SchemaVersion: 1, PluginID: manifest.PluginID, CurrentVersion: manifest.Version, Releases: []artifacts.Release{{Version: manifest.Version, Manifest: manifest, Artifacts: []artifacts.Artifact{{Platform: runtime.GOOS + "-" + runtime.GOARCH, Format: "zip", SHA256: hex.EncodeToString(zipSHA[:]), ExecutableSHA256: hex.EncodeToString(programSHA[:]), Size: int64(buffer.Len()), HostContract: pluginResourcesSDKContract}}}}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/index":
			_ = json.NewEncoder(w).Encode(index)
		case "/artifact":
			_, _ = w.Write(buffer.Bytes())
		default:
			t.Error("unexpected UI artifact endpoint")
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	index.Releases[0].Artifacts[0].URL = server.URL + "/artifact"
	body, _ := json.Marshal(map[string]string{"plugin_id": manifest.PluginID, "repo_url": server.URL + "/index"})
	res := callTest(t, a.Handler(), "POST", "/api/plugins/install", string(body), cookie, "")
	assertOriginalRegistrySuccess(t, res.Code, res.Body.Bytes())
	a.wg.Wait()
	v, ok := a.pluginRunningInstances.Load(manifest.PluginID)
	if !ok || !v.(*originalPluginInstance).external || v.(*originalPluginInstance).hostContract != pluginResourcesSDKContract {
		t.Fatal("UI did not install an actual resources SDK worker")
	}
	t.Logf("actual UI worker SHA256=%x", programSHA)
}

// 同一真实宿主同时运行 S05 DAO、S06 查询和 clear_cache，验证权限、冷重启及不缓存历史事实。
func TestHistoryIntegrationIndependentLiveUIAndSharedHost(t *testing.T) {
	ctx := context.Background()
	a := newTestApp(t)
	cookie := setupTestAdmin(t, a)
	installIndependentHistoryUI(t, a, cookie)
	s05 := installSharedSubscriptionsFixture(t, a, cookie, sharedSubscriptionsAll)
	subscriptionID := "independent-history-coexistence"
	seedSharedSubscription(t, a, subscriptionID)
	_, subBefore := sharedSubscriptionReadRow(t, s05, subscriptionID)
	root := t.TempDir()
	source, target := filepath.Join(root, "source"), filepath.Join(root, "target")
	for _, path := range []string{source, target} {
		if err := os.Mkdir(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	a.config = Settings{AllowedRoots: []string{source}, Roots: []Root{{ID: "root", Path: target, Kind: "mixed"}}}
	identity := importFixture(t, a, "movie")
	identity.Title = "Independent UI <script> literal Ω"
	importCache(t, a, "media:movie:123", identity)
	path := filepath.Join(source, "Independent.UI.2024.mkv")
	content := []byte("independent UI native physical copy\n")
	importWrite(t, path, content)
	scrape := false
	plan, err := a.previewImport(ctx, ImportRequest{MediaType: "movie", TMDBID: 123, RootID: "root", Mode: "copy", Scraping: &scrape, Files: []ImportFileRequest{{Path: path}}})
	if err != nil {
		t.Fatal(err)
	}
	if a.originalTransferHasHandlers() {
		t.Fatal("ordinary UI enabled an organizer extension")
	}
	job, err := a.executeImport(ctx, plan.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	importWrite(t, plan.Files[0].Target, []byte("owned collision"))
	independentHistoryRunFailure(t, a, job)
	configure := func(app *App, id, path, storage string) {
		t.Helper()
		raw, _ := json.Marshal(map[string]any{"enabled": true, "path": path, "storage": storage, "view": "source", "preserve": "independent"})
		res := callTest(t, app.Handler(), "PUT", "/api/plugins/"+id, string(raw), cookie, "")
		assertOriginalRegistrySuccess(t, res.Code, res.Body.Bytes())
		app.wg.Wait()
	}
	configure(a, "OwnedHistoryUI", path, "local")
	server := httptest.NewServer(a.Handler())
	defer server.Close()
	observed := map[string]any{}
	request := func(appServer *httptest.Server, id, endpoint, method, body, authority string, status int) map[string]any {
		t.Helper()
		req, e := http.NewRequest(method, appServer.URL+"/api/v1/plugin/"+id+endpoint, strings.NewReader(body))
		if e != nil {
			t.Fatal(e)
		}
		req.Header.Set("Content-Type", "application/json")
		switch authority {
		case "cookie":
			req.AddCookie(cookie)
		case "bearer":
			req.Header.Set("Authorization", "Bearer "+cookie.Value)
		case "bad-bearer":
			req.Header.Set("Authorization", "Bearer invalid-owned-probe")
		case "cross-site":
			req.AddCookie(cookie)
			req.Header.Set("Sec-Fetch-Site", "cross-site")
			req.Header.Set("Origin", "https://owned-invalid.example")
		}
		resp, e := appServer.Client().Do(req)
		if e != nil {
			t.Fatal(e)
		}
		defer resp.Body.Close()
		raw, e := io.ReadAll(resp.Body)
		if e != nil {
			t.Fatal(e)
		}
		if resp.StatusCode != status {
			t.Fatalf("UI route %s %s got=%d want=%d body=%s", method, endpoint, resp.StatusCode, status, raw)
		}
		var out map[string]any
		if e = json.Unmarshal(raw, &out); e != nil {
			t.Fatal(e)
		}
		return out
	}
	configBefore, err := a.pluginStore.Config(ctx, "OwnedHistoryUI")
	if err != nil {
		t.Fatal(err)
	}
	for _, probe := range []struct {
		method, endpoint, body, auth string
		status                       int
	}{
		{"POST", "/query-destination", "{}", "", 401}, {"POST", "/query-destination", "{}", "bad-bearer", 403}, {"POST", "/query-destination", "{}", "cross-site", 403},
		{"GET", "/query-source", "", "cookie", 405}, {"POST", "/undeclared", "{}", "cookie", 404}, {"POST", "/query-source", "", "cookie", 422},
	} {
		request(server, "OwnedHistoryUI", probe.endpoint, probe.method, probe.body, probe.auth, probe.status)
	}
	configAfter, _ := a.pluginStore.Config(ctx, "OwnedHistoryUI")
	if !bytes.Equal(configBefore.Value, configAfter.Value) {
		t.Fatal("rejected HTTP request modified UI config")
	}
	failed := request(server, "OwnedHistoryUI", "/query-source", "POST", `{"path":"/forged","sql":"ignored","record":{"id":"forged"}}`, "cookie", 200)
	failedRow, ok := failed["record"].(map[string]any)
	if !ok || failedRow["status"] != false || failedRow["src"] != path || failed["instance_id"] != "OwnedHistoryUI" {
		t.Fatal("UI query substituted body facts or lost native failure")
	}
	observed["failed_query"] = failed
	cloneRes := callTest(t, a.Handler(), "POST", "/api/plugins/OwnedHistoryUI/clone", `{"suffix":"Independent"}`, cookie, "")
	assertOriginalRegistrySuccess(t, cloneRes.Code, cloneRes.Body.Bytes())
	a.wg.Wait()
	clone := "OwnedHistoryUIindependent"
	configure(a, clone, plan.Files[0].Target, "local")
	missingDest := request(server, clone, "/query-destination", "POST", "{}", "bearer", 200)
	if missingDest["record"] != nil {
		t.Fatal("failed native transfer fabricated a destination row")
	}
	if err = os.Rename(plan.Files[0].Target, plan.Files[0].Target+".collision"); err != nil {
		t.Fatal(err)
	}
	retry, err := a.executeImport(ctx, plan.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	reorgRunJob(t, a, retry)
	for _, filename := range []string{path, plan.Files[0].Target} {
		got, e := os.ReadFile(filename)
		if e != nil || !bytes.Equal(got, content) {
			t.Fatal("native physical bytes differ", e)
		}
	}
	// get_page itself must re-read SQLite without another query button or config save.
	pageRow := func(app *App, id string) map[string]any {
		t.Helper()
		res := callTest(t, app.Handler(), "GET", "/api/plugins/"+id+"/page", "", cookie, "")
		if res.Code != 200 || !strings.Contains(res.Body.String(), "plugin/"+id+"/query-source") || !strings.Contains(res.Body.String(), "plugin/"+id+"/query-destination") {
			t.Fatal("page does not address its current instance", res.Body.String())
		}
		var tree any
		if err := json.Unmarshal(res.Body.Bytes(), &tree); err != nil {
			t.Fatal(err)
		}
		var result map[string]any
		count := 0
		var visit func(any)
		visit = func(node any) {
			switch v := node.(type) {
			case []any:
				for _, child := range v {
					visit(child)
				}
			case map[string]any:
				if v["component"] == "pre" {
					text, ok := v["text"].(string)
					if !ok {
						t.Fatal("not text rendering")
					}
					if err := json.Unmarshal([]byte(text), &result); err != nil {
						t.Fatal(err)
					}
					count++
				}
				for _, child := range v {
					visit(child)
				}
			}
		}
		visit(tree)
		if count != 1 {
			t.Fatalf("history page has %d text records", count)
		}
		return result
	}
	fresh := pageRow(a, "OwnedHistoryUI")
	if fresh["status"] != true || fresh["id"] != failedRow["id"] || fresh["src"] != path || fresh["dest"] != plan.Files[0].Target {
		t.Fatal("UI page cached failure instead of current host record")
	}
	cloneFresh := pageRow(a, clone)
	if !reflect.DeepEqual(fresh, cloneFresh) {
		t.Fatal("clone page failed live destination query")
	}
	observed["fresh_page"] = fresh
	// UI queries do not mirror facts into plugin KV; only the view selector changes.
	for _, id := range []string{"OwnedHistoryUI", clone} {
		value, e := a.pluginStore.Config(ctx, id)
		if e != nil {
			t.Fatal(e)
		}
		var fields map[string]any
		if e = json.Unmarshal(value.Value, &fields); e != nil {
			t.Fatal(e)
		}
		if len(fields) != 5 || fields["preserve"] != "independent" {
			t.Fatal("UI altered unrelated configuration or stored history facts", fields)
		}
		var count int
		if e = a.Store.DB.QueryRow(`SELECT COUNT(*) FROM plugin_values WHERE instance_id=? AND kind='data'`, id).Scan(&count); e != nil || count != 0 {
			t.Fatal("history mirrored in plugin data", e, count)
		}
	}
	_, subAfter := sharedSubscriptionReadRow(t, s05, subscriptionID)
	if !reflect.DeepEqual(subBefore, subAfter) {
		t.Fatal("history/UI mutated S05 records")
	}
	if _, err = sharedSubscriptionsCall(t, s05, plugins.SharedSubscriptionsUpdate, plugins.SharedSubscriptionUpdateV1{ID: subscriptionID, Patch: json.RawMessage(`{"note":"coexisting real SDK"}`)}); err != nil {
		t.Fatal(err)
	}
	_, subUpdated := sharedSubscriptionReadRow(t, s05, subscriptionID)
	if string(subUpdated["note"]) != `"coexisting real SDK"` {
		t.Fatal("S05 dispatch lost after S06 integration")
	}
	clear := callTest(t, a.Handler(), "POST", "/api/v1/message/web", `{"text":"/clear_cache"}`, cookie, "")
	if clear.Code != 200 || !strings.Contains(clear.Body.String(), `"accepted":true`) {
		t.Fatal("clear ingress failed")
	}
	a.wg.Wait()
	var cacheRows int
	if err = a.Store.DB.QueryRow(`SELECT COUNT(*) FROM records WHERE kind='cache' AND id='media:movie:123'`).Scan(&cacheRows); err != nil || cacheRows != 0 {
		t.Fatal("native cache was not cleared")
	}
	if !reflect.DeepEqual(fresh, pageRow(a, "OwnedHistoryUI")) {
		t.Fatal("clear cache destroyed frozen history")
	}
	_, subAfter = sharedSubscriptionReadRow(t, s05, subscriptionID)
	if !reflect.DeepEqual(subUpdated, subAfter) {
		t.Fatal("clear/history modified subscription")
	}
	warm := request(server, clone, "/query-destination", "POST", "{}", "cookie", 200)
	if warm["pid"] == failed["pid"] {
		t.Fatal("clone reused source worker")
	}
	observed["warm_clone"] = warm
	if err = a.Close(); err != nil {
		t.Fatal(err)
	}
	cold, err := New(Options{DataDir: a.Dir})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cold.Close() })
	if err = cold.initializeOriginalPlugins(ctx); err != nil {
		t.Fatal(err)
	}
	cold.wg.Wait()
	coldServer := httptest.NewServer(cold.Handler())
	defer coldServer.Close()
	coldRead := request(coldServer, clone, "/query-destination", "POST", "{}", "cookie", 200)
	if coldRead["pid"] == warm["pid"] || !reflect.DeepEqual(coldRead["record"], fresh) {
		t.Fatal("cold process lost live history or reused PID")
	}
	if !reflect.DeepEqual(fresh, pageRow(cold, "OwnedHistoryUI")) || !reflect.DeepEqual(fresh, pageRow(cold, clone)) {
		t.Fatal("cold UI pages differ")
	}
	v, ok := cold.pluginRunningInstances.Load("OwnedSharedHost")
	if !ok {
		t.Fatal("cold S05 worker missing")
	}
	_, subCold := sharedSubscriptionReadRow(t, v.(*originalPluginInstance), subscriptionID)
	if !reflect.DeepEqual(subCold, subUpdated) {
		t.Fatal("cold S05 record differs")
	}
	observed["cold_clone"] = coldRead
	observed["rejected_http_cases"] = 6
	observed["coexistence"] = []string{"real S05 read/update/cold", "normal native copy failure/retry", "clear cache preserves durable history and subscriptions"}
	independentHistoryEvidence(t, "integrated-ui-observations.json", observed)
}
