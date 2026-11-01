package main

import (
	"context"
	"encoding/json"
	"testing"

	"light-media/internal/plugins"
)

func TestOwnedQBTagFixtureManifestAndRoute(t *testing.T) {
	manifest := fixtureManifest()
	if err := manifest.Validate(); err != nil {
		t.Fatal(err)
	}
	if len(manifest.APIs) != 1 || manifest.APIs[0].Key != "qb_tags" || manifest.APIs[0].Path != "/qb-tags" {
		t.Fatal("fixed API key/path changed")
	}
	invalid := fixtureManifest()
	invalid.APIs[0].Key = "qb-tags"
	if invalid.Validate() == nil {
		t.Fatal("original invalid fixture key unexpectedly admitted")
	}
	object := &fixtureObject{}
	request, err := json.Marshal(plugins.HTTPRequest{Key: "qb_tags", Method: "POST", Path: "/qb-tags", Body: json.RawMessage(`{"operations":[{"kind":"qbittorrent","op":"delete_tags","downloader_id":"owned-qb-tags-qb","ids":["owned-a"],"tag":"owned-label"}]}`)})
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	_, err = object.handle(manifest)(context.Background(), "http", request, func(_ context.Context, method string, _ json.RawMessage) (json.RawMessage, error) {
		calls++
		if method != plugins.QBittorrentDeleteTagsCapability {
			t.Fatal("unexpected fixed SDK capability", method)
		}
		return json.RawMessage(`{"value":true}`), nil
	})
	if err != nil || calls != 1 {
		t.Fatal("corrected key did not reach exactly one fixed SDK call", err, calls)
	}
}
