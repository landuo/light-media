package plugins

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func invokeFourSDK(ctx context.Context, host HostCaller, id, op string, a, b any) (any, error) {
	q := SharedQBittorrentCommands{Host: host, DownloaderID: id}
	t := SharedTransmissionCommands{Host: host, DownloaderID: id}
	switch op {
	case "tags":
		return nil, q.SetTorrentsTag(ctx, a, b)
	case "category":
		return q.SetTorrentCategory(ctx, a, b)
	case "qb-stop":
		return q.StopTorrents(ctx, a)
	case "tr-stop":
		return t.StopTorrents(ctx, a)
	default:
		panic("unplanned owned SDK operation")
	}
}

// These are recording HostCaller wire expectations, not source execution or
// native downloader acceptance. Real source 17-case replay belongs to leaf.
func TestSharedDownloaderFourCommandsWire(t *testing.T) {
	ctx := context.WithValue(context.Background(), struct{}{}, "owned-context")
	for _, c := range []struct {
		name, op, cap, request, reply string
		a, b, result                  any
	}{
		{"tag-null", "tags", QBittorrentSetTagsCapability, `{"op":"invoke","downloader_id":" Owned-ID ","ids":null,"tags":null}`, `{"value":null}`, nil, nil, nil},
		{"tag-duplicates", "tags", QBittorrentSetTagsCapability, `{"op":"invoke","downloader_id":" Owned-ID ","ids":["owned-a","owned-a"],"tags":[]}`, `{"value": null }`, []string{"owned-a", "owned-a"}, []string{}, nil},
		{"category-empty-hash", "category", QBittorrentSetCategoryCapability, `{"op":"invoke","downloader_id":" Owned-ID ","hash_string":"","category":null}`, `{"value":false}`, "", nil, false},
		{"category-raw", "category", QBittorrentSetCategoryCapability, `{"op":"invoke","downloader_id":" Owned-ID ","hash_string":" Not 40hex ","category":false}`, `{"value":true}`, " Not 40hex ", false, true},
		{"qb-stop-null", "qb-stop", QBittorrentStopCapability, `{"op":"invoke","downloader_id":" Owned-ID ","ids":null}`, `{"value":true}`, nil, nil, true},
		{"qb-stop-empty", "qb-stop", QBittorrentStopCapability, `{"op":"invoke","downloader_id":" Owned-ID ","ids":""}`, `{"value":false}`, "", nil, false},
		{"tr-stop-mixed", "tr-stop", TransmissionStopCapability, `{"op":"invoke","downloader_id":" Owned-ID ","ids":[0,"Owned-TR"]}`, `{"value":true}`, []any{json.Number("0"), "Owned-TR"}, nil, true},
		{"tr-stop-empty", "tr-stop", TransmissionStopCapability, `{"op":"invoke","downloader_id":" Owned-ID ","ids":[]}`, `{"value":false}`, []any{}, nil, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			calls := 0
			host := func(actual context.Context, cap string, raw json.RawMessage) (json.RawMessage, error) {
				calls++
				if actual != ctx || cap != c.cap || string(raw) != c.request {
					t.Fatalf("actual frame/context changed: %s %s", cap, raw)
				}
				return json.RawMessage(c.reply), nil
			}
			result, err := invokeFourSDK(ctx, host, " Owned-ID ", c.op, c.a, c.b)
			if err != nil || calls != 1 || !reflect.DeepEqual(result, c.result) {
				t.Fatal(result, err, calls)
			}
		})
	}
}

type fourSDKMarshaler string

func (fourSDKMarshaler) MarshalJSON() ([]byte, error) { panic("custom marshaler must not execute") }

func TestSharedDownloaderFourCommandsBoundary(t *testing.T) {
	for _, op := range []string{"tags", "category", "qb-stop", "tr-stop"} {
		t.Run(op, func(t *testing.T) {
			for _, bad := range []string{`{}`, `null`, `{"value":true,"value":false}`, `{"Value":null}`, `{"value":null,"extra":0}`, `{"value":"false"}`, `{"value":0}`, `{"value":true} {}`} {
				calls := 0
				host := func(context.Context, string, json.RawMessage) (json.RawMessage, error) {
					calls++
					return json.RawMessage(bad), nil
				}
				if _, err := invokeFourSDK(context.Background(), host, "Owned", op, "owned-a", nil); err == nil || calls != 1 {
					t.Fatalf("accepted ambiguous result/retried %s: %v %d", bad, err, calls)
				}
			}
			wrongType := `{"value":null}`
			if op == "tags" {
				wrongType = `{"value":false}`
			}
			calls := 0
			host := func(context.Context, string, json.RawMessage) (json.RawMessage, error) {
				calls++
				return json.RawMessage(wrongType), nil
			}
			if _, err := invokeFourSDK(context.Background(), host, "Owned", op, nil, nil); err == nil || calls != 1 {
				t.Fatal("None/bool result conflated", err, calls)
			}
			marker := errors.New("owned unknown effect")
			calls = 0
			host = func(context.Context, string, json.RawMessage) (json.RawMessage, error) { calls++; return nil, marker }
			if _, err := invokeFourSDK(context.Background(), host, "Owned", op, nil, nil); !errors.Is(err, marker) || calls != 1 {
				t.Fatal("host error swallowed/retried", err, calls)
			}
			calls = 0
			canceled, cancel := context.WithCancel(context.Background())
			cancel()
			for _, admission := range []struct {
				ctx context.Context
				id  string
				a   any
			}{
				{nil, "Owned", nil}, {canceled, "Owned", nil}, {context.Background(), "", nil},
				{context.Background(), "Owned", []byte("not-material")},
				{context.Background(), "Owned", fourSDKMarshaler("not ordinary JSON")},
				{context.Background(), "Owned", json.RawMessage(`{"a":1,"a":2}`)},
				{context.Background(), "Owned", strings.Repeat("a", 128<<10)},
			} {
				if _, err := invokeFourSDK(admission.ctx, host, admission.id, op, admission.a, nil); err == nil {
					t.Fatal("bad admission accepted")
				}
			}
			if calls != 0 {
				t.Fatal("invalid admission called host", calls)
			}
			if _, err := invokeFourSDK(context.Background(), nil, "Owned", op, nil, nil); err == nil {
				t.Fatal("nil host accepted")
			}
			// A caller canceling during dispatch cannot receive a source value;
			// it is still exactly one attempt and never a rollback/retry.
			ctx, stop := context.WithCancel(context.Background())
			calls = 0
			host = func(context.Context, string, json.RawMessage) (json.RawMessage, error) {
				calls++
				stop()
				return json.RawMessage(`{"value":null}`), nil
			}
			if _, err := invokeFourSDK(ctx, host, "Owned", op, nil, nil); !errors.Is(err, context.Canceled) || calls != 1 {
				t.Fatal("cancellation lost", err, calls)
			}
		})
	}
}
