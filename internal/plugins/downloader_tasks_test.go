package plugins

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestDownloaderTasksSelectionAndSDKPrecision(t *testing.T) {
	hash := strings.Repeat("a", 40)
	for _, request := range []DownloaderTasksRequestV1{
		{DownloaderID: "owned", Hashes: nil},
		{DownloaderID: "", Hashes: []string{}},
		{DownloaderID: "owned/name", Hashes: []string{}},
		{DownloaderID: "owned", Hashes: []string{"all"}},
		{DownloaderID: "owned", Hashes: []string{"owned:" + hash}},
	} {
		if _, err := request.CanonicalSelection(); err == nil {
			t.Fatal("ambiguous selector accepted")
		}
	}
	for _, hashes := range [][]string{{}, {strings.ToUpper(hash), hash}} {
		got, err := (DownloaderTasksRequestV1{DownloaderID: "owned", Hashes: hashes}).CanonicalSelection()
		if err != nil || got == nil {
			t.Fatal("explicit selection rejected", err)
		}
		if len(hashes) > 0 && !reflect.DeepEqual(got, []string{hash}) {
			t.Fatal("canonical exact hash selection changed")
		}
	}
	// No invented business-count cap: the production host's byte ceiling is
	// the admission bound. Duplicates are collapsed, never expanded to all.
	many := make([]string, 257)
	for i := range many {
		many[i] = hash
	}
	if got, err := (DownloaderTasksRequestV1{DownloaderID: "owned", Hashes: many}).CanonicalSelection(); err != nil || len(got) != 1 {
		t.Fatal("invented count gate", err)
	}
	calls := 0
	d := Downloaders{Host: func(ctx context.Context, method string, raw json.RawMessage) (json.RawMessage, error) {
		calls++
		if method != DownloaderTasksReadCapability {
			t.Fatal("wrong capability")
		}
		var input DownloaderTasksRequestV1
		if json.Unmarshal(raw, &input) != nil || input.DownloaderID != "owned" || !reflect.DeepEqual(input.Hashes, []string{hash}) {
			t.Fatal("SDK changed selection")
		}
		return json.RawMessage(`{"schema_version":1,"downloader_id":"owned","kind":"transmission","tasks":[{"hash":"` + hash + `","transmission":{"id":9007199254740993,"uploadedEver":18446744073709551615}}]}`), nil
	}}
	out, err := d.Tasks(context.Background(), DownloaderTasksRequestV1{DownloaderID: "owned", Hashes: []string{hash}})
	if err != nil || calls != 1 || len(out.Tasks) != 1 || out.Tasks[0].Transmission == nil {
		t.Fatal("SDK decode failed", err)
	}
	row := out.Tasks[0].Transmission
	if row.ID == nil || *row.ID != "9007199254740993" || row.UploadedEver == nil || *row.UploadedEver != "18446744073709551615" {
		t.Fatal("integer precision lost")
	}
}
