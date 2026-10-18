package download

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDefaultSavePathIsReadOnlyForBothTransmissionDialects(t *testing.T) {
	for _, dialect := range []string{"jsonrpc", "legacy"} {
		t.Run(dialect, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var request map[string]any
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Error(err)
					return
				}
				method, _ := request["method"].(string)
				if method != "session_get" && method != "session-get" {
					t.Errorf("preferences read mutated daemon: %s", method)
				}
				w.Header().Set("Content-Type", "application/json")
				if dialect == "legacy" {
					if method == "session_get" {
						_ = json.NewEncoder(w).Encode(map[string]any{"result": "method name not recognized", "arguments": map[string]any{}, "tag": request["id"]})
						return
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"result": "success", "arguments": map[string]any{"version": "4.0.6", "download-dir": "/media/download/"}, "tag": request["tag"]})
				} else {
					_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": request["id"], "result": map[string]any{"version": "4.1.0", "download_dir": "/media/download/"}})
				}
			}))
			defer server.Close()
			client, err := New(Config{ID: "tr", Kind: "transmission", BaseURL: server.URL})
			if err != nil {
				t.Fatal(err)
			}
			path, err := client.DefaultSavePath(context.Background())
			if err != nil || path != "/media/download" {
				t.Fatalf("default directory wrong: %q %v", path, err)
			}
		})
	}
}

func TestDefaultSavePathRejectsMissingAndRelativeDaemonSettings(t *testing.T) {
	for _, value := range []string{"", "relative/path", "/"} {
		t.Run(value, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_ = json.NewEncoder(w).Encode(map[string]any{"save_path": value})
			}))
			defer server.Close()
			client, err := New(Config{ID: "qb", Kind: "qb", BaseURL: server.URL})
			if err != nil {
				t.Fatal(err)
			}
			if _, err = client.DefaultSavePath(context.Background()); err == nil {
				t.Fatal("unsafe default accepted")
			}
		})
	}
}
