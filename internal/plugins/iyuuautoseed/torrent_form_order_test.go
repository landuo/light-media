package iyuuautoseed

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"testing"
)

type orderedTorrentFormPeer struct {
	*torrentOracleHost
	order []string
	data  map[string]string
}

func (p *orderedTorrentFormPeer) PostOrdered(ctx context.Context, address string, data map[string]string, order []string) (*TorrentResponse, error) {
	p.order = append([]string{}, order...)
	p.data = map[string]string{}
	for k, v := range data {
		p.data[k] = v
	}
	return p.torrentOracleHost.Post(ctx, address, data)
}
func TestTorrentFirstDownloadPreservesSourceFormOrder(t *testing.T) {
	if fmt.Sprintf("%x", sha256.Sum256(sourceTorrentFormOrderJSON)) != "b33a38722142eee1a72fe8450c376d80354037681dc669d1a56c9a8df1a47497" {
		t.Fatal("source oracle binding changed")
	}
	var observed struct {
		InputHTML string     `json:"input_html"`
		Matches   [][]string `json:"regex_matches"`
		Ordered   [][]string `json:"ordered_items"`
	}
	if err := json.Unmarshal(sourceTorrentFormOrderJSON, &observed); err != nil {
		t.Fatal(err)
	}
	matches := [][]string{}
	for _, match := range firstInputs.FindAllStringSubmatch(observed.InputHTML, -1) {
		matches = append(matches, match[1:])
	}
	if !reflect.DeepEqual(matches, observed.Matches) {
		t.Fatalf("source regex %v target %v", observed.Matches, matches)
	}
	page := "下载种子文件<form action=\"?\">" + observed.InputHTML + "</form>"
	torrent := []byte("d4:infod6:lengthi1e4:name9:owned.mkvee")
	legacy := func() *torrentOracleHost {
		return &torrentOracleHost{cache: map[string][]byte{}, rows: []*TorrentResponse{{Status: 200, Content: []byte(page), Text: page}, {Status: 200, Content: torrent}}}
	}
	peer := &orderedTorrentFormPeer{torrentOracleHost: legacy()}
	out, err := (TorrentHelper{HTTP: peer, Cache: peer}).Download(context.Background(), "https://owned.invalid/form", true)
	actualItems := [][]string{}
	for _, key := range peer.order {
		actualItems = append(actualItems, []string{key, peer.data[key]})
	}
	if err != nil || !reflect.DeepEqual(out.Files, []string{"owned.mkv"}) || !reflect.DeepEqual(actualItems, observed.Ordered) {
		t.Fatalf("ordered form: %#v %#v %#v %v", out, peer.order, peer.data, err)
	}
	old := legacy()
	fallback, err := (TorrentHelper{HTTP: old, Cache: old}).Download(context.Background(), "https://owned.invalid/form", true)
	if err != nil || !reflect.DeepEqual(out, fallback) {
		t.Fatalf("legacy Post contract changed: %#v %v", fallback, err)
	}
}

//go:embed testdata/torrent-form-order-source.json
var sourceTorrentFormOrderJSON []byte

// The pinned helper indexes headers["Location"] before any second GET.
func TestTorrentRedirectMissingLocationSourceException(t *testing.T) {
	peer := &torrentOracleHost{cache: map[string][]byte{}, rows: []*TorrentResponse{{Status: 301, Headers: map[string]string{}}, {Status: 200, Content: []byte("unreachable")}}}
	out, err := (TorrentHelper{HTTP: peer, Cache: peer}).Download(context.Background(), "https://owned.invalid/missing-location", true)
	var source *SourceFault
	if !errors.As(err, &source) || source.Class != "KeyError" || out.Content != nil || out.Magnet != "" || len(peer.rows) != 1 || len(peer.cache) != 0 {
		t.Fatalf("source missing Location exception changed: %#v %v remaining=%d", out, err, len(peer.rows))
	}
	if len(peer.calls) != 2 || peer.calls[0].(map[string]any)["op"] != "cache_get" || peer.calls[1].(map[string]any)["op"] != "get" {
		t.Fatalf("missing Location performed another request/cache write: %#v", peer.calls)
	}
}
