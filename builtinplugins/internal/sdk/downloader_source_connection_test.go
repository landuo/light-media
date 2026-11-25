package plugins

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestSourceConnectionSDKRequiresExplicitPresenceAndInactive(t *testing.T) {
	for _, raw := range []string{`{"schema_version":1,"downloader_id":"owned","kind":"qbittorrent","inactive":false}`, `{"schema_version":1,"downloader_id":"owned","kind":"qbittorrent","exists":true}`, `{"schema_version":1,"downloader_id":"foreign","kind":"qbittorrent","inactive":false,"exists":true}`, `{"schema_version":1,"downloader_id":"owned","kind":"qbittorrent","inactive":true,"exists":false}`} {
		d := Downloaders{Host: func(context.Context, string, json.RawMessage) (json.RawMessage, error) {
			return json.RawMessage(raw), nil
		}}
		if _, err := d.SourceConnection(context.Background(), DownloaderConnectionRequestV1{DownloaderID: "owned"}); err == nil {
			t.Fatal("malformed/foreign presence accepted", raw)
		}
	}
	for _, flags := range []struct{ exists, inactive bool }{{false, false}, {true, false}, {true, true}} {
		d := Downloaders{Host: func(_ context.Context, method string, _ json.RawMessage) (json.RawMessage, error) {
			if method != DownloaderSourceConnectionCapability {
				t.Fatal(method)
			}
			return json.Marshal(DownloaderSourceConnectionV1{DownloaderConnectionV1: DownloaderConnectionV1{SchemaVersion: 1, DownloaderID: "owned", Kind: "transmission", Inactive: flags.inactive}, Exists: flags.exists})
		}}
		out, err := d.SourceConnection(context.Background(), DownloaderConnectionRequestV1{DownloaderID: "owned"})
		if err != nil || out.Exists != flags.exists || out.Inactive != flags.inactive {
			t.Fatal(out, err)
		}
	}
	sentinel := errors.New("owned scope failure")
	d := Downloaders{Host: func(context.Context, string, json.RawMessage) (json.RawMessage, error) { return nil, sentinel }}
	if _, err := d.SourceConnection(context.Background(), DownloaderConnectionRequestV1{DownloaderID: "owned"}); !errors.Is(err, sentinel) {
		t.Fatal("scope failure became inactive", err)
	}
}
func TestSourceFindByTagSDKKeepsFiniteSelectionAndIdentity(t *testing.T) {
	calls := 0
	d := Downloaders{Host: func(_ context.Context, method string, _ json.RawMessage) (json.RawMessage, error) {
		calls++
		if method != DownloaderSourceFindByTagCapability {
			t.Fatal(method)
		}
		return json.RawMessage(`{"schema_version":1,"downloader_id":"owned","hash":"` + strings.Repeat("a", 40) + `"}`), nil
	}}
	if _, err := d.SourceFindByTag(context.Background(), DownloaderFindByTagRequestV1{DownloaderID: "owned"}); err == nil || calls != 0 {
		t.Fatal("unselected lookup dispatched")
	}
	out, err := d.SourceFindByTag(context.Background(), DownloaderFindByTagRequestV1{DownloaderID: "owned", Tags: []string{"marker"}})
	if err != nil || out.Hash == nil || calls != 1 {
		t.Fatal(out, err, calls)
	}
}
