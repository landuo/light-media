package plugins

import (
	"context"
	"encoding/json"
	"testing"
)

func TestDownloaderConfigurationsRetainOrderAndDuplicateNames(t *testing.T) {
	refs := []BrushDownloaderReferenceV1{{ID: "z", Name: "B", Kind: "transmission", Enabled: true}, {ID: "a", Name: "A", Kind: "qbittorrent", Enabled: true}, {ID: "b", Name: "B", Kind: "qbittorrent", Enabled: true}, {ID: "c", Name: "off", Kind: "unknown", Enabled: false}}
	host := func(_ context.Context, method string, raw json.RawMessage) (json.RawMessage, error) {
		if method != DownloaderConfigurationsReadCapability || string(raw) != "{}" {
			t.Fatalf("unexpected configurations request: %s %s", method, raw)
		}
		return json.Marshal(DownloaderConfigurationsV1{SchemaVersion: 1, Downloaders: refs})
	}
	got, err := (Downloaders{Host: host}).Configurations(context.Background())
	if err != nil || len(got.Downloaders) != 4 {
		t.Fatal(got, err)
	}
	for i, ref := range refs {
		if got.Downloaders[i] != ref {
			t.Fatal("configuration order/value changed", got.Downloaders)
		}
	}
}

func TestDownloaderConfigurationsRejectInvalidResponseIdentity(t *testing.T) {
	for _, raw := range []string{`{"schema_version":2,"downloaders":[]}`, `{"schema_version":1,"downloaders":null}`, `{"schema_version":1,"downloaders":[{"id":""}]}`, `{"schema_version":1,"downloaders":[{"id":"a"},{"id":"a"}]}`} {
		t.Run(raw, func(t *testing.T) {
			host := func(context.Context, string, json.RawMessage) (json.RawMessage, error) {
				return json.RawMessage(raw), nil
			}
			if _, err := (Downloaders{Host: host}).Configurations(context.Background()); err == nil {
				t.Fatal("invalid configuration response accepted", raw)
			}
		})
	}
}
