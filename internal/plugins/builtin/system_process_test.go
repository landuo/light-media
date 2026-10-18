package builtin

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestSystemProcessOriginalTableOrderEmptyAndDuration(t *testing.T) {
	table := systemProcessTable([]ProcessInfo{{PID: 2, Name: "<fixture&name>", CPU: 125.5, Memory: 1.2, RunTime: 3661}, {PID: 10, Name: "newer", RunTime: 0}}, false)
	raw, _ := json.Marshal(table)
	text := string(raw)
	if strings.Index(text, `"text":10`) > strings.Index(text, `"text":2`) || !strings.Contains(text, "1时1分1秒") || !strings.Contains(text, "125.5%") || !strings.Contains(text, "1.2MB") || !strings.Contains(text, "0秒") {
		t.Fatal("original process table differs", text)
	}
	empty, _ := json.Marshal(systemProcessTable(nil, true))
	if !strings.Contains(string(empty), "暂无数据") || !strings.Contains(string(empty), "242px") {
		t.Fatal("empty or compact dashboard table missing")
	}
	if _, err := HandleSystemProcess(context.Background(), "invoke", json.RawMessage(`{"action":"delete","config":{}}`), nil); err == nil {
		t.Fatal("unexpected write operation accepted")
	}
}

// The installed original enumerates the entire process list. A larger host must
// still render both surfaces rather than gain an invented 1024-row cutoff.
func TestSystemProcessRendersAllOriginalRowsBeyond1024(t *testing.T) {
	items := make([]ProcessInfo, 1025)
	for i := range items {
		items[i] = ProcessInfo{PID: int32(i + 1), Name: "owned-process", Memory: 1, CreateTime: 1}
	}
	payload, err := json.Marshal(items)
	if err != nil {
		t.Fatal(err)
	}
	for _, method := range []string{"page", "dashboard"} {
		t.Run(method, func(t *testing.T) {
			calls := 0
			out, err := HandleSystemProcess(context.Background(), method, json.RawMessage(`{"config":{"enable":true}}`), func(_ context.Context, name string, raw json.RawMessage) (json.RawMessage, error) {
				calls++
				if name != "process.list" || string(raw) != "{}" {
					t.Fatal("unexpected host request", name)
				}
				return payload, nil
			})
			if err != nil {
				t.Fatal(err)
			}
			if calls != 1 || strings.Count(string(out), `"text":"owned-process"`) != len(items) {
				t.Fatal("process rows were omitted", calls)
			}
			if strings.Index(string(out), `"text":1025`) > strings.Index(string(out), `"text":1}`) {
				t.Fatal("original descending PID order changed")
			}
		})
	}
}
