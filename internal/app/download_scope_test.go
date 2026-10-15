package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"light-media/internal/download"
)

func TestDownloadListScopesQBToDownloadingApplicationTasks(t *testing.T) {
	a, _, _ := importTestApp(t)
	makeTask := func(letter, state, tags string, progress float64) map[string]any {
		task := workflowTask(strings.Repeat(letter, 40), progress)
		task["state"], task["tags"] = state, tags
		return task
	}
	rows := []any{
		makeTask("a", "downloading", "light-media", .5),
		makeTask("b", "downloading", "unrelated", .5),
		makeTask("c", "downloading", "MOVIEPILOT", .5),
		makeTask("d", "downloading", "", .5),
		makeTask("e", "uploading", "light-media", 1),
		makeTask("f", "downloading", "", .5),
	}
	queries := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2/torrents/info" {
			http.NotFound(w, r)
			return
		}
		queries++
		if r.URL.Query().Get("filter") != "downloading" {
			t.Error("download page did not request qBittorrent's downloading filter")
		}
		workflowJSON(w, rows)
	}))
	defer server.Close()
	a.config.Downloaders = []DownloaderConfig{
		{Config: download.Config{ID: "qb", Kind: "qb", BaseURL: server.URL}},
		{Config: download.Config{ID: "not-selected", Kind: "qb", BaseURL: "http://127.0.0.1:1"}},
	}
	ctx := context.Background()
	for _, record := range []NativeDownload{
		{ID: "adopted-duplicate", Hash: strings.Repeat("d", 40), DownloaderID: "qb", State: "accepted"},
		{ID: "different-instance", Hash: strings.Repeat("b", 40), DownloaderID: "not-selected", State: "accepted"},
		{ID: "cancelled", Hash: strings.Repeat("f", 40), DownloaderID: "qb", State: "cancelled"},
	} {
		if err := a.Store.Put(ctx, "native_download", record.ID, record); err != nil {
			t.Fatal(err)
		}
	}
	result, err := a.downloadList(ctx, "qb")
	if err != nil {
		t.Fatal(err)
	}
	instances := result.(map[string]any)["instances"].([]map[string]any)
	if len(instances) != 1 || queries != 1 {
		t.Fatalf("page queried unrelated downloaders: %v / %d", instances, queries)
	}
	tasks := instances[0]["tasks"].([]download.Task)
	if len(tasks) != 2 || tasks[0].Hash != strings.Repeat("a", 40) || tasks[1].Hash != strings.Repeat("d", 40) {
		t.Fatalf("download scope leaked unrelated, old MoviePilot, cancelled, or seeding torrents: %+v", tasks)
	}
}

func TestDownloadListTransmissionUsesOnlyOwnDownloadAndDownloadQueueStates(t *testing.T) {
	a, _, _ := importTestApp(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var call struct {
			Method string `json:"method"`
			ID     uint64 `json:"id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&call); err != nil {
			t.Error(err)
			return
		}
		result := map[string]any{"version": "4.1.0"}
		if call.Method == "torrent_get" {
			rows := []any{}
			for state := 0; state <= 6; state++ {
				rows = append(rows, map[string]any{"hash_string": strings.Repeat(string(rune('a'+state)), 40), "name": "task", "status": state, "download_dir": "/downloads", "total_size": 100, "left_until_done": 50, "percent_done": .5, "labels": []string{"light-media"}})
			}
			rows = append(rows, map[string]any{"hash_string": strings.Repeat("8", 40), "name": "other app", "status": 4, "download_dir": "/downloads", "total_size": 100, "left_until_done": 50, "percent_done": .5, "labels": []string{"MOVIEPILOT"}})
			result = map[string]any{"torrents": rows}
		}
		workflowJSON(w, map[string]any{"jsonrpc": "2.0", "id": call.ID, "result": result})
	}))
	defer server.Close()
	a.config.Downloaders = []DownloaderConfig{{Config: download.Config{ID: "tr", Kind: "tr", BaseURL: server.URL}}}
	result, err := a.downloadList(context.Background(), "tr")
	if err != nil {
		t.Fatal(err)
	}
	entry := result.(map[string]any)["instances"].([]map[string]any)[0]
	if entry["error"] != nil {
		t.Fatal(entry["error"])
	}
	tasks := entry["tasks"].([]download.Task)
	if len(tasks) != 2 || tasks[0].Hash != strings.Repeat("d", 40) || tasks[1].Hash != strings.Repeat("e", 40) {
		t.Fatalf("unexpected original Transmission download scope: %+v", tasks)
	}
}

func TestDownloadPageDeleteIncludesFilesOnlyForOwnedDownloadingTask(t *testing.T) {
	a, _, _ := importTestApp(t)
	active, seeding, unrelated := strings.Repeat("a", 40), strings.Repeat("b", 40), strings.Repeat("c", 40)
	mutations := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/torrents/info":
			first, second, third := workflowTask(active, .5), workflowTask(seeding, 1), workflowTask(unrelated, .5)
			first["state"], first["tags"] = "downloading", "light-media"
			second["state"], second["tags"] = "uploading", "light-media"
			third["state"] = "downloading"
			rows := []any{}
			for _, row := range []map[string]any{first, second, third} {
				if hash := r.URL.Query().Get("hashes"); hash != "" && hash != row["hash"] {
					continue
				}
				rows = append(rows, row)
			}
			workflowJSON(w, rows)
		case "/api/v2/torrents/delete":
			if err := r.ParseForm(); err != nil {
				t.Error(err)
			}
			if r.FormValue("hashes") != active || r.FormValue("deleteFiles") != "true" {
				t.Errorf("wrong original delete semantics: %v", r.Form)
			}
			mutations++
		default:
			t.Errorf("unexpected mutation %s", r.URL.Path)
		}
	}))
	defer server.Close()
	a.config.Downloaders = []DownloaderConfig{{Config: download.Config{ID: "qb", Kind: "qb", BaseURL: server.URL}}}
	ctx := context.Background()
	for _, action := range []string{"pause", "remove_with_data"} {
		if err := a.downloaderTaskAction(ctx, "qb", unrelated, action); err == nil {
			t.Fatalf("unrelated task accepted action %s", action)
		}
	}
	if err := a.downloaderTaskAction(ctx, "qb", seeding, "remove_with_data"); err == nil {
		t.Fatal("completed seeding task accepted destructive page action")
	}
	if err := a.downloaderTaskAction(ctx, "qb", active, "remove_with_data"); err != nil {
		t.Fatal(err)
	}
	if mutations != 1 {
		t.Fatalf("unexpected real mutation count=%d", mutations)
	}
}
