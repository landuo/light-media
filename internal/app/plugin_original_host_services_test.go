package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"light-media/internal/plugins"
)

func ownedHostServicesFixture(t *testing.T) (*App, *http.Cookie, *originalPluginInstance) {
	t.Helper()
	binary := os.Getenv("OWNED_HOST_SERVICES_FIXTURE_BINARY")
	if binary == "" {
		t.Fatal("OWNED_HOST_SERVICES_FIXTURE_BINARY must name the separately built standard concurrent SDK worker")
	}
	a := newTestApp(t)
	cookie := setupTestAdmin(t, a)
	manifest := installOwnedResourceProcessWorker(t, a, cookie, binary)
	value, ok := a.pluginRunningInstances.Load(manifest.PluginID)
	if !ok {
		t.Fatal("actual SDK object was not loaded")
	}
	return a, cookie, value.(*originalPluginInstance)
}

func ownedHostServicesCall(t *testing.T, object *originalPluginInstance, method string, input any) (json.RawMessage, error) {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"method": method, "input": input})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result, err := object.callOriginalMethod(ctx, "resource", raw)
	return result.Result, err
}

func hostServiceItem(t *testing.T, path string) *plugins.ModuleFileItem {
	t.Helper()
	item, err := plugins.NewModuleFileItem(map[string]any{"storage": "local", "path": path, "type": "dir"})
	if err != nil {
		t.Fatal(err)
	}
	return item
}

func TestOriginalHostStorageActualSDKPinnedSourceFilesCloneAndColdRecovery(t *testing.T) {
	a, cookie, object := ownedHostServicesFixture(t)
	ctx := context.Background()
	folder := t.TempDir()
	for _, path := range []string{"nested", ".hidden"} {
		if err := os.Mkdir(filepath.Join(folder, path), 0750); err != nil {
			t.Fatal(err)
		}
	}
	for path, value := range map[string]string{"nested/child.txt": "child", "top.txt": "top", ".hidden/excluded.txt": "hidden"} {
		if err := os.WriteFile(filepath.Join(folder, path), []byte(value), 0640); err != nil {
			t.Fatal(err)
		}
	}
	settings := a.Settings()
	settings.AllowedRoots = []string{folder}
	settings.Roots = nil
	if err := a.saveSettings(ctx, settings); err != nil {
		t.Fatal(err)
	}
	var oracle struct {
		SourceCommit string `json:"source_commit"`
		Cases        []struct {
			Case     string              `json:"case"`
			Items    []map[string]string `json:"items"`
			Returned bool                `json:"returned"`
			Exists   bool                `json:"exists"`
		} `json:"cases"`
	}
	oraw, err := os.ReadFile("../../testdata/plugin-host-services-20261031/storage-source-oracle.json")
	if err != nil || json.Unmarshal(oraw, &oracle) != nil || oracle.SourceCommit != "8a83efeacd3e26b82be495e17d50146e31c96a60" || len(oracle.Cases) != 7 {
		t.Fatal("fixed source oracle missing", err)
	}
	for _, sample := range oracle.Cases {
		path := folder
		switch sample.Case {
		case "file", "delete_file":
			path = filepath.Join(folder, "top.txt")
		case "delete_directory":
			path = filepath.Join(folder, "nested")
		case "missing", "delete_missing":
			path = filepath.Join(folder, "absent")
		}
		request := plugins.StorageFilesRequest{FileItem: hostServiceItem(t, path), Recursion: sample.Case == "recursive"}
		method := plugins.StorageListCapability
		if strings.HasPrefix(sample.Case, "delete_") {
			method = plugins.StorageDeleteCapability
		}
		result, err := ownedHostServicesCall(t, object, method, request)
		if err != nil {
			t.Fatalf("%s actual SDK: %v", sample.Case, err)
		}
		if method == plugins.StorageListCapability {
			var actual []map[string]any
			if json.Unmarshal(result, &actual) != nil {
				t.Fatal(string(result))
			}
			projected := []map[string]string{}
			for _, item := range actual {
				relative, err := filepath.Rel(folder, strings.TrimSuffix(item["path"].(string), "/"))
				if err != nil {
					t.Fatal(err)
				}
				projected = append(projected, map[string]string{"type": item["type"].(string), "path": filepath.ToSlash(relative)})
			}
			if !reflect.DeepEqual(projected, sample.Items) {
				t.Fatalf("%s source=%v target=%v", sample.Case, sample.Items, projected)
			}
		} else {
			var actual bool
			if json.Unmarshal(result, &actual) != nil || actual != sample.Returned {
				t.Fatal("source delete result", sample.Case, string(result))
			}
			_, err := os.Stat(path)
			if (err == nil) != sample.Exists {
				t.Fatal("source delete side effect", sample.Case, err)
			}
		}
	}
	if _, err := ownedHostServicesCall(t, object, plugins.StorageExistsCapability, plugins.StorageFilesRequest{FileItem: hostServiceItem(t, filepath.Dir(folder))}); err == nil {
		t.Fatal("outside authorized root accepted")
	}
	if _, err := ownedHostServicesCall(t, object, plugins.StorageDeleteCapability, plugins.StorageFilesRequest{FileItem: hostServiceItem(t, folder)}); err == nil {
		t.Fatal("authorized root deletion accepted")
	}
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(folder, "escape")); err != nil {
		t.Fatal(err)
	}
	if _, err := ownedHostServicesCall(t, object, plugins.StorageListCapability, plugins.StorageFilesRequest{FileItem: hostServiceItem(t, filepath.Join(folder, "escape"))}); err == nil {
		t.Fatal("symlink accepted")
	}
	if _, err := ownedHostServicesCall(t, object, plugins.StorageExistsCapability, plugins.StorageFilesRequest{FileItem: hostServiceItem(t, filepath.Join(folder, "absent"))}); err != nil {
		t.Fatal(err)
	}
	response := callTest(t, a.Handler(), "POST", "/api/plugins/OwnedHostServices/clone", `{"suffix":"copy"}`, cookie, "")
	assertOriginalRegistrySuccess(t, response.Code, response.Body.Bytes())
	clone, ok := a.pluginRunningInstances.Load("OwnedHostServicescopy")
	if !ok {
		t.Fatal("actual clone missing")
	}
	if raw, err := ownedHostServicesCall(t, clone.(*originalPluginInstance), plugins.StorageExistsCapability, plugins.StorageFilesRequest{FileItem: hostServiceItem(t, folder)}); err != nil || string(raw) != "true" {
		t.Fatal("clone actual storage", string(raw), err)
	}
	sourceData, err := a.pluginStore.Data(ctx, "OwnedHostServices", "last")
	if err != nil {
		t.Fatal(err)
	}
	cloneData, err := a.pluginStore.Data(ctx, "OwnedHostServicescopy", "last")
	if err != nil || string(cloneData.Value) != "true" || string(sourceData.Value) == "true" {
		t.Fatal("instance result data isolation", err)
	}
	dir := a.Dir
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	cold, err := New(Options{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer cold.Close()
	cold.Start(context.Background())
	loaded, ok := cold.pluginRunningInstances.Load("OwnedHostServicescopy")
	if !ok {
		t.Fatal("cold SDK clone not restored")
	}
	if raw, err := ownedHostServicesCall(t, loaded.(*originalPluginInstance), plugins.StorageExistsCapability, plugins.StorageFilesRequest{FileItem: hostServiceItem(t, filepath.Join(folder, "nested"))}); err != nil || string(raw) != "false" {
		t.Fatal("cold source deletion persisted", string(raw), err)
	}
	response = callTest(t, cold.Handler(), "DELETE", "/api/plugins/OwnedHostServicescopy", "", cookie, "")
	assertOriginalRegistrySuccess(t, response.Code, response.Body.Bytes())
	if _, ok := cold.pluginRunningInstances.Load("OwnedHostServicescopy"); ok {
		t.Fatal("uninstalled clone retained running object")
	}
}

func TestOriginalHostMediaServersActualSDKProtocolAndRefresh(t *testing.T) {
	a, _, object := ownedHostServicesFixture(t)
	ctx := context.Background()
	var scans atomic.Int32
	var reject atomic.Bool
	const guid = "11111111111111111111111111111111"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v/api/v1/sys/version":
			fmt.Fprint(w, `{"code":0,"data":{"version":"1.0.0","mediasrvVersion":"1.0.0"}}`)
		case "/v/api/v1/login":
			fmt.Fprint(w, `{"code":0,"data":{"token":"owned-session"}}`)
		case "/v/api/v1/user/info":
			fmt.Fprint(w, `{"code":0,"data":{"is_admin":1}}`)
		case "/v/api/v1/mdb/list":
			fmt.Fprint(w, `{"code":0,"data":[{"guid":"`+guid+`","name":"owned","category":"Mix","dir_list":["/owned/library/"]}]}`)
		case "/v/api/v1/mdb/scan/" + guid:
			body, _ := io.ReadAll(r.Body)
			if len(body) != 0 || r.Method != "POST" || r.Header.Get("Content-Type") != "application/json" {
				t.Error("fixed source empty POST protocol", string(body))
			}
			scans.Add(1)
			if reject.Load() {
				fmt.Fprint(w, `{"code":1,"message":"private synthetic failure"}`)
			} else {
				fmt.Fprint(w, `{"code":0,"data":true}`)
			}
		case "/v/api/v1/task/running":
			fmt.Fprint(w, `{"code":0,"data":false}`)
		default:
			t.Error("unexpected media protocol", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	config := a.Settings()
	config.FNOS = FNConfig{Host: server.URL, Username: "owned-user", Password: "OWNED_ONLY_PASSWORD", Enabled: true}
	if err := a.saveSettings(ctx, config); err != nil {
		t.Fatal(err)
	}
	for _, probe := range []bool{false, true} {
		raw, err := ownedHostServicesCall(t, object, plugins.MediaServersListCapability, plugins.MediaServersRequest{Probe: probe})
		if err != nil {
			t.Fatal(err)
		}
		var actual []plugins.MediaServer
		if json.Unmarshal(raw, &actual) != nil || len(actual) != 1 || actual[0].Configured != true || probe && (actual[0].Connected == nil || !*actual[0].Connected) || !probe && actual[0].Connected != nil {
			t.Fatal("configured/probed distinction", string(raw))
		}
		if strings.Contains(string(raw), "OWNED_ONLY_PASSWORD") || strings.Contains(string(raw), server.URL) {
			t.Fatal("server secrets returned")
		}
	}
	raw, err := ownedHostServicesCall(t, object, plugins.MediaServerLibraryRefreshCapability, plugins.MediaRefreshLibraryRequest{Name: "fnos", LibraryID: guid})
	if err != nil {
		t.Fatal(err)
	}
	var result plugins.MediaRefreshResult
	if json.Unmarshal(raw, &result) != nil || !result.Accepted || result.Result == nil || !*result.Result || result.AllLibraries {
		t.Fatal("bounded explicit library did not confirm", string(raw))
	}
	if scans.Load() != 1 {
		t.Fatal("actual scan side effects", scans.Load())
	}
	if _, err := ownedHostServicesCall(t, object, plugins.MediaServerLibraryRefreshCapability, plugins.MediaRefreshLibraryRequest{Name: "fnos", LibraryID: "22222222222222222222222222222222"}); err == nil || scans.Load() != 1 {
		t.Fatal("unconfigured GUID scan accepted", err)
	}
	reject.Store(true)
	if _, err := ownedHostServicesCall(t, object, plugins.MediaServerLibraryRefreshCapability, plugins.MediaRefreshLibraryRequest{Name: "fnos", LibraryID: guid}); err == nil {
		t.Fatal("scan failure falsely successful")
	}
	config.FNOS.Enabled = false
	if err := a.saveSettings(ctx, config); err != nil {
		t.Fatal(err)
	}
	if raw, err := ownedHostServicesCall(t, object, plugins.MediaServersListCapability, plugins.MediaServersRequest{}); err != nil || string(raw) != "[]" {
		t.Fatal("disabled service source default", string(raw), err)
	}
}

func TestOriginalHostMediaRecognitionAndManagerActualSDK(t *testing.T) {
	a, cookie, object := ownedHostServicesFixture(t)
	model := json.RawMessage(`{"title":"Pinned result","type":"电影","source":"themoviedb","tmdb_id":123,"year":"2024"}`)
	configuration, _ := json.Marshal(map[string]any{"enabled": true, "recognized": model})
	save := callTest(t, a.Handler(), "PUT", "/api/plugins/OwnedHostServices", string(configuration), cookie, "")
	assertOriginalRegistrySuccess(t, save.Code, save.Body.Bytes())
	result, err := ownedHostServicesCall(t, object, plugins.MediaRecognizeCapability, plugins.MediaRecognizeRequest{TMDBID: 123, Type: "movie", Meta: map[string]any{"tmdbid": 999, "media_source": "douban", "media_id": "12345"}})
	if err != nil {
		t.Fatal("real nested recognition", err)
	}
	var observed map[string]any
	if json.Unmarshal(result, &observed) != nil || observed["title"] != "Pinned result" {
		t.Fatal(string(result))
	}
	call, err := a.pluginStore.Data(context.Background(), "OwnedHostServices", "recognize_call")
	if err != nil {
		t.Fatal(err)
	}
	var input struct {
		Kwargs map[string]any `json:"kwargs"`
	}
	if json.Unmarshal(call.Value, &input) != nil || input.Kwargs["source"] != "douban" || input.Kwargs["tmdbid"] != nil || input.Kwargs["doubanid"] != "12345" {
		t.Fatal("fixed source meta/source precedence", string(call.Value))
	}
	var oracle struct {
		Recognition []struct {
			Request plugins.MediaRecognizeRequest `json:"request"`
			Kwargs  json.RawMessage               `json:"kwargs"`
		} `json:"recognition"`
	}
	oraw, err := os.ReadFile("../../testdata/plugin-host-services-20261031/storage-source-oracle.json")
	if err != nil || json.Unmarshal(oraw, &oracle) != nil || len(oracle.Recognition) != 5 {
		t.Fatal("fixed ChainBase recognition oracle", err)
	}
	for _, sample := range oracle.Recognition {
		if _, err := ownedHostServicesCall(t, object, plugins.MediaRecognizeCapability, sample.Request); err != nil {
			t.Fatal("source kwargs replay", err)
		}
		call, err := a.pluginStore.Data(context.Background(), "OwnedHostServices", "recognize_call")
		if err != nil {
			t.Fatal(err)
		}
		var observed struct {
			Kwargs map[string]any `json:"kwargs"`
		}
		var expected map[string]any
		if json.Unmarshal(call.Value, &observed) != nil || json.Unmarshal(sample.Kwargs, &expected) != nil || !reflect.DeepEqual(observed.Kwargs, expected) {
			t.Fatal("fixed source request/ID/cache/meta precedence", string(sample.Kwargs), string(call.Value))
		}
	}
	result, err = ownedHostServicesCall(t, object, plugins.PluginManagerReadCapability, plugins.PluginManagerRequest{IncludeState: true})
	if err != nil {
		t.Fatal("actual self/all state reentry", err)
	}
	var states []plugins.PluginHostState
	if json.Unmarshal(result, &states) != nil || len(states) != 1 || states[0].Enabled == nil || !*states[0].Enabled || !states[0].Loaded {
		t.Fatal("actual live state", string(result))
	}
	for _, action := range []string{"services.remove", "services.refresh"} {
		if _, err := ownedHostServicesCall(t, object, plugins.PluginManagerControlCapability, plugins.PluginManagerRequest{InstanceID: "OwnedHostServices", Action: action}); err != nil {
			t.Fatal(action, err)
		}
	}
	response := callTest(t, a.Handler(), "POST", "/api/plugins/OwnedHostServices/clone", `{"suffix":"copy"}`, cookie, "")
	assertOriginalRegistrySuccess(t, response.Code, response.Body.Bytes())
	if _, err := ownedHostServicesCall(t, object, plugins.PluginManagerControlCapability, plugins.PluginManagerRequest{InstanceID: "OwnedHostServicescopy", Action: "init", Config: json.RawMessage(`{"enabled":true}`)}); err != nil {
		t.Fatal("direct init supplied config", err)
	}
	clone, ok := a.pluginRunningInstances.Load("OwnedHostServicescopy")
	if !ok {
		t.Fatal("clone missing")
	}
	if enabled, err := clone.(*originalPluginInstance).getState(context.Background()); err != nil || !enabled {
		t.Fatal("init did not use provided live config", enabled, err)
	}
	stored, err := a.originalManagerConfiguration(context.Background(), "OwnedHostServicescopy")
	if err != nil {
		t.Fatal(err)
	}
	var storedConfig map[string]any
	if json.Unmarshal(stored, &storedConfig) != nil || storedConfig["enabled"] != false {
		t.Fatal("init unexpectedly persisted supplied config", string(stored))
	}
	for _, action := range []string{"init", "reload", "stop", "reload", "uninstall"} {
		result, err = ownedHostServicesCall(t, object, plugins.PluginManagerControlCapability, plugins.PluginManagerRequest{InstanceID: "OwnedHostServicescopy", Action: action})
		if err != nil {
			t.Fatal("actual cross lifecycle", action, err)
		}
		var control plugins.PluginControlResult
		if json.Unmarshal(result, &control) != nil || !control.Accepted || !control.Completed {
			t.Fatal("completed cross operation", action, string(result))
		}
	}
	if _, ok := a.pluginRunningInstances.Load("OwnedHostServicescopy"); ok {
		t.Fatal("cross uninstall not applied")
	}
	a.pluginOriginalOperationMu.Lock()
	_, err = ownedHostServicesCall(t, object, plugins.PluginManagerControlCapability, plugins.PluginManagerRequest{InstanceID: "Other", Action: "uninstall"})
	a.pluginOriginalOperationMu.Unlock()
	if err == nil {
		t.Fatal("reentrant operation lock falsely succeeded")
	}
}
