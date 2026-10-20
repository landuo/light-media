package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"light-media/internal/download"
	iyuuplugin "light-media/internal/plugins/iyuuautoseed"
	sr "light-media/internal/plugins/seedrescue"
	"light-media/internal/sites"
)

func rescueFinish(t *testing.T, a *App, run PluginRun) PluginRun {
	t.Helper()
	a.executePluginRun(context.Background(), run)
	done, e := a.pluginStore.Run(context.Background(), run.ID)
	if e != nil || done.State != "succeeded" {
		t.Fatalf("actual SDK %s %s %s: %v", run.Action, done.State, done.ErrorCode, e)
	}
	return done
}
func rescueInstallation(t *testing.T, a *App, cfg sr.Object) (PluginInstallation, PluginRun) {
	t.Helper()
	run := uninstallInstall(t, a, "SeedRescue", sr.NativeManifest(), cfg, true)
	in, e := a.pluginStore.Installation(context.Background(), run.InstanceID)
	if e != nil {
		t.Fatal(e)
	}
	return in, run
}
func rescueJSON(t *testing.T, w *httptest.ResponseRecorder) sr.Object {
	t.Helper()
	if w.Code != 200 {
		t.Fatalf("HTTP %d %s", w.Code, w.Body.String())
	}
	var v sr.Object
	d := json.NewDecoder(w.Body)
	d.UseNumber()
	if e := d.Decode(&v); e != nil {
		t.Fatal(e)
	}
	return v
}
func TestSeedRescueActualSDKConfigureFormSidebarAndCold(t *testing.T) {
	a := newTestApp(t)
	useBuiltInPluginProcess(t, a)
	cookie := setupTestAdmin(t, a)
	cfg := sr.ParseConfig(sr.Object{}).Current()
	cfg["token"] = "owned-private-token"
	cfg["rss_urls"] = "https://owned.example/rss?passkey=private"
	in, run := rescueInstallation(t, a, cfg)
	rescueFinish(t, a, run)
	state, e := a.seedRescueSidebar(context.Background(), in)
	if e != nil || len(state) != 0 {
		t.Fatal(state, e)
	}
	form := callTest(t, a.Handler(), "GET", "/api/plugins/SeedRescue/form", "", cookie, "")
	if form.Code != 200 || strings.Contains(form.Body.String(), "owned-private-token") || strings.Contains(form.Body.String(), "passkey=private") {
		t.Fatal("form secret leak", form.Code, form.Body.String())
	}
	var response struct {
		Model    sr.Object `json:"model"`
		Revision int64     `json:"revision"`
	}
	var wrapper struct {
		Data json.RawMessage `json:"data"`
	}
	json.Unmarshal(form.Body.Bytes(), &wrapper)
	json.Unmarshal(wrapper.Data, &response)
	var metadata map[string]any
	json.Unmarshal(wrapper.Data, &metadata)
	if metadata["render_mode"] != "vue" {
		t.Fatal("SDK form lost original Vue mode", metadata["render_mode"])
	}
	if response.Model["token"] != seedRescuePrivateToken || response.Model["rss_urls"] != seedRescuePrivateRSS {
		t.Fatal(response)
	}
	response.Model["enabled"] = true
	save := callTest(t, a.Handler(), "PUT", "/api/plugins/SeedRescue/config", string(mustCleanSeedJSON(map[string]any{"revision": response.Revision, "config": response.Model})), cookie, "")
	if save.Code != 200 {
		t.Fatal(save.Code, save.Body.String())
	}
	// Unapplied enabled config cannot expose an active sidebar.
	if list, e := a.seedRescueSidebar(context.Background(), in); e != nil || len(list) != 0 {
		t.Fatal("pending config presented as active", list, e)
	}
	next, e := a.pluginStore.ClaimRun(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	rescueFinish(t, a, next)
	if list, e := a.seedRescueSidebar(context.Background(), in); e != nil || len(list) != 1 {
		t.Fatal(list, e)
	}
	stored, _ := a.pluginStore.Config(context.Background(), in.InstanceID)
	if !bytes.Contains(stored.Value, []byte("owned-private-token")) {
		t.Fatal("form marker overwrote real token")
	}
	dir := a.Dir
	if e = a.Close(); e != nil {
		t.Fatal(e)
	}
	cold, e := New(Options{DataDir: dir})
	if e != nil {
		t.Fatal(e)
	}
	defer cold.Close()
	if list, e := cold.seedRescueSidebar(context.Background(), in); e != nil || len(list) != 1 {
		t.Fatal("cold effective state lost", list, e)
	}
	if initialize, e := cold.shouldInitializeSeedRescue(context.Background(), in, stored); e != nil || initialize {
		t.Fatal("confirmed init replay", initialize, e)
	}
}
func TestSeedRescueActualHTTPAllSixRoutesAndNativeAdd(t *testing.T) { rescueNativeHTTPFlow(t, false) }
func TestSeedRescueActualLostAddACKColdOnlyObservesSameHash(t *testing.T) {
	rescueNativeHTTPFlow(t, true)
}
func rescueNativeHTTPFlow(t *testing.T, lostACK bool) {
	rescueNativeHTTPFlowConfigured(t, lostACK, nil)
}
func rescueNativeHTTPFlowConfigured(t *testing.T, lostACK bool, initialize func(*App, sr.Object, nativeSiteIdentity, nativeSiteIdentity) (PluginInstallation, PluginRun)) {
	a := newTestApp(t)
	useBuiltInPluginProcess(t, a)
	cookie := setupTestAdmin(t, a)
	torrent := []byte("d4:infod6:lengthi1e4:name5:owned12:piece lengthi16384e6:pieces20:aaaaaaaaaaaaaaaaaaaaee")
	hash, e := sr.InfoHashFromTorrent(torrent)
	if e != nil {
		t.Fatal(e)
	}
	var adds atomic.Int32
	var known atomic.Bool
	qb := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/app/webapiVersion":
			io.WriteString(w, "2.11.3")
		case "/api/v2/torrents/info":
			r.ParseForm()
			if known.Load() && r.Form.Get("hashes") == hash {
				fmt.Fprintf(w, `[{"hash":%q,"state":"downloading","progress":0.1,"amount_left":1}]`, hash)
			} else {
				io.WriteString(w, `[]`)
			}
		case "/api/v2/torrents/add":
			adds.Add(1)
			r.ParseMultipartForm(1 << 20)
			if r.FormValue("paused") != "False" || r.FormValue("stopped") != "False" || r.FormValue("category") != sr.Category || r.FormValue("savepath") != "/owned/download" {
				t.Error("source add shape changed", r.Form)
			}
			known.Store(true)
			io.WriteString(w, "Ok.")
		default:
			t.Error("unexpected downloader RPC", r.URL.Path)
		}
	}))
	defer qb.Close()
	var siteURL string
	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/rss.php":
			if r.Header.Get("Cookie") != "" {
				t.Error("RSS leaked site cookie")
			}
			w.Header().Set("Content-Type", "application/rss+xml")
			fmt.Fprintf(w, `<rss><channel><item><title>Owned Movie 2026</title><link>%s/details.php?id=9</link><enclosure url="%s/download.php?id=9" length="1" /></item></channel></rss>`, siteURL, siteURL)
		case "/download.php":
			if r.Header.Get("Cookie") != "session=owned-private" {
				t.Error("wrong bound cookie")
			}
			w.Header().Set("Content-Type", "application/x-bittorrent")
			w.Write(torrent)
		case "/details.php":
			if initialize == nil {
				t.Error("unexpected owned detail lookup")
			}
			w.WriteHeader(http.StatusNotFound)
		case "/torrents.php":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			io.WriteString(w, `<html><table id="torrenttable"><tr><td>none</td></tr></table></html>`)
		default:
			t.Error("unexpected site route", r.URL.String())
			w.WriteHeader(404)
		}
	}))
	defer site.Close()
	siteURL = site.URL
	sourceURL := strings.Replace(site.URL, "127.0.0.1", "localhost", 1)
	a.cfgMu.Lock()
	a.config.Sites = []SiteConfig{{Config: sites.Config{ID: "target", Kind: "nexusphp", Name: "Owned target", BaseURL: site.URL, Cookie: "session=owned-private"}, Enabled: true}, {Config: sites.Config{ID: "source", Kind: "nexusphp", Name: "Owned source", BaseURL: sourceURL, Cookie: "session=other"}, Enabled: true}}
	a.config.Downloaders = []DownloaderConfig{{Config: download.Config{ID: "owned-qb", Kind: "qbittorrent", BaseURL: qb.URL}, Name: "Owned qB"}}
	a.cfgMu.Unlock()
	if e = a.bootstrapNativeSiteIdentities(context.Background()); e != nil {
		t.Fatal(e)
	}
	target, _ := a.Store.nativeSiteByAppID(context.Background(), "target")
	source, _ := a.Store.nativeSiteByAppID(context.Background(), "source")
	iyuu := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Header.Get("token") != "owned-token" {
			t.Error("wrong helper credential")
		}
		switch r.URL.Path {
		case "/reseed/sites/index":
			fmt.Fprintf(w, `{"code":0,"data":{"sites":[{"id":7,"base_url":%q,"download_page":"download.php?id={}"}]}}`, sourceURL+"/")
		case "/reseed/sites/reportExisting":
			io.WriteString(w, `{"code":0,"data":{"sid_sha1":"owned-sid"}}`)
		case "/reseed/index/index":
			io.WriteString(w, `{"code":404,"msg":"未查询到可辅种数据"}`)
		default:
			t.Error(r.URL.Path)
		}
	}))
	defer iyuu.Close()
	a.pluginSeedRescue.loopbackAPI = iyuu.URL
	// This synthetic dependency only supplies the actual installed/enabled source
	// state. No original IYUU credentials or user configuration are imported.
	iyuuRun := uninstallInstall(t, a, "OwnedIYUU", iyuuplugin.Manifest(), map[string]any{"enabled": true, "onlyonce": false, "clearcache": false, "cron": "0 0 1 1 *", "downloaders": []any{"Owned qB"}, "sites": []any{source.publicSourceID()}, "token": "owned-dependency-token", "size": json.Number("0")}, true)
	rescueFinish(t, a, iyuuRun)
	cfg := sr.ParseConfig(sr.Object{}).Current()
	cfg["enabled"] = true
	cfg["notify"] = true
	cfg["token"] = "owned-token"
	cfg["target_site_id"] = target.SourceID
	cfg["source_site_ids"] = []int64{source.SourceID}
	cfg["downloader"] = "Owned qB"
	cfg["save_path"] = "/owned/download"
	cfg["rss_urls"] = site.URL + "/rss.php?passkey=owned"
	cfg["cron"] = "0 0 1 1 *"
	var in PluginInstallation
	var initial PluginRun
	if initialize == nil {
		in, initial = rescueInstallation(t, a, cfg)
	} else {
		in, initial = initialize(a, cfg, target, source)
	}
	rescueFinish(t, a, initial)
	api := func(method, path, body string) sr.Object {
		t.Helper()
		return rescueJSON(t, callTest(t, a.Handler(), method, "/api/plugins/SeedRescue/api/"+path, body, cookie, ""))
	}
	if r := api("GET", "status", ""); r["success"] != true {
		t.Fatal(r)
	}
	scan := api("POST", "scan", "{}")
	if scan["success"] != true {
		t.Fatal("scan not queued", scan)
	}
	queued, e := a.pluginStore.ClaimRun(context.Background())
	if e != nil || queued.Action != "scan" {
		t.Fatal(queued, e)
	}
	rescueFinish(t, a, queued)
	job := api("GET", "jobs/"+hash, "")
	if job["success"] != true {
		t.Fatal("RSS did not create durable job", job)
	}
	if r := api("POST", "jobs/"+hash+"/retry", "{}"); r["success"] != true {
		t.Fatal("retry", r)
	}
	// Fresh target fallback has unknown promotion. Its first attempt must show
	// original risk confirmation and make no selected native write.
	selection := map[string]any{"candidate_id": fmt.Sprintf("target:%d:%s", sr.ParseConfig(cfg).TargetSiteID, hash)}
	risk := api("POST", "jobs/"+hash+"/download", string(mustCleanSeedJSON(selection)))
	if risk["success"] != false || adds.Load() != 0 {
		t.Fatal("risk bypass", risk, adds.Load())
	}
	selection["confirm_unknown"] = true
	selection["skip_future_risk_confirmation"] = true
	if lostACK {
		a.pluginSeedRescue.afterEffect = func(kind string) error {
			if kind == "add_torrent" {
				return errors.New("owned lost local acknowledgement")
			}
			return nil
		}
		failed := callTest(t, a.Handler(), "POST", "/api/plugins/SeedRescue/api/jobs/"+hash+"/download", string(mustCleanSeedJSON(selection)), cookie, "")
		if failed.Code != 502 || adds.Load() != 1 {
			t.Fatal("lost ack not unknown", failed.Code, adds.Load(), failed.Body.String())
		}
		state, _, e := a.pluginStore.seedRescueRead(context.Background(), a.Store.DB, in.InstanceID, in.Generation)
		if e != nil || seedRescueNoUnknown(state) == nil {
			t.Fatal("unknown intent vanished", e)
		}
		// The native daemon is temporarily unable to observe its accepted hash.
		known.Store(false)
		blocked := callTest(t, a.Handler(), "POST", "/api/plugins/SeedRescue/api/scan", "{}", cookie, "")
		if blocked.Code != 502 || adds.Load() != 1 {
			t.Fatal("unknown allowed new write", blocked.Code, adds.Load())
		}
		settings := a.Settings()
		sealed, e := a.Store.Seal(settings)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = a.Store.DB.Exec(`INSERT INTO records VALUES('config','main',?,?) ON CONFLICT(kind,id) DO UPDATE SET payload=excluded.payload,updated_at=excluded.updated_at`, sealed, now()); e != nil {
			t.Fatal(e)
		}
		dir := a.Dir
		if e = a.Close(); e != nil {
			t.Fatal(e)
		}
		cold, e := New(Options{DataDir: dir})
		if e != nil {
			t.Fatal(e)
		}
		defer cold.Close()
		useBuiltInPluginProcess(t, cold)
		cold.pluginSeedRescue.loopbackAPI = iyuu.URL
		known.Store(true)
		queued := rescueJSON(t, callTest(t, cold.Handler(), "POST", "/api/plugins/SeedRescue/api/scan", "{}", cookie, ""))
		if queued["success"] != true {
			t.Fatal("read-only observation did not permit source reconciliation", queued)
		}
		run, e := cold.pluginStore.claimRunForInstance(context.Background(), in.InstanceID)
		if e != nil {
			t.Fatal(e)
		}
		rescueFinish(t, cold, run)
		if adds.Load() != 1 {
			t.Fatal("cold unknown re-added torrent", adds.Load())
		}
		state, _, e = cold.pluginStore.seedRescueRead(context.Background(), cold.Store.DB, in.InstanceID, in.Generation)
		if e != nil || seedRescueNoUnknown(state) != nil {
			t.Fatal("observed receipt remains unknown", e)
		}
		var observed bool
		for _, action := range state.Runs {
			observed = observed || action.ErrorCode == seedRescueObserved
		}
		if !observed {
			t.Fatal("no authenticated read observation")
		}
		return
	}
	added := api("POST", "jobs/"+hash+"/download", string(mustCleanSeedJSON(selection)))
	if added["success"] != true || adds.Load() != 1 {
		t.Fatal("native add", added, adds.Load())
	}
	if r := api("POST", "jobs/"+hash+"/ignore", "{}"); r["success"] != false || adds.Load() != 1 {
		t.Fatal("active ignored/re-added", r, adds.Load())
	}
	state, _, e := a.pluginStore.seedRescueRead(context.Background(), a.Store.DB, in.InstanceID, in.Generation)
	if e != nil || seedRescueNoUnknown(state) != nil {
		t.Fatal("receipt inconsistent", e)
	}
	cfgValue, _ := a.pluginStore.Config(context.Background(), in.InstanceID)
	if !bytes.Contains(cfgValue.Value, []byte(`"confirm_download_risk":false`)) {
		t.Fatal("own config receipt lost", string(cfgValue.Value))
	}
}
func TestSeedRescueBusyAPIAndReadDoNotWaitForOwner(t *testing.T) {
	a := newTestApp(t)
	useBuiltInPluginProcess(t, a)
	cookie := setupTestAdmin(t, a)
	in, run := rescueInstallation(t, a, sr.ParseConfig(sr.Object{}).Current())
	rescueFinish(t, a, run)
	owner, _ := a.seedRescueOwner(context.Background(), in)
	if e := owner.lock(context.Background()); e != nil {
		t.Fatal(e)
	}
	defer owner.unlock()
	start := time.Now()
	status := callTest(t, a.Handler(), "GET", "/api/plugins/SeedRescue/api/status", "", cookie, "")
	if v := rescueJSON(t, status); v["success"] != true {
		t.Fatal(v)
	}
	busy := rescueJSON(t, callTest(t, a.Handler(), "POST", "/api/plugins/SeedRescue/api/scan", "{}", cookie, ""))
	if busy["success"] != false || time.Since(start) > 5*time.Second {
		t.Fatal("API waited on source owner", busy, time.Since(start))
	}
	state, _, e := a.pluginStore.seedRescueRead(context.Background(), a.Store.DB, in.InstanceID, in.Generation)
	if e != nil || seedRescueNoUnknown(state) != nil {
		t.Fatal("busy created false unknown", e)
	}
}

var _ = url.Values{}
