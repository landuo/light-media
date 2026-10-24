package app

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"light-media/internal/plugins"
	"light-media/internal/plugins/artifacts"
)

const originalFrontendFixtureID = "OwnedFrontendFixture"

type originalFrontendRepository struct {
	mu      sync.RWMutex
	index   artifacts.Index
	payload []byte
	server  *httptest.Server
}

func originalFrontendHash(data []byte) string {
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:])
}

func originalFrontendArchive(t *testing.T, binaryEnv, marker string) (artifacts.Index, []byte) {
	t.Helper()
	binary := os.Getenv(binaryEnv)
	if binary == "" {
		t.Fatalf("%s must name the real compiled standard frontend fixture", binaryEnv)
	}
	worker, err := os.ReadFile(binary)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	manifestBytes, err := exec.CommandContext(ctx, binary, "--manifest").Output()
	cancel()
	if err != nil {
		t.Fatal("real frontend fixture manifest", err)
	}
	var manifest plugins.Manifest
	if err = json.Unmarshal(manifestBytes, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.PluginID != originalFrontendFixtureID || manifest.Frontend == nil || len(manifest.Capabilities) != 0 {
		t.Fatal("unexpected real worker manifest")
	}
	if err = manifest.Validate(); err != nil {
		t.Fatal(err)
	}
	files := map[string][]byte{
		manifest.Entry:                   worker,
		"frontend/assets/remoteEntry.js": []byte("let scope;export const init=s=>{scope=s};export const get=async name=>{if(!['./Config','./Page','./Dashboard','./AppPage'].includes(name))throw Error('unknown expose');const vue=(await scope.vue[Object.keys(scope.vue)[0]].get())();const m=await import('./chunk.js');return()=>m.component(vue,name)};\n//" + marker),
		"frontend/assets/chunk.js":       []byte("export const component=(vue,name)=>({name:'OwnedFrontend',setup(){return()=>vue.h('div',{'data-owned-frontend':name},'" + marker + "')}});"),
		"frontend/assets/style.css":      []byte(".owned-" + marker + "{color:purple}"),
		"frontend/assets/font.woff2":     []byte("owned-font-" + marker),
	}
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	slices.Sort(names)
	var raw bytes.Buffer
	archive := zip.NewWriter(&raw)
	var inventory []artifacts.PackageFile
	for _, name := range names {
		f, err := archive.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = f.Write(files[name]); err != nil {
			t.Fatal(err)
		}
		inventory = append(inventory, artifacts.PackageFile{Path: name, Size: int64(len(files[name])), SHA256: originalFrontendHash(files[name])})
	}
	if err = archive.Close(); err != nil {
		t.Fatal(err)
	}
	payload := raw.Bytes()
	artifact := artifacts.Artifact{Platform: runtime.GOOS + "-" + runtime.GOARCH, Format: "zip", Size: int64(len(payload)), SHA256: originalFrontendHash(payload), ExecutableSHA256: originalFrontendHash(worker), HostContract: pluginPureSDKContract, Files: inventory}
	return artifacts.Index{SchemaVersion: 1, PluginID: manifest.PluginID, CurrentVersion: manifest.Version, Releases: []artifacts.Release{{Version: manifest.Version, Manifest: manifest, Artifacts: []artifacts.Artifact{artifact}}}}, payload
}

func originalFrontendFixture(t *testing.T) (*App, *http.Cookie, *originalFrontendRepository) {
	t.Helper()
	index, payload := originalFrontendArchive(t, "OWNED_FRONTEND_FIXTURE_BINARY", "first")
	repo := &originalFrontendRepository{index: index, payload: payload}
	repo.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		repo.mu.RLock()
		defer repo.mu.RUnlock()
		switch r.URL.Path {
		case "/index":
			_ = json.NewEncoder(w).Encode(repo.index)
		case "/package.zip":
			_, _ = w.Write(repo.payload)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(repo.server.Close)
	repo.index.Releases[0].Artifacts[0].URL = repo.server.URL + "/package.zip"
	a, err := New(Options{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if a.Store.DB.Ping() == nil {
			_ = a.Close()
		}
	})
	t.Cleanup(a.closeOriginalSessions)
	cookie := setupTestAdmin(t, a)
	originalFrontendInstall(t, a, cookie, repo, false)
	return a, cookie, repo
}

func originalFrontendInstall(t *testing.T, a *App, cookie *http.Cookie, repo *originalFrontendRepository, force bool) *httptest.ResponseRecorder {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"plugin_id": originalFrontendFixtureID, "repo_url": repo.server.URL + "/index", "force": force})
	r := callTest(t, a.Handler(), "POST", "/api/plugins/install", string(body), cookie, "")
	assertOriginalRegistrySuccess(t, r.Code, r.Body.Bytes())
	a.wg.Wait()
	return r
}

func originalFrontendReadDescriptor(t *testing.T, a *App, cookie *http.Cookie, instance string) originalFrontendDescriptor {
	t.Helper()
	r := callTest(t, a.Handler(), "GET", "/api/plugins/"+instance+"/frontend", "", cookie, "")
	var response struct {
		Data originalFrontendDescriptor `json:"data"`
	}
	if r.Code != 200 || json.Unmarshal(r.Body.Bytes(), &response) != nil || response.Data.PackageSHA256 == "" || response.Data.InstanceID != instance {
		t.Fatalf("actual frontend descriptor: %d %s", r.Code, r.Body.String())
	}
	return response.Data
}

func TestPluginOriginalFrontendRealArtifactHTTPAndAuth(t *testing.T) {
	a, cookie, _ := originalFrontendFixture(t)
	d := originalFrontendReadDescriptor(t, a, cookie, originalFrontendFixtureID)
	if d.PluginID != originalFrontendFixtureID || d.Version != "1.0" || !strings.Contains(d.EntryPath, "/frontend/"+d.PackageSHA256+"/assets/") {
		t.Fatal("descriptor identity mismatch", d)
	}
	entry := callTest(t, a.Handler(), "GET", "/api"+d.EntryPath, "", cookie, "")
	if entry.Code != 200 || !strings.Contains(entry.Header().Get("Content-Type"), "javascript") || strings.Contains(entry.Body.String(), `"request_id"`) || !strings.Contains(entry.Body.String(), "//first") {
		t.Fatalf("not actual JS bytes: %d %s", entry.Code, entry.Body.String())
	}
	if entry.Header().Get("Cache-Control") != "private, no-cache" || entry.Header().Get("ETag") == "" {
		t.Fatal("unsafe asset cache headers")
	}
	for _, name := range []string{"chunk.js", "style.css", "font.woff2"} {
		r := callTest(t, a.Handler(), "GET", strings.TrimSuffix("/api"+d.EntryPath, "remoteEntry.js")+name, "", cookie, "")
		if r.Code != 200 || !strings.Contains(r.Body.String(), "first") {
			t.Fatal("real graph member unavailable", name, r.Code)
		}
	}
	head := callTest(t, a.Handler(), "HEAD", "/api"+d.EntryPath, "", cookie, "")
	if head.Code != 200 || head.Body.Len() != 0 || head.Header().Get("Content-Length") != entry.Header().Get("Content-Length") {
		t.Fatal("HEAD differs from GET")
	}
	request := httptest.NewRequest("GET", "/api"+d.EntryPath, nil)
	request.AddCookie(cookie)
	request.Header.Set("If-None-Match", entry.Header().Get("ETag"))
	notModified := httptest.NewRecorder()
	a.Handler().ServeHTTP(notModified, request)
	if notModified.Code != 304 || notModified.Body.Len() != 0 {
		t.Fatal("valid immutable ETag not honored")
	}
	for _, target := range []string{"/api/plugins/" + originalFrontendFixtureID + "/frontend", "/api" + d.EntryPath} {
		unauthorized := callTest(t, a.Handler(), "GET", target, "", nil, "")
		if unauthorized.Code != 401 {
			t.Fatal("frontend bypassed authentication", target, unauthorized.Code)
		}
	}
	key := callTest(t, a.Handler(), "GET", "/api/plugins/"+originalFrontendFixtureID+"/api/key-status", "", cookie, "")
	if key.Code != 401 {
		t.Fatal("frontend relaxed apikey authorization", key.Code)
	}
	status := callTest(t, a.Handler(), "GET", "/api/plugins/"+originalFrontendFixtureID+"/api/status", "", cookie, "")
	if status.Code != 200 || !strings.Contains(status.Body.String(), `"version":"1.0"`) {
		t.Fatal("standard instance API unavailable", status.Code, status.Body.String())
	}
	alias := callTest(t, a.Handler(), "GET", "/api/plugins/"+originalFrontendFixtureID+"/frontend/assets/remoteEntry.js", "", cookie, "")
	if alias.Code != 307 || alias.Header().Get("Location") != "/api"+d.EntryPath || alias.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("unversioned entry route is not a safe alias")
	}
	for _, suffix := range []string{"../owned-frontend-fixture", "%2e%2e/owned-frontend-fixture", "%252e%252e/x.js", "dir%2fchunk.js", ".acquired.json", "missing.js"} {
		target := strings.TrimSuffix("/api"+d.EntryPath, "remoteEntry.js") + suffix
		r := callTest(t, a.Handler(), "GET", target, "", cookie, "")
		if r.Code == 200 || r.Code == 304 || (r.Code >= 300 && r.Code < 400) {
			t.Fatal("unsafe or missing file served/redirected", suffix, r.Code)
		}
	}
	wrong := callTest(t, a.Handler(), "GET", strings.Replace("/api"+d.EntryPath, d.PackageSHA256, strings.Repeat("0", 64), 1), "", cookie, "")
	if wrong.Code != 409 {
		t.Fatal("wrong package pin accepted", wrong.Code)
	}
	if err := os.WriteFile(filepath.Join(a.pluginPackageRoot(), originalFrontendFixtureID, "frontend/assets/style.css"), []byte("tampered"), 0600); err != nil {
		t.Fatal(err)
	}
	request = httptest.NewRequest("GET", "/api"+d.EntryPath, nil)
	request.AddCookie(cookie)
	request.Header.Set("If-None-Match", entry.Header().Get("ETag"))
	tampered := httptest.NewRecorder()
	a.Handler().ServeHTTP(tampered, request)
	if tampered.Code == 200 || tampered.Code == 304 {
		t.Fatal("304 bypassed whole-graph integrity")
	}
}
