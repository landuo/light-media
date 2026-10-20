package download

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func TestSourceQBEpisodeFileIDsAndFixedMutations(t *testing.T) {
	var operations []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.Referer() != sOrigin(r)+"/" {
			t.Error("source episode operation lost configured authority or method")
		}
		r.ParseForm()
		operations = append(operations, r.URL.Path)
		switch r.URL.Path {
		case "/api/v2/torrents/files":
			if r.Form.Get("hash") != nativeHash {
				t.Error("widened source file selector")
			}
			io.WriteString(w, `[{"index":7,"id":99,"name":"Owned.S01E01.mkv"},{"id":99,"name":"Owned.S01E02.mkv"},{"index":90071992547409931,"name":"Owned.S01E03.mkv"}]`)
		case "/api/v2/torrents/filePrio":
			if r.Form.Get("hash") != nativeHash || r.Form.Get("id") != "1|90071992547409931" || r.Form.Get("priority") != "0" {
				t.Error("source unwanted file IDs rounded or replaced with positions", r.Form)
			}
		case "/api/v2/torrents/start":
			if r.Form.Get("hashes") != nativeHash {
				t.Error("source start selector")
			}
		case "/api/v2/torrents/setForceStart":
			if r.Form.Get("hashes") != nativeHash || r.Form.Get("value") != "true" {
				t.Error("source force-start selector")
			}
		default:
			t.Error("ungranted operation", r.URL.Path)
			w.WriteHeader(400)
		}
	}))
	defer server.Close()
	c, e := New(Config{ID: "owned", Kind: "qb", BaseURL: server.URL})
	if e != nil {
		t.Fatal(e)
	}
	files, e := c.SourceEpisodeFiles(context.Background(), nativeHash)
	if e != nil || len(files) != 3 || files[0].ID != "7" || files[1].ID != "1" || files[2].ID != "90071992547409931" {
		t.Fatal("qB source library index->id semantics differ", files, e)
	}
	if ok, e := c.SourceEpisodeFileSelection(context.Background(), nativeHash, []json.Number{files[1].ID, files[2].ID}, false); e != nil || !ok {
		t.Fatal(ok, e)
	}
	if ok, e := c.SourceEpisodeFileSelection(context.Background(), nativeHash, nil, false); e != nil || ok {
		t.Fatal("source qB empty IDs should be False without an HTTP call", ok, e)
	}
	for _, force := range []bool{false, true} {
		if ok, e := c.SourceStartEpisodeDownload(context.Background(), nativeHash, force); e != nil || !ok {
			t.Fatal(ok, e)
		}
	}
	if !reflect.DeepEqual(operations, []string{"/api/v2/torrents/files", "/api/v2/torrents/filePrio", "/api/v2/torrents/start", "/api/v2/torrents/setForceStart"}) {
		t.Fatal("source fixed operation sequence changed", operations)
	}
	if _, e = c.SourceEpisodeFiles(context.Background(), "other:"+nativeHash); e == nil || len(operations) != 4 {
		t.Fatal("foreign instance selector reached daemon", e)
	}
}

func TestSourceTransmissionEpisodeFilesZipAndBothDialects(t *testing.T) {
	for _, dialect := range []string{"legacy", "jsonrpc"} {
		t.Run(dialect, func(t *testing.T) {
			var operations []string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				q := nativeJSON(t, body)
				method := q["method"].(string)
				args, id := q["params"], q["id"]
				if dialect == "legacy" {
					args, id = q["arguments"], q["tag"]
				}
				operations = append(operations, method)
				if !reflect.DeepEqual(args.(map[string]any)["ids"], []any{nativeHash}) {
					t.Error("source Transmission selector widened", args)
				}
				var value any = map[string]any{}
				switch strings.ReplaceAll(method, "-", "_") {
				case "torrent_get":
					key := "hash_string"
					if dialect == "legacy" {
						key = "hashString"
					}
					value = map[string]any{"torrents": []any{map[string]any{key: nativeHash, "files": []any{map[string]any{"name": "Owned.S01E01.mkv"}, map[string]any{"name": "Owned.S01E02.mkv"}}, "priorities": []int{0, 0}, "wanted": []bool{true}}}}
				case "torrent_set":
					key := "files_wanted"
					if len(operations) == 3 {
						key = "files_unwanted"
					}
					if dialect == "legacy" {
						key = strings.ReplaceAll(key, "_", "-")
					}
					want := []any{}
					if len(operations) == 3 {
						want = []any{json.Number("1"), json.Number("90071992547409931")}
					}
					if !reflect.DeepEqual(args.(map[string]any)[key], want) {
						t.Error("source wanted/unwanted wire differs", args, key)
					}
				case "torrent_start":
				default:
					t.Error("ungranted operation", method)
				}
				if dialect == "legacy" {
					reply(w, map[string]any{"tag": id, "result": "success", "arguments": value})
				} else {
					reply(w, map[string]any{"jsonrpc": "2.0", "id": id, "result": value})
				}
			}))
			defer server.Close()
			c, e := New(Config{ID: "owned", Kind: "tr", BaseURL: server.URL})
			if e != nil {
				t.Fatal(e)
			}
			// This test owns an already-negotiated native client; negotiation and
			// authentication have independent transport/dialect coverage.
			c.trDialect = dialect
			files, e := c.SourceEpisodeFiles(context.Background(), nativeHash)
			if e != nil || len(files) != 1 || files[0].ID != "0" || files[0].Name != "Owned.S01E01.mkv" {
				t.Fatal("source zip(files,priorities,wanted) semantics differ", files, e)
			}
			for _, wanted := range []bool{true, false} {
				var ids []json.Number
				if !wanted {
					ids = []json.Number{"1", "90071992547409931"}
				}
				if ok, e := c.SourceEpisodeFileSelection(context.Background(), nativeHash, ids, wanted); e != nil || !ok {
					t.Fatal(ok, e)
				}
			}
			if ok, e := c.SourceStartEpisodeDownload(context.Background(), nativeHash, false); e != nil || !ok || len(operations) != 4 {
				t.Fatal(ok, e, operations)
			}
		})
	}
}
