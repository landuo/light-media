package download

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"sync/atomic"
	"testing"
)

func TestSourceQbCategoryActualSourceAndProtocol(t *testing.T) {
	raw, err := os.ReadFile("testdata/category-source-oracle.json")
	var oracle struct {
		Cases []struct {
			Enabled  bool
			Category *string
			Calls    []struct {
				Auto     bool `json:"use_auto_torrent_management"`
				Category *string
			}
		}
	}
	if err != nil || json.Unmarshal(raw, &oracle) != nil || len(oracle.Cases) != 3 {
		t.Fatal("fixed category source oracle", err)
	}
	for _, sample := range oracle.Cases {
		t.Run(strconv.FormatBool(sample.Enabled)+"_"+strconv.FormatBool(sample.Category != nil), func(t *testing.T) {
			torrent, hash := testTorrent()
			var added atomic.Bool
			if len(sample.Calls) != 1 {
				t.Fatal("fixed source qbc call count")
			}
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/v2/torrents/info":
					if added.Load() {
						reply(w, []any{qbRow(hash)})
					} else {
						reply(w, []any{})
					}
				case "/api/v2/torrents/add":
					if r.ParseMultipartForm(4<<20) != nil {
						t.Error("actual category multipart")
					}
					want := sample.Calls[0]
					if r.FormValue("autoTMM") != strconv.FormatBool(want.Auto) {
						t.Error("source autoTMM", r.FormValue("autoTMM"), want.Auto)
					}
					category, exists := r.MultipartForm.Value["category"]
					if want.Category == nil && exists || want.Category != nil && (!exists || len(category) != 1 || category[0] != *want.Category) {
						t.Error("source native category", category, want.Category)
					}
					added.Store(true)
					_, _ = io.WriteString(w, "Ok.")
				default:
					http.NotFound(w, r)
				}
			}))
			defer peer.Close()
			client, err := New(Config{ID: "owned", Kind: "qb", BaseURL: peer.URL, AutoCategory: sample.Enabled})
			if err != nil {
				t.Fatal(err)
			}
			result, err := client.Add(context.Background(), AddRequest{Torrent: torrent, SavePath: "/owned/downloads", Tags: []string{"light-media"}, SourceCategorySet: true, SourceCategory: sample.Category})
			if err != nil || result.Hash != hash || !added.Load() {
				t.Fatal("category Add was not actually observed", result, err)
			}
		})
	}
}
