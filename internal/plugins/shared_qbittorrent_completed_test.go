package plugins

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

func TestQBittorrentCompletedSDKRawFieldsAndNone(t *testing.T) {
	for _, failed := range []bool{false, true} {
		sdk := QBittorrent{Host: func(_ context.Context, method string, raw json.RawMessage) (json.RawMessage, error) {
			if method != QBittorrentCompletedQueryCapability {
				t.Fatal("wrong finite capability")
			}
			var request map[string]json.RawMessage
			if json.Unmarshal(raw, &request) != nil || string(request["ids"]) != "[]" || string(request["tags"]) != `[" 原标签 "]` {
				t.Fatal("source selector changed")
			}
			return json.Marshal(QBittorrentCompletedResult{SchemaVersion: 1, DownloaderID: "owned", Error: failed, Torrents: []QBittorrentTorrent{}})
		}}
		rows, err := sdk.GetCompletedTorrents(context.Background(), QBittorrentCompletedSelection{DownloaderID: "owned", IDs: []string{}, Tags: []string{" 原标签 "}})
		if err != nil || (rows == nil) != failed {
			t.Fatal("source None and successful [] conflated")
		}
	}
	sdk := QBittorrent{Host: func(context.Context, string, json.RawMessage) (json.RawMessage, error) {
		return json.RawMessage(`{"schema_version":1,"downloader_id":"owned","error":false,"torrents":[{"fields":{"hash":"unowned-source-hash","uploaded":18446744073709551615,"tags":" 原标签 ","tracker":"http://owned.invalid/synthetic","nested":{"x":9007199254740993}}}]}`), nil
	}}
	rows, err := sdk.GetCompletedTorrents(context.Background(), QBittorrentCompletedSelection{DownloaderID: "owned"})
	if err != nil || len(rows) != 1 {
		t.Fatal("full-field query failed", err)
	}
	var uploaded json.Number
	if rows[0].DecodeField("uploaded", &uploaded) != nil || uploaded != "18446744073709551615" || string(rows[0].Fields["nested"]) != `{"x":9007199254740993}` {
		t.Fatal("native precision/fields lost")
	}
	if _, exists := rows[0].Fields["tracker"]; !exists {
		t.Fatal("source full field projected away")
	}
}

func TestQBittorrentCompletedSDKRejectsInputAndHostFault(t *testing.T) {
	calls := 0
	sdk := QBittorrent{Host: func(context.Context, string, json.RawMessage) (json.RawMessage, error) {
		calls++
		return nil, errors.New("current host revoked")
	}}
	for _, request := range []QBittorrentCompletedSelection{{DownloaderID: string([]byte{255})}, {DownloaderID: "owned", IDs: []any{string([]byte{255})}}, {DownloaderID: "owned", Tags: map[string]any{"x": string([]byte{255})}}} {
		if _, err := sdk.GetCompletedTorrents(context.Background(), request); err == nil {
			t.Fatal("lossy Go Unicode accepted")
		}
	}
	if calls != 0 {
		t.Fatal("invalid input dispatched")
	}
	if rows, err := sdk.GetCompletedTorrents(context.Background(), QBittorrentCompletedSelection{DownloaderID: "owned"}); err == nil || rows != nil {
		t.Fatal("host failure became source None")
	}
	for _, raw := range []string{`null`, `{}`, `{"schema_version":1,"downloader_id":"other","torrents":[],"error":false}`, `{"schema_version":1,"downloader_id":"owned","torrents":[{"fields":{}}],"error":true}`, `{"schema_version":1,"downloader_id":"owned","torrents":[{"fields":null}],"error":false}`} {
		sdk.Host = func(context.Context, string, json.RawMessage) (json.RawMessage, error) {
			return json.RawMessage(raw), nil
		}
		if _, err := sdk.QueryCompletedTorrents(context.Background(), QBittorrentCompletedSelection{DownloaderID: "owned"}); err == nil {
			t.Fatal("invalid result accepted")
		}
	}
}
