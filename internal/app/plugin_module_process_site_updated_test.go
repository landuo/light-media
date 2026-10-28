package app

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"light-media/internal/plugins"
	"light-media/internal/plugins/artifacts"
)

func verifyOwnedProductionCoreSiteUpdated(t *testing.T, dir string, request func(string, string, any, int) []byte, success func(string, string, any), stop, start func()) {
	t.Helper()
	path := os.Getenv("OWNED_SITE_MODULE_PROCESS_BINARY")
	payload, err := os.ReadFile(path)
	if err != nil {
		t.Fatal("actual site module binary required", err)
	}
	raw, err := exec.Command(path, "--manifest").Output()
	var manifest plugins.Manifest
	if err != nil || json.Unmarshal(raw, &manifest) != nil || manifest.Validate() != nil || manifest.PluginID != ownedModuleID || len(manifest.APIs) != 1 {
		t.Fatal("actual site module manifest")
	}
	digest := sha256.Sum256(payload)
	sum := hex.EncodeToString(digest[:])
	index := artifacts.Index{SchemaVersion: 1, PluginID: manifest.PluginID, CurrentVersion: manifest.Version, Releases: []artifacts.Release{{Version: manifest.Version, Manifest: manifest, Artifacts: []artifacts.Artifact{{Platform: runtime.GOOS + "-" + runtime.GOARCH, Format: "executable", SHA256: sum, ExecutableSHA256: sum, Size: int64(len(payload)), HostContract: pluginResourcesSDKContract}}}}}
	repo := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/index":
			_ = json.NewEncoder(w).Encode(index)
		case "/worker":
			_, _ = w.Write(payload)
		default:
			http.NotFound(w, r)
		}
	}))
	defer repo.Close()
	index.Releases[0].Artifacts[0].URL = repo.URL + "/worker"
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" && r.URL.Path != "/favicon.ico" {
			t.Error("module override performed unrelated native account request", r.URL.Path)
		}
		if r.URL.Path == "/favicon.ico" {
			data, _ := base64.StdEncoding.DecodeString("R0lGODlhAQABAIAAAACq/wAAACH5BAAAAAAALAAAAAABAAEAAAICRAEAOw==")
			w.Header().Set("Content-Type", "image/gif")
			_, _ = w.Write(data)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<html><head><link rel="icon" href="/favicon.ico"></head><body>Owned icon page</body></html>`))
	}))
	defer peer.Close()
	stop()
	seed, err := New(Options{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	c := seed.Settings()
	site := parallelSearchSite("owned-site-event", peer.URL)
	c.Sites = []SiteConfig{site}
	if err = seed.saveSettings(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	if err = seed.Close(); err != nil {
		t.Fatal(err)
	}
	start()
	success("POST", "/api/plugins/install", map[string]any{"plugin_id": manifest.PluginID, "repo_url": repo.URL + "/index"})
	const source, clone = ownedModuleID, ownedModuleID + "site"
	configure := func(id string, value any) {
		success("PUT", "/api/plugins/"+id, map[string]any{"enabled": true, "modules": map[string]string{"refresh_userdata": "module_execute"}, "result": value})
	}
	configure(source, nil)
	success("POST", "/api/plugins/"+source+"/clone", map[string]any{"suffix": "site", "name": "Owned site clone"})
	configure(clone, map[string]any{"username": "clone", "upload": 99, "message_unread": 2, "ratio": 0.5})
	snapshot := func(id string) ownedModuleSnapshot {
		var out ownedModuleSnapshot
		if json.Unmarshal(request("GET", "/api/v1/plugin/"+id+"/snapshot", nil, 200), &out) != nil || out.PID <= 0 {
			t.Fatal("actual site module process snapshot")
		}
		return out
	}
	one, two := snapshot(source), snapshot(clone)
	if one.PID == two.PID {
		t.Fatal("site module clone process identity shared")
	}
	// A second App/Store correctly refuses the running server's flock. The
	// observer only opens this test's SQLite file; it never initializes another
	// application, runs migrations or steals its execution lock.
	db, err := sql.Open("sqlite", filepath.Join(dir, "light-media.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	key, err := os.ReadFile(filepath.Join(dir, "secret.key"))
	if err != nil || len(key) != 32 {
		t.Fatal("owned test database observer key unavailable")
	}
	store := &Store{DB: db, key: key}
	count := func(kind string) int {
		var count int
		if err := store.DB.QueryRow(`SELECT count(*) FROM records WHERE kind=?`, kind).Scan(&count); err != nil {
			t.Fatal(err)
		}
		return count
	}
	send := func(sender string, domain any) {
		request("POST", "/api/v1/plugin/"+sender+"/input", map[string]any{"operation": "event.send", "input": map[string]any{"kind": "broadcast", "type": "site.updated", "data": map[string]any{"domain": domain}}}, 200)
	}
	wait := func(check func() bool) {
		deadline := time.Now().Add(10 * time.Second)
		for !check() {
			if time.Now().After(deadline) {
				t.Fatal("actual site event side effect deadline")
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
	notices := count("notification")
	send("OwnedInput", peer.URL)
	wait(func() bool { return count("site_stats_history") == 1 && count("notification") == notices+2 })
	read := func() siteSnapshot {
		var raw []byte
		var value siteSnapshot
		if err := store.DB.QueryRow(`SELECT payload FROM records WHERE kind='site_stats_latest' AND id=?`, site.ID).Scan(&raw); err != nil || store.openSiteAccountSnapshot("site_stats_latest", site.ID, raw, &value) != nil || value.SourceData == nil {
			t.Fatal("actual source account cache missing", err)
		}
		return value
	}
	if read().SourceData.Upload != 99 || *read().SourceData.Username != "clone" || snapshot(source).Calls["module_execute"] != one.Calls["module_execute"]+1 || snapshot(clone).Calls["module_execute"] != two.Calls["module_execute"]+1 {
		t.Fatal("actual source/clone ordinary module order or persisted DTO differs")
	}
	before := snapshot(clone)
	configure(source, false)
	connectionStamp := func() string {
		var raw []byte
		var value siteSnapshot
		if err := store.DB.QueryRow(`SELECT payload FROM records WHERE kind='site_connection' AND id=?`, site.ID).Scan(&raw); err != nil || store.openSiteAccountSnapshot("site_connection", site.ID, raw, &value) != nil {
			t.Fatal("actual site connection observation missing", err)
		}
		return value.Connection.CheckedAt
	}
	beforeStamp := connectionStamp()
	send("OwnedInputservice", peer.URL)
	wait(func() bool { return connectionStamp() != beforeStamp })
	if snapshot(clone).Calls["module_execute"] != before.Calls["module_execute"] || count("notification") != notices+2 || read().SourceData.Upload != 99 {
		t.Fatal("false source result continued into clone/core or changed daily data")
	}
	configure(source, map[string]any{"username": "must-rollback", "upload": 500, "message_unread": 1})
	if _, err := store.DB.Exec(`CREATE TRIGGER owned_site_event_sql_failure BEFORE INSERT ON records WHEN NEW.kind='site_stats_history' BEGIN SELECT RAISE(ABORT,'owned source DB failure'); END`); err != nil {
		t.Fatal(err)
	}
	eventErrors := func() int {
		var result struct {
			Events []struct {
				Type string
				Data struct {
					EventType string `json:"event_type"`
					Handler   string `json:"event_handle"`
				}
			}
		}
		if json.Unmarshal(request("GET", "/api/v1/plugin/OwnedInput/events", nil, 200), &result) != nil {
			t.Fatal("actual event failure observation")
		}
		count := 0
		for _, event := range result.Events {
			if event.Type == "system.error" && event.Data.EventType == "site.updated" && event.Data.Handler == "go.core.Site.cache_site_userdata" {
				count++
			}
		}
		return count
	}
	beforeErrors := eventErrors()
	send("OwnedInput", peer.URL)
	wait(func() bool { return eventErrors() == beforeErrors+1 })
	if count("notification") != notices+2 || read().SourceData.Upload != 99 {
		t.Fatal("actual SQL failure replaced committed account or posted source notice")
	}
	if _, err := store.DB.Exec(`DROP TRIGGER owned_site_event_sql_failure`); err != nil {
		t.Fatal(err)
	}
	send("OwnedInput", peer.URL)
	wait(func() bool { return count("notification") == notices+3 })
	if read().SourceData.Upload != 500 {
		t.Fatal("actual SQL recovery did not persist source DTO")
	}
	one, two = snapshot(source), snapshot(clone)
	stop()
	start()
	afterOne, afterTwo := snapshot(source), snapshot(clone)
	if one.PID == afterOne.PID || two.PID == afterTwo.PID || afterOne.PID == afterTwo.PID || read().SourceData.Upload != 500 || count("notification") != notices+3 {
		t.Fatal("actual cold site event state/PID or no-replay behavior differs")
	}
	configure(source, nil)
	send("OwnedInputservice", peer.URL)
	wait(func() bool { return count("notification") == notices+5 })
	if read().SourceData.Upload != 99 {
		t.Fatal("cold clone config was not consumed by new ordinary event")
	}
	success("DELETE", "/api/plugins/"+clone, nil)
	success("DELETE", "/api/plugins/"+source, nil)
	t.Logf("actual site updated source/clone PIDs %d/%d -> %d/%d; account99->SQL rollback99->recovery500->cold99; notices+5", one.PID, two.PID, afterOne.PID, afterTwo.PID)
}
