package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"

	"light-media/internal/download"
	"light-media/internal/plugins"
)

func TestOriginalCoreDownloadFileDeletedActualSDKSource(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/standard-event-target/file-consumer-source-oracle.json")
	var oracle struct {
		Deleted struct {
			Cases []struct {
				Name    string
				Input   json.RawMessage
				Rows    []json.RawMessage
				Removal bool
				Calls   []struct {
					Method string
					Kwargs json.RawMessage
				}
			}
		}
	}
	if err != nil || json.Unmarshal(raw, &oracle) != nil || len(oracle.Deleted.Cases) != 6 {
		t.Fatal("actual fixed DownloadFileDeleted consumer oracle", err)
	}
	for _, sample := range oracle.Deleted.Cases {
		t.Run(sample.Name, func(t *testing.T) {
			a, cookie := installOwnedInputFixture(t)
			configuration := `{"enabled":true,"event_controls":[{"action":"add","kind":"broadcast","type":"download.deleted","method":"event_dynamic"}]}`
			r := callTest(t, a.Handler(), "PUT", "/api/plugins/OwnedInput", configuration, cookie, "")
			assertOriginalRegistrySuccess(t, r.Code, r.Body.Bytes())
			r = callTest(t, a.Handler(), "POST", "/api/plugins/OwnedInput/clone", `{"suffix":"deletion"}`, cookie, "")
			assertOriginalRegistrySuccess(t, r.Code, r.Body.Bytes())
			r = callTest(t, a.Handler(), "PUT", "/api/plugins/OwnedInputdeletion", configuration, cookie, "")
			assertOriginalRegistrySuccess(t, r.Code, r.Body.Bytes())
			const hash = "0123456789012345678901234567890123456789"
			var mu sync.Mutex
			queries, deletes := 0, 0
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				switch r.URL.Path {
				case "/api/v2/torrents/info":
					queries++
					rows := []any{}
					if len(sample.Rows) > 0 {
						task := workflowTask(hash, 0)
						task["tags"], task["save_path"] = "light-media", t.TempDir()
						rows = append(rows, task)
					}
					workflowJSON(w, rows)
				case "/api/v2/torrents/delete":
					if r.ParseForm() != nil || r.FormValue("hashes") != hash || r.FormValue("deleteFiles") != "false" {
						t.Error("ordinary event delete was not exact-hash keep-data")
					}
					deletes++
					if !sample.Removal {
						w.WriteHeader(500)
					}
				default:
					http.NotFound(w, r)
				}
			}))
			defer peer.Close()
			a.config.Downloaders = []DownloaderConfig{{Config: download.Config{ID: "owned-deleted", Kind: "qb", BaseURL: peer.URL}, Name: "Owned downloader"}}
			a.config.DefaultDownloader = "owned-deleted"
			if got := ownedSDKEventCall(t, a, cookie, "event.check", `{"kind":"broadcast","type":"downloadfile.deleted"}`); string(got) != "true" {
				t.Fatal("ordinary core download consumer missing")
			}
			if sample.Name == "no_event" {
				if err = a.consumeOriginalCoreDownloadFileDeleted(context.Background(), nil); err != nil {
					t.Fatal(err)
				}
			} else {
				input := strings.ReplaceAll(string(sample.Input), "owned-hash", hash)
				send, _ := json.Marshal(plugins.EventSendRequest{Kind: plugins.BroadcastEventKind, Type: "downloadfile.deleted", Data: json.RawMessage(input)})
				response := ownedInputRequest(t, a, cookie, "OwnedInputdeletion", "event.send", string(send))
				if response.Code != 200 {
					t.Fatal(response.Code, response.Body.String())
				}
			}
			a.wg.Wait()
			wantList, wantDelete, wantEvent := 0, 0, 0
			for _, call := range sample.Calls {
				switch call.Method {
				case "list_torrents":
					wantList++
				case "remove_torrents":
					wantDelete++
				case "send_event":
					wantEvent++
				}
			}
			mu.Lock()
			if queries < wantList || deletes != wantDelete || wantList == 0 && queries != 0 {
				t.Fatal("fixed core consumer effects", sample.Name, queries, deletes, wantList, wantDelete)
			}
			mu.Unlock()
			for _, id := range []string{"OwnedInput", "OwnedInputdeletion"} {
				count := 0
				for _, raw := range ownedInputEvents(t, a, cookie, id) {
					var event struct {
						Type string
						Data struct {
							Hash     string
							Torrents []map[string]any
						}
					}
					_ = json.Unmarshal(raw, &event)
					if event.Type == "download.deleted" {
						count++
						if event.Data.Hash != hash || len(event.Data.Torrents) != 1 || event.Data.Torrents[0]["hash"] != hash || event.Data.Torrents[0]["downloader"] != "Owned downloader" {
							t.Fatal("fixed source emitted snapshot", string(raw))
						}
					}
				}
				if count != wantEvent {
					t.Fatal("source false removal must still publish to both actual SDK identities", id, count, wantEvent)
				}
			}
		})
	}
}

func TestOriginalCoreDownloadFileDeletedModuleBoundaries(t *testing.T) {
	for _, method := range []string{"list_torrents", "remove_torrents"} {
		t.Run(method, func(t *testing.T) {
			a, cookie := installOwnedInputFixture(t)
			r := callTest(t, a.Handler(), "PUT", "/api/plugins/OwnedInput", `{"enabled":true,"event_controls":[{"action":"add","kind":"broadcast","type":"download.deleted","method":"event_dynamic"}]}`, cookie, "")
			assertOriginalRegistrySuccess(t, r.Code, r.Body.Bytes())
			manifest, index := ownedDownloadModuleArtifact(t, "OWNED_DOWNLOADER_CONTROL_FIXTURE_BINARY")
			body, _ := json.Marshal(map[string]string{"plugin_id": manifest.PluginID, "repo_url": index})
			r = callTest(t, a.Handler(), "POST", "/api/plugins/install", string(body), cookie, "")
			assertOriginalRegistrySuccess(t, r.Code, r.Body.Bytes())
			body, _ = json.Marshal(map[string]any{"enabled": true, "module": method, "result": false})
			r = callTest(t, a.Handler(), "PUT", "/api/plugins/"+manifest.PluginID, string(body), cookie, "")
			assertOriginalRegistrySuccess(t, r.Code, r.Body.Bytes())
			const hash = "0123456789012345678901234567890123456789"
			queries, deletes := 0, 0
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/v2/torrents/info":
					queries++
					task := workflowTask(hash, 0)
					task["tags"] = "light-media"
					workflowJSON(w, []any{task})
				case "/api/v2/torrents/delete":
					deletes++
				default:
					http.NotFound(w, r)
				}
			}))
			defer peer.Close()
			a.config.Downloaders = []DownloaderConfig{{Config: download.Config{ID: "owned-deleted", Kind: "qb", BaseURL: peer.URL}, Name: "Owned downloader"}}
			a.config.DefaultDownloader = "owned-deleted"
			ownedSDKEventCall(t, a, cookie, "event.send", `{"kind":"broadcast","type":"downloadfile.deleted","data":{"hash":"`+hash+`"}}`)
			a.wg.Wait()
			wantEvent := 0
			if method == "remove_torrents" {
				wantEvent = 1
			}
			count := 0
			for _, raw := range ownedInputEvents(t, a, cookie, "OwnedInput") {
				var event struct{ Type string }
				_ = json.Unmarshal(raw, &event)
				if event.Type == "download.deleted" {
					count++
				}
			}
			if count != wantEvent || deletes != 0 || method == "list_torrents" && queries != 0 || method == "remove_torrents" && queries != 1 {
				t.Fatal("module false did not control the actual source/core consumer", count, queries, deletes)
			}
			observation := callTest(t, a.Handler(), "GET", "/api/v1/plugin/"+manifest.PluginID+"/snapshot", "", cookie, "")
			var snap struct {
				Calls int
				Input struct{ Kwargs map[string]json.RawMessage }
			}
			if json.Unmarshal(observation.Body.Bytes(), &snap) != nil || snap.Calls != 1 || string(snap.Input.Kwargs["downloader"]) != "null" || string(snap.Input.Kwargs["hashs"]) != `["`+hash+`"]` {
				t.Fatal("exact ordinary consumer wrapper kwargs", observation.Body.String())
			}
			if method == "list_torrents" && (len(snap.Input.Kwargs) != 4 || string(snap.Input.Kwargs["status"]) != "null" || string(snap.Input.Kwargs["include_all_tags"]) != "false") || method == "remove_torrents" && (len(snap.Input.Kwargs) != 3 || string(snap.Input.Kwargs["delete_file"]) != "false") {
				t.Fatal("exact source list/remove signature", observation.Body.String())
			}
		})
	}
}
