package app

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"testing"
	"time"

	"light-media/internal/plugins"
	"light-media/internal/plugins/artifacts"
)

func verifyOwnedProductionBrowserDownload(t *testing.T, dir, origin string, request func(string, string, any, int) []byte, success func(string, string, any), stop, start func()) {
	t.Helper()
	worker := os.Getenv("OWNED_TRANSFER_MODULE_PROCESS_BINARY")
	payload, err := os.ReadFile(worker)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := exec.Command(worker, "--manifest").Output()
	var manifest plugins.Manifest
	if err != nil || json.Unmarshal(raw, &manifest) != nil || manifest.Validate() != nil || manifest.PluginID != ownedModuleID {
		t.Fatal("actual module transfer worker", err)
	}
	digest := sha256.Sum256(payload)
	sum := hex.EncodeToString(digest[:])
	index := artifacts.Index{SchemaVersion: 1, PluginID: manifest.PluginID, CurrentVersion: manifest.Version, Releases: []artifacts.Release{{Version: manifest.Version, Manifest: manifest, Artifacts: []artifacts.Artifact{{Platform: runtime.GOOS + "-" + runtime.GOARCH, Format: "executable", SHA256: sum, ExecutableSHA256: sum, Size: int64(len(payload)), HostContract: pluginResourcesSDKContract}}}}}
	repository := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/index":
			_ = json.NewEncoder(w).Encode(index)
		case "/worker":
			_, _ = w.Write(payload)
		default:
			http.NotFound(w, r)
		}
	}))
	defer repository.Close()
	index.Releases[0].Artifacts[0].URL = repository.URL + "/worker"
	success("POST", "/api/plugins/install", map[string]any{"plugin_id": manifest.PluginID, "repo_url": repository.URL + "/index"})

	success("POST", "/api/plugins/"+manifest.PluginID+"/clone", map[string]any{"suffix": "Download"})
	clone := manifest.PluginID + "download"
	base := t.TempDir()
	var settings struct{ Data Settings }
	if json.Unmarshal(request("GET", "/api/settings", nil, 200), &settings) != nil {
		t.Fatal("settings")
	}
	settings.Data.AllowedRoots = []string{base}
	request("PUT", "/api/settings", settings.Data, 200)
	var roots struct{ Data []browserRoot }
	if json.Unmarshal(request("GET", "/api/files/roots", nil, 200), &roots) != nil || len(roots.Data) != 1 {
		t.Fatal("roots")
	}
	root := roots.Data[0]
	for name, body := range map[string]string{"native.txt": "owned native content", "override.txt": "owned plugin content"} {
		if err = os.WriteFile(filepath.Join(base, name), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	read := func(id string) ownedModuleSnapshot {
		t.Helper()
		var x ownedModuleSnapshot
		if json.Unmarshal(request("GET", "/api/v1/plugin/"+id+"/snapshot", nil, 200), &x) != nil || x.PID <= 0 {
			t.Fatal("worker")
		}
		return x
	}
	set := func(id string, value any, fail bool) {
		method := "module_execute"
		if fail {
			method = "module_fail"
		}
		success("PUT", "/api/plugins/"+id, map[string]any{"enabled": true, "modules": map[string]string{"download_file": method}, "result": value})
	}
	uri := func(name string) string {
		return "/api/files/content?" + url.Values{"root_id": {root.ID}, "path": {filepath.Join(base, name)}}.Encode()
	}
	var oracle struct {
		Cases []struct {
			Action, Name string
			Plugin       json.RawMessage
			Fail         bool
			Calls        []string
			Body         *string
		}
	}
	oracleRaw, err := os.ReadFile("testdata/standard-module-host/browser-download-source-oracle.json")
	if err != nil || json.Unmarshal(oracleRaw, &oracle) != nil {
		t.Fatal("oracle", err)
	}
	var proof []any
	for _, c := range oracle.Cases {
		if c.Action != "download" {
			continue
		}
		var value any
		if json.Unmarshal(c.Plugin, &value) != nil {
			t.Fatal("oracle value")
		}
		if name, ok := value.(string); ok {
			value = filepath.Join(base, name)
		}
		set(manifest.PluginID, value, c.Fail)
		set(clone, nil, false)
		before := [2]ownedModuleSnapshot{read(manifest.PluginID), read(clone)}
		status := 500
		if c.Body != nil {
			status = 200
		}
		raw := request("GET", uri("native.txt"), nil, status)
		if c.Body != nil && string(raw) != *c.Body {
			t.Fatal("actual content", c.Name, string(raw))
		}
		after := [2]ownedModuleSnapshot{read(manifest.PluginID), read(clone)}
		method := "module_execute"
		if c.Fail {
			method = "module_fail"
		}
		n := 0
		if slices.Contains(c.Calls, "clone") {
			n = 1
		}
		if after[0].Calls[method]-before[0].Calls[method] != 1 || after[1].Calls["module_execute"]-before[1].Calls["module_execute"] != n {
			t.Fatal("actual dispatch", c.Name, before, after)
		}
		proof = append(proof, map[string]any{"case": c.Name, "status": status, "body": string(raw), "before": before, "after": after})
	}
	// Distinct PNG dimensions let the real browser prove it decoded the override.
	pngs := map[string][]byte{}
	for name, size := range map[string]image.Point{"Native.png": {X: 1, Y: 1}, "Override.png": {X: 2, Y: 3}} {
		im := image.NewRGBA(image.Rect(0, 0, size.X, size.Y))
		for y := 0; y < size.Y; y++ {
			for x := 0; x < size.X; x++ {
				im.Set(x, y, color.RGBA{R: 20, G: 90, B: 200, A: 255})
			}
		}
		var encoded bytes.Buffer
		if err = png.Encode(&encoded, im); err != nil {
			t.Fatal(err)
		}
		pngs[name] = encoded.Bytes()
		if err = os.WriteFile(filepath.Join(base, name), encoded.Bytes(), 0600); err != nil {
			t.Fatal(err)
		}
	}
	set(manifest.PluginID, nil, false)
	set(clone, filepath.Join(base, "Override.png"), false)
	actual := request("GET", uri("Native.png"), nil, 200)
	if !bytes.Equal(actual, pngs["Override.png"]) {
		t.Fatal("actual clone bytes")
	}
	before := [2]ownedModuleSnapshot{read(manifest.PluginID), read(clone)}
	stop()
	start()
	after := [2]ownedModuleSnapshot{read(manifest.PluginID), read(clone)}
	for i, x := range after {
		if x.PID == before[i].PID || x.Calls["module_execute"] != 0 {
			t.Fatal("cold replay", before, after)
		}
	}
	proof = append(proof, map[string]any{"mode": "cold", "before": before, "after": after})
	if !bytes.Equal(request("GET", uri("Native.png"), nil, 200), pngs["Override.png"]) {
		t.Fatal("cold override content")
	}
	if statePath := os.Getenv("PLUGIN_PROCESS_BROWSER_DOWNLOAD_STATE"); statePath != "" {
		state := map[string]any{"origin": origin, "data_dir": dir, "source": manifest.PluginID, "clone": clone, "root": root, "source_path": filepath.Join(base, "Native.png"), "override_path": filepath.Join(base, "Override.png"), "uri": uri("Native.png")}
		if err = os.WriteFile(statePath, mustCleanSeedJSON(state), 0600); err != nil {
			t.Fatal(err)
		}
		done := false
		for until := time.Now().Add(25 * time.Minute); time.Now().Before(until); {
			if _, e := os.Stat(statePath + ".restart"); e == nil {
				stop()
				start()
				os.Remove(statePath + ".restart")
				os.WriteFile(statePath+".restart.done", mustCleanSeedJSON([]ownedModuleSnapshot{read(manifest.PluginID), read(clone)}), 0600)
			}
			if _, e := os.Stat(statePath + ".done"); e == nil {
				done = true
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
		if !done {
			t.Fatal("download browser gate timeout")
		}
	}
	for name, want := range pngs {
		got, e := os.ReadFile(filepath.Join(base, name))
		if e != nil || !bytes.Equal(got, want) {
			t.Fatal("read-only download changed native files")
		}
	}
	writeOwnedEventsProcessEvidence(t, "browser-download50-proof.json", mustCleanSeedJSON(proof))
	success("DELETE", "/api/plugins/"+clone, nil)
	success("DELETE", "/api/plugins/"+manifest.PluginID, nil)
	request("GET", "/api/v1/plugin/"+clone+"/snapshot", nil, 404)
	request("GET", "/api/v1/plugin/"+manifest.PluginID+"/snapshot", nil, 404)
	if !bytes.Equal(request("GET", uri("Native.png"), nil, 200), pngs["Native.png"]) {
		t.Fatal("uninstall did not restore native bytes")
	}
	t.Log("real download source cases/clone PNG/cold zero replay/uninstall native restoration and files unchanged")
}
