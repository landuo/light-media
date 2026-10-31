package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"light-media/internal/plugins"
	"light-media/internal/plugins/artifacts"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"runtime"
	"sync/atomic"
	"testing"
	"time"
)

type ownedOCRWorkerResult struct {
	Value       *plugins.OCRValue `json:"value"`
	ErrorClass  string            `json:"error_class"`
	PID         int               `json:"pid"`
	StaleDenied bool              `json:"stale_denied"`
}

func ownedOCRWorkerFixture(t *testing.T, host string) (*App, *http.Cookie, *atomic.Int32) {
	t.Helper()
	executable := os.Getenv("OWNED_OCR_FIXTURE_BINARY")
	if executable == "" {
		t.Fatal("OWNED_OCR_FIXTURE_BINARY must bind separately built owned SDK worker")
	}
	payload, err := os.ReadFile(executable)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	manifestRaw, err := exec.CommandContext(ctx, executable, "--manifest").Output()
	cancel()
	if err != nil {
		t.Fatal(err)
	}
	var manifest plugins.Manifest
	if err = json.Unmarshal(manifestRaw, &manifest); err != nil || manifest.Validate() != nil {
		t.Fatal("invalid actual owned manifest")
	}
	if manifest.PluginID != "OwnedOCR" || manifest.Version != "1.0" || len(manifest.Capabilities) != 1 || manifest.Capabilities[0] != plugins.SharedOCRGetCaptchaText || len(manifest.APIs) != 1 || manifest.APIs[0].Path != "/query" {
		t.Fatal("owned fixture contract differs")
	}
	digest := sha256.Sum256(payload)
	hash := hex.EncodeToString(digest[:])
	index := artifacts.Index{SchemaVersion: 1, PluginID: manifest.PluginID, CurrentVersion: manifest.Version, Releases: []artifacts.Release{{Version: manifest.Version, Manifest: manifest, Artifacts: []artifacts.Artifact{{Platform: runtime.GOOS + "-" + runtime.GOARCH, Format: "executable", SHA256: hash, ExecutableSHA256: hash, Size: int64(len(payload)), HostContract: pluginResourcesSDKContract}}}}}
	var indexHits, artifactHits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			t.Error("owned artifact write requested")
			w.WriteHeader(502)
			return
		}
		switch r.URL.Path {
		case "/index":
			indexHits.Add(1)
			_ = json.NewEncoder(w).Encode(index)
		case "/worker":
			artifactHits.Add(1)
			_, _ = w.Write(payload)
		default:
			t.Error("unexpected artifact route")
			w.WriteHeader(502)
		}
	}))
	t.Cleanup(server.Close)
	index.Releases[0].Artifacts[0].URL = server.URL + "/worker"
	a := newOwnedOCRApp(t, host)
	t.Cleanup(a.closeOriginalSessions)
	cookie := setupTestAdmin(t, a)
	body, _ := json.Marshal(map[string]string{"plugin_id": "OwnedOCR", "repo_url": server.URL + "/index"})
	result := callTest(t, a.Handler(), "POST", "/api/plugins/install", string(body), cookie, "")
	assertOriginalRegistrySuccess(t, result.Code, result.Body.Bytes())
	a.wg.Wait()
	value, ok := a.pluginRunningInstances.Load("OwnedOCR")
	if !ok {
		t.Fatal("real owned SDK instance absent")
	}
	object := value.(*originalPluginInstance)
	if !object.external || object.hostContract != pluginResourcesSDKContract {
		t.Fatal("worker replaced by in-process fake")
	}
	if indexHits.Load() != 1 || artifactHits.Load() != 1 {
		t.Fatal("artifact not acquired once")
	}
	t.Cleanup(func() {
		if indexHits.Load() != 1 || artifactHits.Load() != 1 {
			t.Error("cold initialization reacquired owned artifact")
		}
	})
	return a, cookie, &indexHits
}
func ownedOCRHTTPQuery(t *testing.T, a *App, cookie *http.Cookie) ownedOCRWorkerResult {
	t.Helper()
	response := callTest(t, a.Handler(), "POST", "/api/v1/plugin/OwnedOCR/query", `{"image_b64":"QQ"}`, cookie, "")
	if response.Code != 200 {
		t.Fatalf("owned standard query HTTP %d %s", response.Code, response.Body)
	}
	var result ownedOCRWorkerResult
	if json.Unmarshal(response.Body.Bytes(), &result) != nil || result.PID <= 0 || result.PID == os.Getpid() || !result.StaleDenied {
		t.Fatal("not a distinct scoped real SDK worker")
	}
	return result
}
func TestSharedOCRActualWorkerFailureRecoveryCold(t *testing.T) {
	var mode, calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/captcha/base64" {
			t.Error("unexpected owned OCR route")
			w.WriteHeader(502)
			return
		}
		calls.Add(1)
		raw, _ := io.ReadAll(r.Body)
		if string(raw) != `{"base64_img": "QQ=="}` || r.Header.Get("Cookie") != "" || r.Header.Get("Authorization") != "" {
			t.Errorf("owned source request differs: %s", raw)
		}
		if mode.Load() == 2 {
			connection, _, err := w.(http.Hijacker).Hijack()
			if err != nil {
				t.Error(err)
				return
			}
			_ = connection.Close()
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if mode.Load() == 1 {
			_, _ = io.WriteString(w, `broken-json`)
		} else {
			_, _ = io.WriteString(w, `{"result":"SDK"}`)
		}
	}))
	defer server.Close()
	a, cookie, indexHits := ownedOCRWorkerFixture(t, server.URL)
	if response := callTest(t, a.Handler(), "POST", "/api/v1/plugin/OwnedOCR/query", `{"image_b64":"QQ"}`, nil, ""); response.Code != 401 {
		t.Fatal("anonymous SDK route accepted")
	}
	if response := callTest(t, a.Handler(), "GET", "/api/v1/plugin/OwnedOCR/query", "", cookie, ""); response.Code != 405 {
		t.Fatal("wrong SDK HTTP method accepted")
	}
	if calls.Load() != 0 {
		t.Fatal("install/initialization/auth control replayed OCR")
	}
	warm := ownedOCRHTTPQuery(t, a, cookie)
	if warm.Value == nil || string(warm.Value.Points) != "SDK" || warm.ErrorClass != "" {
		t.Fatal("warm result differs")
	}
	mode.Store(1)
	failure := ownedOCRHTTPQuery(t, a, cookie)
	if failure.PID != warm.PID || failure.ErrorClass != "JSONDecodeError" || failure.Value != nil {
		t.Fatal("typed JSON error/reused worker differs")
	}
	mode.Store(2)
	empty := ownedOCRHTTPQuery(t, a, cookie)
	if empty.PID != warm.PID || empty.ErrorClass != "" || empty.Value == nil || empty.Value.Kind != "string" || len(empty.Value.Points) != 0 {
		t.Fatal("network failure did not retain empty source result")
	}
	mode.Store(0)
	recovery := ownedOCRHTTPQuery(t, a, cookie)
	if recovery.PID != warm.PID || recovery.Value == nil || string(recovery.Value.Points) != "SDK" {
		t.Fatal("warm recovery differs")
	}
	if calls.Load() != 4 {
		t.Fatal("warm request count differs")
	}
	dir := a.Dir
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	host := server.URL
	cold, err := New(Options{DataDir: dir, OCRHost: &host})
	if err != nil {
		t.Fatal(err)
	}
	defer cold.Close()
	if err = cold.initializeOriginalPlugins(context.Background()); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 4 || indexHits.Load() != 1 {
		t.Fatal("cold initialize replayed OCR or reinstall")
	}
	login := callTest(t, cold.Handler(), "POST", "/api/auth/login", `{"username":"admin","password":"a-strong-test-password"}`, nil, "")
	if login.Code != 200 {
		t.Fatal("cold actual login failed")
	}
	var coldCookie *http.Cookie
	for _, value := range login.Result().Cookies() {
		if value.Name == "lm_session" {
			coldCookie = value
		}
	}
	if coldCookie == nil {
		t.Fatal("cold session cookie absent")
	}
	restored := ownedOCRHTTPQuery(t, cold, coldCookie)
	if restored.PID == warm.PID || restored.Value == nil || string(restored.Value.Points) != "SDK" || calls.Load() != 5 {
		t.Fatal("cold independent SDK worker/result/count differs")
	}
	if err = cold.Close(); err != nil {
		t.Fatal(err)
	}
	t.Logf("new actual owned SDK workers %d -> %d, five synthetic HTTP calls; not OCR engine quality", warm.PID, restored.PID)
}
