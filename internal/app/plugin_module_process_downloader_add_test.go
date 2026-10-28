package app

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"light-media/internal/download"
	"light-media/internal/plugins"
	"light-media/internal/sites"
)

// This enters the real POST /downloads/add route, its persisted job, SDK
// download callback, provider/core HTTP side effects, independent task readback,
// failed acceptance and cold restart. The peer is an owned protocol fixture.
func verifyOwnedProductionDownloaderAdd(t *testing.T, dir string, request func(string, string, any, int) []byte, success func(string, string, any), stop, start func()) {
	t.Helper()
	oraclePath := os.Getenv("PLUGIN_PROCESS_DOWNLOADER_ADD_ORACLE")
	if oraclePath == "" {
		oraclePath = "../../testdata/standard-module-host/downloader-add-oracle.json"
	}
	raw, err := os.ReadFile(oraclePath)
	var oracle struct {
		Cases []struct {
			Name     string
			Accepted bool
		}
	}
	if err != nil || json.Unmarshal(raw, &oracle) != nil || len(oracle.Cases) != 11 {
		t.Fatal("fixed download-single wrapper/caller oracle", err)
	}
	expected := map[string]bool{}
	for _, sample := range oracle.Cases {
		expected[sample.Name] = sample.Accepted
	}
	state := &ownedDownloadModulePeer{Directory: dir}
	peer := httptest.NewServer(http.HandlerFunc(state.serveHTTP))
	defer peer.Close()
	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		state.mu.Lock()
		defer state.mu.Unlock()
		switch r.URL.Path {
		case "/torrents.php":
			_, _ = io.WriteString(w, workflowHTML("Owned.Download.Module.2024.1080p.WEB-DL.x264-TEST", "", false))
		case "/download.php":
			_, _ = w.Write(state.Torrent)
		default:
			http.NotFound(w, r)
		}
	}))
	defer site.Close()
	stop()
	seed, err := New(Options{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	settings := seed.Settings()
	settings.AllowedRoots = []string{dir}
	settings.Downloaders = []DownloaderConfig{{Config: download.Config{ID: "owned-add", Kind: "qb", BaseURL: peer.URL, ForceResume: true}, Name: "Owned downloader", DownloadPath: dir}}
	settings.DefaultDownloader = "owned-add"
	settings.Sites = []SiteConfig{{Config: sites.Config{ID: "owned-add-site", Kind: "ubits", BaseURL: site.URL, Name: "Owned add site", Cookie: "owned-cookie"}, Enabled: true, DailyBudget: 1000}}
	if err = seed.saveSettings(context.Background(), settings); err != nil {
		t.Fatal(err)
	}
	if err = seed.Close(); err != nil {
		t.Fatal(err)
	}
	start()
	manifest, index := ownedDownloadModuleArtifact(t, "OWNED_DOWNLOADER_CONTROL_PROCESS_FIXTURE_BINARY")
	success("POST", "/api/plugins/install", map[string]string{"plugin_id": manifest.PluginID, "repo_url": index})
	defer success("DELETE", "/api/plugins/"+manifest.PluginID, nil)
	configure := func(id string, result any, endpoint string, fail bool) {
		success("PUT", "/api/plugins/"+id, map[string]any{"enabled": true, "module": "download", "result": result, "endpoint": endpoint, "fail": fail})
	}
	callbackCount := func(id string) int {
		body := request("GET", "/api/v1/plugin/"+id+"/snapshot", nil, 200)
		var observation struct{ Calls int }
		if json.Unmarshal(body, &observation) != nil {
			t.Fatal("owned callback baseline")
		}
		return observation.Calls
	}
	lookup := func(id string, wantTerminal bool) (NativeDownload, []byte) {
		t.Helper()
		deadline := time.Now().Add(8 * time.Second)
		for {
			status := request("GET", "/api/downloads/native/"+id, nil, 200)
			var response struct{ Data NativeDownload }
			if json.Unmarshal(status, &response) != nil || response.Data.ID != id {
				t.Fatal("actual native download status response", string(status))
			}
			if !wantTerminal || response.Data.State == "accepted" || response.Data.State == "failed" {
				writeOwnedEventsProcessEvidence(t, "modules43-add-status-"+response.Data.State+".json", status)
				return response.Data, request("GET", "/api/downloads?downloader_id=owned-add", nil, 200)
			}
			if time.Now().After(deadline) {
				t.Fatal("actual queued download did not finish", id, string(status))
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
	snapshot := func(id, name string) (int, int) {
		body := request("GET", "/api/v1/plugin/"+id+"/snapshot", nil, 200)
		var observation struct {
			Calls, PID int
			Input      struct {
				Args   []json.RawMessage
				Kwargs map[string]json.RawMessage
			}
		}
		if json.Unmarshal(body, &observation) != nil || observation.PID <= 0 || observation.Input.Args == nil || len(observation.Input.Args) != 0 || len(observation.Input.Kwargs) != 7 || string(observation.Input.Kwargs["cookie"]) != `"owned-cookie"` || string(observation.Input.Kwargs["category"]) != `null` || string(observation.Input.Kwargs["episodes"]) != `null` || string(observation.Input.Kwargs["label"]) != `null` || string(observation.Input.Kwargs["downloader"]) != `null` {
			t.Fatal("actual source seven kwargs", string(body))
		}
		state.mu.Lock()
		content, ok := plugins.DecodeModuleResultBytes(observation.Input.Kwargs["content"])
		same := string(content) == string(state.Torrent)
		state.mu.Unlock()
		if !ok || !same {
			t.Fatal("actual SDK bytes changed")
		}
		writeOwnedEventsProcessEvidence(t, name, body)
		return observation.Calls, observation.PID
	}
	for _, boundary := range []string{"tuple_terminal", "false_terminal", "empty_list_after_core", "plugin_exception_core"} {
		state.mu.Lock()
		state.Torrent, state.Hash = workflowTorrent("Owned.Download.Module." + boundary + ".mkv")
		state.Present = false
		hash := state.Hash
		state.mu.Unlock()
		adds, forces, overrides := state.counts()
		var value any = map[string]any{plugins.ModuleTupleKey: []any{"Owned downloader", hash, "Original", ""}}
		endpoint := ""
		if boundary == "tuple_terminal" {
			endpoint = peer.URL + "/module-add"
		}
		if boundary == "false_terminal" {
			value = false
		}
		if boundary == "empty_list_after_core" {
			value = []any{}
		}
		configure(manifest.PluginID, value, endpoint, boundary == "plugin_exception_core")
		beforeCalls := callbackCount(manifest.PluginID)
		body := request("POST", "/api/downloads/add", NativeDownloadRequest{SiteID: "owned-add-site", ResourceID: "101", Query: "Owned Download", DownloadPath: dir}, 200)
		var response struct{ Data NativeDownload }
		if json.Unmarshal(body, &response) != nil || response.Data.ID == "" || response.Data.Hash != hash || string(response.Data.SourceDownloader) != "null" {
			t.Fatal("actual native add did not freeze source choice", string(body))
		}
		record, readback := lookup(response.Data.ID, true)
		if (record.State == "accepted") != expected[boundary] || !record.SourceModuleStarted {
			t.Fatal("actual fixed caller/native persisted result", boundary, record.State, record.Error)
		}
		writeOwnedEventsProcessEvidence(t, "modules43-add-"+boundary+"-native.json", readback)
		calls, _ := snapshot(manifest.PluginID, "modules43-add-"+boundary+"-sdk.json")
		if calls-beforeCalls != 1 {
			t.Fatal("actual module callback count delta", calls-beforeCalls)
		}
		afterAdds, afterForces, afterOverrides := state.counts()
		wantCore := 0
		if boundary == "empty_list_after_core" || boundary == "plugin_exception_core" {
			wantCore = 1
		}
		wantOverride := 0
		if boundary == "tuple_terminal" {
			wantOverride = 1
		}
		if afterAdds-adds != wantCore || afterForces-forces != wantCore || afterOverrides-overrides != wantOverride {
			t.Fatal("actual main Add/force/provider side effects", boundary, afterAdds-adds, afterForces-forces, afterOverrides-overrides)
		}
		if boundary == "empty_list_after_core" {
			// Keep the same real task present during cold startup, so a false
			// source result cannot be accidentally adopted as a successful Add.
			stop()
			start()
			failed, cold := lookup(record.ID, false)
			if failed.State != "failed" || !failed.SourceModuleStarted {
				t.Fatal("cold falsely accepted a module-declined native task", failed.State)
			}
			var page struct {
				Data struct {
					Instances []struct{ Tasks []download.Task }
				}
			}
			if json.Unmarshal(cold, &page) != nil || len(page.Data.Instances) != 1 || len(page.Data.Instances[0].Tasks) != 1 || page.Data.Instances[0].Tasks[0].Hash != hash {
				t.Fatal("actual failed source task was not retained during cold readback", string(cold))
			}
			writeOwnedEventsProcessEvidence(t, "modules43-add-cold-failed.json", cold)
			time.Sleep(150 * time.Millisecond)
			coldAdds, coldForces, coldOverrides := state.counts()
			if coldAdds != afterAdds || coldForces != afterForces || coldOverrides != afterOverrides {
				t.Fatal("cold replayed module/native Add")
			}
		}
	}
	// The ordinary module registry owns clone discovery. No download-specific
	// clone management exists; source None continues into the clone tuple.
	clone := manifest.PluginID + "addclone"
	success("POST", "/api/plugins/"+manifest.PluginID+"/clone", map[string]string{"suffix": "addclone", "name": "Owned add clone"})
	defer success("DELETE", "/api/plugins/"+clone, nil)
	state.mu.Lock()
	state.Torrent, state.Hash = workflowTorrent("Owned.Download.Module.Clone.mkv")
	state.Present = false
	hash := state.Hash
	state.mu.Unlock()
	configure(manifest.PluginID, nil, "", false)
	configure(clone, map[string]any{plugins.ModuleTupleKey: []any{"Owned downloader", hash, "Original", ""}}, peer.URL+"/module-add", false)
	beforeAdds, beforeForces, beforeOverrides := state.counts()
	sourceBaseline, cloneBaseline := callbackCount(manifest.PluginID), callbackCount(clone)
	body := request("POST", "/api/downloads/add", NativeDownloadRequest{SiteID: "owned-add-site", ResourceID: "101", Query: "Owned Download", DownloadPath: dir}, 200)
	var created struct{ Data NativeDownload }
	if json.Unmarshal(body, &created) != nil || created.Data.ID == "" {
		t.Fatal("clone actual Add creation")
	}
	accepted, readback := lookup(created.Data.ID, true)
	if accepted.State != "accepted" {
		t.Fatal("clone actual module did not create/confirm task", accepted.Error)
	}
	writeOwnedEventsProcessEvidence(t, "modules43-add-clone-native.json", readback)
	sourceCalls, sourcePID := snapshot(manifest.PluginID, "modules43-add-clone-source-sdk.json")
	cloneCalls, clonePID := snapshot(clone, "modules43-add-clone-sdk.json")
	if sourceCalls-sourceBaseline != 1 || cloneCalls-cloneBaseline != 1 || sourcePID == clonePID {
		t.Fatal("clone module identity/call isolation", sourceCalls, cloneCalls, sourcePID, clonePID)
	}
	afterAdds, afterForces, afterOverrides := state.counts()
	if afterAdds != beforeAdds || afterForces != beforeForces || afterOverrides-beforeOverrides != 1 {
		t.Fatal("clone tuple failed to stop native Add")
	}
	state.mu.Lock()
	input := append(json.RawMessage(nil), state.Input...)
	state.mu.Unlock()
	writeOwnedEventsProcessEvidence(t, "modules43-add-provider-material.json", input)
	t.Logf("actual main/native download: seven source kwargs with binary metainfo, tuple provider task readback/core0, false no Add, [] real Add+force yet failed, throw native fallback, failed cold no replay, source+clone separate PID %d/%d", sourcePID, clonePID)
}
