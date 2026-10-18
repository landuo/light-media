package iyuuautoseed

import (
	"context"
	"encoding/base64"
	"os"
	"reflect"
	"testing"
)

type torrentOracleHost struct {
	calls []any
	rows  []*TorrentResponse
	cache map[string][]byte
}

func (h *torrentOracleHost) Get(_ context.Context, url string) (*TorrentResponse, error) {
	h.calls = append(h.calls, map[string]any{"op": "get", "args": map[string]any{"url": url, "allow_redirects": false}})
	v := h.rows[0]
	h.rows = h.rows[1:]
	return v, nil
}
func (h *torrentOracleHost) Post(_ context.Context, url string, data map[string]string) (*TorrentResponse, error) {
	h.calls = append(h.calls, map[string]any{"op": "post", "args": map[string]any{"url": url, "data": data}})
	v := h.rows[0]
	h.rows = h.rows[1:]
	return v, nil
}
func (h *torrentOracleHost) GetTorrent(_ context.Context, key string) ([]byte, error) {
	h.calls = append(h.calls, map[string]any{"op": "cache_get", "key": key, "region": "torrents"})
	return h.cache[key], nil
}
func (h *torrentOracleHost) PutTorrent(_ context.Context, key string, data []byte) error {
	h.calls = append(h.calls, map[string]any{"op": "cache_put", "key": key, "region": "torrents", "content": base64.StdEncoding.EncodeToString(data)})
	h.cache[key] = data
	return nil
}
func (h *torrentOracleHost) AddInvalid(_ context.Context, url string) error {
	h.calls = append(h.calls, map[string]any{"op": "invalid", "url": url})
	return nil
}
func oracleBase64(t *testing.T, v any) []byte {
	t.Helper()
	s, ok := v.(string)
	if !ok {
		t.Fatalf("not base64 string: %T", v)
	}
	raw, e := base64.StdEncoding.DecodeString(s)
	if e != nil {
		t.Fatal(e)
	}
	return raw
}
func TestInstalledTorrentHelperOracle(t *testing.T) {
	raw, e := os.ReadFile("reference/torrent-helper-oracle.json")
	if e != nil {
		t.Fatal(e)
	}
	v, e := decodeJSON(raw)
	if e != nil {
		t.Fatal(e)
	}
	doc := v.(map[string]any)
	for _, raw := range doc["files"].([]any) {
		row := raw.(map[string]any)
		t.Run(row["name"].(string), func(t *testing.T) {
			folder, files := TorrentFiles(oracleBase64(t, row["content"]))
			got := map[string]any{"value": []any{folder, files}}
			assertOracle(t, got, row["result"])
		})
	}
	if len(doc["downloads"].([]any)) != 18 {
		t.Fatal("download case count")
	}
	for _, raw := range doc["downloads"].([]any) {
		row := raw.(map[string]any)
		t.Run(row["name"].(string), func(t *testing.T) {
			url := row["url"].(string)
			h := &torrentOracleHost{calls: []any{}, cache: map[string][]byte{}}
			if b := oracleBase64(t, row["initial_cache"]); len(b) > 0 {
				h.cache[torrentCachePath(url)] = b
			}
			for _, r := range row["responses"].([]any) {
				if r == nil {
					h.rows = append(h.rows, nil)
					continue
				}
				m := r.(map[string]any)
				headers := map[string]string{}
				for k, v := range m["headers"].(map[string]any) {
					headers[k] = v.(string)
				}
				status, _ := m["status"].(interface{ Int64() (int64, error) }).Int64()
				content := oracleBase64(t, m["content"])
				h.rows = append(h.rows, &TorrentResponse{Status: int(status), Content: content, Text: pythonUTF8Replace(content), Headers: headers})
			}
			out, e := (TorrentHelper{HTTP: h, Cache: h}).Download(context.Background(), url, row["cache_invalid"].(bool))
			if e != nil {
				t.Fatal(e)
			}
			var cachePath, content any
			if out.CachePath != "" {
				cachePath = map[string]any{"$path": out.CachePath}
			}
			if out.Magnet != "" {
				content = out.Magnet
			} else if len(out.Content) > 0 {
				content = map[string]any{"$bytes": base64.StdEncoding.EncodeToString(out.Content)}
			}
			assertOracle(t, map[string]any{"value": []any{cachePath, content, out.Folder, out.Files, out.ErrorMessage}}, row["result"])
			wantCalls := []any{}
			for _, c := range row["calls"].([]any) {
				if c.(map[string]any)["op"] != "http_init" {
					wantCalls = append(wantCalls, c)
				}
			}
			assertOracle(t, h.calls, wantCalls)
		})
	}
}
func assertOracle(t *testing.T, got, want any) {
	t.Helper()
	g, e := cloneJSON(got)
	if e != nil {
		t.Fatal(e)
	}
	w, e := cloneJSON(want)
	if e != nil {
		t.Fatal(e)
	}
	if !reflect.DeepEqual(g, w) {
		t.Fatalf("got %#v\nwant %#v", g, w)
	}
}
