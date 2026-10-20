package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"light-media/internal/catalog"
	"light-media/internal/download"
	"light-media/internal/notice"
)

// This first test uses preexisting APIs and is independently extracted for a
// readonly overlay against frozen checkpoint4; its before failure is real.
func TestPluginLarkActualDownloadingCommandQueryNoticeAndKnownReplay(t *testing.T) {
	a := newTestApp(t)
	installUnregisteredLarkConfig(t, a, map[string]any{"enabled": true})
	core, ctx := larkStartedCoreFixture(t, a)
	var queries atomic.Int64
	daemon := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		queries.Add(1)
		if r.URL.Path != "/api/v2/torrents/info" || r.URL.Query().Get("filter") != "downloading" {
			t.Error("not original downloading query", r.URL.Path)
			w.WriteHeader(400)
			return
		}
		workflowJSON(w, []any{map[string]any{"hash": strings.Repeat("a", 40), "name": "Synthetic.Show.S01E02.2024.mkv", "state": "downloading", "save_path": "/owned", "total_size": 1024, "progress": .5, "completed": 512, "dlspeed": 8, "upspeed": 0, "tags": "light-media", "category": "owned"}, map[string]any{"hash": strings.Repeat("b", 40), "name": "Foreign task", "state": "downloading", "save_path": "/owned", "total_size": 1024, "progress": .5, "tags": "MOVIEPILOT"}})
	}))
	defer daemon.Close()
	a.config.Downloaders = []DownloaderConfig{{Config: download.Config{ID: "owned-qb", Kind: "qb", BaseURL: daemon.URL}, Name: "Owned qB"}}
	a.config.AppDomain = "https://owned.invalid/subpath"
	sender := installNoticeSourceFixtures(t, a, catalog.Media{}, 0, 0)
	var lost atomic.Bool
	a.pluginStore.notices.afterCommit = func(phase string) error {
		if phase == "queue_finish" && lost.CompareAndSwap(false, true) {
			return errors.New("owned actual queue Commit ACK lost")
		}
		return nil
	}
	if e := a.larkRouteCore(ctx, core, nil, "/downloading ignored arguments", "ou_owned", "", ""); e != nil {
		t.Fatal("source downloading consumer did not execute", e)
	}
	if queries.Load() != 1 || sender.Sends.Load() != 1 || sender.Metadata.Load() != 0 || !lost.Load() {
		t.Fatal("real query/actual notice path missing", queries.Load(), sender.Sends.Load(), sender.Metadata.Load(), lost.Load())
	}
	r := noticeV2OnlyRoute(t, a)
	if !r.Complete || r.SourceError != "" || r.BusinessV2 == nil || r.BusinessV2.Projection != "absent-source-context/v1" {
		t.Fatal("no authenticated completed command route", r)
	}
	render, e := a.pluginStore.noticeBusinessRender(ctx, r.ID)
	if e != nil {
		t.Fatal(e)
	}
	fields := render.Snapshot.Fields
	if fields["title"] != "共 1 个任务正在下载：" || fields["text"] != "1. Synthetic.Show.S01E02.2024.mkv 1.0K 50.0%" || fields["save_history"] != false || fields["mtype"] != "资源下载" || fields["channel"] != "飞书" || fields["source"] != "Lark" || fields["userid"] != "ou_owned" || fields["link"] != "https://owned.invalid/subpath/#/downloading" {
		t.Fatal("original message projection changed", fields)
	}
	histories, e := a.Store.List(ctx, "notification", 10, 0)
	if e != nil || len(histories) != 0 {
		t.Fatal("original save_history=False ignored", histories, e)
	}
	core.ordinal = 0
	if e = a.larkRouteCore(ctx, core, nil, "/downloading ignored arguments", "ou_owned", "", ""); e != nil || queries.Load() != 1 || sender.Sends.Load() != 1 {
		t.Fatal("known command replay queried/sent again", e, queries.Load(), sender.Sends.Load())
	}
	// A cold trusted read authenticates the same complete route without a new
	// source action, new daemon lookup, or callback delivery.
	dir := a.Dir
	core.release()
	if e = a.Close(); e != nil {
		t.Fatal(e)
	}
	cold, e := New(Options{DataDir: dir})
	if e != nil {
		t.Fatal(e)
	}
	defer cold.Close()
	got, _, e := cold.pluginStore.readNoticeRoute(context.Background(), cold.Store.DB, r.ID)
	if e != nil || !got.Complete || got.AcceptedSHA != r.AcceptedSHA || queries.Load() != 1 || sender.Sends.Load() != 1 {
		t.Fatal("cold complete route changed", e)
	}
}

func TestPluginLarkActualDownloadingTransmissionAndCaughtQuery(t *testing.T) {
	for _, kind := range []string{"transmission", "qb-error", "qb-empty"} {
		t.Run(kind, func(t *testing.T) {
			a := newTestApp(t)
			installUnregisteredLarkConfig(t, a, map[string]any{"enabled": true})
			core, ctx := larkStartedCoreFixture(t, a)
			var queries atomic.Int64
			daemon := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if kind == "qb-error" {
					queries.Add(1)
					w.WriteHeader(500)
					return
				}
				if kind == "qb-empty" {
					queries.Add(1)
					fmt.Fprint(w, `[]`)
					return
				}
				var request struct {
					Method string
					ID     uint64
				}
				if json.NewDecoder(r.Body).Decode(&request) != nil {
					w.WriteHeader(400)
					return
				}
				var value any = map[string]any{"version": "4.1.0"}
				if request.Method == "torrent_get" {
					queries.Add(1)
					rows := []any{}
					for state := 0; state <= 6; state++ {
						rows = append(rows, map[string]any{"id": state + 1, "hash_string": strings.Repeat(string(rune('a'+state)), 40), "name": "Synthetic.Show.S01E02.2024.mkv", "status": state, "labels": []string{"light-media"}, "download_dir": "/owned", "total_size": 1024, "percent_done": .123456, "left_until_done": 900, "rate_download": 8, "rate_upload": 4})
					}
					value = map[string]any{"torrents": rows}
				}
				workflowJSON(w, map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": value})
			}))
			defer daemon.Close()
			driver := "qb"
			if kind == "transmission" {
				driver = "tr"
			}
			a.config.Downloaders = []DownloaderConfig{{Config: download.Config{ID: "owned-daemon", Kind: driver, BaseURL: daemon.URL}, Name: "Owned daemon"}}
			sender := installNoticeSourceFixtures(t, a, catalog.Media{}, 0, 0)
			if e := a.larkRouteCore(ctx, core, nil, "/downloading", "ou_owned", "", ""); e != nil {
				t.Fatal(e)
			}
			if queries.Load() != 1 || sender.Sends.Load() != 1 {
				t.Fatal(queries.Load(), sender.Sends.Load())
			}
			r := noticeV2OnlyRoute(t, a)
			render, e := a.pluginStore.noticeBusinessRender(ctx, r.ID)
			if e != nil {
				t.Fatal(e)
			}
			f := render.Snapshot.Fields
			if kind == "transmission" {
				if f["title"] != "共 1 个任务正在下载：" || f["text"] != "1. Synthetic.Show.S01E02.2024.mkv 1.0K 12.3%" {
					t.Fatal("actual installed library state/progress semantics lost", f)
				}
			} else if f["title"] != "没有正在下载的任务！" || f["text"] != nil {
				t.Fatal("caught/empty source text must retain None", f)
			}
			root, e := a.pluginStore.larkRootRead(ctx, a.Store.DB, core.inbox.Instance, core.inbox.Generation)
			if e != nil {
				t.Fatal(e)
			}
			steps, e := a.pluginStore.larkInboxStepsTx(ctx, a.Store.DB, root.Inboxes[core.inbox.ID])
			if e != nil || len(steps) != 3 {
				t.Fatal(len(steps), e)
			}
			var fact larkCommandDownloading
			if json.Unmarshal(steps[1].Result, &fact) != nil || len(fact.Queries) != 1 {
				t.Fatal("missing actual query source receipt")
			}
			if (fact.Queries[0].CaughtCode != "") != (kind == "qb-error") {
				t.Fatal("caught failure became normal empty query", fact.Queries)
			}
		})
	}
}

func TestPluginNoticeAbsentContextCannotPretendTMDBProjection(t *testing.T) {
	good := sourceBusinessNotice{AbsentContext: true, Envelope: notice.Object{"title": "owned"}, Kwargs: notice.Object{}}
	input, e := larkBusinessNoticeContext(good)
	if e != nil || input.Meta != nil || input.Media != nil || input.Torrent != nil || input.Transfer != nil {
		t.Fatal(input, e)
	}
	for _, field := range []string{"meta", "media", "provider", "registration", "catalog"} {
		t.Run(field, func(t *testing.T) {
			bad := good
			switch field {
			case "meta":
				bad.Meta = json.RawMessage(`{}`)
			case "media":
				bad.MediaInfo = json.RawMessage(`{}`)
			case "provider":
				bad.ProviderInfo = json.RawMessage(`{}`)
			case "registration":
				bad.Registration = json.RawMessage(`{}`)
			case "catalog":
				bad.Media.ID = 1
			}
			if _, e := larkBusinessNoticeContext(bad); !errors.Is(e, notice.ErrTemplateDependency) {
				t.Fatal("partial object accepted as original None", field, e)
			}
		})
	}
}

func TestPluginLarkSourceDownloaderModulePriority(t *testing.T) {
	a := newTestApp(t)
	installUnregisteredLarkConfig(t, a, map[string]any{"enabled": true})
	core, ctx := larkStartedCoreFixture(t, a)
	var queries atomic.Int64
	daemon := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" {
			queries.Add(1)
			workflowJSON(w, []any{map[string]any{"name": "QB.Synthetic.2024.mkv", "hash": strings.Repeat("a", 40), "save_path": "/owned", "tags": "light-media", "state": "downloading", "total_size": 1024, "progress": .5}})
			return
		}
		var call struct {
			Method string
			ID     uint64
		}
		if json.NewDecoder(r.Body).Decode(&call) != nil {
			w.WriteHeader(400)
			return
		}
		var result any = map[string]any{"version": "4.1.0"}
		if call.Method == "torrent_get" {
			queries.Add(1)
			result = map[string]any{"torrents": []any{map[string]any{"id": 1, "status": 4, "name": "TR.Synthetic.2024.mkv", "hash_string": strings.Repeat("b", 40), "download_dir": "/owned", "labels": []string{"light-media"}, "total_size": 1024, "percent_done": .25}}}
		}
		workflowJSON(w, map[string]any{"jsonrpc": "2.0", "id": call.ID, "result": result})
	}))
	defer daemon.Close()
	// Reverse global config order must not reverse source module priorities.
	a.config.Downloaders = []DownloaderConfig{{Config: download.Config{ID: "tr", Kind: "tr", BaseURL: daemon.URL}}, {Config: download.Config{ID: "qb", Kind: "qb", BaseURL: daemon.URL}}}
	sender := installNoticeSourceFixtures(t, a, catalog.Media{}, 0, 0)
	if e := a.larkRouteCore(ctx, core, nil, "/downloading", "ou_owned", "", ""); e != nil {
		t.Fatal(e)
	}
	r := noticeV2OnlyRoute(t, a)
	render, e := a.pluginStore.noticeBusinessRender(ctx, r.ID)
	if e != nil {
		t.Fatal(e)
	}
	if queries.Load() != 2 || sender.Sends.Load() != 1 || render.Snapshot.Fields["text"] != "1. QB.Synthetic.2024.mkv 1.0K 50.0%\n2. TR.Synthetic.2024.mkv 1.0K 25.0%" {
		t.Fatal("module order or actual query changed", render.Snapshot.Fields, queries.Load(), sender.Sends.Load())
	}
}
