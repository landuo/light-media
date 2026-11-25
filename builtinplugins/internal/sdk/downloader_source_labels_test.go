package plugins

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestSourceAddQBLabelsFiniteSDKProtocol(t *testing.T) {
	for n := 0; n <= 4; n++ {
		labels := []string{"a", "b", "c", "d"}[:n]
		calls := 0
		d := Downloaders{Host: func(_ context.Context, method string, raw json.RawMessage) (json.RawMessage, error) {
			calls++
			if method != DownloaderSourceAddCapability {
				t.Fatal(method)
			}
			var request DownloaderSourceAddRequestV1
			if json.Unmarshal(raw, &request) != nil || !reflect.DeepEqual(request.QBLabels, labels) && len(labels) > 0 {
				t.Fatal("SDK labels changed", string(raw))
			}
			if len(labels) == 0 && strings.Contains(string(raw), `"qb_labels"`) {
				t.Fatal("empty labels field was not omitted")
			}
			return json.RawMessage(`{"schema_version":1,"downloader_id":"owned","accepted":true,"outcome":"confirmed","native_error":false}`), nil
		}}
		out, err := d.SourceAdd(context.Background(), DownloaderSourceAddRequestV1{DownloaderAddRequestV1: DownloaderAddRequestV1{DownloaderID: "owned", Torrent: []byte("owned")}, QBLabels: labels})
		if err != nil || !out.Accepted || calls != 1 {
			t.Fatal("labels finite client outcome", out, err, calls)
		}
	}
	calls := 0
	d := Downloaders{Host: func(context.Context, string, json.RawMessage) (json.RawMessage, error) { calls++; return nil, nil }}
	for _, labels := range [][]string{{string([]byte{0xff})}, {strings.Repeat("x", MaxPayloadBytes+1)}, {strings.Repeat("\x00", MaxPayloadBytes/6)}} {
		if _, err := d.SourceAdd(context.Background(), DownloaderSourceAddRequestV1{DownloaderAddRequestV1: DownloaderAddRequestV1{DownloaderID: "owned"}, QBLabels: labels}); err == nil {
			t.Fatal("invalid/oversize label request accepted")
		}
	}
	if calls != 0 {
		t.Fatal("invalid labels contacted host")
	}
}
