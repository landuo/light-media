package download

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTransmissionDeleteExplicitlyDistinguishesTaskAndDataForBothDialects(t *testing.T) {
	for _, dialect := range []string{"jsonrpc", "legacy"} {
		t.Run(dialect, func(t *testing.T) {
			hash := strings.Repeat("a", 40)
			deletions := []bool{}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var call map[string]any
				if err := json.NewDecoder(r.Body).Decode(&call); err != nil {
					t.Error(err)
					return
				}
				method, _ := call["method"].(string)
				if dialect == "legacy" && method == "session_get" {
					json.NewEncoder(w).Encode(map[string]any{"arguments": map[string]any{}, "result": "method name not recognized", "tag": call["id"]})
					return
				}
				method = strings.ReplaceAll(method, "-", "_")
				result := map[string]any{}
				switch method {
				case "session_get":
					result["version"] = "4.1.0"
				case "torrent_get":
					if dialect == "legacy" {
						result["torrents"] = []any{map[string]any{"hashString": hash, "name": "own.mkv", "status": 4, "downloadDir": "/downloads", "totalSize": 100, "leftUntilDone": 50, "percentDone": .5, "labels": []string{"light-media"}}}
					} else {
						result["torrents"] = []any{map[string]any{"hash_string": hash, "name": "own.mkv", "status": 4, "download_dir": "/downloads", "total_size": 100, "left_until_done": 50, "percent_done": .5, "labels": []string{"light-media"}}}
					}
				case "torrent_remove":
					argsKey, dataKey := "params", "delete_local_data"
					if dialect == "legacy" {
						argsKey, dataKey = "arguments", "delete-local-data"
					}
					args, ok := call[argsKey].(map[string]any)
					if !ok {
						t.Errorf("missing %s", argsKey)
						return
					}
					deleteData, ok := args[dataKey].(bool)
					if !ok {
						t.Errorf("missing explicit %s", dataKey)
					}
					ids, _ := args["ids"].([]any)
					if len(ids) != 1 || ids[0] != hash {
						t.Errorf("mutation did not use the single verified hash: %v", ids)
					}
					deletions = append(deletions, deleteData)
				default:
					t.Errorf("unexpected RPC %s", method)
				}
				if dialect == "legacy" {
					json.NewEncoder(w).Encode(map[string]any{"arguments": result, "result": "success", "tag": call["tag"]})
				} else {
					json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "result": result, "id": call["id"]})
				}
			}))
			defer server.Close()
			client, err := New(Config{ID: "tr", Kind: "tr", BaseURL: server.URL})
			if err != nil {
				t.Fatal(err)
			}
			for _, action := range []string{"remove_keep_data", "remove_with_data"} {
				if err := client.Control(context.Background(), hash, action); err != nil {
					t.Fatal(err)
				}
			}
			if len(deletions) != 2 || deletions[0] || !deletions[1] {
				t.Fatalf("deletion data policy was not explicit: %v", deletions)
			}
		})
	}
}
