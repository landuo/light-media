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
	"testing"
	"time"
)

func TestScrapeFileCleanLinuxAMD64BrowserPackage(t *testing.T) {
	bundle := os.Getenv("SOURCE_SCRAPE_FILE_CLEAN_BROWSER_PACKAGE_DIR")
	webDist := os.Getenv("SOURCE_SCRAPE_FILE_CLEAN_BROWSER_WEB_DIR")
	if runtime.GOARCH != "amd64" || bundle == "" || webDist == "" {
		t.Skip("requires the owned ScrapeFileClean Linux amd64 browser fixture")
	}
	const pluginID = "ScrapeFileClean"
	for filename, digest := range map[string]string{
		"worker.zip": "adf3dbdd633d0ac663f854dd8239aac9f0d1b617a4bd370db5c3b6314826b723",
		"index.json": "9bc45cd4c98d65310f7b39eb6ba922162542ee7f65f96cf1e84b835ac33cf219",
	} {
		raw, err := os.ReadFile(filepath.Join(bundle, filename))
		if err != nil || fmt.Sprintf("%x", sha256.Sum256(raw)) != digest {
			t.Fatal("expected the exact owned ScrapeFileClean package input", filename, err)
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
	a.Start(ctx)
	installMergeSiteSwitchPackage(t, a, cookie, bundle, pluginID)

	finished := make(chan bool, 1)
	handler := a.Handler()
	server := &http.Server{ReadHeaderTimeout: 5 * time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/__owned_scrape_clean_browser_finished" || r.Method != http.MethodPost {
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
	t.Log("ScrapeFileClean browser-ready: 127.0.0.1:5192")
	select {
	case passed := <-finished:
		if !passed {
			t.Fatal("owned browser did not complete the source configuration save; see browser-result.json")
		}
	case <-time.After(180 * time.Second):
		t.Fatal("owned browser handoff timed out")
	}

	raw, _, err := a.readOriginalBaseConfigForInit(ctx, pluginID)
	var config struct {
		Enabled    *bool           `json:"enabled"`
		Monitor    string          `json:"monitor_dirs"`
		Extensions string          `json:"custom_scrap_extensions"`
		Delay      json.RawMessage `json:"delay_seconds"`
	}
	if err != nil || json.Unmarshal(raw, &config) != nil || config.Enabled == nil || *config.Enabled ||
		config.Monitor != "/owned-scrape-ui/downloads\n/owned-scrape-ui/library" ||
		config.Extensions != ".owned-extra.json\n-owned-sidecar.txt" ||
		(string(config.Delay) != "47" && string(config.Delay) != `"47"`) {
		t.Fatal("browser save did not persist the exact synthetic multiline settings while disabled", err)
	}
	enabled, err := originalBaseSDKLoaded(t, a, pluginID).getState(ctx)
	if err != nil || enabled {
		t.Fatal("disabled form save activated ScrapeFileClean", enabled, err)
	}
	t.Logf("owned browser persisted two monitor directories, two custom extensions and delay %s, plugin still disabled", config.Delay)
}
