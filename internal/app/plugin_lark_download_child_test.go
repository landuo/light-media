package app

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestPluginLarkPrivateDownloadChildActualCommitColdAndUnconfirmed(t *testing.T) {
	for _, boundary := range []string{"lost_ack_cold", "preflight", "add_unconfirmed", "parent_cancelled"} {
		t.Run(boundary, func(t *testing.T) {
			a, _, _ := importTestApp(t)
			m := importFixture(t, a, "movie")
			torrent, hash := workflowTorrent("测试影片.2024.1080p.mkv")
			var adds atomic.Int64
			qb := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/v2/torrents/info":
					if adds.Load() == 0 {
						workflowJSON(w, []any{})
					} else {
						workflowJSON(w, []any{workflowTask(hash, 0)})
					}
				case "/api/v2/torrents/add":
					adds.Add(1)
					if boundary == "add_unconfirmed" {
						w.WriteHeader(500)
					} else {
						io.WriteString(w, "Ok.")
					}
				default:
					t.Error("ungranted daemon endpoint", r.URL.Path)
					w.WriteHeader(400)
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
			in, e := larkCoreWrite(ctx, a, core, "download.confirm", preview.ID, func(ctx context.Context) (Intent, error) { return a.confirmDownload(ctx, preview.ID, true) })
			if e != nil || in.LarkInbox != core.inbox.ID {
				t.Fatal(in.LarkInbox, e)
			}
			// There is only the held parent and this queued child. A normal worker
			// must not consume its only slot waiting for source-owned execution.
			if j, e := a.claim(ctx); !errors.Is(e, sql.ErrNoRows) {
				t.Fatal("generic worker claimed private source child", j.ID, e)
			}
			j, e := scanJob(a.Store.DB.QueryRow(`SELECT `+jobColumns+` FROM jobs WHERE kind='lark_download' AND json_extract(payload,'$.id')=?`, in.ID))
			if e != nil {
				t.Fatal(e)
			}
			if e = a.runDownload(context.Background(), j, in.ID); pluginErrorCodeInternal(e) != "LARK_CORE_SCOPE_DENIED" || adds.Load() != 0 {
				t.Fatal("generic execution bypassed original source authorization", e, adds.Load())
			}
			if boundary == "preflight" {
				a.config.Downloaders = nil
			}
			if boundary == "parent_cancelled" {
				if _, e = a.Store.DB.Exec(`UPDATE jobs SET state='cancelled',generation=generation+1 WHERE id=?`, core.job.ID); e != nil {
					t.Fatal(e)
				}
			}
			accepted, e := larkCoreWrite(ctx, a, core, "download.submit", in.ID, func(ctx context.Context) (Intent, error) {
				child, e := a.larkClaimDownloadChild(ctx, in)
				if e != nil {
					return Intent{}, e
				}
				if e = a.runDownload(ctx, child, in.ID); e != nil {
					return Intent{}, a.larkRejectUnsubmittedChild(ctx, child, in, e)
				}
				var accepted Intent
				if e = a.Store.Get(ctx, "intent", in.ID, &accepted); e != nil {
					return Intent{}, e
				}
				return accepted, errors.New("owned ACK lost after actual daemon and source Commit")
			})
			if boundary == "parent_cancelled" {
				if e == nil || adds.Load() != 0 {
					t.Fatal("cancelled parent dispatched child", e, adds.Load())
				}
				return
			}
			if boundary == "preflight" {
				var reservations int
				a.Store.DB.QueryRow(`SELECT count(*) FROM reservations WHERE intent_id=?`, in.ID).Scan(&reservations)
				if e == nil || pluginErrorCodeInternal(e) == "LARK_UNCONFIRMED" || reservations != 0 || adds.Load() != 0 {
					t.Fatal("known pre-Add failure retained reservation or poisoned source", e, reservations, adds.Load())
				}
				return
			}
			if boundary == "add_unconfirmed" {
				if pluginErrorCodeInternal(e) != "LARK_UNCONFIRMED" || adds.Load() != 1 {
					t.Fatal("unconfirmed daemon mutation became a fallback candidate", e, adds.Load())
				}
				core.ordinal = 1
				if _, e = a.larkSubmitDownload(ctx, core, in); pluginErrorCodeInternal(e) != "LARK_UNCONFIRMED" || adds.Load() != 1 {
					t.Fatal("unconfirmed Add replayed", e, adds.Load())
				}
				return
			}
			if e != nil || accepted.State != "accepted" || adds.Load() != 1 {
				t.Fatal("actual atomic acceptance was not read back", accepted.State, e, adds.Load())
			}
			core.release()
			cold, e := NewPluginStore(context.Background(), a.Store)
			if e != nil {
				t.Fatal(e)
			}
			a.pluginStore = cold
			next := &pluginLarkCoreExecution{a: a, job: core.job}
			if _, e = a.larkInboxForJob(context.Background(), next.job, core.inbox.ID, next); e != nil {
				t.Fatal(e)
			}
			defer next.release()
			next.ordinal = 1
			ctx = context.WithValue(context.Background(), pluginLarkCoreKey{}, next)
			replay, e := a.larkSubmitDownload(ctx, next, in)
			if e != nil || replay.ID != accepted.ID || replay.State != "accepted" || adds.Load() != 1 {
				t.Fatal("cold source result replay changed original daemon effect", replay.ID, e, adds.Load())
			}
			var state string
			if e = a.Store.DB.QueryRow(`SELECT state FROM jobs WHERE id=?`, j.ID).Scan(&state); e != nil || state != "succeeded" {
				t.Fatal("child completion not atomic with accepted source result", state, e)
			}
		})
	}
}
