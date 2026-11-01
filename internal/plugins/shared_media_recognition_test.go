package plugins

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// Recording HostCaller results below are wire specimens, not MoviePilot oracles
// or claims that a partial model implements the source's full MediaInfo.
func TestSharedMediaRecognitionWireAndOwnership(t *testing.T) {
	ctx := context.Background()
	var requests []json.RawMessage
	response := json.RawMessage(`{"value":null}`)
	host := func(gotCtx context.Context, capability string, raw json.RawMessage) (json.RawMessage, error) {
		if gotCtx != ctx || capability != MediaRecognizeCapability {
			t.Fatal("context/capability changed")
		}
		requests = append(requests, append(json.RawMessage(nil), raw...))
		return response, nil
	}
	sdk := SharedMediaRecognition{Host: host}
	got, err := sdk.RecognizeMedia(ctx, RecognizeMediaRequest{})
	if err != nil || got != nil || len(requests) != 1 || string(requests[0]) != `{"args":{}}` {
		t.Fatalf("omitted defaults / None: %v %v %s", got, err, requests)
	}
	null := json.RawMessage(`null`)
	allNull := RecognizeMediaRequest{null, null, null, null, null, null, null, null, null, null, null}
	if got, err = sdk.RecognizeMedia(ctx, allNull); err != nil || got != nil {
		t.Fatalf("explicit None: %v %v", got, err)
	}
	var envelope struct {
		Args map[string]json.RawMessage `json:"args"`
	}
	if json.Unmarshal(requests[1], &envelope) != nil || len(envelope.Args) != 11 {
		t.Fatal("explicit null argument presence lost")
	}
	for _, name := range []string{"meta", "mtype", "source", "mediaid", "tmdbid", "doubanid", "bangumiid", "anilistid", "episode_group", "cache", "share_meta"} {
		if string(envelope.Args[name]) != "null" {
			t.Fatalf("null lost: %s", name)
		}
	}
	input := RecognizeMediaRequest{
		Meta: null, MType: json.RawMessage(`"电影"`), Source: json.RawMessage(`" TMDB "`),
		MediaID: json.RawMessage(`"000123"`), TMDBID: json.RawMessage(`900719925474099312345`),
		DoubanID: null, BangumiID: json.RawMessage(`1e3`), AniListID: json.RawMessage(`0`),
		EpisodeGroup: json.RawMessage(`""`), Cache: json.RawMessage(`false`), ShareMeta: null,
	}
	response = json.RawMessage(`{"value":{"tmdb_id":900719925474099312345,"title":"Owned wire model","recognize_cache_hit":false,"provider":{"precise":1.0000000000000000001},"source_text":"\ud800","optional":null}}`)
	got, err = sdk.RecognizeMedia(ctx, input)
	if err != nil || got == nil || len(got.Fields) != 6 {
		t.Fatalf("raw model: %v %v", got, err)
	}
	if string(got.Fields["tmdb_id"]) != "900719925474099312345" || string(got.Fields["provider"]) != `{"precise":1.0000000000000000001}` || string(got.Fields["source_text"]) != `"\ud800"` || string(got.Fields["optional"]) != "null" {
		t.Fatal("lossy or compacted model")
	}
	if json.Unmarshal(requests[2], &envelope) != nil || string(envelope.Args["tmdbid"]) != "900719925474099312345" || string(envelope.Args["bangumiid"]) != "1e3" || string(envelope.Args["source"]) != `" TMDB "` || string(envelope.Args["mediaid"]) != `"000123"` || string(envelope.Args["cache"]) != "false" {
		t.Fatal("argument coercion or default replacement")
	}
	for i := range response {
		response[i] = ' '
	}
	input.TMDBID[0] = '1'
	if string(got.Fields["tmdb_id"]) != "900719925474099312345" || !bytes.Contains(requests[2], []byte("900719925474099312345")) {
		t.Fatal("caller/host raw byte ownership leaked")
	}
	clone := got.Clone()
	clone.Fields["tmdb_id"][0] = '1'
	if got.Fields["tmdb_id"][0] != '9' {
		t.Fatal("existing model Clone alias")
	}
	response = json.RawMessage(`{"value":{}}`)
	got, err = sdk.RecognizeMedia(ctx, RecognizeMediaRequest{Cache: json.RawMessage(`true`)})
	if err != nil || got == nil || got.Fields == nil || len(got.Fields) != 0 || len(requests) != 4 {
		t.Fatal("wire empty object incorrectly changed to None")
	}
	response = json.RawMessage(`{"value":{"标题":"owned","\ud83d\ude00":1}}`)
	got, err = sdk.RecognizeMedia(ctx, RecognizeMediaRequest{})
	if err != nil || string(got.Fields["标题"]) != `"owned"` || string(got.Fields["😀"]) != "1" || len(requests) != 5 {
		t.Fatal("lossless model field names rejected")
	}
}

func TestSharedMediaRecognitionStrictBoundaryAndNoReplay(t *testing.T) {
	ctx := context.Background()
	calls := 0
	good := HostCaller(func(context.Context, string, json.RawMessage) (json.RawMessage, error) {
		calls++
		return json.RawMessage(`{"value":null}`), nil
	})
	sdk := SharedMediaRecognition{Host: good}
	if out, err := sdk.RecognizeMedia(nil, RecognizeMediaRequest{}); err == nil || out != nil || calls != 0 {
		t.Fatal("nil context callback")
	}
	if out, err := (SharedMediaRecognition{}).RecognizeMedia(ctx, RecognizeMediaRequest{}); err == nil || out != nil {
		t.Fatal("nil host accepted")
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if out, err := sdk.RecognizeMedia(canceled, RecognizeMediaRequest{}); err != context.Canceled || out != nil || calls != 0 {
		t.Fatal("pre-cancellation changed or callback attempted")
	}
	for _, tc := range []struct {
		name  string
		input RecognizeMediaRequest
	}{
		{"non_nil_empty", RecognizeMediaRequest{TMDBID: json.RawMessage{}}},
		{"invalid", RecognizeMediaRequest{TMDBID: json.RawMessage(`01`)}},
		{"trailing", RecognizeMediaRequest{TMDBID: json.RawMessage(`1 2`)}},
		{"nonfinite", RecognizeMediaRequest{TMDBID: json.RawMessage(`NaN`)}},
		{"invalid_utf8", RecognizeMediaRequest{Source: json.RawMessage{'"', 0xff, '"'}}},
		{"duplicate", RecognizeMediaRequest{TMDBID: json.RawMessage(`{"x":1,"\u0078":2}`)}},
		{"nested_duplicate", RecognizeMediaRequest{Cache: json.RawMessage(`[{"a":1,"a":2}]`)}},
		{"meta_object", RecognizeMediaRequest{Meta: json.RawMessage(`{}`)}},
		{"meta_false", RecognizeMediaRequest{Meta: json.RawMessage(`false`)}},
		{"share_array", RecognizeMediaRequest{ShareMeta: json.RawMessage(`[]`)}},
		{"share_string", RecognizeMediaRequest{ShareMeta: json.RawMessage(`"owned"`)}},
		{"over_bound", RecognizeMediaRequest{Source: json.RawMessage(`"` + strings.Repeat("x", 128<<10) + `"`)}},
		{"envelope_over_bound", RecognizeMediaRequest{Source: json.RawMessage(`"` + strings.Repeat("x", (128<<10)-2) + `"`)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if out, err := sdk.RecognizeMedia(ctx, tc.input); err == nil || out != nil || calls != 0 {
				t.Fatalf("invalid request reached host: %v %v calls=%d", out, err, calls)
			}
		})
	}
	// The ceiling belongs to the complete ordinary request, not each argument.
	// A request exactly at that ceiling remains admitted.
	exactSize := (128 << 10) - len(`{"args":{"source":""}}`)
	if out, err := sdk.RecognizeMedia(ctx, RecognizeMediaRequest{Source: json.RawMessage(`"` + strings.Repeat("x", exactSize) + `"`)}); err != nil || out != nil || calls != 1 {
		t.Fatal("exact existing request limit rejected")
	}
	for _, raw := range []string{
		``, `null`, `[]`, `{}`, `{"value":false}`, `{"value":0}`, `{"value":"owned"}`, `{"value":[]}`,
		`{"value":null,"extra":1}`, `{"value":null,"value":{}}`, `{"value":null,"\u0076alue":{}}`,
		`{"value":{"id":1,"id":2}}`, `{"value":{"nested":[{"x":1,"x":2}]}}`,
		`{"value":{"\ud800":1}}`, `{"value":null} true`, string([]byte{'{', '"', 'v', 0xff, '"', ':', '0', '}'}),
	} {
		before := calls
		bad := SharedMediaRecognition{Host: func(context.Context, string, json.RawMessage) (json.RawMessage, error) {
			calls++
			return json.RawMessage(raw), nil
		}}
		if out, err := bad.RecognizeMedia(ctx, RecognizeMediaRequest{}); err == nil || out != nil || calls != before+1 {
			t.Fatalf("invalid reply accepted/retried: %q / %v %v", raw, out, err)
		}
	}
	oversize := SharedMediaRecognition{Host: func(context.Context, string, json.RawMessage) (json.RawMessage, error) {
		calls++
		return json.RawMessage(`{"value":{"owned":"` + strings.Repeat("x", MaxPayloadBytes) + `"}}`), nil
	}}
	before := calls
	if out, err := oversize.RecognizeMedia(ctx, RecognizeMediaRequest{}); err == nil || out != nil || calls != before+1 {
		t.Fatal("oversize response accepted/retried")
	}
	sentinel := &Error{Code: "invalid_invocation"}
	for _, hostErr := range []error{sentinel, errors.New("owned host error")} {
		before := calls
		failed := SharedMediaRecognition{Host: func(context.Context, string, json.RawMessage) (json.RawMessage, error) {
			calls++
			return json.RawMessage(`{"value":null}`), hostErr
		}}
		if out, err := failed.RecognizeMedia(ctx, RecognizeMediaRequest{}); err != hostErr || out != nil || calls != before+1 {
			t.Fatal("host error identity changed or call retried")
		}
	}
	afterCtx, afterCancel := context.WithCancel(ctx)
	after := SharedMediaRecognition{Host: func(context.Context, string, json.RawMessage) (json.RawMessage, error) {
		calls++
		afterCancel()
		return json.RawMessage(`{"value":{"id":1}}`), nil
	}}
	before = calls
	if out, err := after.RecognizeMedia(afterCtx, RecognizeMediaRequest{}); err != context.Canceled || out != nil || calls != before+1 {
		t.Fatal("post-callback cancellation ignored/retried")
	}
}
