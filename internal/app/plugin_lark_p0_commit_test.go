package app

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestPluginLarkActualSubscriptionCommitAndSourceResultAreAtomic(t *testing.T) {
	for _, branch := range []string{"lost_ack", "preflight", "wrong_kind"} {
		t.Run(branch, func(t *testing.T) {
			a := subscriptionTestApp(t)
			installUnregisteredLarkConfig(t, a, map[string]any{"enabled": true})
			core, ctx := larkStartedCoreFixture(t, a)
			request := subscriptionRequest(t)
			operation := "subscription.save"
			if branch == "wrong_kind" {
				operation = "download.confirm"
			}
			if branch == "preflight" {
				request.TMDBID = 0
			}
			var calls atomic.Int64
			saved, e := larkCoreWrite(ctx, a, core, operation, request, func(ctx context.Context) (Subscription, error) {
				calls.Add(1)
				s, e := a.saveSubscription(ctx, "", request)
				if e != nil {
					return s, e
				}
				return s, errors.New("owned caller ACK lost after actual source+subscription Commit")
			})
			var rows int
			if err := a.Store.DB.QueryRow(`SELECT COUNT(*) FROM records WHERE kind='subscription'`).Scan(&rows); err != nil {
				t.Fatal(err)
			}
			if branch != "lost_ack" {
				if e == nil || pluginErrorCodeInternal(e) == "LARK_UNCONFIRMED" || rows != 0 {
					t.Fatal("zero-effect transaction was accepted or became unknown", branch, rows, e)
				}
				if e = a.finishLarkInbox(ctx, core, e); e != nil {
					t.Fatal(e)
				}
				if _, e = a.pluginStore.EnqueueRun(ctx, "LarkMessager", "page", nil); e != nil {
					t.Fatal("known rejected P0 write poisoned queue", e)
				}
				return
			}
			if e != nil || saved.ID == "" || rows != 1 || calls.Load() != 1 {
				t.Fatal("real committed result not recovered", saved.ID, rows, calls.Load(), e)
			}
			core.ordinal = 0
			replay, e := larkCoreWrite(ctx, a, core, operation, request, func(context.Context) (Subscription, error) {
				calls.Add(1)
				return Subscription{}, errors.New("must not execute again")
			})
			if e != nil || replay.ID != saved.ID || calls.Load() != 1 {
				t.Fatal("real subscription replayed", calls.Load(), e)
			}
		})
	}
}
func TestPluginLarkActualDownloadConfirmationAndSourceResultAreAtomic(t *testing.T) {
	a, _, _ := importTestApp(t)
	m := importFixture(t, a, "movie")
	torrent, _ := workflowTorrent("测试影片.2024.1080p.mkv")
	var adds atomic.Int64
	qb := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/torrents/info":
			workflowJSON(w, []any{})
		case "/api/v2/torrents/add":
			adds.Add(1)
			io.WriteString(w, "Ok.")
		default:
			http.NotFound(w, r)
		}
	}))
	defer qb.Close()
	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/torrents.php":
			io.WriteString(w, workflowHTML("测试影片.2024.1080p.WEB-DL.x264-TEST", "", false))
		case "/download.php":
			w.Write(torrent)
		default:
			http.NotFound(w, r)
		}
	}))
	defer site.Close()
	workflowConfigure(t, a, site.URL, qb.URL)
	installUnregisteredLarkConfig(t, a, map[string]any{"enabled": true})
	core, ctx := larkStartedCoreFixture(t, a)
	preview, e := a.previewDownload(ctx, PreviewRequest{SearchRequest: SearchRequest{MediaType: m.MediaType, TMDBID: m.ID}, SiteID: "fixture-site", ResourceID: "101", DownloaderID: "fixture-qb", RootID: "root"}, false)
	if e != nil {
		t.Fatal(e)
	}
	confirmed, e := larkCoreWrite(ctx, a, core, "download.confirm", preview.ID, func(ctx context.Context) (Intent, error) {
		in, e := a.confirmDownload(ctx, preview.ID, false)
		if e != nil {
			return in, e
		}
		return in, errors.New("owned caller ACK lost after actual source+intent Commit")
	})
	if e != nil || confirmed.ID == "" || confirmed.State != "planned" {
		t.Fatal(confirmed.ID, e)
	}
	core.ordinal = 0
	replay, e := larkCoreWrite(ctx, a, core, "download.confirm", preview.ID, func(context.Context) (Intent, error) { t.Fatal("actual P0 intent duplicated"); return Intent{}, nil })
	if e != nil || replay.ID != confirmed.ID {
		t.Fatal(replay.ID, e)
	}
	var intents, jobs int
	if e = a.Store.DB.QueryRow(`SELECT COUNT(*) FROM records WHERE kind='intent'`).Scan(&intents); e != nil {
		t.Fatal(e)
	}
	if e = a.Store.DB.QueryRow(`SELECT COUNT(*) FROM jobs WHERE kind='lark_download' AND json_extract(payload,'$.id')=?`, confirmed.ID).Scan(&jobs); e != nil {
		t.Fatal(e)
	}
	if intents != 1 || jobs != 1 || adds.Load() != 0 {
		t.Fatal("confirmation duplicated or falsely dispatched downloader", intents, jobs, adds.Load())
	}
}
