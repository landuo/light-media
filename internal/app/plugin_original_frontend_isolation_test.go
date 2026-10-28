package app

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"light-media/internal/plugins/artifacts"
)

func replaceOriginalFrontendRepository(t *testing.T, repo *originalFrontendRepository, binaryEnv, marker string) {
	t.Helper()
	index, payload := originalFrontendArchive(t, binaryEnv, marker)
	index.Releases[0].Artifacts[0].URL = repo.server.URL + "/package.zip"
	repo.mu.Lock()
	repo.index, repo.payload = index, payload
	repo.mu.Unlock()
}

func TestPluginOriginalFrontendCloneUpgradeSameVersionAndForceFailure(t *testing.T) {
	a, cookie, repo := originalFrontendFixture(t)
	const source = originalFrontendFixtureID
	const clone = originalFrontendFixtureID + "copy"
	saved := callTest(t, a.Handler(), "PUT", "/api/plugins/"+source, `{"enabled":false,"marker":"source"}`, cookie, "")
	assertOriginalRegistrySuccess(t, saved.Code, saved.Body.Bytes())
	created := callTest(t, a.Handler(), "POST", "/api/plugins/"+source+"/clone", `{"suffix":"Copy","version":"99.9"}`, cookie, "")
	assertOriginalRegistrySuccess(t, created.Code, created.Body.Bytes())
	a.wg.Wait()
	sourceBefore := originalFrontendReadDescriptor(t, a, cookie, source)
	cloneBefore := originalFrontendReadDescriptor(t, a, cookie, clone)
	if cloneBefore.PluginID != source || cloneBefore.Version != "1.0" || cloneBefore.PackageSHA256 == sourceBefore.PackageSHA256 || cloneBefore.EntryPath == sourceBefore.EntryPath {
		t.Fatal("clone UI confused display version or shared URL")
	}
	cloneBytes := callTest(t, a.Handler(), "GET", "/api"+cloneBefore.EntryPath, "", cookie, "").Body.Bytes()
	cloneObject, ok := a.pluginRunningInstances.Load(clone)
	if !ok {
		t.Fatal("clone has no real process")
	}
	changed := callTest(t, a.Handler(), "PUT", "/api/plugins/"+clone, `{"enabled":false,"marker":"clone"}`, cookie, "")
	assertOriginalRegistrySuccess(t, changed.Code, changed.Body.Bytes())
	a.wg.Wait()
	for id, marker := range map[string]string{source: "source", clone: "clone"} {
		response := callTest(t, a.Handler(), "GET", "/api/plugins/"+id+"/api/status", "", cookie, "")
		if response.Code != 200 || !strings.Contains(response.Body.String(), `"marker":"`+marker+`"`) {
			t.Fatal("real API namespace leaked", id, response.Body.String())
		}
	}
	// Replace actual bytes at the SAME real version while the old loaded object
	// is still alive. A source label is not permission to serve the new graph.
	replaceOriginalFrontendRepository(t, repo, "OWNED_FRONTEND_FIXTURE_BINARY", "same-version-new-graph")
	a.pluginImplementationMu.Lock()
	acquired, err := artifacts.Acquire(context.Background(), artifacts.Request{Root: a.pluginPackageRoot(), RepositoryURL: repo.server.URL + "/index", PluginID: source, Platform: runtime.GOOS + "-" + runtime.GOARCH, Force: true, Validate: validatePluginArtifactContract}, repo.server.Client())
	a.pluginImplementationMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if acquired.PackageSHA256 == sourceBefore.PackageSHA256 || acquired.Manifest.Version != sourceBefore.Version {
		t.Fatal("same-version byte replacement did not change pin")
	}
	for _, target := range []string{"/api" + sourceBefore.EntryPath, "/api/plugins/" + source + "/frontend"} {
		response := callTest(t, a.Handler(), "GET", target, "", cookie, "")
		if response.Code != 409 {
			t.Fatal("old object served replacement bytes under old identity", target, response.Code)
		}
	}
	stillClone := callTest(t, a.Handler(), "GET", "/api"+cloneBefore.EntryPath, "", cookie, "")
	if stillClone.Code != 200 || !bytes.Equal(stillClone.Body.Bytes(), cloneBytes) {
		t.Fatal("same-version source replacement changed clone graph")
	}
	originalFrontendInstall(t, a, cookie, repo, true)
	sameVersion := originalFrontendReadDescriptor(t, a, cookie, source)
	if sameVersion.PackageSHA256 != acquired.PackageSHA256 || sameVersion.Version != "1.0" {
		t.Fatal("new source object did not capture actual new content pin")
	}
	newBytes := callTest(t, a.Handler(), "GET", "/api"+sameVersion.EntryPath, "", cookie, "")
	if newBytes.Code != 200 || !strings.Contains(newBytes.Body.String(), "same-version-new-graph") {
		t.Fatal("new object has wrong source graph")
	}
	oldURL := callTest(t, a.Handler(), "GET", "/api"+sourceBefore.EntryPath, "", cookie, "")
	if oldURL.Code != 409 {
		t.Fatal("old URL silently upgraded")
	}
	// A real, separately compiled version 2 replaces only the source instance.
	replaceOriginalFrontendRepository(t, repo, "OWNED_FRONTEND_FIXTURE_BINARY_V2", "second-version")
	originalFrontendInstall(t, a, cookie, repo, true)
	versionTwo := originalFrontendReadDescriptor(t, a, cookie, source)
	if versionTwo.Version != "2.0" || versionTwo.PackageSHA256 == sameVersion.PackageSHA256 {
		t.Fatal("real version 2 not installed")
	}
	cloneAfter := originalFrontendReadDescriptor(t, a, cookie, clone)
	if cloneAfter.Version != "1.0" || cloneAfter.PackageSHA256 != cloneBefore.PackageSHA256 {
		t.Fatal("source upgrade changed clone content identity")
	}
	if value, _ := a.pluginRunningInstances.Load(clone); value != cloneObject {
		t.Fatal("source upgrade replaced clone process")
	}
	stillClone = callTest(t, a.Handler(), "GET", "/api"+cloneBefore.EntryPath, "", cookie, "")
	if stillClone.Code != 200 || !bytes.Equal(stillClone.Body.Bytes(), cloneBytes) {
		t.Fatal("source upgrade modified clone bytes")
	}
	// Force failure retains an old running object but removes its disk code.
	// Asset lookup must fail closed, not borrow a clone or an embedded frontend.
	repo.mu.Lock()
	repo.index.Releases[0].Artifacts[0].SHA256 = strings.Repeat("0", 64)
	repo.mu.Unlock()
	body, _ := json.Marshal(map[string]any{"plugin_id": source, "repo_url": repo.server.URL + "/index", "force": true})
	failed := callTest(t, a.Handler(), "POST", "/api/plugins/install", string(body), cookie, "")
	if failed.Code != 200 || !strings.Contains(failed.Body.String(), `"success":false`) {
		t.Fatal("force failure fabricated success", failed.Code, failed.Body.String())
	}
	if _, err := os.Stat(filepath.Join(a.pluginPackageRoot(), source)); !os.IsNotExist(err) {
		t.Fatal("force fixture did not remove source tree", err)
	}
	oldAsset := callTest(t, a.Handler(), "GET", "/api"+versionTwo.EntryPath, "", cookie, "")
	if oldAsset.Code != 409 {
		t.Fatal("missing disk code served stale or borrowed graph", oldAsset.Code)
	}
	stillClone = callTest(t, a.Handler(), "GET", "/api"+cloneBefore.EntryPath, "", cookie, "")
	if stillClone.Code != 200 || !bytes.Equal(stillClone.Body.Bytes(), cloneBytes) {
		t.Fatal("source force failure affected independent clone")
	}
}

func TestPluginOriginalFrontendMountAndRenderMode(t *testing.T) {
	a, cookie, _ := originalFrontendFixture(t)
	d := originalFrontendReadDescriptor(t, a, cookie, originalFrontendFixtureID)
	login := callTest(t, a.Handler(), "POST", "/light-media/api/auth/login", `{"username":"admin","password":"a-strong-test-password"}`, nil, "")
	if login.Code != 200 {
		t.Fatal("mount login failed", login.Code)
	}
	var secure *http.Cookie
	for _, c := range login.Result().Cookies() {
		if c.Name == "__Secure-lm_session" {
			secure = c
		}
	}
	if secure == nil {
		t.Fatal("mount cookie missing")
	}
	resource := callTest(t, a.Handler(), "GET", "/light-media/api"+d.EntryPath, "", secure, "")
	if resource.Code != 200 || !strings.Contains(resource.Body.String(), "//first") {
		t.Fatal("mounted asset failed", resource.Code)
	}
	alias := callTest(t, a.Handler(), "GET", "/light-media/api/plugins/"+originalFrontendFixtureID+"/frontend/assets/remoteEntry.js", "", secure, "")
	if alias.Code != 307 || alias.Header().Get("Location") != "/light-media/api"+d.EntryPath {
		t.Fatal("asset alias escaped HTTPS mount", alias.Code, alias.Header().Get("Location"))
	}
	changed := callTest(t, a.Handler(), "PUT", "/api/plugins/"+originalFrontendFixtureID, `{"enabled":false,"wrong_render_root":true}`, cookie, "")
	assertOriginalRegistrySuccess(t, changed.Code, changed.Body.Bytes())
	a.wg.Wait()
	mismatch := callTest(t, a.Handler(), "GET", "/api/plugins/"+originalFrontendFixtureID+"/frontend", "", cookie, "")
	if mismatch.Code != 409 {
		t.Fatal("worker selected an unverified frontend directory", mismatch.Code)
	}
}

func TestPluginOriginalFrontendRestartReadsExactInstalledGraph(t *testing.T) {
	a, cookie, _ := originalFrontendFixture(t)
	d := originalFrontendReadDescriptor(t, a, cookie, originalFrontendFixtureID)
	before := callTest(t, a.Handler(), "GET", "/api"+d.EntryPath, "", cookie, "").Body.Bytes()
	dir := a.Dir
	a.closeOriginalSessions()
	a.wg.Wait()
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := New(Options{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	// Test startup explicitly, without claiming background scheduler execution.
	if reopened.startOriginalPlugin(context.Background(), originalFrontendFixtureID) == nil {
		t.Fatal("real package did not restart")
	}
	t.Cleanup(reopened.closeOriginalSessions)
	after := originalFrontendReadDescriptor(t, reopened, cookie, originalFrontendFixtureID)
	if after.PackageSHA256 != d.PackageSHA256 || after.EntryPath != d.EntryPath {
		t.Fatal("restart changed installed asset identity")
	}
	resource := callTest(t, reopened.Handler(), "GET", "/api"+after.EntryPath, "", cookie, "")
	if resource.Code != 200 || !bytes.Equal(resource.Body.Bytes(), before) {
		t.Fatal("restart substituted different asset bytes")
	}
}
