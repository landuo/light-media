package plugins

import (
	"context"
	"encoding/json"
	"testing"
)

func TestTransmissionQueryTypedPreservesRawFieldsAndNumericPrecision(t *testing.T) {
	sdk := Transmission{Host: func(_ context.Context, method string, raw json.RawMessage) (json.RawMessage, error) {
		if method != TransmissionTorrentsQueryCapability {
			t.Fatal("wrong capability")
		}
		var req map[string]json.RawMessage
		if json.Unmarshal(raw, &req) != nil || string(req["ids"]) != "[]" {
			t.Fatal("empty source selector changed")
		}
		return json.RawMessage(`{"schema_version":1,"downloader_id":"owned","error":false,"torrents":[{"fields":{"id":9007199254740993,"hashString":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","name":"owned","status":3,"labels":[" 双语 "],"uploadedEver":18446744073709551615,"trackers":[{"announce":"https://owned.invalid/synthetic"}]}}]}`), nil
	}}
	got, err := sdk.GetTorrents(context.Background(), TransmissionQuery{DownloaderID: "owned", IDs: []string{}})
	if err != nil {
		t.Fatal(err)
	}
	row := got.Torrents[0]
	var id, uploaded json.Number
	if row.DecodeField("id", &id) != nil || row.DecodeField("uploadedEver", &uploaded) != nil || id != "9007199254740993" || uploaded != "18446744073709551615" {
		t.Fatal("numeric precision lost")
	}
	if status, err := row.Status(); err != nil || status != "download pending" {
		t.Fatal("source status changed")
	}
	if labels, err := row.Labels(); err != nil || len(labels) != 1 || labels[0] != " 双语 " {
		t.Fatal("returned native labels trimmed")
	}
	if _, ok := row.Fields["trackers"]; !ok {
		t.Fatal("full task material dropped")
	}
}

func TestTransmissionQueryTypedRejectsLossyInputAndInvalidResponse(t *testing.T) {
	calls := 0
	sdk := Transmission{Host: func(context.Context, string, json.RawMessage) (json.RawMessage, error) {
		calls++
		return json.RawMessage(`null`), nil
	}}
	for _, request := range []TransmissionQuery{{DownloaderID: string([]byte{255})}, {DownloaderID: "owned", IDs: []string{string([]byte{255})}}, {DownloaderID: "owned", Tags: string([]byte{255})}} {
		if _, err := sdk.GetTorrents(context.Background(), request); err == nil {
			t.Fatal("invalid Go Unicode accepted")
		}
	}
	if calls != 0 {
		t.Fatal("lossy input crossed callback")
	}
	for _, raw := range []string{`null`, `{}`, `{"schema_version":1,"downloader_id":"other","torrents":[],"error":false}`, `{"schema_version":1,"downloader_id":"owned","torrents":[{"fields":{}}],"error":true}`, `{"schema_version":1,"downloader_id":"owned","torrents":[{"fields":null}],"error":false}`} {
		sdk.Host = func(context.Context, string, json.RawMessage) (json.RawMessage, error) {
			return json.RawMessage(raw), nil
		}
		if _, err := sdk.GetTorrents(context.Background(), TransmissionQuery{DownloaderID: "owned"}); err == nil {
			t.Fatal("invalid response accepted")
		}
	}
}

func TestTransmissionQueryConvenienceNoneAndExactSourcePending(t *testing.T) {
	for _, completed := range []bool{false, true} {
		for _, failed := range []bool{false, true} {
			sdk := Transmission{Host: func(_ context.Context, method string, raw json.RawMessage) (json.RawMessage, error) {
				var query struct {
					Status []string `json:"status"`
				}
				if json.Unmarshal(raw, &query) != nil || len(query.Status) != 2 {
					t.Fatal("missing source filter")
				}
				want := "download_pending"
				if completed {
					want = "seed_pending"
				}
				if query.Status[1] != want {
					t.Fatal("source pending spelling was silently corrected")
				}
				return json.Marshal(TransmissionQueryResult{SchemaVersion: 1, DownloaderID: "owned", Torrents: []TransmissionTorrent{}, Error: failed})
			}}
			var rows []TransmissionTorrent
			var err error
			if completed {
				rows, err = sdk.GetCompletedTorrents(context.Background(), TransmissionSelection{DownloaderID: "owned"})
			} else {
				rows, err = sdk.GetDownloadingTorrents(context.Background(), TransmissionSelection{DownloaderID: "owned"})
			}
			if err != nil || (rows == nil) != failed {
				t.Fatal("source None and empty success conflated")
			}
		}
	}
}
