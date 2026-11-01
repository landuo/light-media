package plugins

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func invokeTRFinal(ctx context.Context, host HostCaller, id string, change bool, args []any) (bool, error) {
	tr := SharedTransmissionCommands{Host: host, DownloaderID: id}
	if change {
		return tr.ChangeTorrent(ctx, args[0], args[1], args[2], args[3], args[4])
	}
	return tr.SetTorrentTag(ctx, args[0], args[1], args[2])
}

// Wire recording only; original23 values/ordered side effects belong to leaf.
func TestSharedTRFinalWireAndSourceErrors(t *testing.T) {
	ctx := context.WithValue(context.Background(), struct{}{}, "owned")
	for _, c := range []struct {
		name                string
		change              bool
		args                []any
		cap, request, reply string
		want                bool
	}{
		{"tags-null", false, []any{nil, nil, nil}, TransmissionSetTagsCapability, `{"op":"invoke","downloader_id":" Owned-ID ","ids":null,"tags":null,"org_tags":null}`, `{"value":false}`, false},
		{"tags-supplied", false, []any{[]string{"owned-a"}, []string{"new", "new"}, []string{"old"}}, TransmissionSetTagsCapability, `{"op":"invoke","downloader_id":" Owned-ID ","ids":["owned-a"],"tags":["new","new"],"org_tags":["old"]}`, `{"value":true}`, true},
		{"change-none", true, []any{"not-40hex", nil, nil, nil, nil}, TransmissionChangeCapability, `{"op":"invoke","downloader_id":" Owned-ID ","hash_string":"not-40hex","upload_limit":null,"download_limit":null,"ratio_limit":null,"seeding_time_limit":null}`, `{"value":false}`, false},
		{"change-values", true, []any{7, "0", -4, json.Number("2.675"), false}, TransmissionChangeCapability, `{"op":"invoke","downloader_id":" Owned-ID ","hash_string":7,"upload_limit":"0","download_limit":-4,"ratio_limit":2.675,"seeding_time_limit":false}`, `{"value":true}`, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			calls := 0
			host := func(actual context.Context, cap string, raw json.RawMessage) (json.RawMessage, error) {
				calls++
				if actual != ctx || cap != c.cap || string(raw) != c.request {
					t.Fatal("wire/context", cap, string(raw))
				}
				return json.RawMessage(c.reply), nil
			}
			got, err := invokeTRFinal(ctx, host, " Owned-ID ", c.change, c.args)
			if err != nil || got != c.want || calls != 1 {
				t.Fatal(got, err, calls)
			}
		})
	}
	for _, change := range []bool{false, true} {
		calls := 0
		host := func(context.Context, string, json.RawMessage) (json.RawMessage, error) {
			calls++
			return json.RawMessage(`{"kind":"source_error","source_error":"ValueError"}`), nil
		}
		_, err := invokeTRFinal(ctx, host, "Owned", change, []any{"owned", nil, nil, nil, nil})
		if calls != 1 || err == nil {
			t.Fatal("missing envelope error", err, calls)
		}
		source, ok := err.(*TRChangeSourceError)
		if change {
			if !ok || source.Class != "ValueError" {
				t.Fatal("lost source class", err)
			}
		} else if ok {
			t.Fatal("tags may not export outside-catch error")
		}
	}
}

func TestSharedTRFinalStrictBoundaryAndNoReplay(t *testing.T) {
	for _, change := range []bool{false, true} {
		for _, bad := range []string{`{}`, `null`, `{"value":null}`, `{"value":true,"value":false}`, `{"Value":true}`, `{"value":1}`, `{"value":"true"}`, `{"value":true,"extra":0}`, `{"value":true} {}`, `{"kind":"source_error","source_error":"TypeError"}`, `{"kind":"source_error","source_error":"ValueError","value":false}`, `{"kind":"source_error","source_error":"ValueError","text":"sensitive"}`, `{"kind":"source_error","source_error":"ValueError","source_error":"ValueError"}`} {
			calls := 0
			host := func(context.Context, string, json.RawMessage) (json.RawMessage, error) {
				calls++
				return json.RawMessage(bad), nil
			}
			if _, err := invokeTRFinal(context.Background(), host, "Owned", change, []any{nil, nil, nil, nil, nil}); err == nil || calls != 1 {
				t.Fatal("ambiguous result/replay", bad, err, calls)
			}
		}
		marker := errors.New("owned selected unknown")
		calls := 0
		host := func(context.Context, string, json.RawMessage) (json.RawMessage, error) { calls++; return nil, marker }
		if _, err := invokeTRFinal(context.Background(), host, "Owned", change, []any{nil, nil, nil, nil, nil}); err != marker || calls != 1 {
			t.Fatal("error identity/retry", err, calls)
		}
		calls = 0
		canceled, cancel := context.WithCancel(context.Background())
		cancel()
		for _, bad := range []struct {
			ctx context.Context
			id  string
			arg any
		}{
			{nil, "Owned", nil}, {canceled, "Owned", nil}, {context.Background(), "", nil},
			{context.Background(), "Owned", []byte("not material")}, {context.Background(), "Owned", fourSDKMarshaler("must not execute")},
			{context.Background(), "Owned", json.RawMessage(`{"a":1,"a":2}`)}, {context.Background(), "Owned", strings.Repeat("x", 128<<10)},
		} {
			if _, err := invokeTRFinal(bad.ctx, host, bad.id, change, []any{bad.arg, nil, nil, nil, nil}); err == nil {
				t.Fatal("invalid input admitted")
			}
		}
		if calls != 0 {
			t.Fatal("invalid admission invoked host", calls)
		}
		if _, err := invokeTRFinal(context.Background(), nil, "Owned", change, []any{nil, nil, nil, nil, nil}); err == nil {
			t.Fatal("nil host")
		}
		ctx, stop := context.WithCancel(context.Background())
		host = func(context.Context, string, json.RawMessage) (json.RawMessage, error) {
			calls++
			stop()
			return json.RawMessage(`{"value":true}`), nil
		}
		if _, err := invokeTRFinal(ctx, host, "Owned", change, []any{nil, nil, nil, nil, nil}); !errors.Is(err, context.Canceled) || calls != 1 {
			t.Fatal("post-dispatch cancellation/replay", err, calls)
		}
	}
}
