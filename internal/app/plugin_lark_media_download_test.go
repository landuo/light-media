package app

import (
	"encoding/json"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"

	"light-media/internal/sites"
)

func TestPluginLarkActualResourceDirectoryConfirmationAndDownloader(t *testing.T) {
	for _, branch := range []string{"one_bypasses_prompt", "selected_root", "custom_subdirectory", "automatic_no_rule", "menu_root_removed", "force_start_ack", "force_start_unknown"} {
		t.Run(branch, func(t *testing.T) {
			a, source, _ := importTestApp(t)
			m := importFixture(t, a, "movie")
			torrent, hash := workflowTorrent("测试影片.2024.1080p.mkv")
			var adds, preferences, forces atomic.Int64
			wantPath := filepath.Join(source, "电影", "华语电影")
			if branch == "custom_subdirectory" {
				wantPath = filepath.Join(source, "custom")
			}
			qb := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/v2/torrents/info":
					if adds.Load() == 0 {
						workflowJSON(w, []any{})
					} else {
						task := workflowTask(hash, 0)
						task["save_path"] = wantPath
						workflowJSON(w, []any{task})
					}
				case "/api/v2/torrents/add":
					if e := r.ParseMultipartForm(4 << 20); e != nil {
						t.Error(e)
					}
					if got := r.FormValue("savepath"); got != wantPath {
						t.Error("actual daemon path differs", got, wantPath)
					}
					adds.Add(1)
					io.WriteString(w, "Ok.")
				case "/api/v2/app/preferences":
					preferences.Add(1)
					t.Error("source directory selection fell back to downloader preferences")
					w.WriteHeader(500)
				case "/api/v2/torrents/setForceStart":
					if e := r.ParseForm(); e != nil || r.Form.Get("hashes") != hash || r.Form.Get("value") != "true" {
						t.Error("Lark force-start was not bound to its actual Add", e, r.Form)
					}
					forces.Add(1)
					if branch == "force_start_unknown" {
						connection, _, e := w.(http.Hijacker).Hijack()
						if e != nil {
							t.Error(e)
							return
						}
						connection.Close()
					}
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
			a.config.Downloaders[0].ForceResume = branch == "force_start_ack" || branch == "force_start_unknown"
			a.config.Directories = []DirectoryRule{{ID: "owned-dir", Name: "Owned", SourcePath: source, MediaType: "mixed", Mode: "hardlink", DownloadTypeFolder: true, DownloadCategoryFolder: true}}
			installUnregisteredLarkConfig(t, a, map[string]any{"enabled": true})
			core, ctx := larkStartedCoreFixture(t, a)
			candidate := Candidate{Resource: sites.Resource{ID: "101", SiteID: "fixture-site"}, Accepted: true}
			s := &pluginLarkMediaSession{Request: "owned-request", User: "ou_owned", Title: m.Title, Phase: "torrent", Selected: &m, Candidates: []Candidate{candidate}, Targets: []SearchRequest{{MediaType: "movie", TMDBID: m.ID}}}
			if branch == "automatic_no_rule" {
				a.config.Directories = nil
			}
			var e error
			if branch == "selected_root" || branch == "menu_root_removed" {
				facts, err := a.larkDownloadDirectoryFacts(ctx, core, m)
				if err != nil {
					t.Fatal(err)
				}
				s.Phase, s.PendingMode, s.PendingPage = "download-dir", "single", 2
				s.PendingCandidate, s.PendingDirectoryFacts, s.Directories = &candidate, &facts, facts.Choices
				if branch == "menu_root_removed" {
					a.config.Directories = nil
				}
				e = a.larkSelectDownloadDirectory(ctx, core, s, big.NewInt(1), "ou_owned", "")
				if s.Phase != "torrent" || s.Page != 2 || s.PendingMode != "" || s.PendingCandidate != nil || s.PendingDirectoryFacts != nil || len(s.Directories) != 0 {
					t.Fatal("source torrent phase was not restored before execution")
				}
			} else if branch == "custom_subdirectory" {
				facts, err := a.larkDownloadDirectoryFacts(ctx, core, m)
				if err != nil {
					t.Fatal(err)
				}
				_, e = a.larkDownloadCandidate(ctx, core, s, candidate, facts, &wantPath)
			} else {
				e = a.larkSelectTorrent(ctx, core, s, big.NewInt(1), "ou_owned", "", "")
			}
			if branch == "automatic_no_rule" || branch == "menu_root_removed" {
				code := "DOWNLOAD_DIRECTORY_NOT_FOUND"
				if branch == "menu_root_removed" {
					code = "DOWNLOAD_DIRECTORY_NOT_ALLOWED"
				}
				if pluginErrorCodeInternal(e) != code || adds.Load() != 0 || preferences.Load() != 0 {
					t.Fatal("source no-directory failure changed", e, adds.Load(), preferences.Load())
				}
				return
			}
			if e != nil {
				t.Fatal(e)
			}
			rows, e := a.Store.List(ctx, "intent", 10, 0)
			if e != nil || len(rows) != 1 {
				t.Fatal("actual confirmation did not create exactly one intent", len(rows), e)
			}
			var in Intent
			if e = json.Unmarshal(rows[0], &in); e != nil || in.DownloadPath != wantPath || adds.Load() != 1 || in.State != "accepted" {
				t.Fatal("source-selected resource did not wait for actual daemon acceptance", in.DownloadPath, adds.Load(), e)
			}
			j, e := scanJob(a.Store.DB.QueryRowContext(ctx, `SELECT `+jobColumns+` FROM jobs WHERE kind='lark_download' AND json_extract(payload,'$.id')=?`, in.ID))
			if e != nil || j.State != "succeeded" || j.ParentJobID != core.job.ID {
				t.Fatal("actual private child was not durably completed", j.State, j.ParentJobID, e)
			}
			if a.config.Downloaders[0].ForceResume {
				post, e := a.sourcePostAddReceipt(ctx, a.Store.DB, in)
				if e != nil || !post.SourceReturned || len(post.Effects) != 1 || post.Effects[0].OriginalReturn != "none" || post.Effects[0].Unknown != (branch == "force_start_unknown") || forces.Load() != 1 {
					t.Fatal("private Lark completion lost actual forceStart evidence", post, forces.Load(), e)
				}
			}
			if e = a.runDownload(ctx, j, in.ID); pluginErrorCodeInternal(e) != "LARK_CORE_SCOPE_DENIED" {
				t.Fatal("terminal private child executed outside its source step", e)
			}
			if e = a.Store.Get(ctx, "intent", in.ID, &in); e != nil || in.State != "accepted" || adds.Load() != 1 || preferences.Load() != 0 {
				t.Fatal("daemon acceptance/retry differs", in.State, adds.Load(), preferences.Load(), e)
			}
		})
	}
}
