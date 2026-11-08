package app

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"light-media/internal/plugins/artifacts"
)

func TestIYUUInstalledAMD64PackageStartup(t *testing.T) {
	bundle := os.Getenv("IYUU_OWNED_PACKAGE_DIR")
	if bundle == "" {
		t.Skip("owned IYUU package fixture required")
	}
	indexRaw, err := os.ReadFile(filepath.Join(bundle, "go-plugins.v1.json"))
	if err != nil {
		t.Fatal(err)
	}
	var index struct{ Plugins []artifacts.Index }
	if err := json.Unmarshal(indexRaw, &index); err != nil || len(index.Plugins) != 1 {
		t.Fatal("owned package index did not contain exactly one plugin")
	}
	selected := index.Plugins[0]
	if selected.PluginID != "IYUUAutoSeed" || len(selected.Releases) != 1 || len(selected.Releases[0].Artifacts) != 1 {
		t.Fatal("unexpected IYUU candidate index")
	}
	archive := filepath.Join(bundle, "IYUUAutoSeed-2.22-linux-amd64.zip")
	var requests atomic.Int32
	repository := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/index.json":
			_ = json.NewEncoder(w).Encode(selected)
		case "/worker.zip":
			requests.Add(1)
			http.ServeFile(w, r, archive)
		default:
			http.NotFound(w, r)
		}
	}))
	defer repository.Close()
	selected.Releases[0].Artifacts[0].URL = repository.URL + "/worker.zip"

	t.Setenv("MP_SERVER_HOST", "")
	app := newTestApp(t)
	cookie := setupTestAdmin(t, app)
	server := httptest.NewServer(app.Handler())
	defer server.Close()
	call := func(method, path string, body []byte) (int, []byte) {
		t.Helper()
		req, err := http.NewRequestWithContext(t.Context(), method, server.URL+path, bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.AddCookie(cookie)
		response, err := server.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		raw, err := io.ReadAll(response.Body)
		if err != nil {
			t.Fatal(err)
		}
		return response.StatusCode, raw
	}
	payload, _ := json.Marshal(map[string]string{"plugin_id": selected.PluginID, "repo_url": repository.URL + "/index.json"})
	status, installed := call(http.MethodPost, "/api/plugins/install", payload)
	assertOriginalRegistrySuccess(t, status, installed)
	if requests.Load() != 1 {
		t.Fatalf("IYUU archive downloads: %d", requests.Load())
	}
	if _, ok := app.pluginRunningInstances.Load(selected.PluginID); !ok {
		t.Fatal("IYUU startup did not publish an initialized worker")
	}
	status, form := call(http.MethodGet, "/api/plugins/IYUUAutoSeed/form", nil)
	if status != http.StatusOK || !bytes.Contains(form, []byte("辅种后增加标签")) {
		t.Fatalf("actual IYUU native form unavailable: HTTP %d", status)
	}
	fmt.Println("IYUU_STAGE verified installed amd64 package worker and native form")
}
