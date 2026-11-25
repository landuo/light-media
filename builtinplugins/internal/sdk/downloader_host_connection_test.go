package plugins

import (
	"context"
	"encoding/json"
	"testing"
)

func TestDownloaderHostConnectionRequiresExplicitBooleanAndIdentity(t *testing.T) {
	for _, response := range []string{
		`{"schema_version":1,"downloader_id":"owned","kind":"qbittorrent"}`,
		`{"schema_version":1,"downloader_id":"other","kind":"qbittorrent","inactive":false}`,
		`{"schema_version":1,"downloader_id":"owned","kind":"other","inactive":false}`,
		`{"schema_version":1,"downloader_id":"owned","kind":"qbittorrent","inactive":false,"password":"unexpected"}`,
	} {
		d := Downloaders{Host: func(context.Context, string, json.RawMessage) (json.RawMessage, error) {
			return json.RawMessage(response), nil
		}}
		if _, err := d.Connection(context.Background(), DownloaderConnectionRequestV1{DownloaderID: "owned"}); err == nil {
			t.Fatal("forged/missing connection status became active", response)
		}
	}
	for _, inactive := range []bool{false, true} {
		d := Downloaders{Host: func(_ context.Context, method string, raw json.RawMessage) (json.RawMessage, error) {
			if method != DownloaderConnectionCapability || string(raw) != `{"downloader_id":"owned"}` {
				t.Fatal("probe widened request", method, string(raw))
			}
			return json.Marshal(DownloaderConnectionV1{SchemaVersion: 1, DownloaderID: "owned", Kind: "qbittorrent", Inactive: inactive})
		}}
		out, err := d.Connection(context.Background(), DownloaderConnectionRequestV1{DownloaderID: "owned"})
		if err != nil || out.Inactive != inactive {
			t.Fatal(out, err)
		}
	}
}
