package downloadersource

import (
	"bytes"
	"encoding/json"
	"os"
	"reflect"
	"testing"
)

func TestInstalledDownloaderTorrentAndRemoteMessage(t *testing.T) {
	raw, e := os.ReadFile("../../testdata/lark-command-source/oracle.json")
	if e != nil {
		t.Fatal(e)
	}
	// The oracle's metadata uses original attribute spelling.
	var wire struct {
		TorrentCases []struct {
			Kind, Name, Error string
			Rows              []map[string]any
			Meta              map[string]string
			Expected          json.RawMessage
		} `json:"torrent_cases"`
		Messages []struct {
			Kind, Name string
			Expected   []map[string]any
		} `json:"messages"`
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if e = dec.Decode(&wire); e != nil || len(wire.TorrentCases) != 49 || len(wire.Messages) != 49 {
		t.Fatal(e)
	}
	for i, c := range wire.TorrentCases {
		t.Run(c.Kind+"/"+c.Name, func(t *testing.T) {
			rows, caught, e := Filter(c.Kind, c.Rows, "light-media")
			if e != nil || caught != "" {
				t.Fatal(caught, e)
			}
			got := []Torrent{}
			year := c.Meta["year"]
			for _, r := range rows {
				m := Meta{Name: c.Meta["name"], Year: &year, SeasonEpisode: c.Meta["season_episode"]}
				norm := func(p string) (string, error) { return PosixPath(p), nil }
				var item Torrent
				if c.Kind == "qbittorrent" {
					item, e = BuildQB(r, "Native synthetic", m, norm)
				} else {
					var attrs map[string]any
					attrs, e = TRAttributes(r)
					if e == nil {
						item, e = BuildTR(attrs, "Native synthetic", m, norm)
					}
				}
				if e != nil {
					t.Fatal(e)
				}
				got = append(got, item)
			}
			normJSON := func(v any) any {
				b, e := json.Marshal(v)
				if e != nil {
					t.Fatal(e)
				}
				var out any
				if e = json.Unmarshal(b, &out); e != nil {
					t.Fatal(e)
				}
				return out
			}
			if !reflect.DeepEqual(normJSON(got), normJSON(c.Expected)) {
				t.Fatalf("source model differs\ngot=%s\nwant=%s", mustSourceJSON(got), mustSourceJSON(c.Expected))
			}
			title, text, e := DownloadingMessage(got)
			if e != nil {
				t.Fatal(e)
			}
			want := wire.Messages[i].Expected[0]
			if title != want["title"] || text != "" && text != want["text"] {
				t.Fatal(title, text, want)
			}
		})
	}
}
func mustSourceJSON(v any) string { b, _ := json.Marshal(v); return string(b) }
func TestSourceQueryStatusAndTagFailureDoNotUsePublicTaskPolicy(t *testing.T) {
	rows := []map[string]any{{"status": json.Number("3"), "labels": []any{"light-media"}}, {"status": json.Number("4"), "labels": []any{"light-media"}}}
	got, caught, e := Filter("transmission", rows, "light-media")
	if e != nil || caught != "" || len(got) != 1 || got[0]["status"] != json.Number("4") {
		t.Fatal(got, caught, e)
	}
	got, caught, e = Filter("qbittorrent", []map[string]any{{"tags": nil}}, "light-media")
	if e != nil || caught != "AttributeError" || len(got) != 0 {
		t.Fatal(got, caught, e)
	}
}
