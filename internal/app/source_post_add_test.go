package app

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestSourcePostAddActualWorkerForceUnknownAndAtomicColdRecovery(t *testing.T) {
	for _, boundary := range []string{"force_ack", "force_unknown", "existing_hash", "add_duplicate", "accept_commit_failed", "receipt_deleted", "receipt_commit_failed", "add_unknown"} {
		t.Run(boundary, func(t *testing.T) {
			ctx := context.Background()
			a, source, _ := importTestApp(t)
			m := importFixture(t, a, "movie")
			torrent, hash := workflowTorrent("测试影片 (2024) 1080p.mkv")
			var adds, forces, lists atomic.Int64
			qb := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/v2/torrents/info":
					n := lists.Add(1)
					if adds.Load() == 0 && boundary != "existing_hash" && !(boundary == "add_duplicate" && n > 1) {
						workflowJSON(w, []any{})
					} else {
						task := workflowTask(hash, 0)
						task["save_path"] = source
						workflowJSON(w, []any{task})
					}
				case "/api/v2/torrents/add":
					if e := r.ParseMultipartForm(4 << 20); e != nil || r.FormValue("stopped") != "false" || r.FormValue("savepath") != source {
						t.Error("unselected source Add changed", e, r.Form)
					}
					adds.Add(1)
					if boundary == "add_unknown" {
						connection, _, e := w.(http.Hijacker).Hijack()
						if e != nil {
							t.Error(e)
							return
						}
						connection.Close()
						return
					}
					io.WriteString(w, "Ok.")
				case "/api/v2/torrents/setForceStart":
					if e := r.ParseForm(); e != nil || r.Form.Get("value") != "true" || r.Form.Get("hashes") != hash {
						t.Error("forceStart was not bound to the actual accepted hash", e, r.Form)
					}
					forces.Add(1)
					if boundary == "force_unknown" {
						connection, _, e := w.(http.Hijacker).Hijack()
						if e != nil {
							t.Error(e)
							return
						}
						connection.Close()
					}
				default:
					t.Error("unexpected downloader endpoint", r.URL.Path)
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
					t.Error("unexpected site endpoint", r.URL.Path)
					w.WriteHeader(400)
				}
			}))
			defer site.Close()
			workflowConfigure(t, a, site.URL, qb.URL)
			a.config.Downloaders[0].ForceResume = true
			a.config.Directories = []DirectoryRule{{ID: "owned-source", SourcePath: source, RootID: "root", MediaType: "movie", Mode: "hardlink"}}
			preview, e := a.previewDownload(ctx, PreviewRequest{SearchRequest: SearchRequest{MediaType: m.MediaType, TMDBID: m.ID}, SiteID: "fixture-site", ResourceID: "101", DownloadPath: source}, false)
			if e != nil {
				t.Fatal(e)
			}
			in, e := a.confirmDownload(ctx, preview.ID, true)
			if e != nil || in.SourcePostAddVersion != 1 {
				t.Fatal("new confirmation did not bind source post-Add receipt", in, e)
			}
			j, e := a.claim(ctx)
			if e != nil {
				t.Fatal(e)
			}
			trigger := ""
			if boundary == "accept_commit_failed" || boundary == "receipt_deleted" {
				trigger = "download_added"
			}
			if boundary == "receipt_commit_failed" {
				trigger = "download_post_add"
			}
			if trigger != "" {
				if _, e = a.Store.DB.Exec(`CREATE TRIGGER owned_post_add_failure BEFORE INSERT ON records WHEN NEW.kind='` + trigger + `' BEGIN SELECT RAISE(ABORT,'owned_post_add_boundary'); END`); e != nil {
					t.Fatal(e)
				}
			}
			e = a.runDownload(ctx, j, in.ID)
			if (trigger != "" || boundary == "add_unknown") != (e != nil) {
				t.Fatal("actual write/commit boundary was not exercised", boundary, e)
			}
			if e = a.Store.Get(ctx, "intent", in.ID, &in); e != nil {
				t.Fatal(e)
			}
			if boundary == "add_unknown" {
				if in.State != "reconcile_pending" || adds.Load() != 1 || forces.Load() != 0 {
					t.Fatal("unknown Add fabricated a successful post-Add", in.State, adds.Load(), forces.Load())
				}
			} else if boundary != "receipt_commit_failed" {
				r, e := a.sourcePostAddReceipt(ctx, a.Store.DB, in)
				if e != nil || !r.SourceReturned || r.SourceError != "" {
					t.Fatal("actual post-Add result missing", r, e)
				}
				if boundary == "existing_hash" || boundary == "add_duplicate" {
					if !r.Duplicate || len(r.Effects) != 0 || adds.Load() != 0 || forces.Load() != 0 {
						t.Fatal("duplicate executed source post-Add effects", r, adds.Load(), forces.Load())
					}
				} else if len(r.Effects) != 1 || r.Effects[0].Operation != "force_start" || r.Effects[0].OriginalReturn != "none" || r.Effects[0].Unknown != (boundary == "force_unknown") || r.Effects[0].Acknowledged == (boundary == "force_unknown") {
					t.Fatal("original None return hid the native forceStart receipt", r)
				}
			}
			if trigger != "" {
				if in.State != "post_add" || adds.Load() != 1 || forces.Load() != 1 {
					t.Fatal("failed local commit lost the external operation boundary", in.State, adds.Load(), forces.Load())
				}
				if _, e = a.Store.DB.Exec(`DROP TRIGGER owned_post_add_failure`); e != nil {
					t.Fatal(e)
				}
			}
			if boundary == "receipt_deleted" {
				if _, e = a.Store.DB.Exec(`DELETE FROM records WHERE kind='download_post_add' AND id=?`, in.ID); e != nil {
					t.Fatal(e)
				}
			}
			config, dir := a.Settings(), a.Dir
			a.Close()
			cold, e := New(Options{DataDir: dir})
			if e != nil {
				t.Fatal(e)
			}
			defer cold.Close()
			cold.config = config
			oldAdds, oldForces, oldLists := adds.Load(), forces.Load(), lists.Load()
			e = cold.runDownload(ctx, j, in.ID)
			uncertain := boundary == "receipt_deleted" || boundary == "receipt_commit_failed" || boundary == "add_unknown"
			if uncertain && pluginErrorCodeInternal(e) != "REVIEW_POST_ADD_UNCONFIRMED" || !uncertain && e != nil || adds.Load() != oldAdds || forces.Load() != oldForces || lists.Load() != oldLists {
				t.Fatal("cold recovery repeated native operations or trusted missing evidence", boundary, e, adds.Load(), forces.Load(), lists.Load())
			}
			if !uncertain {
				if e = cold.Store.Get(ctx, "intent", in.ID, &in); e != nil || in.State != "accepted" {
					t.Fatal("complete source receipt did not finish actual acceptance", in.State, e)
				}
				var n int
				if e = cold.Store.DB.QueryRow(`SELECT COUNT(*) FROM records WHERE kind='download_added'`).Scan(&n); e != nil || n != 1 {
					t.Fatal("cold recovery duplicated or lost the accepted event", n, e)
				}
			}
		})
	}
}
