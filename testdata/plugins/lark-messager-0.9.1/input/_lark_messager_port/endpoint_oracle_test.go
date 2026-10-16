package larkmessager

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestOriginalSourceEndpointsAndPrivateKV(t *testing.T) {
	f, e := os.Open("testdata/source-oracle.jsonl.gz")
	if e != nil {
		t.Fatal(e)
	}
	defer f.Close()
	z, _ := gzip.NewReader(f)
	defer z.Close()
	d := json.NewDecoder(z)
	count := 0
	for {
		var c oracleCase
		if e = d.Decode(&c); e == io.EOF {
			break
		} else if e != nil {
			t.Fatal(e)
		}
		if c.Kind != "endpoint_calls" {
			continue
		}
		count++
		input := asInput[Object](t, c.Input)
		t.Run(text(input["method"])+"/"+str(count), func(t *testing.T) {
			fault := asInput[struct {
				Fault int `json:"fault"`
			}](t, c.Input).Fault
			host := &algorithmHost{fault: fault, calls: []any{}}
			kv := newTestKV()
			kv.data = map[string]Object{"last_test_result": {"ok": true, "msg": "old", "time": "old", "displayed": false, "unknown": json.Number("9007199254740993")}, "last_chats": {"ok": true, "msg": "old", "time": "old", "displayed": false, "chats": []any{}}}
			p := New(PluginHost{API: host, KV: kv, Now: func() time.Time { return time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC) }})
			_, e := p.Init(obj(input["config"]))
			if e != nil {
				t.Fatal(e)
			}
			var result Object
			switch input["method"] {
			case "_test_endpoint":
				result, e = p.Test(context.Background())
			case "_status_endpoint":
				result, e = p.Status(context.Background())
			case "_fetch_chats_endpoint":
				result, e = p.FetchChats(context.Background())
			default:
				t.Fatal(input)
			}
			if e != nil {
				t.Fatal(e)
			}
			want := asInput[Object](t, c.Output)
			if c.Error != "" {
				t.Fatal("unexpected original endpoint exception", c.Error)
			}
			scrub := func(v Object) {
				msg := text(v["msg"])
				if strings.HasPrefix(msg, "发送失败：") {
					v["msg"] = "发送失败：Lark API request failed"
				}
				if strings.HasPrefix(msg, "请求失败：") {
					v["msg"] = "请求失败：Lark transport failed"
				}
			}
			scrub(obj(want["result"]))
			for _, v := range obj(want["kv"]) {
				scrub(obj(v))
			}
			equalJSON(t, uuidJSON(want["result"]), result)
			equalJSON(t, uuidJSON(want["kv"]), kv.data)
			wantCalls, gotCalls := canonicalSourceCalls(arr(want["calls"])), canonicalSourceCalls(host.calls)
			if !reflect.DeepEqual(wantCalls, gotCalls) {
				t.Fatalf("source endpoint call prefix differs\nwant %s\ngot %s", uuidJSON(wantCalls), uuidJSON(gotCalls))
			}
		})
	}
	if count != 48 {
		t.Fatal(count)
	}
}
