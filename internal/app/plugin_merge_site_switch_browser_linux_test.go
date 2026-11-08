package app

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"
	"time"
)

func TestMergeSiteSwitchLinuxAMD64BrowserPackage(t *testing.T) {
	bundle := os.Getenv("SOURCE_MERGE_SITE_SWITCH_BROWSER_PACKAGE_DIR")
	webDist := os.Getenv("SOURCE_MERGE_SITE_SWITCH_BROWSER_WEB_DIR")
	if runtime.GOARCH != "amd64" || bundle == "" || webDist == "" {
		t.Skip("requires the owned MergeSiteSwitch Linux amd64 browser fixture")
	}
	const pluginID = "MergeSiteSwitch"
	for filename, digest := range map[string]string{
		"worker.zip": "38e2b5cc7b1b7c85e3b46beb3ed1da5aedffa1511c3fe0a62dc8df362c2e898b",
		"index.json": "87b3c647e3e7bae674879ea8d40862e4c4703824dc5b08fd2973e2fe8932c1dd",
	} {
		raw, err := os.ReadFile(filepath.Join(bundle, filename))
		if err != nil || fmt.Sprintf("%x", sha256.Sum256(raw)) != digest {
			t.Fatal("expected the exact owned MergeSiteSwitch package input", filename, err)
		}
	}
	if _, err := os.Stat(filepath.Join(webDist, "index.html")); err != nil {
		t.Fatal("expected the current compiled frontend", err)
	}
	t.Setenv("MP_SERVER_HOST", "")
	markets := []string{}
	a, err := New(Options{DataDir: t.TempDir(), Web: http.FileServer(http.Dir(webDist)), PluginMarketsOverride: &markets})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	cookie := setupTestAdmin(t, a)
	ctx := t.Context()
	handler := a.Handler()
	seedSite := func(id, name, address string, priority int, enabled bool) int64 {
		t.Helper()
		body := mustCleanSeedJSON(map[string]any{"id": id, "name": name, "base_url": address, "pri": priority, "enabled": enabled})
		response := callTest(t, handler, http.MethodPost, "/api/sites/manage", string(body), cookie, "")
		if response.Code != http.StatusOK {
			t.Fatal("could not seed the owned site", id, response.Code)
		}
		identity, err := a.Store.nativeSiteByAppID(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		value, err := a.Store.nativeSourcePublicSiteID(ctx, a.Store.DB, identity)
		if err != nil || value <= 0 {
			t.Fatal("missing numeric source site identity", id, err)
		}
		return value
	}
	alpha := seedSite("owned-alpha", "聚合甲", "https://owned-alpha.example.org", 10, true)
	beta := seedSite("owned-beta", "聚合乙", "https://owned-beta.example.net", 20, false)
	a.Start(ctx)
	installMergeSiteSwitchPackage(t, a, cookie, bundle, pluginID)

	finished := make(chan bool, 1)
	server := &http.Server{ReadHeaderTimeout: 5 * time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/__owned_merge_site_browser_finished" || r.Method != http.MethodPost {
			handler.ServeHTTP(w, r)
			return
		}
		var report struct{ Passed bool }
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&report); err != nil {
			http.Error(w, "invalid owned browser report", http.StatusBadRequest)
			return
		}
		select {
		case finished <- report.Passed:
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusConflict)
		}
	})}
	listener, err := net.Listen("tcp4", "127.0.0.1:5192")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = server.Serve(listener) }()
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = server.Shutdown(ctx)
	}()
	t.Logf("MergeSiteSwitch browser-ready: 127.0.0.1:5192 alpha=%d beta=%d", alpha, beta)
	select {
	case passed := <-finished:
		if !passed {
			t.Fatal("owned browser did not complete the VSelect save; see browser-result.json")
		}
	case <-time.After(180 * time.Second):
		t.Fatal("owned browser handoff timed out")
	}

	raw, _, err := a.readOriginalBaseConfigForInit(ctx, pluginID)
	var config struct {
		EnableSites []int64 `json:"enable_sites"`
	}
	if err != nil || json.Unmarshal(raw, &config) != nil || !slices.Equal(config.EnableSites, []int64{alpha, beta}) {
		t.Fatal("browser selection did not persist ordered numeric source IDs", err)
	}
	var active []string
	for _, site := range a.Settings().Sites {
		if site.Enabled {
			active = append(active, site.ID)
		}
	}
	slices.Sort(active)
	if !slices.Equal(active, []string{"owned-alpha", "owned-beta"}) {
		t.Fatal("browser save did not enable the second owned site", active)
	}
	enabled, err := originalBaseSDKLoaded(t, a, pluginID).getState(ctx)
	if err != nil || enabled {
		t.Fatal("site selection should leave unrelated source follow flags disabled", enabled, err)
	}
	t.Logf("owned browser VSelect persisted numeric IDs %v and enabled both owned sites, with follow flags still disabled", config.EnableSites)
}
