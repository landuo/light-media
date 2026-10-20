package download

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestSourceTorrentRowsKeepRawFieldsSeparateAndUseOriginalRequests(t *testing.T) {
	raw, e := os.ReadFile("../../testdata/lark-command-source/transmission-fields.json")
	if e != nil {
		t.Fatal(e)
	}
	var source struct{ Fields []string }
	if e = json.Unmarshal(raw, &source); e != nil || len(source.Fields) != 28 {
		t.Fatal(e)
	}
	for _, kind := range []string{"qb", "transmission-jsonrpc", "transmission-legacy"} {
		t.Run(kind, func(t *testing.T) {
			queries := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if kind == "qb" {
					queries++
					if r.Method != "GET" || r.URL.Query().Get("filter") != "downloading" {
						t.Error("source query changed")
					}
					fmt.Fprint(w, `[{"hash":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","name":"owned","tags":"light-media","content_path":"/owned/item","category":"source-category","ratio_limit":2.5,"trackers":["https://tracker.invalid/announce?passkey=owned-private"]}]`)
					return
				}
				var call struct {
					Method            string
					Params, Arguments map[string]any
					ID, Tag           uint64
				}
				if json.NewDecoder(r.Body).Decode(&call) != nil {
					w.WriteHeader(400)
					return
				}
				if kind == "transmission-legacy" && call.Method == "session_get" {
					fmt.Fprint(w, `{"result":"success","arguments":{"version":"4.0.6"},"tag":1}`)
					return
				}
				legacy := strings.Contains(call.Method, "-")
				args := call.Params
				if legacy {
					args = call.Arguments
				}
				var result any = map[string]any{"version": "4.1.0"}
				if call.Method == "torrent_get" || call.Method == "torrent-get" {
					queries++
					fields := []string{}
					for _, f := range args["fields"].([]any) {
						fields = append(fields, f.(string))
					}
					hashKey := "hash_string"
					dirKey := "download_dir"
					if legacy {
						hashKey, dirKey = "hashString", "downloadDir"
					}
					if legacy && !reflect.DeepEqual(fields, source.Fields) {
						t.Error("legacy RPC did not preserve original field order/duplicates", fields)
					}
					if len(fields) != 28 || !slices.Contains(fields, hashKey) || !slices.Contains(fields, dirKey) || slices.Contains(fields, "download_limit") {
						t.Error("original _trarg fields changed", fields)
					}
					row := map[string]any{hashKey: strings.Repeat("b", 40), "name": "owned", "status": 4, "labels": []string{"light-media"}, dirKey: "/owned", "downloadLimit": 25, "seedRatioLimit": 2.5}
					result = map[string]any{"torrents": []any{row}}
				}
				w.Header().Set("Content-Type", "application/json")
				if legacy {
					json.NewEncoder(w).Encode(map[string]any{"tag": call.Tag, "result": "success", "arguments": result})
				} else {
					json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": call.ID, "result": result})
				}
			}))
			defer server.Close()
			driver := "tr"
			if kind == "qb" {
				driver = "qb"
			}
			client, e := New(Config{ID: "owned", Kind: driver, BaseURL: server.URL})
			if e != nil {
				t.Fatal(e)
			}
			out, e := client.SourceDownloadingRows(context.Background())
			if e != nil || len(out.Rows) != 1 || queries != 1 {
				t.Fatal(out.Kind, e, len(out.Rows), queries)
			}
			row := out.Rows[0]
			if kind == "qb" {
				if row["category"] != "source-category" || row["ratio_limit"] != json.Number("2.5") || row["content_path"] != "/owned/item" {
					t.Fatal("source fields discarded")
				}
			} else if row["hashString"] != strings.Repeat("b", 40) || row["downloadDir"] != "/owned" || row["downloadLimit"] != json.Number("25") || row["seedRatioLimit"] != json.Number("2.5") {
				t.Fatal("source RPC attributes discarded")
			}
		})
	}
}
