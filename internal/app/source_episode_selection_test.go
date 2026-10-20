package app

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"light-media/internal/catalog"
	"light-media/internal/metainfo"
)

func TestSourceEpisodeSelectionThroughActualIntentWorkerAndColdReceipts(t *testing.T) {
	for _, boundary := range []string{"accepted_and_deleted_receipt", "cancelled_after_add", "force_start"} {
		t.Run(boundary, func(t *testing.T) {
			a, source, _ := importTestApp(t)
			m := importFixture(t, a, "tv")
			episodes := []catalog.Episode{}
			for ep := 1; ep <= 3; ep++ {
				episodes = append(episodes, catalog.Episode{EpisodeNumber: ep, SeasonNumber: 1, AirDate: "2024-01-01"})
			}
			importCache(t, a, "season:123:1", catalog.Season{SeriesID: 123, SeasonNumber: 1, Episodes: episodes})
			torrent := automaticPackTorrent([]string{"测试剧集 S01E01 1080p.mkv", "测试剧集 S01E02 1080p.mkv", "测试剧集 S01E03 1080p.mkv"})
			material, e := metainfo.ParseMaterial(torrent)
			if e != nil {
				t.Fatal(e)
			}
			var adds, reads, sets, starts atomic.Int64
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			qb := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/v2/torrents/info":
					if adds.Load() == 0 {
						workflowJSON(w, []any{})
					} else {
						task := workflowTask(material.Hash, 0)
						task["save_path"] = source
						workflowJSON(w, []any{task})
					}
				case "/api/v2/torrents/add":
					if e := r.ParseMultipartForm(4 << 20); e != nil {
						t.Error(e)
					}
					if r.FormValue("stopped") != "true" || r.FormValue("savepath") != source {
						t.Error("actual selected-file Add did not use paused selected location")
					}
					adds.Add(1)
					io.WriteString(w, "Ok.")
				case "/api/v2/torrents/files":
					reads.Add(1)
					if boundary == "cancelled_after_add" {
						cancel()
						return
					}
					io.WriteString(w, `[{"index":7,"name":"测试剧集 S01E01 1080p.mkv"},{"index":3,"name":"测试剧集 S01E02 1080p.mkv"},{"index":12,"name":"测试剧集 S01E03 1080p.mkv"},{"index":99,"name":"readme.txt"}]`)
				case "/api/v2/torrents/filePrio":
					r.ParseForm()
					if r.Form.Get("id") != "7|12|99" || r.Form.Get("priority") != "0" || r.Form.Get("hash") != material.Hash {
						t.Error("actual native MetaInfo selected wrong daemon files", r.Form)
					}
					sets.Add(1)
				case "/api/v2/torrents/start":
					if boundary == "force_start" {
						t.Error("force_resume used ordinary Start")
					}
					starts.Add(1)
				case "/api/v2/torrents/setForceStart":
					if e := r.ParseForm(); e != nil || boundary != "force_start" || r.Form.Get("hashes") != material.Hash || r.Form.Get("value") != "true" {
						t.Error("source forceStart identity changed", r.Form, e)
					}
					starts.Add(1)
				default:
					t.Error("ungranted daemon endpoint", r.URL.Path)
					w.WriteHeader(400)
				}
			}))
			defer qb.Close()
			site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/torrents.php":
					io.WriteString(w, workflowHTML("测试剧集 S01E01-E03 1080p", "", false))
				case "/download.php":
					w.Write(torrent)
				default:
					http.NotFound(w, r)
				}
			}))
			defer site.Close()
			workflowConfigure(t, a, site.URL, qb.URL)
			a.config.Downloaders[0].ForceResume = boundary == "force_start"
			a.config.Directories = []DirectoryRule{{ID: "owned", SourcePath: source, MediaType: "tv", Mode: "hardlink", RootID: "root"}}
			preview, e := a.previewDownload(ctx, PreviewRequest{SearchRequest: SearchRequest{MediaType: m.MediaType, TMDBID: m.ID, Season: 1, Episodes: []int{2}}, selectedEpisodes: []int{2}, SiteID: "fixture-site", ResourceID: "101", DownloadPath: source}, false)
			if e != nil || len(preview.SelectedEpisodes) != 1 || preview.SelectedEpisodes[0] != 2 {
				t.Fatal("actual selected-file preview failed", preview.SelectedEpisodes, e)
			}
			in, e := a.confirmDownload(ctx, preview.ID, true)
			if e != nil || len(in.SelectedEpisodes) != 1 || in.SelectedEpisodes[0] != 2 {
				t.Fatal("actual selected-file confirmation failed", in.SelectedEpisodes, e)
			}
			j, e := scanJob(a.Store.DB.QueryRowContext(ctx, `SELECT `+jobColumns+` FROM jobs WHERE kind='download' AND json_extract(payload,'$.id')=?`, in.ID))
			if e != nil {
				t.Fatal(e)
			}
			if _, e = a.Store.DB.ExecContext(ctx, `UPDATE jobs SET state='running',generation=1 WHERE id=?`, j.ID); e != nil {
				t.Fatal(e)
			}
			j.State, j.Generation = "running", 1
			e = a.runDownload(ctx, j, in.ID)
			if boundary == "cancelled_after_add" {
				if e == nil || adds.Load() != 1 || reads.Load() != 1 || starts.Load() != 0 {
					t.Fatal("source post-Add cancellation incorrectly accepted or lost original Add", e, adds.Load(), reads.Load(), starts.Load())
				}
				// The original wrapper catches the cancelled file read and returns
				// its already-accepted hash. Its sealed normal return can finish
				// the local commit without repeating Files or claiming Start.
				if e = a.runDownload(context.Background(), j, in.ID); e != nil || adds.Load() != 1 || reads.Load() != 1 || starts.Load() != 0 {
					t.Fatal("sealed original return did not resume without native writes", e, adds.Load(), reads.Load())
				}
				if e = a.Store.Get(context.Background(), "intent", in.ID, &in); e != nil {
					t.Fatal(e)
				}
				post, e := a.sourcePostAddReceipt(context.Background(), a.Store.DB, in)
				if e != nil || !post.SourceReturned || !post.Selection.MetadataMissing || post.Selection.Started || len(post.Effects) != 1 || post.Effects[0].Acknowledged {
					t.Fatal("cancelled read was promoted to a file or Start ACK", post, e)
				}
				return
			}
			if e != nil || adds.Load() != 1 || reads.Load() != 1 || sets.Load() != 1 || starts.Load() != 1 {
				t.Fatal("actual native selected-file worker failed", e, adds.Load(), reads.Load(), sets.Load(), starts.Load())
			}
			var sealed []byte
			if e = a.Store.DB.QueryRowContext(ctx, `SELECT payload FROM records WHERE kind='download_episode_selection' AND id=?`, in.ID).Scan(&sealed); e != nil {
				t.Fatal(e)
			}
			raw, e := a.pluginStore.unseal("download_episode_selection", "system", in.ID, sealed)
			var receipt sourceEpisodeSelectionReceipt
			if e != nil || json.Unmarshal(raw, &receipt) != nil || !receipt.Started || receipt.MetadataMissing || len(receipt.Effects) != 3 {
				t.Fatal("real file-selection outcome not saved", receipt, e)
			}
			cold, e := NewPluginStore(context.Background(), a.Store)
			if e != nil {
				t.Fatal(e)
			}
			a.pluginStore = cold
			if e = a.runDownload(context.Background(), j, in.ID); e != nil || adds.Load() != 1 || sets.Load() != 1 || starts.Load() != 1 {
				t.Fatal("cold accepted workflow repeated native writes", e)
			}
			if _, e = a.Store.DB.Exec(`DELETE FROM records WHERE kind='download_episode_selection' AND id=?`, in.ID); e != nil {
				t.Fatal(e)
			}
			if e = a.runDownload(context.Background(), j, in.ID); pluginErrorCodeInternal(e) != "REVIEW_FILE_SELECTION_UNCONFIRMED" || adds.Load() != 1 || sets.Load() != 1 {
				t.Fatal("deleted source selection proof was silently trusted", e)
			}
		})
	}
}
