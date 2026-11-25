package brushflowlanduo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"
)

type brushDownloadLimitHost struct {
	globalPartialHost
	torrents map[string][]DownloadState
	readErr  error
	reads    map[string]int
	attempts int
	added    int
}

func (*brushDownloadLimitHost) Sites(context.Context) ([]Site, error) {
	return []Site{{ID: 1, Name: "UBits"}}, nil
}

func (*brushDownloadLimitHost) Downloaders(context.Context) ([]Downloader, error) {
	return []Downloader{{Name: "qb"}, {Name: "tr"}}, nil
}

func (h *brushDownloadLimitHost) Torrents(_ context.Context, name string) ([]DownloadState, error) {
	h.reads[name]++
	return h.torrents[name], h.readErr
}

func (*brushDownloadLimitHost) TransferInfo(context.Context) ([]Transfer, error) { return nil, nil }
func (*brushDownloadLimitHost) Sleep(context.Context, time.Duration) error       { return nil }
func (*brushDownloadLimitHost) RandomTag() string                                { return "marker" }
func (*brushDownloadLimitHost) Event(context.Context, Event) error               { return nil }

func (*brushDownloadLimitHost) Browse(context.Context, int, bool) ([]Row, error) {
	candidates := make([]Row, 24)
	for i := range candidates {
		candidates[i] = Row{"title": fmt.Sprintf("candidate-%d", i), "site_name": "UBits"}
	}
	return candidates, nil
}

func (h *brushDownloadLimitHost) Download(context.Context, Task, Row, Row, string) (string, error) {
	h.attempts++
	if h.attempts == 1 {
		return "", nil
	}
	h.added++
	// The daemon's listing does not reflect any of these additions yet.
	return fmt.Sprintf("hash-%d", h.added), nil
}

func TestBrushDownloadLimit(t *testing.T) {
	listErr := errors.New("downloader list unavailable")
	ubits := Task{Row{"id": "ubits", "name": "UBits", "site_id": 1, "downloader": "qb", "enabled": true, "maxdlcount": 5}}
	for _, test := range []struct {
		name       string
		global     int
		readErr    error
		wantAdded  int
		wantReason string
	}{
		{name: "task vacancy", wantAdded: 2, wantReason: "同时下载任务数达到上限 5"},
		{name: "global vacancy", global: 6, wantAdded: 1, wantReason: "全局同时下载任务数达到上限 6"},
		{name: "global full", global: 5, wantReason: "全局同时下载任务数达到上限 5"},
		{name: "query failure", readErr: listErr},
	} {
		t.Run(test.name, func(t *testing.T) {
			tags := GlobalTag + ", " + ubits.Tag()
			host := &brushDownloadLimitHost{
				globalPartialHost: globalPartialHost{data: map[string]json.RawMessage{}},
				reads:             map[string]int{},
				readErr:           test.readErr,
				torrents: map[string][]DownloadState{
					"qb": {
						{Kind: "qbittorrent", Raw: Row{"state": "queuedDL", "progress": 0, "tags": tags}},
						{Kind: "qbittorrent", Raw: Row{"state": "checkingDL", "progress": 0.5, "tags": tags}},
						{Kind: "qbittorrent", Raw: Row{"state": "stoppedDL", "progress": 0.5, "tags": tags}},
						{Kind: "qbittorrent", Raw: Row{"state": "stalledUP", "progress": 1, "tags": tags}},
						{Kind: "qbittorrent", Raw: Row{"progress": 0, "tags": GlobalTag + ", 刷流-other"}},
						{Kind: "qbittorrent", Raw: Row{"progress": 0, "tags": "subscription"}},
					},
					"tr": {
						{Kind: "transmission", Raw: Row{"status": 0, "percentDone": 0.99999, "progress": 100, "labels": []string{GlobalTag, "刷流-tr"}}},
						{Kind: "transmission", Raw: Row{"status": 6, "percentDone": 1, "progress": 100, "labels": []string{GlobalTag, "刷流-tr"}}},
					},
				},
			}
			core := newCore(host, Config{Settings: Row{"enabled": true, "global_maxdlcount": test.global}, Tasks: []Task{
				ubits, {Row{"id": "other", "downloader": "qb"}}, {Row{"id": "tr", "downloader": "tr"}},
			}})
			if err := core.Brush(t.Context(), ubits.ID()); !errors.Is(err, test.readErr) {
				t.Fatalf("Brush error = %v, want %v", err, test.readErr)
			}
			if host.added != test.wantAdded {
				t.Fatalf("added %d torrents, want %d", host.added, test.wantAdded)
			}
			if test.wantAdded > 0 && host.attempts != test.wantAdded+1 {
				t.Fatalf("a rejected add used a slot or the downloader got an extra request: %d attempts", host.attempts)
			}
			if host.reads["qb"] != 1 || test.global != 0 && host.reads["tr"] != 1 {
				t.Fatalf("expected one snapshot per relevant downloader, got %v", host.reads)
			}
			if test.wantReason == "" {
				return
			}
			runs, err := core.runs(t.Context(), ubits.ID())
			if err != nil || len(runs) != 1 {
				t.Fatalf("expected one run report, got %v (%v)", runs, err)
			}
			report := row(runs[0])
			if report["result"] != "precondition_blocked" || number(row(report["reason_counts"])[test.wantReason]) != 1 {
				t.Fatalf("expected the run to record %q, got %v", test.wantReason, report)
			}
		})
	}
}
