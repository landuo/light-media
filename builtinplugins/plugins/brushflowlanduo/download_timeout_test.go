package brushflowlanduo

import (
	"encoding/json"
	"math"
	"slices"
	"testing"
	"time"

	sdk "light-media/builtinplugins/internal/sdk"
)

func TestUnfinishedDownloadSeconds(t *testing.T) {
	for _, test := range []struct {
		name, kind string
		raw        Row
		want       any
	}{
		{"qb zero", "qbittorrent", Row{"progress": 0, "time_active": 0, "seeding_time": 0}, int64(0)},
		{"qb discount previous seeding", "qbittorrent", Row{"progress": .4, "time_active": json.Number("5400"), "seeding_time": json.Number("1800")}, int64(3600)},
		{"qb missing seeding counter", "qbittorrent", Row{"progress": 0, "time_active": 9000}, nil},
		{"qb missing active counter", "qbittorrent", Row{"progress": 0, "seeding_time": 0}, nil},
		{"qb seeding exceeds active", "qbittorrent", Row{"progress": 0, "time_active": 9000, "seeding_time": 9001}, nil},
		{"qb negative counter", "qbittorrent", Row{"progress": 0, "time_active": 9000, "seeding_time": -1}, nil},
		{"qb overflow", "qbittorrent", Row{"progress": 0, "time_active": json.Number("9223372036854775808"), "seeding_time": 0}, nil},
		{"qb complete", "qbittorrent", Row{"progress": 1, "time_active": 9000, "seeding_time": 0}, nil},
		{"qb missing progress", "qbittorrent", Row{"time_active": 9000, "seeding_time": 0}, nil},
		{"tr unrounded percent", "transmission", Row{"percentDone": .99999, "progress": 100, "secondsDownloading": 3600.0}, int64(3600)},
		{"tr zero", "transmission", Row{"percentDone": 0, "secondsDownloading": json.Number("0")}, int64(0)},
		{"tr missing", "transmission", Row{"percentDone": 0, "added_date": 1, "secondsSeeding": 20000}, nil},
		{"tr unknown progress", "transmission", Row{"progress": 0, "secondsDownloading": 9000}, nil},
		{"tr complete", "transmission", Row{"percentDone": 1, "secondsDownloading": 9000}, nil},
		{"tr out of range progress", "transmission", Row{"percentDone": -.01, "secondsDownloading": 9000}, nil},
		{"tr invalid type", "transmission", Row{"percentDone": 0, "secondsDownloading": "99999"}, nil},
		{"tr fractional", "transmission", Row{"percentDone": 0, "secondsDownloading": 9000.1}, nil},
		{"tr infinite", "transmission", Row{"percentDone": 0, "secondsDownloading": math.Inf(1)}, nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			got := unfinishedDownloadSeconds(DownloadState{Kind: test.kind, Raw: test.raw})
			if got != test.want {
				t.Fatalf("active download seconds = %v, want %v", got, test.want)
			}
		})
	}
}

func TestDownloadTimeoutThroughNativeTaskProjection(t *testing.T) {
	for _, test := range []struct{ kind, fields string }{
		{"qbittorrent", `{"progress":0.5,"time_active":7200,"seeding_time":3600}`},
		{"transmission", `{"percentDone":0.99999,"secondsDownloading":3600}`},
	} {
		t.Run(test.kind, func(t *testing.T) {
			var snapshot sdk.DownloaderTasksV1
			if err := json.Unmarshal([]byte(`{"kind":"`+test.kind+`","tasks":[{"hash":"a","`+test.kind+`":`+test.fields+`}]}`), &snapshot); err != nil {
				t.Fatal(err)
			}
			ds, err := brushTaskRows(snapshot)
			if err != nil || len(ds) != 1 {
				t.Fatalf("native projection: %v %v", ds, err)
			}
			core := newCore(&promotionHost{now: promotionNow()}, Config{})
			info := core.torrentInfo(ds[0])
			for _, test := range []struct {
				hours float64
				want  bool
			}{{0, false}, {-1, false}, {1.01, false}, {1, true}, {.99, true}} {
				got := downloadTimeoutReason(Task{Row{"download_time": test.hours}}, info)
				if (got != "") != test.want {
					t.Errorf("%g-hour threshold, got %q (seconds %v)", test.hours, got, info["dltime"])
				}
			}
		})
	}
}

func TestDownloadTimeoutAcrossChecksAndRestart(t *testing.T) {
	for _, mode := range []string{"ordinary", "task dynamic", "global dynamic"} {
		t.Run(mode, func(t *testing.T) {
			ctx := t.Context()
			now := promotionNow()
			task := promotionTask()
			task.Row["download_time"], task.Row["del_no_free"] = 4, false
			cfg := Config{Tasks: []Task{task}, Settings: Row{"enabled": true}}
			switch mode {
			case "task dynamic":
				task.Row["proxy_delete"], task.Row["delete_size_range"] = true, "100-200"
			case "global dynamic":
				cfg.Settings["global_proxy_delete"], cfg.Settings["global_delete_size_range"] = true, "100-200"
			}
			const running = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
			const unknown = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
			const complete = "cccccccccccccccccccccccccccccccccccccccc"
			host := &promotionHost{now: now, data: map[string]json.RawMessage{}}
			records := Rows{}
			for _, hash := range []string{running, unknown, complete} {
				records[hash] = promotionRecord(task, hash, now.Add(-30*24*time.Hour), .5)
				records[hash]["dltime"] = 30 * 86400 // old stored data is not a live activity reading
				ds := promotionTorrent(task, hash, now.Add(-30*24*time.Hour), .5)
				host.torrents = append(host.torrents, ds)
			}
			host.torrents[0].Raw["seeding_time"] = 4000
			host.torrents[2].Raw["progress"] = 1
			host.torrents[2].Raw["time_active"], host.torrents[2].Raw["seeding_time"] = 100000, 0
			if err := host.Put(ctx, taskKey(task.ID(), "torrents"), records); err != nil {
				t.Fatal(err)
			}
			core := newCore(host, cfg)
			for _, step := range []struct {
				state    string
				seconds  int
				restart  bool
				wantGone bool
			}{
				{"downloading", 1800, false, false}, {"stoppedDL", 1800, false, false},
				{"queuedDL", 1800, true, false}, {"stalledDL", 14399, false, false},
				{"downloading", 14400, false, true},
			} {
				host.now = host.now.Add(48 * time.Hour)
				host.torrents[0].Raw["state"] = step.state
				host.torrents[0].Raw["time_active"] = 4000 + step.seconds
				if step.restart {
					core = newCore(host, cfg)
				}
				if err := core.Check(ctx, task.ID(), true); err != nil {
					t.Fatal(err)
				}
				if !step.wantGone && len(host.deletes) != 0 {
					t.Fatalf("%s at %d active seconds: unexpected deletion %v", step.state, step.seconds, host.deletes)
				}
				if step.wantGone && (len(host.deletes) != 1 || !slices.Equal(host.deletes[0], []string{running})) {
					t.Fatalf("%s at threshold: expected only the unfinished timed-out torrent, got %v", step.state, host.deletes)
				}
			}
		})
	}
}

func TestDownloadTimeoutPreservesOtherTorrentClocks(t *testing.T) {
	host := &promotionHost{now: promotionNow()}
	core := newCore(host, Config{})
	task := Task{Row{"download_time": 1, "seed_inactivetime": 5}}
	ds := DownloadState{Kind: "qbittorrent", Raw: Row{
		"added_on": host.now.Unix() - 2000, "completion_on": host.now.Unix() - 12,
		"last_activity": host.now.Unix() - 600, "uploaded": 80000, "progress": .5,
	}}
	info := core.torrentInfo(ds)
	if info["dltime"] != nil || number(info["avg_upspeed"]) != 40 || number(info["seeding_time"]) != 12 {
		t.Fatalf("unexpected clock or average upload rate: %v", info)
	}
	remove, why, err := core.deleteCondition(t.Context(), task, info, Row{})
	if err != nil || !remove || why != "未活动时间达到 5 分钟" {
		t.Fatalf("independent inactivity rule: remove=%t, reason=%s, err=%v", remove, why, err)
	}
}
