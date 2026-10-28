package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"light-media/internal/download"
)

func TestOriginalDownloaderListActualSDKFixedSource(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/standard-module-host/downloader-list-oracle.json")
	var oracle struct {
		Cases []struct {
			Name          string
			PluginResult  json.RawMessage `json:"plugin_result"`
			PluginFailure bool            `json:"plugin_failure"`
			CoreFailure   bool            `json:"core_failure"`
			Selected      *string
			Observations  []struct {
				Kind   string
				Kwargs json.RawMessage
			}
			Returned []json.RawMessage
		}
	}
	if err != nil || json.Unmarshal(raw, &oracle) != nil || len(oracle.Cases) != 11 {
		t.Fatal("fixed downloader list source oracle", err)
	}
	for _, sample := range oracle.Cases {
		t.Run(sample.Name, func(t *testing.T) {
			a, cookie, object := ownedModuleFixture(t, true)
			method := "module_execute"
			if sample.PluginFailure {
				method = "module_fail"
			}
			config, _ := json.Marshal(map[string]any{"enabled": true, "modules": map[string]string{"list_torrents": method}, "result": sample.PluginResult})
			saveOwnedModule(t, a, cookie, object, string(config))
			object = originalBaseSDKLoaded(t, a, ownedModuleID)
			before := ownedModuleRead(t, object)
			queries := 0
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/api/v2/torrents/info" || r.URL.Query().Get("filter") != "downloading" {
					t.Error("existing downloading core protocol changed", r.URL)
				}
				queries++
				if sample.CoreFailure {
					w.WriteHeader(http.StatusInternalServerError)
					return
				}
				task := workflowTask(strings.Repeat("a", 40), .5)
				task["tags"], task["state"] = "light-media", "downloading"
				workflowJSON(w, []any{task})
			}))
			defer peer.Close()
			a.config.Downloaders = []DownloaderConfig{{Config: download.Config{ID: "owned", Kind: "qb", BaseURL: peer.URL}, Name: "Owned downloader"}}
			selector := ""
			if sample.Selected != nil {
				selector = "owned"
			}
			result, err := a.downloadList(context.Background(), selector)
			if err != nil {
				t.Fatal(err)
			}
			tasks := result.(map[string]any)["instances"].([]map[string]any)[0]["tasks"].([]download.Task)
			wantCore := 0
			var want json.RawMessage
			for _, observation := range sample.Observations {
				if observation.Kind == "core" {
					wantCore++
				} else {
					want = observation.Kwargs
				}
			}
			after := ownedModuleRead(t, object)
			var actual struct {
				Args   []json.RawMessage
				Kwargs json.RawMessage
			}
			if queries != wantCore || after.Calls[method]-before.Calls[method] != 1 || json.Unmarshal(after.Inputs[method], &actual) != nil || actual.Args == nil || len(actual.Args) != 0 || !originalBaseJSONEqual(actual.Kwargs, want) {
				t.Fatal("source list invocation/order/core query count", queries, wantCore, string(actual.Kwargs), string(want))
			}
			if len(tasks) != len(sample.Returned) {
				t.Fatal("fixed source result count", len(tasks), len(sample.Returned))
			}
			for i, source := range sample.Returned {
				var item struct{ Hash string }
				json.Unmarshal(source, &item)
				if tasks[i].Hash != item.Hash {
					t.Fatal("fixed source list order", i, tasks[i].Hash, item.Hash)
				}
				if len(tasks[i].SourceTorrent) > 0 && (!originalBaseJSONEqual(tasks[i].SourceTorrent, source) || tasks[i].Progress != .37 || tasks[i].TotalSize != 2048) {
					t.Fatal("source model or percent-to-fraction projection changed", tasks[i])
				}
			}
		})
	}
}

func TestOriginalDownloaderListSourceProjectionKeepsOwnershipBoundary(t *testing.T) {
	d := DownloaderConfig{Config: download.Config{ID: "owned"}, Name: "Owned downloader"}
	for _, sample := range []struct {
		Tags  string
		Owned bool
	}{{"MOVIEPILOT", false}, {"other", false}, {"light-media", true}} {
		raw, _ := json.Marshal(map[string]any{"downloader": d.Name, "hash": strings.Repeat("b", 40), "tags": sample.Tags})
		task, match, err := originalDownloadingTask(raw, d)
		if err != nil || !match || (downloaderTaskOwnership{}).contains(task) != sample.Owned {
			t.Fatal("ordinary extension bypassed existing task ownership", sample, err)
		}
	}
}

func TestOriginalDownloaderListOptionalSourceModel(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/standard-module-host/downloader-list-oracle.json")
	var oracle struct {
		Samples []json.RawMessage `json:"projection_samples"`
	}
	if err != nil || json.Unmarshal(raw, &oracle) != nil || len(oracle.Samples) != 2 {
		t.Fatal("fixed optional DownloaderTorrent models", err)
	}
	d := DownloaderConfig{Config: download.Config{ID: "owned"}, Name: "Owned downloader"}
	for i, sample := range oracle.Samples {
		task, match, err := originalDownloadingTask(sample, d, true)
		if err != nil || !match || !originalBaseJSONEqual(task.SourceTorrent, sample) {
			t.Fatal("source optional model rejected or changed", i, err)
		}
		if i == 0 && (task.Hash != "" || task.Status != "" || task.TotalSize != 0) {
			t.Fatal("optional source null/large float corrupted native counter", task)
		}
		if i == 1 && task.TotalSize != -10 {
			t.Fatal("source signed float narrowed incorrectly", task)
		}
	}
}
