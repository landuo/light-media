package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"light-media/internal/download"
)

func verifyOwnedProductionDownloadFileDeleted(t *testing.T, dir string, request func(string, string, any, int) []byte, success func(string, string, any), stop, start func()) {
	t.Helper()
	oraclePath := os.Getenv("PLUGIN_PROCESS_FILE_CONSUMER_ORACLE")
	if oraclePath == "" {
		oraclePath = "../../testdata/standard-event-target/file-consumer-source-oracle.json"
	}
	raw, err := os.ReadFile(oraclePath)
	var oracle struct {
		Deleted struct {
			Cases []struct {
				Name  string
				Calls []struct{ Method string }
			}
		}
	}
	if err != nil || json.Unmarshal(raw, &oracle) != nil || len(oracle.Deleted.Cases) != 6 {
		t.Fatal("fixed file consumer source oracle", err)
	}
	for _, name := range []string{"listed_remove_false_still_publish", "listed_remove_true_publish"} {
		found := false
		for _, c := range oracle.Deleted.Cases {
			if c.Name == name {
				for _, call := range c.Calls {
					if call.Method == "send_event" {
						found = true
					}
				}
			}
		}
		if !found {
			t.Fatal("fixed false/true source publication cases missing")
		}
	}
	const hash = "0123456789012345678901234567890123456789"
	marker := filepath.Join(dir, "owned-file-consumer-keep-data.bin")
	if err = os.WriteFile(marker, []byte("owned keep-data marker"), 0600); err != nil {
		t.Fatal(err)
	}
	defer os.Remove(marker)
	var mu sync.Mutex
	present, fail := true, true
	queries, deletes := 0, 0
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch r.URL.Path {
		case "/api/v2/torrents/info":
			queries++
			items := []any{}
			if present {
				task := workflowTask(hash, 0)
				task["tags"], task["save_path"], task["name"] = "light-media", dir, "Owned.2024.1080p.mkv"
				items = append(items, task)
			}
			workflowJSON(w, items)
		case "/api/v2/torrents/delete":
			if r.ParseForm() != nil || r.FormValue("hashes") != hash || r.FormValue("deleteFiles") != "false" {
				t.Error("actual core delete escaped owned exact hash/keep-data")
			}
			deletes++
			if fail {
				w.WriteHeader(500)
			} else {
				present = false
			}
		default:
			http.NotFound(w, r)
		}
	}))
	defer peer.Close()
	stop()
	seed, err := New(Options{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	settings := seed.Settings()
	settings.Downloaders = []DownloaderConfig{{Config: download.Config{ID: "owned-file-delete", Kind: "qb", BaseURL: peer.URL}, Name: "Owned downloader"}}
	settings.DefaultDownloader = "owned-file-delete"
	settings.Sites = nil
	if err = seed.saveSettings(context.Background(), settings); err != nil {
		t.Fatal(err)
	}
	if err = seed.Close(); err != nil {
		t.Fatal(err)
	}
	start()
	ids := []string{"OwnedInput", "OwnedInputservice"}
	configure := func() {
		for _, id := range ids {
			success("PUT", "/api/plugins/"+id, map[string]any{"enabled": true, "event_controls": []map[string]string{{"action": "add", "kind": "broadcast", "type": "download.deleted", "method": "event_dynamic"}}})
		}
	}
	configure()
	send := func(id string, data any) {
		request("POST", "/api/v1/plugin/"+id+"/input", map[string]any{"operation": "event.send", "input": map[string]any{"kind": "broadcast", "type": "downloadfile.deleted", "data": data}}, 200)
	}
	observations := func(id string) ([]json.RawMessage, []byte) {
		body := request("GET", "/api/v1/plugin/"+id+"/events", nil, 200)
		var response struct{ Events []json.RawMessage }
		if json.Unmarshal(body, &response) != nil {
			t.Fatal("actual file consumer SDK observations")
		}
		rows := []json.RawMessage{}
		for _, raw := range response.Events {
			var e struct{ Type string }
			_ = json.Unmarshal(raw, &e)
			if e.Type == "download.deleted" {
				rows = append(rows, raw)
			}
		}
		return rows, body
	}
	wait := func(name string) {
		for _, id := range ids {
			deadline := time.Now().Add(5 * time.Second)
			for {
				rows, body := observations(id)
				if len(rows) == 1 {
					var e struct {
						Data struct {
							Hash     string
							Torrents []map[string]any
						}
					}
					if json.Unmarshal(rows[0], &e) != nil || e.Data.Hash != hash || len(e.Data.Torrents) != 1 || e.Data.Torrents[0]["hash"] != hash || e.Data.Torrents[0]["downloader"] != "Owned downloader" {
						t.Fatal("actual fixed download deleted snapshot", string(rows[0]))
					}
					writeOwnedEventsProcessEvidence(t, name+"-"+id+".json", body)
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("actual source/core deleted event was not consumed", id, string(body))
				}
				time.Sleep(15 * time.Millisecond)
			}
		}
	}
	// Src-only is an exact source no-op, even with a matching task available.
	send(ids[0], map[string]any{"src": marker})
	time.Sleep(70 * time.Millisecond)
	mu.Lock()
	q, d := queries, deletes
	mu.Unlock()
	if q != 0 || d != 0 {
		t.Fatal("src-only source event performed a download operation", q, d)
	}
	for _, id := range ids {
		rows, _ := observations(id)
		if len(rows) != 0 {
			t.Fatal("src-only invented DownloadDeleted")
		}
	}
	send(ids[0], map[string]any{"hash": hash, "src": marker})
	wait("events36-file-delete-false")
	mu.Lock()
	q, d, remaining := queries, deletes, present
	mu.Unlock()
	if q < 1 || d != 1 || !remaining {
		t.Fatal("failed native remove did not preserve actual task", q, d, remaining)
	}
	if data, err := os.ReadFile(marker); err != nil || string(data) != "owned keep-data marker" {
		t.Fatal("ordinary removal changed local data", err)
	}
	// The source callback is memory-only; a cold restart never retries its
	// HTTP-500 removal. Its still-present task and marker remain unchanged.
	stop()
	start()
	configure()
	time.Sleep(100 * time.Millisecond)
	mu.Lock()
	coldD, coldPresent := deletes, present
	fail = false
	mu.Unlock()
	if coldD != 1 || !coldPresent {
		t.Fatal("cold replayed a source ordinary removal", coldD, coldPresent)
	}
	send(ids[1], map[string]any{"hash": hash})
	wait("events36-file-delete-true")
	mu.Lock()
	d, remaining = deletes, present
	mu.Unlock()
	if d != 2 || remaining {
		t.Fatal("clone source event did not remove actual native task", d, remaining)
	}
	if _, err = os.Stat(marker); err != nil {
		t.Fatal("keep-data event deleted its owned local marker", err)
	}
	writeOwnedEventsProcessEvidence(t, "events36-file-delete-native-effects.json", mustCleanSeedJSON(map[string]any{"initial_task": "synthetic full native qB peer row", "failed_remove_requests": 1, "successful_remove_requests": 1, "delete_files": false, "task_present_after_false": coldPresent, "task_present_after_true": remaining, "data_marker_retained": true, "cold_replayed": false}))
	t.Log("actual ordinary SDK source/clone → core list/remove native HTTP, false/500 still emits full DownloadDeleted, src-only no-op, cold no retry, true removes task and retains local data")
}
