package main

import (
	"context"
	"encoding/json"
	"testing"

	"light-media/internal/plugins"
)

// Only fixture routing, fixed declarations and lock-safe synchronous reentry
// are exercised here. This recording HostCaller is not an App/native oracle,
// concurrent protocol negotiation, installed worker or HTTP/cache proof.
func TestOwnedMediaRecognitionFixtureManifestAndReentry(t *testing.T) {
	manifest := fixtureManifest()
	if err := manifest.Validate(); err != nil {
		t.Fatal(err)
	}
	if manifest.PluginID != "OwnedMediaRecognition" || manifest.Entry != "owned-media-recognition-fixture" || len(manifest.Capabilities) != 1 || manifest.Capabilities[0] != plugins.MediaRecognizeCapability || len(manifest.APIs) != 1 || manifest.APIs[0].Key != "media_recognize" || manifest.APIs[0].Path != "/media-recognize" {
		t.Fatal("fixed manifest drift")
	}
	invalid := fixtureManifest()
	invalid.APIs[0].Key = "media-recognize"
	if invalid.Validate() == nil {
		t.Fatal("invalid API key accepted")
	}
	ctx := context.Background()
	object := &fixtureObject{}
	handler := object.handle(manifest)
	if _, err := handler(ctx, "init", json.RawMessage(`{}`), nil); err != nil {
		t.Fatal(err)
	}
	moduleFrame := func(id string) json.RawMessage {
		return json.RawMessage(`{"args":[],"kwargs":{"meta":null,"mtype":"电影","source":"themoviedb","mediaid":null,"tmdbid":` + id + `,"doubanid":null,"bangumiid":null,"anilistid":null,"episode_group":null,"cache":false}}`)
	}
	calls := 0
	host := func(ctx context.Context, capability string, raw json.RawMessage) (json.RawMessage, error) {
		calls++
		if capability != plugins.MediaRecognizeCapability {
			t.Fatal("unexpected capability")
		}
		var request struct {
			Args map[string]json.RawMessage `json:"args"`
		}
		if json.Unmarshal(raw, &request) != nil || string(request.Args["tmdbid"]) != "900043" || string(request.Args["meta"]) != "null" || string(request.Args["share_meta"]) != "null" {
			t.Fatal("invalid fixed SDK request", string(raw))
		}
		if string(request.Args["mtype"]) == `"未知"` {
			return nil, &plugins.Error{Code: "SOURCE_RECOGNITION_ID_UNSUPPORTED"}
		}
		if string(request.Args["cache"]) != "false" {
			t.Fatal("cache false lost")
		}
		// Calling these handlers synchronously while snapshot is active proves
		// they do not take its businessMu. Actual ServeConcurrent is separate.
		state, err := handler(ctx, "get_state", nil, nil)
		if err != nil || string(state) != "true" {
			t.Fatal("reentrant state", err)
		}
		declaration, err := handler(ctx, "get_module", nil, nil)
		if err != nil || string(declaration) != `{"recognize_media":"recognize_owned"}` {
			t.Fatal("reentrant declaration", err)
		}
		model, err := handler(ctx, "recognize_owned", moduleFrame("900043"), nil)
		if err != nil {
			return nil, err
		}
		return json.Marshal(struct {
			Value json.RawMessage `json:"value"`
		}{model})
	}
	body := json.RawMessage(`{"operations":[{"op":"unsupported_type"},{"op":"pre_canceled"},{"op":"module_hit","cache":false}],"continue_on_error":true}`)
	request, err := json.Marshal(plugins.HTTPRequest{Key: "media_recognize", Method: "POST", Path: "/media-recognize", Body: body})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := handler(ctx, "http", request, host)
	var response plugins.HTTPResponse
	var snapshot fixtureSnapshot
	if err != nil || json.Unmarshal(raw, &response) != nil || response.Status != 200 || json.Unmarshal(response.Body, &snapshot) != nil {
		t.Fatal("owned route", err, string(raw))
	}
	if calls != 2 || snapshot.OK || snapshot.PID <= 0 || snapshot.InitCount != 1 || snapshot.Invocation != 1 || snapshot.Requested != 3 || snapshot.Completed != 3 || snapshot.Succeeded != 1 || snapshot.Failed != 2 || len(snapshot.Results) != 3 {
		t.Fatal("fixed operation accounting", calls, snapshot)
	}
	if snapshot.Results[0].ErrorCode != "SOURCE_RECOGNITION_ID_UNSUPPORTED" || snapshot.Results[0].HostCalls.Failed != 1 || snapshot.Results[0].Result != nil {
		t.Fatal("host boundary swallowed")
	}
	if snapshot.Results[1].ErrorCode != "context_canceled" || snapshot.Results[1].HostCalls.Total != 0 || snapshot.Results[1].Result != nil {
		t.Fatal("canceled operation reached host")
	}
	hit := snapshot.Results[2]
	if !hit.OK || hit.ResultKind != "full_raw_media_info" || hit.HostCalls.Total != 1 || hit.HostCalls.Succeeded != 1 || hit.HostCalls.Failed != 0 || hit.After.State-hit.Before.State != 1 || hit.After.Declaration-hit.Before.Declaration != 1 || hit.After.Recognize-hit.Before.Recognize != 1 {
		t.Fatal("actual recording/reentry accounting", hit)
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(hit.Result, &fields) != nil || len(fields) != 66 || string(fields["tmdb_id"]) != "900043" {
		t.Fatal("complete owned field set lost")
	}
	var provider map[string]json.RawMessage
	if json.Unmarshal(fields["tmdb_info"], &provider) != nil || string(provider["owned_precise_integer"]) != "900719925474099312345" {
		t.Fatal("owned raw numeric precision lost")
	}
	if len(snapshot.ModuleTrace) != 1 || snapshot.ModuleTrace[0].Returned != "owned_complete_model" {
		t.Fatal("owned trace missing")
	}
	// Only module returns are checked here. None is not asserted as an outer
	// successful recognition: real native fallthrough remains a separate gate.
	if got, err := handler(ctx, "recognize_owned", moduleFrame("900044"), nil); err != nil || string(got) != "false" {
		t.Fatal("fixed false terminal", err, string(got))
	}
	if got, err := handler(ctx, "recognize_owned", moduleFrame("900045"), nil); err != nil || string(got) != "null" {
		t.Fatal("fixed native fallthrough", err, string(got))
	}
	for _, bad := range []string{`{"operations":[{"op":"unknown"}]}`, `{"operations":[{"op":"module_hit","op":"native"}]}`, `{"operations":[{"op":"module_hit","endpoint":"https://not-allowed.invalid"}]}`} {
		if _, err := handler(ctx, "snapshot", json.RawMessage(bad), host); err == nil {
			t.Fatal("unknown fixture input accepted")
		}
	}
	if calls != 2 {
		t.Fatal("negative fixture input invoked host")
	}
	wrong, _ := json.Marshal(plugins.HTTPRequest{Key: "media-recognize", Method: "POST", Path: "/media-recognize", Body: body})
	if _, err := handler(ctx, "http", wrong, host); err == nil {
		t.Fatal("wrong route admitted")
	}
	cold := &fixtureObject{}
	if _, err := cold.handle(manifest)(ctx, "init", json.RawMessage(`{}`), nil); err != nil || cold.initCount != 1 || cold.invocation != 0 || cold.readCounters() != (moduleCounters{}) {
		t.Fatal("owned cold object not reset")
	}
}

// This extends only the fixed worker routing contract. The recording callback
// is deliberately not a native provider result or an unknown-type oracle.
func TestOwnedMediaRecognitionFixtureNativeTypeRoutes(t *testing.T) {
	for _, tc := range []struct{ op, mtype string }{{"native", `"电影"`}, {"native_tv", `"电视剧"`}, {"native_unknown", "null"}} {
		t.Run(tc.op, func(t *testing.T) {
			object := &fixtureObject{}
			calls := 0
			host := func(ctx context.Context, capability string, raw json.RawMessage) (json.RawMessage, error) {
				calls++
				var request struct {
					Args map[string]json.RawMessage `json:"args"`
				}
				if capability != plugins.MediaRecognizeCapability || json.Unmarshal(raw, &request) != nil || string(request.Args["tmdbid"]) != "900045" || string(request.Args["mtype"]) != tc.mtype || string(request.Args["cache"]) != "false" {
					t.Fatal("fixed native type SDK input differs")
				}
				frame := json.RawMessage(`{"args":[],"kwargs":{"meta":null,"mtype":` + tc.mtype + `,"source":"themoviedb","mediaid":null,"tmdbid":900045,"doubanid":null,"bangumiid":null,"anilistid":null,"episode_group":null,"cache":false}}`)
				value, err := object.recognize(frame)
				if err != nil || string(value) != "null" {
					t.Fatal("fixed native module must decline", err)
				}
				return json.RawMessage(`{"value":null}`), nil
			}
			snapshot, err := object.snapshot(context.Background(), json.RawMessage(`{"operations":[{"op":"`+tc.op+`","cache":false}]}`), host)
			if err != nil || calls != 1 || !snapshot.OK || len(snapshot.Results) != 1 || snapshot.Results[0].HostCalls.Total != 1 || snapshot.Results[0].ResultKind != "source_none" {
				t.Fatal("fixed type routing", err, snapshot)
			}
		})
	}
	// Existing non-native identities remain movie-only; no arbitrary type/ID
	// escape hatch is introduced by the two additional fixed operation names.
	object := &fixtureObject{}
	for _, mtype := range []string{`"电视剧"`, "null"} {
		raw := json.RawMessage(`{"args":[],"kwargs":{"meta":null,"mtype":` + mtype + `,"source":"themoviedb","mediaid":null,"tmdbid":900043,"doubanid":null,"bangumiid":null,"anilistid":null,"episode_group":null,"cache":false}}`)
		if _, err := object.recognize(raw); err == nil {
			t.Fatal("native type leaked to module-hit identity")
		}
	}
}
