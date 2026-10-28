package app

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"

	"light-media/internal/plugins"
)

func TestOriginalDownloadAddedActualNativeModuleAfterCommitAndCold(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/standard-module-host/download-added-source-oracle.json")
	var oracle struct {
		Cases []struct {
			Name             string
			Result           json.RawMessage
			CallbackError    bool `json:"callback_error"`
			SubmitError      bool `json:"submit_error"`
			CallsBeforeDrain int  `json:"calls_before_drain"`
			Calls            []map[string]json.RawMessage
		}
	}
	if err != nil || json.Unmarshal(raw, &oracle) != nil || len(oracle.Cases) != 6 {
		t.Fatal("fixed background caller oracle", err)
	}
	for _, sample := range oracle.Cases {
		if sample.SubmitError {
			continue // App shutdown admission has its own concrete test below.
		}
		for _, commitFails := range []bool{false, true} {
			name := sample.Name
			if commitFails {
				name += "_acceptance_SQL_failure"
			}
			t.Run(name, func(t *testing.T) {
				a, input, adds := nativeProducerFixture(t)
				input.MediaType, input.TMDBID = "", 0
				cookie := setupTestAdmin(t, a)
				manifest := installOrderWorker(t, a, cookie, os.Getenv("OWNED_MODULE_OVERRIDE_BINARY"))
				method := "module_execute"
				if sample.CallbackError {
					method = "module_fail"
				}
				config, _ := json.Marshal(map[string]any{"enabled": true, "modules": map[string]string{"download_added": method}, "result": sample.Result})
				saveOwnedModule(t, a, cookie, originalBaseSDKLoaded(t, a, manifest.PluginID), string(config))
				clone := ownedModuleClone(t, a, cookie, "post")
				cloneConfig, _ := json.Marshal(map[string]any{"enabled": true, "modules": map[string]string{"download_added": "module_execute"}, "result": nil})
				saveOwnedModule(t, a, cookie, clone, string(cloneConfig))
				source := originalBaseSDKLoaded(t, a, manifest.PluginID)
				beforeSource, beforeClone := ownedModuleRead(t, source), ownedModuleRead(t, clone)
				ctx := context.Background()
				native, err := a.addNativeDownload(ctx, input)
				if err != nil {
					t.Fatal("real native request", err)
				}
				if commitFails {
					if _, err = a.Store.DB.Exec(`CREATE TRIGGER owned_post_added_commit_failure BEFORE INSERT ON domain_events WHEN NEW.type='download.accepted' BEGIN SELECT RAISE(ABORT,'owned_post_added_commit_failure'); END`); err != nil {
						t.Fatal(err)
					}
				}
				job, err := a.claim(ctx)
				if err != nil || job.Kind != "native_download" {
					t.Fatal("actual native job", err)
				}
				err = a.safeRun(ctx, job)
				if (err != nil) != commitFails || adds.Load() != 1 {
					t.Fatal("post-Add callback must not change actual acceptance", err, adds.Load())
				}
				a.wg.Wait()
				afterSource, afterClone := ownedModuleRead(t, source), ownedModuleRead(t, clone)
				wantSource, wantClone := 1, 1
				if string(sample.Result) == "false" || string(sample.Result) == "true" {
					wantClone = 0 // exact scalar terminal behavior in ChainBase
				}
				if commitFails {
					wantSource, wantClone = 0, 0
				}
				if afterSource.Calls[method]-beforeSource.Calls[method] != wantSource || afterClone.Calls["module_execute"]-beforeClone.Calls["module_execute"] != wantClone {
					t.Fatal("actual committed source/clone call order", sample.Name, afterSource.Calls, afterClone.Calls)
				}
				if commitFails {
					return
				}
				frozen, err := a.loadDownloadContext(ctx, "native_download", native.ID, native.Hash)
				if err != nil || frozen == nil || frozen.Original == nil || string(frozen.OriginalLayout) != `"Original"` {
					t.Fatal("encrypted accepted source fields", err)
				}
				var call struct {
					Args   []json.RawMessage
					Kwargs map[string]json.RawMessage
				}
				if json.Unmarshal(afterSource.Inputs[method], &call) != nil || len(call.Args) != 0 || len(call.Kwargs) != len(sample.Calls[0]) {
					t.Fatal("exact source post-Add signature", string(afterSource.Inputs[method]))
				}
				for key := range sample.Calls[0] {
					if len(call.Kwargs[key]) == 0 {
						t.Fatal("fixed source keyword absent", key)
					}
				}
				full, _ := json.Marshal(frozen.Original)
				var path string
				if !originalBaseJSONEqual(full, call.Kwargs["context"]) || json.Unmarshal(call.Kwargs["download_dir"], &path) != nil || path != input.DownloadPath {
					t.Fatal("actual Context/directory lost", string(afterSource.Inputs[method]))
				}
				content, ok := plugins.DecodeModuleResultBytes(call.Kwargs["torrent_content"])
				material, err := a.resolveMaterial(ctx, native.MaterialRef, materialPurpose)
				if !ok || err != nil || !bytes.Equal(content, material) {
					t.Fatal("actual metainfo bytes changed", err)
				}
				if err = a.runNativeDownload(ctx, job, native.ID); err != nil {
					t.Fatal(err)
				}
				a.wg.Wait()
				if ownedModuleRead(t, source).Calls[method] != afterSource.Calls[method] || adds.Load() != 1 {
					t.Fatal("accepted rerun repeated module/Add")
				}
				dir := a.Dir
				if err = a.Close(); err != nil {
					t.Fatal(err)
				}
				a, err = New(Options{DataDir: dir})
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = a.Close() })
				if err = a.initializeOriginalPlugins(ctx); err != nil {
					t.Fatal(err)
				}
				cold := originalBaseSDKLoaded(t, a, manifest.PluginID)
				if err = a.runNativeDownload(ctx, job, native.ID); err != nil {
					t.Fatal(err)
				}
				a.wg.Wait()
				state := ownedModuleRead(t, cold)
				if state.PID == afterSource.PID || state.Calls[method] != 0 || adds.Load() != 1 {
					t.Fatal("cold replayed post-Add module", state.Calls)
				}
			})
		}
	}
}

func TestOriginalDownloadAddedActualHeldSDKDoesNotBlockAcceptance(t *testing.T) {
	a, input, adds := nativeProducerFixture(t)
	input.MediaType, input.TMDBID = "", 0
	cookie := setupTestAdmin(t, a)
	manifest, repository := ownedDownloadModuleArtifact(t, "OWNED_DOWNLOADER_CONTROL_FIXTURE_BINARY")
	originalCatalogInstall(t, a, cookie, manifest.PluginID, repository, false)
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		select {
		case <-release:
		case <-r.Context().Done():
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer func() {
		once.Do(func() { close(release) })
		peer.Close()
	}()
	config, _ := json.Marshal(map[string]any{"enabled": true, "module": "download_added", "endpoint": peer.URL})
	saveOwnedModule(t, a, cookie, originalBaseSDKLoaded(t, a, manifest.PluginID), string(config))
	native, err := a.addNativeDownload(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	job, err := a.claim(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- a.safeRun(context.Background(), job) }()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("actual SDK post-Add peer not entered")
	}
	select {
	case err := <-done:
		if err != nil || adds.Load() != 1 {
			t.Fatal("post-Add blocked or rejected real acceptance", err, adds.Load())
		}
	case <-time.After(300 * time.Millisecond):
		t.Fatal("accepted download waited for held ordinary post-Add callback")
	}
	var current NativeDownload
	if err := a.Store.Get(context.Background(), "native_download", native.ID, &current); err != nil || current.State != "accepted" {
		t.Fatal("acceptance not durably visible while post-Add peer held", err, current.State)
	}
	once.Do(func() { close(release) })
	a.wg.Wait()
}

func TestOriginalDownloadAddedActualAdmissionFailurePreservesAcceptedTask(t *testing.T) {
	a, input, adds := nativeProducerFixture(t)
	input.MediaType, input.TMDBID = "", 0
	cookie := setupTestAdmin(t, a)
	manifest := installOrderWorker(t, a, cookie, os.Getenv("OWNED_MODULE_OVERRIDE_BINARY"))
	object := originalBaseSDKLoaded(t, a, manifest.PluginID)
	saveOwnedModule(t, a, cookie, object, `{"enabled":true,"modules":{"download_added":"module_execute"},"result":null}`)
	a.wg.Wait()
	// This is the controlled ThreadHelper.submit failure equivalent, without
	// stopping storage or pretending the downloader's successful Add failed.
	a.closeOriginalBackgroundAdmission()
	native, err := a.addNativeDownload(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	job, err := a.claim(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := a.safeRun(context.Background(), job); err != nil || adds.Load() != 1 {
		t.Fatal("background admission failure changed actual Add", err, adds.Load())
	}
	a.wg.Wait()
	if snapshot := ownedModuleRead(t, object); snapshot.Calls["module_execute"] != 0 {
		t.Fatal("failed submission invoked SDK callback", snapshot.Calls)
	}
	var current NativeDownload
	if err := a.Store.Get(context.Background(), "native_download", native.ID, &current); err != nil || current.State != "accepted" {
		t.Fatal("submission failure invalidated acceptance", err, current.State)
	}
}
