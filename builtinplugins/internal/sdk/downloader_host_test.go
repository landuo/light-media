package plugins

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestDownloaderHostExactEmptyAndValidation(t *testing.T) {
	var observed json.RawMessage
	d := Downloaders{Host: func(ctx context.Context, method string, raw json.RawMessage) (json.RawMessage, error) {
		observed = append([]byte{}, raw...)
		return json.RawMessage(`{"schema_version":1,"downloader_id":"owned","kind":"qbittorrent","tasks":[]}`), nil
	}}
	for _, hashes := range [][]string{nil, {}} {
		if _, err := d.Completed(context.Background(), DownloaderCompletedRequestV1{DownloaderID: "owned", Hashes: hashes}); err != nil {
			t.Fatal(err)
		}
		want := `"hashes":null`
		if hashes != nil {
			want = `"hashes":[]`
		}
		if !strings.Contains(string(observed), want) {
			t.Fatal("enumeration/empty selection lost in transport", string(observed))
		}
	}
	for _, request := range []DownloaderControlRequestV1{
		{DownloaderID: "owned", Action: "start"},
		{DownloaderID: "owned", Hashes: []string{}, Action: "remove_task"},
		{DownloaderID: "owned", Hashes: []string{"all"}, Action: "start"},
		{DownloaderID: "owned", Hashes: []string{strings.Repeat("a", 40)}, Action: "delete_files"},
	} {
		if request.Validate() == nil {
			t.Fatal("ambiguous/broad selector or data deletion accepted")
		}
	}
	if (DownloaderAddRequestV1{DownloaderID: "owned", Magnet: "https://other.invalid/private"}).Validate() == nil {
		t.Fatal("arbitrary downloader URL accepted")
	}
}

func TestDownloaderHostRejectsForeignOrUnselectedResponses(t *testing.T) {
	for _, response := range []string{
		`{"schema_version":1,"downloader_id":"other","kind":"qbittorrent","tasks":[]}`,
		`{"schema_version":1,"downloader_id":"owned","kind":"qbittorrent","tasks":[{"hash":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","qbittorrent":{}}]}`,
		`{"schema_version":1,"downloader_id":"owned","kind":"qbittorrent","tasks":[{"hash":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","qbittorrent":{},"transmission":{}}]}`,
	} {
		d := Downloaders{Host: func(context.Context, string, json.RawMessage) (json.RawMessage, error) {
			return json.RawMessage(response), nil
		}}
		if _, err := d.Completed(context.Background(), DownloaderCompletedRequestV1{DownloaderID: "owned", Hashes: []string{strings.Repeat("a", 40)}}); err == nil {
			t.Fatal("foreign or widened response accepted")
		}
	}
}
