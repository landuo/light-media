package plugins

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

func TestSharedQBTagsWireAndSourceValues(t *testing.T) {
	ctx := context.Background()
	for _, c := range []struct{ name, reply, want string }{
		{"none", `{"value":null}`, `null`}, {"false", `{"value":false}`, `false`}, {"empty", `{"value":""}`, `""`}, {"number", `{"value":9007199254740993}`, `9007199254740993`}, {"string", `{"value":"owned-found"}`, `"owned-found"`},
	} {
		t.Run(c.name, func(t *testing.T) {
			calls := 0
			q := SharedQBittorrentCommands{DownloaderID: " Owned ", Host: func(actual context.Context, cap string, raw json.RawMessage) (json.RawMessage, error) {
				calls++
				if actual != ctx || cap != QBittorrentGetIDByTagCapability || string(raw) != `{"op":"invoke","downloader_id":" Owned ","tags":["owned","owned"],"status":null}` {
					t.Fatal("lookup wire changed", cap, string(raw))
				}
				return json.RawMessage(c.reply), nil
			}}
			value, err := q.GetTorrentIDByTag(ctx, []string{"owned", "owned"}, nil)
			if err != nil || string(value) != c.want || calls != 1 {
				t.Fatal("source value changed", string(value), err, calls)
			}
		})
	}
	for _, reply := range []string{`{"value":false}`, `{"value":true}`} {
		calls := 0
		q := SharedQBittorrentCommands{DownloaderID: "Owned", Host: func(_ context.Context, cap string, raw json.RawMessage) (json.RawMessage, error) {
			calls++
			if cap != QBittorrentDeleteTagsCapability || string(raw) != `{"op":"invoke","downloader_id":"Owned","ids":null,"tag":[]}` {
				t.Fatal("delete wire changed", cap, string(raw))
			}
			return json.RawMessage(reply), nil
		}}
		value, err := q.DeleteTorrentsTag(ctx, nil, []any{})
		if err != nil || value != (reply == `{"value":true}`) || calls != 1 {
			t.Fatal(value, err, calls)
		}
	}
}

func TestSharedQBTagsStrictErrorsAndNoReplay(t *testing.T) {
	calls := 0
	q := SharedQBittorrentCommands{DownloaderID: "Owned", Host: func(context.Context, string, json.RawMessage) (json.RawMessage, error) {
		calls++
		return json.RawMessage(`{"kind":"source_error","source_error":"AttributeError"}`), nil
	}}
	value, err := q.GetTorrentIDByTag(context.Background(), nil, nil)
	source, ok := err.(*QBTagSourceError)
	if !ok || source.Class != "AttributeError" || value != nil || calls != 1 {
		t.Fatal("source escaping exception lost", value, err, calls)
	}
	if _, err = q.DeleteTorrentsTag(context.Background(), nil, nil); err == nil {
		t.Fatal("delete admitted lookup source envelope")
	}
	for _, raw := range []string{`{}`, `{"Value":null}`, `{"value":null,"value":false}`, `{"value":null,"source_error":"AttributeError"}`, `{"kind":"source_error","source_error":null}`, `{"kind":"source_error","source_error":"RuntimeError"}`, `{"kind":"source_error","source_error":"private credentials"}`, `{"kind":"source_error","source_error":"AttributeError","message":"private"}`, `{"kind":"source_error","source_error":"AttributeError","source_error":"AttributeError"}`, `{"value":null} null`} {
		calls = 0
		q.Host = func(context.Context, string, json.RawMessage) (json.RawMessage, error) {
			calls++
			return json.RawMessage(raw), nil
		}
		if _, err = q.GetTorrentIDByTag(context.Background(), nil, nil); err == nil || calls != 1 {
			t.Fatal("ambiguous envelope accepted/retried", raw, err, calls)
		}
	}
	marker := errors.New("owned unknown effect")
	for _, lookup := range []bool{false, true} {
		calls = 0
		q.Host = func(context.Context, string, json.RawMessage) (json.RawMessage, error) { calls++; return nil, marker }
		if lookup {
			_, err = q.GetTorrentIDByTag(context.Background(), nil, nil)
		} else {
			_, err = q.DeleteTorrentsTag(context.Background(), nil, nil)
		}
		if !errors.Is(err, marker) || calls != 1 {
			t.Fatal("host error converted/replayed", err, calls)
		}
		calls = 0
		if lookup {
			_, err = q.GetTorrentIDByTag(nil, nil, nil)
		} else {
			_, err = q.DeleteTorrentsTag(nil, nil, nil)
		}
		if err == nil || calls != 0 {
			t.Fatal("nil context dispatched")
		}
		if lookup {
			_, err = q.GetTorrentIDByTag(context.Background(), []byte("not material"), nil)
		} else {
			_, err = q.DeleteTorrentsTag(context.Background(), nil, []byte("not material"))
		}
		if err == nil || calls != 0 {
			t.Fatal("bytes escaped ordinary JSON domain")
		}
	}
}
