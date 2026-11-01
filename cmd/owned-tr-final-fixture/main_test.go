package main

import (
	"context"
	"encoding/json"
	"testing"

	"light-media/internal/plugins"
)

// Unit route/manifest test only; actual installed worker/host/daemon-shaped
// owned HTTP evidence belongs to the separate normal CLI gate.
func TestOwnedTRFinalFixtureManifestAndRoute(t *testing.T) {
	manifest := fixtureManifest()
	if err := manifest.Validate(); err != nil {
		t.Fatal(err)
	}
	if manifest.PluginID != "OwnedTRFinal" || manifest.Entry != "owned-tr-final-fixture" || len(manifest.Capabilities) != 2 || len(manifest.APIs) != 1 || manifest.APIs[0].Key != "tr_final" || manifest.APIs[0].Path != "/tr-final" {
		t.Fatal("fixed manifest contract changed")
	}
	invalid := fixtureManifest()
	invalid.APIs[0].Key = "tr-final"
	if invalid.Validate() == nil {
		t.Fatal("invalid method key admitted")
	}
	object := &fixtureObject{}
	handler := object.handle(manifest)
	if _, err := handler(context.Background(), "init", json.RawMessage(`{}`), nil); err != nil {
		t.Fatal(err)
	}
	body := json.RawMessage(`{"operations":[{"kind":"transmission","op":"set_tags","downloader_id":"owned-tr-final-tr","ids":7,"tags":["owned-new","owned-old","owned-new"],"org_tags":["owned-old","owned-retained"]},{"kind":"transmission","op":"change","downloader_id":"owned-tr-final-tr","hash_string":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","upload_limit":null,"download_limit":null,"ratio_limit":null,"seeding_time_limit":null},{"kind":"transmission","op":"change","downloader_id":"owned-tr-final-tr","hash_string":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","upload_limit":"owned-invalid","download_limit":null,"ratio_limit":null,"seeding_time_limit":null}],"continue_on_error":true}`)
	request, err := json.Marshal(plugins.HTTPRequest{Key: "tr_final", Method: "POST", Path: "/tr-final", Body: body})
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	raw, err := handler(context.Background(), "http", request, func(_ context.Context, method string, frame json.RawMessage) (json.RawMessage, error) {
		calls++
		var fields map[string]json.RawMessage
		if json.Unmarshal(frame, &fields) != nil || string(fields["op"]) != `"invoke"` || string(fields["downloader_id"]) != `"owned-tr-final-tr"` {
			t.Fatal("unexpected exact SDK frame", string(frame))
		}
		if calls == 1 {
			if method != plugins.TransmissionSetTagsCapability || len(fields) != 5 || string(fields["ids"]) != `7` || string(fields["org_tags"]) != `["owned-old","owned-retained"]` {
				t.Fatal("tags SDK call changed", method, string(frame))
			}
			return json.RawMessage(`{"value":true}`), nil
		}
		if method != plugins.TransmissionChangeCapability || len(fields) != 7 {
			t.Fatal("change SDK call changed", method, string(frame))
		}
		if calls == 2 {
			if string(fields["upload_limit"]) != `null` {
				t.Fatal("explicit None changed")
			}
			return json.RawMessage(`{"value":false}`), nil
		}
		if calls != 3 || string(fields["upload_limit"]) != `"owned-invalid"` {
			t.Fatal("unexpected retry or extra callback", calls)
		}
		return json.RawMessage(`{"kind":"source_error","source_error":"ValueError"}`), nil
	})
	var response plugins.HTTPResponse
	var snapshot fixtureSnapshot
	if err != nil || calls != 3 || json.Unmarshal(raw, &response) != nil || response.Status != 200 || json.Unmarshal(response.Body, &snapshot) != nil {
		t.Fatal("fixed route/response failed", err, calls, string(raw))
	}
	if snapshot.OK || snapshot.PID <= 0 || snapshot.InitCount != 1 || snapshot.Invocation != 1 || snapshot.Requested != 3 || snapshot.Completed != 3 || snapshot.Succeeded != 2 || snapshot.Failed != 1 || len(snapshot.Results) != 3 {
		t.Fatal("typed completion/source error accounting changed", snapshot)
	}
	for i, attempt := range snapshot.Results {
		if attempt.HostCalls.Total != 1 || attempt.HostCalls.Succeeded != 1 || attempt.HostCalls.Failed != 0 || attempt.HostCalls.InputJSONBytes <= 0 || attempt.HostCalls.OutputJSONBytes <= 0 {
			t.Fatal("actual SDK callback accounting differs", i, attempt)
		}
	}
	if !snapshot.Results[0].OK || string(snapshot.Results[0].Result) != `true` || !snapshot.Results[1].OK || string(snapshot.Results[1].Result) != `false` || snapshot.Results[1].SourceTruthy == nil || *snapshot.Results[1].SourceTruthy {
		t.Fatal("source False incorrectly means failed callback or mutation success", snapshot.Results)
	}
	last := snapshot.Results[2]
	if last.OK || last.ErrorCode != "source_exception" || last.SourceError != "ValueError" || last.Result != nil || last.SourceTruthy != nil || last.ResultKind != "" {
		t.Fatal("source exception classification leaked/coerced", last)
	}
	bad, err := json.Marshal(plugins.HTTPRequest{Key: "tr-final", Method: "POST", Path: "/tr-final", Body: body})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := handler(context.Background(), "http", bad, nil); err == nil {
		t.Fatal("wrong registered route key admitted")
	}
}
