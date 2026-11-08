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

func TestBrushFlowLinuxAMD64BrowserPackage(t *testing.T) {
	bundle := os.Getenv("SOURCE_BRUSH_FLOW_PACKAGE_DIR")
	webDist := os.Getenv("SOURCE_BRUSH_FLOW_BROWSER_WEB_DIR")
	if runtime.GOARCH != "amd64" || bundle == "" || webDist == "" {
		t.Skip("requires the owned BrushFlow Linux amd64 browser fixture")
	}
	const archiveName = "BrushFlowLanduo-5.2.18-linux-amd64.zip"
	const archiveSHA = "5b5d602993bbe92e5c4e48e18e3ba1c29244ddcda8d94c27bc81314565617523"
	archive, err := os.ReadFile(filepath.Join(bundle, archiveName))
	if err != nil || fmt.Sprintf("%x", sha256.Sum256(archive)) != archiveSHA {
		t.Fatal("expected the exact owned BrushFlow archive", err)
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
	a.Start(t.Context())
	installBrushCandidate(t, a, cookie, bundle)
	if enabled, err := originalBaseSDKLoaded(t, a, brushPackageFixtureID).getState(t.Context()); err != nil || enabled {
		t.Fatal("new BrushFlow worker did not start disabled", enabled, err)
	}

	finished := make(chan bool, 1)
	handler := a.Handler()
	server := &http.Server{ReadHeaderTimeout: 5 * time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/__owned_brush_browser_finished" || r.Method != http.MethodPost {
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
	t.Log("BrushFlow browser-ready: 127.0.0.1:5192")
	select {
	case passed := <-finished:
		if !passed {
			t.Fatal("owned browser did not complete the Config/Page flow; see browser-result.json")
		}
	case <-time.After(180 * time.Second):
		t.Fatal("owned browser handoff timed out")
	}

	raw, _, err := a.readOriginalBaseConfigForInit(t.Context(), brushPackageFixtureID)
	var config struct {
		Enabled        bool  `json:"enabled"`
		ShowSidebarNav *bool `json:"show_sidebar_nav"`
	}
	if err != nil || json.Unmarshal(raw, &config) != nil || config.Enabled || config.ShowSidebarNav == nil || *config.ShowSidebarNav {
		t.Fatal("browser setting was not persisted while the plugin remained disabled", err)
	}
	t.Log("owned browser Config/Page verified and show_sidebar_nav=false persisted without enabling BrushFlow")
}
