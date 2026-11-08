package app

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestSeedRescueLinuxAMD64BrowserPackage(t *testing.T) {
	bundle := os.Getenv("SOURCE_SEED_RESCUE_BROWSER_PACKAGE_DIR")
	webDist := os.Getenv("SOURCE_SEED_RESCUE_BROWSER_WEB_DIR")
	if runtime.GOARCH != "amd64" || bundle == "" || webDist == "" {
		t.Skip("requires the indexed SeedRescue Linux amd64 browser fixture")
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

	const pluginID = "SeedRescue"
	installMergeSiteSwitchPackage(t, a, cookie, bundle, pluginID)
	appPageOnly := os.Getenv("SOURCE_SEED_RESCUE_BROWSER_APP_ONLY") == "1"
	if appPageOnly {
		reply := callTest(t, a.Handler(), http.MethodPut, "/api/plugins/"+pluginID, `{"enabled":true,"cron":" ","onlyonce":false}`, cookie, "")
		assertOriginalRegistrySuccess(t, reply.Code, reply.Body.Bytes())
	}
	installed, err := a.pluginStore.Installation(t.Context(), pluginID)
	if err != nil || installed.Version != "1.6.3" {
		t.Fatal("expected SeedRescue 1.6.3 from the owned index", err)
	}
	finished := make(chan bool, 1)
	handler := a.Handler()
	server := &http.Server{ReadHeaderTimeout: 5 * time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/__owned_seed_rescue_browser_finished" {
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
	t.Log("SeedRescue browser-ready: 127.0.0.1:5192")
	select {
	case passed := <-finished:
		if !passed {
			t.Fatal("owned Chrome did not finish the Config/Page/AppPage flow; see browser-result.json")
		}
	case <-time.After(180 * time.Second):
		t.Fatal("owned Chrome handoff timed out")
	}

	raw, present, err := a.readOriginalBaseConfigForInit(t.Context(), pluginID)
	var config struct {
		Enabled  bool   `json:"enabled"`
		OnlyOnce bool   `json:"onlyonce"`
		Cron     string `json:"cron"`
		Token    string `json:"token"`
	}
	if err != nil || !present || json.Unmarshal(raw, &config) != nil || !config.Enabled ||
		config.OnlyOnce || strings.TrimSpace(config.Cron) != "" || config.Token != "" {
		t.Fatal("browser did not save only the intended non-operational configuration", err)
	}
	services, err := a.originalPublicServicesResult(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	for _, service := range services {
		if service.InstanceID == pluginID {
			t.Fatal("the owned UI fixture unexpectedly registered a SeedRescue schedule")
		}
	}
	if appPageOnly {
		t.Log("owned Chrome opened the installed sidebar AppPage and its configuration registered no schedule")
	} else {
		t.Log("owned Chrome mounted exact Config, Page and sidebar AppPage; the saved configuration registered no schedule")
	}
}
