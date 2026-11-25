package brushflowlanduo

import (
	"context"
	"encoding/json"
	"slices"
	"testing"
	"time"
)

type globalPartialHost struct {
	Host
	data    map[string]json.RawMessage
	effects []string
}

func (h *globalPartialHost) Now() time.Time { return time.Unix(1700000000, 0) }

func (h *globalPartialHost) Get(_ context.Context, key string) (any, error) {
	if value, ok := h.data[key]; ok {
		return value, nil
	}
	return nil, nil
}

func (h *globalPartialHost) Put(_ context.Context, key string, value any) error {
	raw, err := json.Marshal(value)
	if err == nil {
		h.data[key] = raw
	}
	return err
}

func (h *globalPartialHost) Downloader(_ context.Context, name string) (*Downloader, error) {
	return &Downloader{Name: name, Kind: "qbittorrent", Active: true}, nil
}

func (h *globalPartialHost) Torrents(_ context.Context, name string) ([]DownloadState, error) {
	return []DownloadState{{Kind: "qbittorrent", Raw: Row{"hash": name, "name": name, "total_size": 3 << 30,
		"progress": 1, "completion_on": h.Now().Unix() - 7200}}}, nil
}

func (h *globalPartialHost) Reannounce(_ context.Context, name string, _ []string) error {
	h.effects = append(h.effects, "reannounce "+name)
	return nil
}

func (h *globalPartialHost) Delete(_ context.Context, name string, _ []string, _ bool) (bool, error) {
	h.effects = append(h.effects, "delete "+name)
	if name == "second" {
		return false, uncertainBrushEffect{}
	}
	return true, nil
}

func (h *globalPartialHost) Log(context.Context, string, string) {}

func (h *globalPartialHost) NewID() string { return "check" }

func (h *globalPartialHost) Sites(context.Context) ([]Site, error) { return nil, nil }

func (h *globalPartialHost) Downloaders(context.Context) ([]Downloader, error) { return nil, nil }

func (h *globalPartialHost) Message(context.Context, Notice) error { return nil }

func TestGlobalDeleteRetainsEarlierConfirmationWhenLaterOutcomeUnknown(t *testing.T) {
	ctx := t.Context()
	host := &globalPartialHost{data: map[string]json.RawMessage{}}
	config := Config{Settings: Row{"enabled": true, "global_proxy_delete": true, "global_delete_size_range": "1-2"}}
	names := []string{"first", "second", "third"}
	for _, name := range names {
		config.Tasks = append(config.Tasks, Task{Row{"id": name, "downloader": name, "enabled": true, "proxy_delete": true}})
		if err := host.Put(ctx, taskKey(name, "torrents"), Rows{name: Row{"title": name, "size": 3 << 30, "deleted": false}}); err != nil {
			t.Fatal(err)
		}
	}
	core := newCore(host, config)
	count, err := core.GlobalDelete(ctx)
	if count != 1 || !boundary(err) || !slices.Equal(host.effects, []string{"reannounce first", "delete first", "reannounce second", "delete second"}) {
		t.Fatalf("expected one known deletion and a stop on the unknown receipt: %d %v %v", count, err, host.effects)
	}
	for i, name := range names {
		records, err := core.records(ctx, name, "torrents")
		if err != nil || truth(records.Values[name]["deleted"]) != (i == 0) {
			t.Fatalf("wrong stored deletion state for %s: %v %v", name, records, err)
		}
	}
}

func TestCheckReportsEarlierConfirmedGlobalDeletion(t *testing.T) {
	ctx := t.Context()
	host := &globalPartialHost{data: map[string]json.RawMessage{}}
	config := Config{Settings: Row{"enabled": true, "global_proxy_delete": true, "global_delete_size_range": "1-2"}}
	for _, name := range []string{"first", "second"} {
		config.Tasks = append(config.Tasks, Task{Row{"id": name, "downloader": name, "enabled": true, "proxy_delete": true}})
		if err := host.Put(ctx, taskKey(name, "torrents"), Rows{name: Row{"title": name, "size": 3 << 30, "deleted": false}}); err != nil {
			t.Fatal(err)
		}
	}
	core := newCore(host, config)
	checkErr := core.Check(ctx, "first", true)
	if !boundary(checkErr) {
		t.Fatalf("expected the later unconfirmed deletion to stop the run: %v", checkErr)
	}
	history, err := core.runs(ctx, "first")
	if err != nil || len(history) != 1 {
		t.Fatalf("expected one persisted check report: %v %v", history, err)
	}
	report := row(history[0])
	if report["success"] != false || report["error"] != checkErr.Error() || number(report["global_deleted_count"]) != 1 || number(report["deleted_count"]) != 1 {
		t.Fatalf("expected the report to retain the confirmed deletion and the later failure: %v", report)
	}
}
