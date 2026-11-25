package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"light-media/internal/download"
	"light-media/internal/plugins"
)

func TestOriginalDownloaderEnumerationSelection(t *testing.T) {
	a, b, c := fmt.Sprintf("%040x", 10), fmt.Sprintf("%040x", 11), fmt.Sprintf("%040x", 12)
	for _, kind := range []string{"qbittorrent", "transmission"} {
		t.Run(kind, func(t *testing.T) {
			key := "hash"
			if kind == "transmission" {
				key = "hashString"
			}
			raw := json.RawMessage(fmt.Sprintf(`[{"%s":"%s"},{"%s":"%s"},{"%s":"%s"}]`, key, b, key, c, key, a))
			for _, tc := range []struct {
				name string
				s    originalDownloaderTaskSelection
				want []string
			}{
				{"enumerate", originalDownloaderTaskSelection{daemonOrder: true}, []string{b, c, a}},
				{"filtered", originalDownloaderTaskSelection{daemonOrder: true, hashes: []string{a, b}}, []string{b, a}},
				{"empty filter", originalDownloaderTaskSelection{daemonOrder: true, hashes: []string{}}, nil},
				{"exact", originalDownloaderTaskSelection{hashes: []string{a, b}}, []string{a, b}},
				{"absent exact", originalDownloaderTaskSelection{}, nil},
			} {
				got, err := tc.s.project(kind, raw, false, false)
				if err != nil || got == nil || len(got) != len(tc.want) {
					t.Fatalf("%s: got %v, %v", tc.name, got, err)
				}
				for i, hash := range tc.want {
					if got[i].Hash != hash {
						t.Fatalf("%s: got %s at %d; want %s", tc.name, got[i].Hash, i, hash)
					}
				}
			}
			for _, badRow := range []string{fmt.Sprintf(`{"%s":"%s"}`, key, b), fmt.Sprintf(`{"%s":"invalid"}`, key)} {
				repeatedOrInvalid := json.RawMessage(fmt.Sprintf(`[{"%s":"%s"},%s]`, key, b, badRow))
				if got, err := (originalDownloaderTaskSelection{daemonOrder: true, hashes: []string{b}}).project(kind, repeatedOrInvalid, false, false); err == nil || got != nil {
					t.Fatalf("accepted ambiguous/invalid daemon response")
				}
			}
		})
	}
}

// Replay the same finite daemon inventory on loopback. Measure the whole host
// callback (including client setup, native filtering and response encoding),
// but not fixture construction or the plugin's own decoding of the response.
func BenchmarkBrushHostTaskEnumeration(b *testing.B) {
	const count = 512
	rows := make([]map[string]any, count)
	for i := range rows {
		row := workflowTask(fmt.Sprintf("%040x", i+1), 0.5)
		row["state"], row["tags"], row["tracker"] = "downloading", "brush", "https://tracker.example/announce"
		row["uploaded"], row["added_on"] = 123456, 1700000000
		rows[i] = row
	}
	body, err := json.Marshal(rows)
	if err != nil {
		b.Fatal(err)
	}
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2/torrents/info" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}))
	defer peer.Close()
	a := &App{}
	a.config.Downloaders = []DownloaderConfig{{Config: download.Config{ID: "bench", Kind: "qbittorrent", BaseURL: peer.URL}}}
	object := &originalPluginInstance{app: a, installation: PluginInstallation{PluginID: "BrushFlowLanduo"}}
	scope := originalBaseCallScope{object: object, installation: object.installation, phase: "check", validate: func(context.Context) error { return nil }}
	for _, tc := range []struct{ method, request string }{
		{plugins.DownloaderListCapability, `{"downloader_id":"bench"}`},
		{plugins.DownloaderDownloadingCapability, `{"downloader_id":"bench","tags":["brush"]}`},
	} {
		b.Run(tc.method, func(b *testing.B) {
			raw := json.RawMessage(tc.request)
			call := func() json.RawMessage {
				result, err := a.originalHostDownloaderResource(b.Context(), scope, tc.method, raw)
				if err != nil {
					b.Fatal(err)
				}
				return result
			}
			var result plugins.DownloaderTasksV1
			if err := json.Unmarshal(call(), &result); err != nil || len(result.Tasks) != count {
				b.Fatalf("got %d tasks: %v", len(result.Tasks), err)
			}
			b.ReportAllocs()
			for b.Loop() {
				call()
			}
		})
	}
}
