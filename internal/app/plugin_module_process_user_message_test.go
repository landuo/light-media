package app

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"runtime"
	"testing"
	"time"

	"light-media/internal/plugins"
	"light-media/internal/plugins/artifacts"
)

func verifyOwnedProductionUserMessages(t *testing.T, request func(string, string, any, int) []byte, success func(string, string, any)) {
	t.Helper()
	worker := os.Getenv("OWNED_USER_MESSAGE_PROCESS_FIXTURE_BINARY")
	payload, err := os.ReadFile(worker)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := exec.Command(worker, "--manifest").Output()
	if err != nil {
		t.Fatal(err)
	}
	var manifest plugins.Manifest
	if json.Unmarshal(raw, &manifest) != nil || manifest.Validate() != nil || manifest.PluginID != "OwnedEventFixture" {
		t.Fatal("actual ordinary user-message worker")
	}
	digest := sha256.Sum256(payload)
	sum := hex.EncodeToString(digest[:])
	index := artifacts.Index{SchemaVersion: 1, PluginID: manifest.PluginID, CurrentVersion: manifest.Version, Releases: []artifacts.Release{{Version: manifest.Version, Manifest: manifest, Artifacts: []artifacts.Artifact{{Platform: runtime.GOOS + "-" + runtime.GOARCH, Format: "executable", SHA256: sum, ExecutableSHA256: sum, Size: int64(len(payload)), HostContract: pluginBaseSDKContract}}}}}
	repo := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/index":
			_ = json.NewEncoder(w).Encode(index)
		case "/worker":
			_, _ = w.Write(payload)
		default:
			http.NotFound(w, r)
		}
	}))
	defer repo.Close()
	index.Releases[0].Artifacts[0].URL = repo.URL + "/worker"
	success("POST", "/api/plugins/install", map[string]any{"plugin_id": manifest.PluginID, "repo_url": repo.URL + "/index"})
	defer success("DELETE", "/api/plugins/"+manifest.PluginID, nil)
	success("PUT", "/api/plugins/"+manifest.PluginID, map[string]any{"enabled": true, "fail_event": true})
	success("POST", "/api/plugins/"+manifest.PluginID+"/clone", map[string]any{"suffix": "User"})
	clone := manifest.PluginID + "user"
	defer success("DELETE", "/api/plugins/"+clone, nil)
	success("PUT", "/api/plugins/"+clone, map[string]any{"enabled": true, "fail_event": false})
	texts := []string{" ordinary \u001cmessage ", "after failure", " /noai： ordinary escaped ", "ordinary image text"}
	expected := append([]string(nil), texts...)
	pids := map[string]int{}
	for i, text := range texts {
		var ack struct {
			Data struct{ Success, Accepted bool }
		}
		body := map[string]any{"text": text, "userid": "forged", "source": "forged", "channel": "Telegram", "chat_id": "forged"}
		if i == 3 {
			body["images"] = []any{map[string]any{"url": "https://owned.invalid/unused.jpg"}}
		}
		if json.Unmarshal(request("POST", "/api/v1/message/web", body, 200), &ack) != nil || !ack.Data.Success || !ack.Data.Accepted {
			t.Fatal("actual ordinary user-message not admitted")
		}
		for _, id := range []string{manifest.PluginID, clone} {
			until := time.Now().Add(5 * time.Second)
			for {
				var got struct {
					PID    int `json:"nonce"`
					Events []ownedBusinessEvent
				}
				if json.Unmarshal(request("GET", "/api/v1/plugin/"+id+"/events", nil, 200), &got) != nil || got.PID <= 0 {
					t.Fatal("actual user-message subscriber API")
				}
				pids[id] = got.PID
				events := []ownedBusinessEvent{}
				for _, e := range got.Events {
					if e.Type == "user.message" {
						events = append(events, e)
					}
				}
				if len(events) == i+1 {
					want, _ := json.Marshal(map[string]any{"text": expected[i], "userid": "service-acceptance", "source": "service-acceptance", "channel": "Web", "chat_id": nil, "reply_to_message_id": nil})
					if !originalBaseJSONEqual(events[i].Data, want) {
						t.Fatal("actual ordinary source payload", string(events[i].Data), string(want))
					}
					body, _ := json.Marshal(got)
					writeOwnedEventsProcessEvidence(t, "events36-user-message-"+id+".json", body)
					break
				}
				if len(events) > i+1 || time.Now().After(until) {
					t.Fatal("actual ordinary source/clone callback count", id, len(events), i+1)
				}
				time.Sleep(10 * time.Millisecond)
			}
		}
	}
	if pids[manifest.PluginID] == pids[clone] {
		t.Fatal("user-message source/clone shares SDK process")
	}
	t.Logf("actual Linux ordinary user.message: 4 real Web submissions preserve raw text/image text and trusted six fields; source PID%d callback fails after real Base SaveData while clone PID%d receives every event and subsequent message; no model-specific prefix rewriting/media parser or image retrieval", pids[manifest.PluginID], pids[clone])
}
