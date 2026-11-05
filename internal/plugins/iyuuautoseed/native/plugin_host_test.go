package download

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"testing"
	"time"
)

func TestPluginHostFindByTagFixedSourceAttemptsAndCleanup(t *testing.T) {
	var oracle struct {
		Lookups []struct {
			Present bool              `json:"present"`
			Sleeps  []int             `json:"sleeps"`
			Reads   []json.RawMessage `json:"reads"`
			Hash    *string           `json:"hash"`
		} `json:"lookups"`
	}
	raw, err := os.ReadFile("../../../../testdata/plugin-host-downloader-20261031/source-oracle.json")
	if err != nil || json.Unmarshal(raw, &oracle) != nil || len(oracle.Lookups) != 2 {
		t.Fatal("fixed source lookup oracle unavailable", err)
	}
	for _, scenario := range oracle.Lookups {
		t.Run(map[bool]string{true: "found", false: "timeout"}[scenario.Present], func(t *testing.T) {
			sleeps := []int{}
			reads, removes, deletes := 0, 0, 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_ = r.ParseForm()
				switch r.URL.Path {
				case "/api/v2/app/webapiVersion":
					_, _ = io.WriteString(w, "2.11.0")
				case "/api/v2/torrents/info":
					reads++
					if r.Form.Get("filter") != "stopped" {
						t.Error("source status filter changed")
					}
					if scenario.Present {
						_, _ = io.WriteString(w, `[{"hash":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","tags":"owned, second"}]`)
					} else {
						_, _ = io.WriteString(w, `[]`)
					}
				case "/api/v2/torrents/removeTags":
					removes++
					if r.Form.Get("hashes") != "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" || r.Form.Get("tags") != "owned" {
						t.Error("cleanup selector changed")
					}
				case "/api/v2/torrents/deleteTags":
					deletes++
					if r.Form.Get("tags") != "owned" {
						t.Error("global tag cleanup changed")
					}
				default:
					t.Error("unexpected lookup operation")
					http.Error(w, "unexpected", 400)
				}
			}))
			defer server.Close()
			client, err := NewIYUUClient(Config{ID: "owned", Kind: "qbittorrent", BaseURL: server.URL}, IYUUOptions{Sleep: func(ctx context.Context, d time.Duration) error {
				sleeps = append(sleeps, int(d/time.Second))
				return nil
			}})
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			status := "paused"
			hash, err := client.PluginHostFindByTag(context.Background(), []string{"owned"}, &status)
			want := ""
			if scenario.Hash != nil {
				want = *scenario.Hash
			}
			if err != nil || hash != want || !reflect.DeepEqual(sleeps, scenario.Sleeps) || reads != len(scenario.Reads) {
				t.Fatal("source retry/sleep/result changed", err, hash, sleeps, reads)
			}
			if scenario.Present && (removes != 1 || deletes != 1) || !scenario.Present && (removes != 0 || deletes != 0) {
				t.Fatal("source cleanup changed", removes, deletes)
			}
		})
	}
}

func TestPluginHostFindByTagReturnsObservedHashWhenCleanupRejected(t *testing.T) {
	removals := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v2/torrents/info" {
			_, _ = io.WriteString(w, `[{"hash":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","tags":"owned"}]`)
			return
		}
		removals++
		http.Error(w, "rejected", 403)
	}))
	defer server.Close()
	client, err := NewIYUUClient(Config{ID: "owned", Kind: "qbittorrent", BaseURL: server.URL}, IYUUOptions{Sleep: func(context.Context, time.Duration) error { return nil }})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	hash, err := client.PluginHostFindByTag(context.Background(), []string{"owned"}, nil)
	if err != nil || hash != "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" || removals != 1 {
		t.Fatal("source ignored-cleanup-false branch changed or write replayed", hash, err, removals)
	}
}
