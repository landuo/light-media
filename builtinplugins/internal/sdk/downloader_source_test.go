package plugins

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestSourceDownloaderSDKRejectsMalformedOrForeignOutcomes(t *testing.T) {
	for _, raw := range []string{`{"schema_version":1,"downloader_id":"owned","kind":"qbittorrent","tasks":[]}`, `{"schema_version":1,"downloader_id":"owned","kind":"qbittorrent","tasks":[],"native_error":null}`, `{"schema_version":1,"downloader_id":"other","kind":"qbittorrent","tasks":[],"native_error":true}`, `{"schema_version":1,"downloader_id":"owned","kind":"qbittorrent","tasks":[{"hash":"` + strings.Repeat("b", 40) + `","qbittorrent":{}}],"native_error":false}`, `{"schema_version":1,"downloader_id":"owned","kind":"qbittorrent","tasks":[{"hash":"` + strings.Repeat("a", 40) + `","qbittorrent":{}}],"native_error":true}`} {
		d := Downloaders{Host: func(context.Context, string, json.RawMessage) (json.RawMessage, error) {
			return json.RawMessage(raw), nil
		}}
		if _, err := d.SourceTasks(context.Background(), DownloaderTasksRequestV1{DownloaderID: "owned", Hashes: []string{strings.Repeat("a", 40)}}); err == nil {
			t.Fatal("invalid source outcome accepted", raw)
		}
	}
	sentinel := errors.New("owned guard denial")
	d := Downloaders{Host: func(context.Context, string, json.RawMessage) (json.RawMessage, error) { return nil, sentinel }}
	if _, err := d.SourceAdd(context.Background(), DownloaderSourceAddRequestV1{DownloaderAddRequestV1: DownloaderAddRequestV1{DownloaderID: "owned"}}); !errors.Is(err, sentinel) {
		t.Fatal("opaque host failure swallowed", err)
	}
	calls := 0
	d.Host = func(context.Context, string, json.RawMessage) (json.RawMessage, error) {
		calls++
		return json.RawMessage(`null`), nil
	}
	if _, err := d.SourceTasks(context.Background(), DownloaderTasksRequestV1{DownloaderID: "owned"}); err == nil || calls != 0 {
		t.Fatal("nil exact selection dispatched")
	}
}
func TestSourceDownloaderSDKPreservesUnknownAndCausalReceipts(t *testing.T) {
	for _, sample := range []struct {
		outcome, code    string
		accepted, native bool
	}{{"rejected", "", false, true}, {"unknown", "", false, true}, {"confirmed", "CALL_CHANGED_AFTER_MUTATION", true, false}, {"unknown", "CALL_CHANGED_AFTER_MUTATION", false, false}, {"unknown", "PARTIAL_MUTATION", false, true}} {
		d := Downloaders{Host: func(_ context.Context, method string, _ json.RawMessage) (json.RawMessage, error) {
			if method != DownloaderSourceControlCapability {
				t.Fatal(method)
			}
			return json.Marshal(DownloaderSourceMutationResultV1{DownloaderMutationV1: DownloaderMutationV1{SchemaVersion: 1, DownloaderID: "owned", Outcome: sample.outcome, Accepted: sample.accepted, FailureCode: sample.code}, NativeError: sample.native})
		}}
		out, err := d.SourceControl(context.Background(), DownloaderControlRequestV1{DownloaderID: "owned", Hashes: []string{strings.Repeat("a", 40)}, Action: "start"})
		if err != nil || out.Outcome != sample.outcome || out.FailureCode != sample.code || out.Accepted != sample.accepted || out.NativeError != sample.native {
			t.Fatal("source receipt lost", out, err)
		}
	}
}
