package brushflowlanduo

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	sdk "light-media/builtinplugins/internal/sdk"
)

type brushCompletedReadHost struct {
	*promotionHost
	caller sdk.HostCaller
}

func (h *brushCompletedReadHost) Completed(ctx context.Context, name string, hashes []string) ([]DownloadState, error) {
	return new(standardHost).Completed(withBrushHost(ctx, h.caller), name, hashes)
}

func TestDynamicDeletionDistinguishesFailedAndEmptyCompletedList(t *testing.T) {
	for _, tc := range []struct {
		name        string
		nativeError bool
	}{
		{"downloader_failed", true},
		{"successfully_empty", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			first, second := strings.Repeat("a", 40), strings.Repeat("b", 40)
			now, task := promotionNow(), promotionTask()
			task.Row["del_no_free"] = false
			task.Row["proxy_delete"] = true
			task.Row["delete_size_range"] = "1-5"
			task.Row["seed_ratio"] = 2
			torrents := []DownloadState{promotionTorrent(task, first, now, 1), promotionTorrent(task, second, now, .25)}
			torrents[0].Raw["ratio"] = 3
			h := &brushCompletedReadHost{promotionHost: &promotionHost{now: now, data: map[string]json.RawMessage{}, torrents: torrents}}
			reads := 0
			h.caller = func(_ context.Context, method string, raw json.RawMessage) (json.RawMessage, error) {
				switch method {
				case sdk.DownloaderConfigurationsReadCapability:
					return json.RawMessage(`{"schema_version":1,"downloaders":[{"id":"owned","name":"qb","kind":"qbittorrent","enabled":true}]}`), nil
				case sdk.DownloaderSourceCompletedCapability:
					var request sdk.DownloaderCompletedRequestV1
					if err := json.Unmarshal(raw, &request); err != nil || !slices.Equal(request.Hashes, []string{second}) {
						t.Fatalf("expected fallback only for unselected torrent, got %+v, %v", request, err)
					}
					reads++
					return json.Marshal(sdk.DownloaderSourceTasksResultV1{DownloaderTasksV1: sdk.DownloaderTasksV1{
						SchemaVersion: 1, DownloaderID: "owned", Kind: "qbittorrent", Tasks: []sdk.DownloaderTaskV1{}}, NativeError: tc.nativeError})
				default:
					t.Fatalf("unexpected callback %s", method)
					return nil, nil
				}
			}
			if err := h.Put(t.Context(), taskKey(task.ID(), "torrents"), Rows{
				first: promotionRecord(task, first, now, 1), second: promotionRecord(task, second, now, .25),
			}); err != nil {
				t.Fatal(err)
			}
			p := newCore(h, Config{Settings: Row{"enabled": true}, Tasks: []Task{task}})
			err := p.Check(t.Context(), task.ID(), false)
			if reads != 1 || (err != nil) != tc.nativeError {
				t.Fatalf("completed reads=%d, check returned %v", reads, err)
			}
			runs, err := p.runs(t.Context(), task.ID())
			if err != nil || len(runs) != 1 || row(runs[0])["success"] != !tc.nativeError {
				t.Fatalf("check history %+v err=%v", runs, err)
			}
			if tc.nativeError {
				if len(h.deletes) != 0 || !truth(p.Runtime(task.ID())["last_error"]) {
					t.Fatalf("failed read submitted deletions %v or lost the task error", h.deletes)
				}
			} else if len(h.deletes) != 1 || !slices.Equal(h.deletes[0], []string{first}) {
				t.Fatalf("valid empty response lost the earlier selection: %v", h.deletes)
			}
		})
	}
}
