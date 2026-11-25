package brushflowlanduo

import (
	"context"
	"encoding/json"
	"testing"
)

type statusSiteHost struct {
	globalPartialHost
	reads int
}

func (h *statusSiteHost) Sites(context.Context) ([]Site, error) {
	h.reads++
	return []Site{{ID: 1, Name: "first", Domain: "first.test"}, {ID: 1, Name: "second"}}, nil
}

func (*statusSiteHost) SiteUsers(context.Context) ([]UserData, error) {
	return []UserData{{Domain: "first.test", Ratio: 2}}, nil
}

func TestStatusReadsSitesOnceForTasksRatiosAndOptions(t *testing.T) {
	host := &statusSiteHost{}
	core := newCore(host, Config{Settings: Row{}, Tasks: []Task{
		{Row{"id": "a", "site_id": 1, "site_ratio_control": true, "site_ratio_target": 1}},
		{Row{"id": "b", "site_id": 1}},
		{Row{"id": "c", "site_id": 2}},
	}})
	status, err := core.Status(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	tasks := status["tasks"].([]any)
	if len(tasks) != 3 || host.reads != 1 {
		t.Fatalf("tasks %d, site reads %d", len(tasks), host.reads)
	}
	for i, want := range []string{"first", "first", "站点已删除"} {
		if got := tasks[i].(Row)["site_name"]; got != want {
			t.Fatalf("task %d site %v, want %s", i, got, want)
		}
	}
	if tasks[0].(Row)["site_ratio"].(Row)["reached"] != true || len(status["options"].(Row)["sites"].([]any)) != 2 {
		t.Fatal("lost task ratio or site options")
	}
}

func TestStatusSeedingSizeObservesChangedBytes(t *testing.T) {
	h := &globalPartialHost{data: map[string]json.RawMessage{}}
	p := newCore(h, Config{})
	key := taskKey("owned", "torrents")
	for _, tc := range []struct {
		data string
		want float64
	}{
		{`{"a":{"size":"32"},"b":{"deleted":"1","size":1024}}`, 32},
		{`{"a":{"size":"32"},"b":{"deleted":"1","size":1024}}`, 32},
		{`{"a":{"deleted":true,"size":"32"},"b":{"size":24}}`, 24},
	} {
		h.data[key] = json.RawMessage(tc.data)
		got, err := p.summarySize(t.Context(), "owned")
		if err != nil || got != tc.want {
			t.Fatal(got, err)
		}
	}
}
