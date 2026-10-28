package app

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"

	"light-media/internal/plugins"
	"light-media/internal/plugins/artifacts"
)

func originalAuthFrontendFixture(t *testing.T) (*App, *http.Cookie) {
	t.Helper()
	binary := os.Getenv("OWNED_AUTH_FRONTEND_FIXTURE_BINARY")
	if binary == "" {
		t.Fatal("OWNED_AUTH_FRONTEND_FIXTURE_BINARY must name actual frontend-enabled owned-auth-fixture")
	}
	worker, err := os.ReadFile(binary)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	raw, err := exec.CommandContext(ctx, binary, "--manifest").Output()
	if err != nil {
		t.Fatal(err)
	}
	var manifest plugins.Manifest
	if json.Unmarshal(raw, &manifest) != nil || manifest.PluginID != "OwnedAuth" || manifest.Frontend == nil || manifest.Validate() != nil {
		t.Fatal("wrong authentication worker")
	}
	files := map[string][]byte{manifest.Entry: worker}
	for _, name := range []string{"remoteEntry.js", "auth.js", "style.css"} {
		files["ui/"+name], err = os.ReadFile("../../testdata/standard-auth/ui/" + name)
		if err != nil {
			t.Fatal(err)
		}
	}
	var archive bytes.Buffer
	writer := zip.NewWriter(&archive)
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	inventory := []artifacts.PackageFile{}
	for _, name := range names {
		file, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = file.Write(files[name]); err != nil {
			t.Fatal(err)
		}
		inventory = append(inventory, artifacts.PackageFile{Path: name, Size: int64(len(files[name])), SHA256: originalFrontendHash(files[name])})
	}
	if err = writer.Close(); err != nil {
		t.Fatal(err)
	}
	payload := archive.Bytes()
	index := artifacts.Index{SchemaVersion: 1, PluginID: manifest.PluginID, CurrentVersion: manifest.Version, Releases: []artifacts.Release{{Version: manifest.Version, Manifest: manifest, Artifacts: []artifacts.Artifact{{Platform: runtime.GOOS + "-" + runtime.GOARCH, Format: "zip", Size: int64(len(payload)), SHA256: originalFrontendHash(payload), ExecutableSHA256: originalFrontendHash(worker), HostContract: pluginResourcesSDKContract, Files: inventory}}}}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/index" {
			_ = json.NewEncoder(w).Encode(index)
		} else if r.URL.Path == "/package.zip" {
			_, _ = w.Write(payload)
		} else {
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	index.Releases[0].Artifacts[0].URL = server.URL + "/package.zip"
	a, err := New(Options{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	cookie := setupTestAdmin(t, a)
	body, _ := json.Marshal(map[string]string{"plugin_id": "OwnedAuth", "repo_url": server.URL + "/index"})
	r := callTest(t, a.Handler(), "POST", "/api/plugins/install", string(body), cookie, "")
	assertOriginalRegistrySuccess(t, r.Code, r.Body.Bytes())
	a.wg.Wait()
	return a, cookie
}

func TestOriginalAuthFrontendAnonymousPackageBoundary(t *testing.T) {
	a, cookie := originalAuthFrontendFixture(t)
	save := func(config string) {
		t.Helper()
		r := callTest(t, a.Handler(), "PUT", "/api/plugins/OwnedAuth", config, cookie, "")
		assertOriginalRegistrySuccess(t, r.Code, r.Body.Bytes())
		a.wg.Wait()
	}
	endpoint := "/api/plugins/OwnedAuth/frontend"
	if got := callTest(t, a.Handler(), "GET", endpoint, "", nil, ""); got.Code != 401 {
		t.Fatal("disabled plugin published frontend", got.Code)
	}
	save(`{"enabled":true}`)
	d := originalFrontendReadDescriptor(t, a, nil, "OwnedAuth")
	for _, name := range []string{"remoteEntry.js", "auth.js", "style.css"} {
		r := callTest(t, a.Handler(), "GET", strings.TrimSuffix("/api"+d.EntryPath, "remoteEntry.js")+name, "", nil, "")
		if r.Code != 200 || r.Body.Len() == 0 {
			t.Fatal("declared auth graph unavailable", name, r.Code, r.Body.String())
		}
	}
	for _, path := range []string{"/api/plugins", "/api/plugins/OwnedAuth", "/api/settings", "/api/plugins/OwnedAuth/form"} {
		if r := callTest(t, a.Handler(), "GET", path, "", nil, ""); r.Code != 401 {
			t.Fatal("authentication graph exposed management", path, r.Code)
		}
	}
	for _, path := range []string{strings.Replace("/api"+d.EntryPath, d.PackageSHA256, strings.Repeat("0", 64), 1), strings.TrimSuffix("/api"+d.EntryPath, "remoteEntry.js") + "../owned-auth-fixture", strings.TrimSuffix("/api"+d.EntryPath, "remoteEntry.js") + "%252e%252e/owned-auth-fixture"} {
		if r := callTest(t, a.Handler(), "GET", path, "", nil, ""); r.Code == 200 || r.Code == 304 || r.Code >= 300 && r.Code < 400 {
			t.Fatal("invalid anonymous graph accepted", path, r.Code)
		}
	}
	save(`{"enabled":true,"providers":[{"name":"hidden","enabled":false}]}`)
	r := callTest(t, a.Handler(), "GET", "/api/auth/providers", "", nil, "")
	var response struct {
		Data []any `json:"data"`
	}
	if r.Code != 200 || json.Unmarshal(r.Body.Bytes(), &response) != nil || len(response.Data) != 0 {
		t.Fatal("hidden provider listed", r.Code, r.Body.String())
	}
	if r = callTest(t, a.Handler(), "GET", "/api"+d.EntryPath, "", nil, ""); r.Code != 200 {
		t.Fatal("source allows declared hidden provider directory", r.Code, r.Body.String())
	}
	save(`{"enabled":false}`)
	if r = callTest(t, a.Handler(), "GET", "/api"+d.EntryPath, "", nil, ""); r.Code != 401 {
		t.Fatal("disabled plugin assets still anonymous", r.Code)
	}
}

func TestOriginalAuthFrontendCloneHasOwnAnonymousIdentity(t *testing.T) {
	a, cookie := originalAuthFrontendFixture(t)
	r := callTest(t, a.Handler(), "PUT", "/api/plugins/OwnedAuth", `{"enabled":true}`, cookie, "")
	assertOriginalRegistrySuccess(t, r.Code, r.Body.Bytes())
	a.wg.Wait()
	r = callTest(t, a.Handler(), "POST", "/api/plugins/OwnedAuth/clone", `{"suffix":"Login"}`, cookie, "")
	assertOriginalRegistrySuccess(t, r.Code, r.Body.Bytes())
	a.wg.Wait()
	if r = callTest(t, a.Handler(), "GET", "/api/plugins/OwnedAuthlogin/frontend", "", nil, ""); r.Code != 401 {
		t.Fatal("new clone must start disabled", r.Code)
	}
	r = callTest(t, a.Handler(), "PUT", "/api/plugins/OwnedAuthlogin", `{"enabled":true}`, cookie, "")
	assertOriginalRegistrySuccess(t, r.Code, r.Body.Bytes())
	a.wg.Wait()
	clone := originalFrontendReadDescriptor(t, a, nil, "OwnedAuthlogin")
	source := originalFrontendReadDescriptor(t, a, nil, "OwnedAuth")
	if clone.PluginID != source.PluginID || clone.InstanceID == source.InstanceID || !strings.Contains(clone.EntryPath, "/OwnedAuthlogin/") {
		t.Fatal("clone auth identity lost", clone, source)
	}
	r = callTest(t, a.Handler(), "PUT", "/api/plugins/OwnedAuth", `{"enabled":false}`, cookie, "")
	assertOriginalRegistrySuccess(t, r.Code, r.Body.Bytes())
	a.wg.Wait()
	r = callTest(t, a.Handler(), "GET", "/api"+clone.EntryPath, "", nil, "")
	if r.Code != 200 {
		t.Fatal("source disable affected clone auth", r.Code, r.Body.String())
	}
}
