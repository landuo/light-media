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
