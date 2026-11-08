package app

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"light-media/internal/priorityrules"
)

// A higher priority download proves progress. It does not grant permission to
// remove every older file in the inventory slot; the original directory policy
// decides each real target independently.
func TestSubscriptionQualityActualDirectoryOverwriteAndCoexistence(t *testing.T) {
	for _, branch := range []string{"same_always", "same_latest", "same_never", "same_size_larger", "same_size_equal", "same_unknown", "same_none", "different_always", "different_never", "different_size", "different_latest", "different_unknown", "different_none", "same_never_metadata_tamper", "different_always_metadata_removed", "different_always_method_tamper"} {
		t.Run(branch, func(t *testing.T) {
			ctx := context.Background()
			a, source, _ := importTestApp(t)
			m := importFixture(t, a, "movie")
			rename := strings.HasPrefix(branch, "same_")
			known := !strings.HasSuffix(branch, "unknown")
			var mode *string
			for _, value := range []string{"always", "never", "size", "latest"} {
				if strings.Contains(branch, value) {
					v := value
					mode = &v
				}
			}
			oldSize := 1024
			if branch == "same_size_larger" {
				oldSize = 512
			}
			newName := "测试影片 (2024) 2160p.mkv"
			oldName := "测试影片 (2024) 1080p.mkv"
			if rename {
				oldName = "测试影片 (2024) 2160p.WEBRip.mkv"
			}
			oldSource := filepath.Join(source, "old", oldName)
			newSource := filepath.Join(source, newName)
			oldBytes, newBytes := bytes.Repeat([]byte{'o'}, oldSize), bytes.Repeat([]byte{'n'}, 1024)
			importWrite(t, oldSource, oldBytes)
			plan, e := a.previewImport(ctx, ImportRequest{MediaType: "movie", TMDBID: m.ID, RootID: "root", Mode: "hardlink", Renaming: &rename, Files: []ImportFileRequest{{Path: oldSource}}})
			if e != nil {
				t.Fatal(e)
			}
			j, e := a.executeImport(ctx, plan.ID, nil)
			if e != nil {
				t.Fatal(e)
			}
			reorgRunJob(t, a, j)
			var old Asset
			if e = a.assetAtPath(ctx, plan.Files[0].Target, &old); e != nil {
				t.Fatal(e)
			}
			reorgRunJob(t, a, reorgJob(t, a, "scrape", old.ID))
			oldNFO := strings.TrimSuffix(old.Path, filepath.Ext(old.Path)) + ".nfo"
			torrent, hash := workflowTorrent(newName)
			var adds atomic.Int64
			var complete atomic.Bool
			qb := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/v2/torrents/info":
					if adds.Load() == 0 {
						workflowJSON(w, []any{})
						return
					}
					progress := 0.0
					if complete.Load() {
						progress = 1
					}
					row := workflowTask(hash, progress)
					row["save_path"] = source
					workflowJSON(w, []any{row})
				case "/api/v2/torrents/add":
					if e := r.ParseMultipartForm(4 << 20); e != nil || r.FormValue("savepath") != source || r.FormValue("stopped") != "false" {
						t.Error("actual Add scope", e, r.Form)
					}
					adds.Add(1)
					io.WriteString(w, "Ok.")
				case "/api/v2/torrents/files":
					workflowJSON(w, []any{map[string]any{"index": 0, "name": newName, "size": 1024, "progress": 1, "priority": 1}})
				default:
					t.Error("ungranted downloader request", r.URL.Path)
					w.WriteHeader(400)
				}
			}))
			defer qb.Close()
			site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/torrents.php":
					io.WriteString(w, workflowHTML("测试影片.2024.2160p.WEB-DL.x264-TEST", "", false))
				case "/download.php":
					w.Write(torrent)
				default:
					http.NotFound(w, r)
				}
			}))
			defer site.Close()
			workflowConfigure(t, a, site.URL, qb.URL)
			a.config.Downloaders[0].DownloadPath = source
			a.config.Downloaders[0].SharedPaths = true
			a.config.Directories = []DirectoryRule{{ID: "quality-policy", Enabled: true, MonitorType: "monitor", SourcePath: source, RootID: "root", MediaType: "movie", Mode: "hardlink", Renaming: &rename, OverwriteMode: mode, OverwriteModeKnown: known}}
			a.config.PriorityRules = &priorityrules.Configuration{SubscribeGroups: []string{"quality"}, Groups: []priorityrules.Group{{Name: "quality", RuleString: priorityText("WEBDL & 4K")}}, Mode: priorityrules.Rust}
			yes := true
			s, e := a.saveSubscription(ctx, "", SubscriptionRequest{MediaType: "movie", TMDBID: m.ID, DownloaderID: "fixture-qb", DownloadPath: source, DirectoryID: "quality-policy", BestVersion: &yes, Schedule: Schedule{Mode: "manual"}})
			if e != nil {
				t.Fatal(e)
			}
			if e = a.checkSubscription(ctx, importClaim(t, a, "subscription", s.ID), s.ID, s.Revision); e != nil {
				t.Fatal(e)
			}
			raw, e := a.Store.List(ctx, "intent", 10, 0)
			if e != nil || len(raw) != 1 {
				t.Fatal(e, len(raw))
			}
			var in Intent
			if e = json.Unmarshal(raw[0], &in); e != nil || in.State != "accepted" || adds.Load() != 1 {
				t.Fatal("not an actual accepted download", in.State, e, adds.Load())
			}
			importWrite(t, newSource, newBytes)
			complete.Store(true)
			if e = a.pollDownloads(ctx); e != nil {
				t.Fatal(e)
			}
			if e = a.Store.Get(ctx, "intent", in.ID, &in); e != nil || in.ImportPlanID == "" {
				t.Fatal("actual accepted organize missing", in.Error, e)
			}
			if e = a.Store.Get(ctx, "import_plan", in.ImportPlanID, &plan); e != nil {
				t.Fatal(e)
			}
			metadataTamper := strings.HasSuffix(branch, "metadata_tamper") || strings.HasSuffix(branch, "metadata_removed") || strings.HasSuffix(branch, "method_tamper")
			if metadataTamper {
				var meta importMetadata
				if e = a.Store.Get(ctx, "import_meta", plan.ID, &meta); e != nil {
					t.Fatal(e)
				}
				switch {
				case strings.HasSuffix(branch, "metadata_removed"):
					meta.Overwrite = nil
				case strings.HasSuffix(branch, "method_tamper"):
					meta.Methods[plan.Files[0].ID] = "copy"
				default:
					v := "always"
					meta.Overwrite = &importOverwritePolicy{Policy: meta.Overwrite.Policy}
					meta.Overwrite.Policy.Mode = &v
				}
				if e = a.Store.Put(ctx, "import_meta", plan.ID, meta); e != nil {
					t.Fatal(e)
				}
			}
			allowed := !metadataTamper && ((!rename && branch != "different_unknown") || branch == "same_always" || branch == "same_latest" || branch == "same_size_larger")
			importJob := qualityRunningJob(t, a, reorgJob(t, a, "import", plan.ID))
			e = a.runImport(ctx, importJob, plan.ID, nil)
			if !allowed {
				if metadataTamper && pluginErrorCodeInternal(e) != "QUALITY_IMPORT_CONTEXT_CHANGED" {
					t.Fatal("plaintext metadata became overwrite authority", e)
				}
				if e == nil {
					t.Fatal("higher priority bypassed original directory policy", branch)
				}
				if got, er := os.ReadFile(old.Path); er != nil || !bytes.Equal(got, oldBytes) {
					t.Fatal("denied policy changed old media", er)
				}
				var still Subscription
				if er := a.Store.Get(ctx, "subscription", s.ID, &still); er != nil || still.CurrentPriority == nil || *still.CurrentPriority != 100 {
					t.Fatal("organize denial discarded actual accepted priority", still.CurrentPriority, er)
				}
				return
			}
			if e != nil {
				t.Fatal("original directory policy failed", branch, e)
			}
			if e = a.Store.Get(ctx, "import_plan", plan.ID, &plan); e != nil {
				t.Fatal(e)
			}
			var replacement Asset
			if e = a.assetAtPath(ctx, plan.Files[0].Target, &replacement); e != nil || replacement.ID == old.ID || replacement.IntentID != in.ID {
				t.Fatal("new asset did not represent actual publication", e)
			}
			if got, er := os.ReadFile(replacement.Path); er != nil || !bytes.Equal(got, newBytes) {
				t.Fatal("new video publication", er)
			}
			if branch == "same_always" {
				// This is a real published plan. Removing its sealed host operation
				// in the registration transaction must not invoke the legacy no-op.
				tx, er := a.Store.DB.BeginTx(ctx, nil)
				if er != nil {
					t.Fatal(er)
				}
				proof, er := a.qualityPlanProof(ctx, tx, plan)
				if er != nil || proof == nil || proof.Version != 3 {
					_ = tx.Rollback()
					t.Fatal("actual new quality proof missing", er)
				}
				_, er = tx.ExecContext(ctx, `DELETE FROM records WHERE kind='directory_overwrite' AND id=?`, plan.ID+"/"+plan.Files[0].ID)
				if er == nil {
					er = a.requireQualityDirectoryOverwriteTx(ctx, tx, plan, plan.Files[0], proof)
				}
				_ = tx.Rollback()
				if pluginErrorCodeInternal(er) != "REVIEW_OVERWRITE_RECEIPT" {
					t.Fatal("new proof accepted missing actual host operation", er)
				}
				qualityRequireDirectoryOverwriteComplete(t, a, plan.ID+"/"+plan.Files[0].ID)
			}
			var previous Asset
			if e = a.Store.Get(ctx, "asset", old.ID, &previous); e != nil {
				t.Fatal(e)
			}
			coexist := !rename && branch != "different_latest"
			if coexist {
				if previous.State != "present" {
					t.Fatal("slot priority falsely superseded differently named version", previous.State)
				}
				if got, er := os.ReadFile(old.Path); er != nil || !bytes.Equal(got, oldBytes) {
					t.Fatal("coexisting version removed", er)
				}
			} else {
				if previous.State != "superseded" {
					t.Fatal("actual overwritten owner not superseded", previous.State)
				}
				if !rename {
					if _, er := os.Stat(old.Path); !os.IsNotExist(er) {
						t.Fatal("latest retained matched old video", er)
					}
				}
			}
			if !rename {
				if _, er := os.Stat(oldNFO); er != nil {
					t.Fatal("different-name overwrite removed old NFO", er)
				}
			}
			reorgRunJob(t, a, reorgJob(t, a, "scrape", replacement.ID))
			if e = a.Store.Get(ctx, "asset", replacement.ID, &replacement); e != nil || replacement.ScrapeState != "scraped" {
				t.Fatal("real replacement scrape", replacement.ScrapeState, e)
			}
			for path, want := range map[string][]byte{oldSource: oldBytes, newSource: newBytes} {
				if got, er := os.ReadFile(path); er != nil || !bytes.Equal(got, want) {
					t.Fatal("seeding source changed", path, er)
				}
			}
			var cleanups int
			a.Store.DB.QueryRow(`SELECT COUNT(*) FROM records WHERE kind='subscription_quality_cleanup'`).Scan(&cleanups)
			if cleanups != 0 {
				t.Fatal("old slot cleanup bypasses per-file source policy", cleanups)
			}
		})
	}
}

func qualityRequireDirectoryOverwriteComplete(t *testing.T, a *App, key string) {
	t.Helper()
	op, e := a.readDirectoryOverwriteOperation(context.Background(), key)
	if e != nil || op.State != "complete" || op.Published.State != "published" {
		t.Fatal("actual directory publication/cleanup incomplete", key, op.State, e)
	}
	for _, old := range op.Targets {
		if _, e := os.Lstat(filepath.Join(filepath.Dir(old.Path), old.Backup)); !os.IsNotExist(e) {
			t.Fatal("authenticated old-file backup not cleaned", e)
		}
	}
}
