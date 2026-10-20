package app

import (
	"bytes"
	"context"
	"encoding/json"
	"golang.org/x/sys/unix"
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

func TestSubscriptionQualityExistingMovieStillPlansHigherResource(t *testing.T) {
	a, source, _ := importTestApp(t)
	m := importFixture(t, a, "movie")
	ctx := context.Background()
	oldSource := filepath.Join(source, "测试影片 (2024) 1080p.mkv")
	importWrite(t, oldSource, bytes.Repeat([]byte{'l'}, 1024))
	oldPlan, err := a.previewImport(ctx, ImportRequest{MediaType: "movie", TMDBID: m.ID, RootID: "root", Mode: "hardlink", Files: []ImportFileRequest{{Path: oldSource}}})
	if err != nil {
		t.Fatal(err)
	}
	job, err := a.executeImport(ctx, oldPlan.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	reorgRunJob(t, a, job)
	torrent, hash := workflowTorrent("测试影片 (2024) 2160p.mkv")
	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/torrents.php":
			_, _ = io.WriteString(w, workflowHTML("测试影片.2024.2160p.WEB-DL.x264-TEST", "", false))
		case "/download.php":
			_, _ = w.Write(torrent)
		default:
			http.NotFound(w, r)
		}
	}))
	defer site.Close()
	var accepted atomic.Bool
	qb := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v2/torrents/add" {
			accepted.Store(true)
			io.WriteString(w, "Ok.")
			return
		}
		if r.URL.Path == "/api/v2/torrents/info" {
			if accepted.Load() {
				workflowJSON(w, []any{workflowTask(hash, 0)})
				return
			}
			workflowJSON(w, []any{})
			return
		}
		http.NotFound(w, r)
	}))
	defer qb.Close()
	workflowConfigure(t, a, site.URL, qb.URL)
	a.config.PriorityRules = &priorityrules.Configuration{SubscribeGroups: []string{"quality"}, Groups: []priorityrules.Group{{Name: "quality", RuleString: priorityText("WEBDL & 4K > WEBDL & 1080P")}}, Mode: priorityrules.Rust}
	var request SubscriptionRequest
	if err = json.Unmarshal([]byte(`{"media_type":"movie","tmdb_id":123,"downloader_id":"fixture-qb","root_id":"root","best_version":true,"schedule":{"mode":"manual"}}`), &request); err != nil {
		t.Fatal(err)
	}
	s, err := a.saveSubscription(ctx, "", request)
	if err != nil {
		t.Fatal(err)
	}
	check := importClaim(t, a, "subscription", s.ID)
	if err = a.checkSubscription(ctx, check, s.ID, s.Revision); err != nil {
		t.Fatal(err)
	}
	var intents int
	if err = a.Store.DB.QueryRow(`SELECT COUNT(*) FROM records WHERE kind='intent' AND json_extract(payload,'$.subscription_id')=?`, s.ID).Scan(&intents); err != nil {
		t.Fatal(err)
	}
	var current Subscription
	if err = a.Store.Get(ctx, "subscription", s.ID, &current); err != nil {
		t.Fatal(err)
	}
	if intents != 1 || current.State != "active" {
		t.Fatalf("existing library incorrectly ends quality search: intents=%d state=%s", intents, current.State)
	}
}

func TestSubscriptionQualityActualMovieAcceptedReplacedScrapedAndSourceRetained(t *testing.T) {
	for _, boundary := range []string{"normal", "swap_commit_cold", "accept_commit", "duplicate", "rejected", "revision_after_add_import", "removed_after_add_import"} {
		t.Run(boundary, func(t *testing.T) { qualityActualMovieWorkflow(t, boundary) })
	}
}
func TestSubscriptionQualityActualMovieSubtitlesReplaceWithVideo(t *testing.T) {
	for _, boundary := range []string{"subtitle_normal", "subtitle_swap_cold", "subtitle_plan_tamper", "subtitle_old_changed", "subtitle_source_changed", "subtitle_unmanaged", "subtitle_cleanup_changed"} {
		t.Run(boundary, func(t *testing.T) { qualityActualMovieWorkflow(t, boundary) })
	}
}
func qualityActualMovieWorkflow(t *testing.T, boundary string, completion ...func(*App, Subscription)) {
	a, source, _ := importTestApp(t)
	m := importFixture(t, a, "movie")
	ctx := context.Background()
	if boundary == "custom_audio" {
		ownedExtensionConfig(a, []string{".mkv"}, []string{".srt"}, []string{".audio"})
	}
	var s Subscription
	oldSource := filepath.Join(source, "测试影片 (2024) 1080p.mkv")
	newName := "测试影片 (2024) 2160p.mkv"
	newSource := filepath.Join(source, newName)
	oldBytes, newBytes := bytes.Repeat([]byte{'l'}, 1024), bytes.Repeat([]byte{'h'}, 1024)
	importWrite(t, oldSource, oldBytes)
	if boundary == "custom_audio" {
		importWrite(t, strings.TrimSuffix(oldSource, filepath.Ext(oldSource))+".audio", []byte("old actual custom audio"))
	}
	subtitles := strings.HasPrefix(boundary, "subtitle_")
	oldSubtitle := strings.TrimSuffix(oldSource, filepath.Ext(oldSource)) + ".zh.srt"
	newSubtitle := strings.TrimSuffix(newSource, filepath.Ext(newSource)) + ".zh.srt"
	oldSubtitleBytes, newSubtitleBytes := []byte("owned old subtitle"), []byte("owned new subtitle")
	if subtitles {
		importWrite(t, oldSubtitle, oldSubtitleBytes)
	}
	oldPlan, err := a.previewImport(ctx, ImportRequest{MediaType: "movie", TMDBID: m.ID, RootID: "root", Mode: "hardlink", Files: []ImportFileRequest{{Path: oldSource}}})
	if err != nil {
		t.Fatal(err)
	}
	job, err := a.executeImport(ctx, oldPlan.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	reorgRunJob(t, a, job)
	var oldAsset Asset
	if err = a.assetAtPath(ctx, oldPlan.Files[0].Target, &oldAsset); err != nil {
		t.Fatal(err)
	}
	reorgRunJob(t, a, reorgJob(t, a, "scrape", oldAsset.ID))
	oldVideo, err := snapshotPath(oldAsset.Path)
	if err != nil {
		t.Fatal(err)
	}
	torrent, hash := workflowTorrent(newName)
	var added, complete atomic.Bool
	var adds atomic.Int64
	added.Store(boundary == "duplicate")
	qb := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/torrents/info":
			if !added.Load() {
				workflowJSON(w, []any{})
				return
			}
			progress := 0.0
			if complete.Load() {
				progress = 1
			}
			task := workflowTask(hash, progress)
			task["save_path"] = source
			workflowJSON(w, []any{task})
		case "/api/v2/torrents/add":
			if err := r.ParseMultipartForm(4 << 20); err != nil || r.FormValue("savepath") != source || r.FormValue("stopped") != "false" {
				t.Error("actual movie Add input", err, r.Form)
			}
			adds.Add(1)
			if boundary == "rejected" {
				io.WriteString(w, "Fails.")
				return
			}
			if boundary == "revision_after_add_import" {
				v := true
				if _, ce := a.saveSubscription(ctx, s.ID, SubscriptionRequest{MediaType: "movie", TMDBID: m.ID, DownloaderID: "fixture-qb", DownloadPath: source, DirectoryID: "quality-watch", BestVersion: &v, Revision: s.Revision, Policy: Policy{Include: "NEW-RULE-ONLY"}, Schedule: Schedule{Mode: "manual"}}); ce != nil {
					t.Error(ce)
				}
			}
			if boundary == "removed_after_add_import" {
				if _, ce := a.Store.DB.Exec(`DELETE FROM records WHERE kind='subscription' AND id=?`, s.ID); ce != nil {
					t.Error(ce)
				}
			}
			added.Store(true)
			io.WriteString(w, "Ok.")
		case "/api/v2/torrents/files":
			workflowJSON(w, []any{map[string]any{"index": 0, "name": newName, "size": 1024, "progress": 1, "priority": 1}})
		default:
			t.Error("unexpected downloader endpoint", r.URL.Path)
			http.NotFound(w, r)
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
	overwriteMode := "always"
	a.config.Directories = []DirectoryRule{{ID: "quality-watch", Enabled: true, MonitorType: "monitor", SourcePath: source, RootID: "root", MediaType: "movie", Mode: "hardlink", OverwriteModeKnown: true, OverwriteMode: &overwriteMode}}
	if boundary == "auto_exdev" {
		a.config.Directories[0].Mode = "auto"
		a.importSourceLink = func(int, string, int, string, int) error { return unix.EXDEV }
	}
	a.config.PriorityRules = &priorityrules.Configuration{SubscribeGroups: []string{"quality"}, Groups: []priorityrules.Group{{Name: "quality", RuleString: priorityText("WEBDL & 4K > WEBDL & 1080P")}}, Mode: priorityrules.Rust}
	best := true
	s, err = a.saveSubscription(ctx, "", SubscriptionRequest{MediaType: "movie", TMDBID: m.ID, DownloaderID: "fixture-qb", DownloadPath: source, DirectoryID: "quality-watch", BestVersion: &best, Schedule: Schedule{Mode: "manual"}})
	if err != nil {
		t.Fatal(err)
	}
	checkJob := importClaim(t, a, "subscription", s.ID)
	if boundary == "accept_commit" {
		if _, err = a.Store.DB.Exec(`CREATE TRIGGER quality_accept_failure BEFORE INSERT ON records WHEN NEW.kind='subscription_quality_accepted' BEGIN SELECT RAISE(ABORT,'owned_quality_accept_boundary'); END`); err != nil {
			t.Fatal(err)
		}
	}
	err = a.checkSubscription(ctx, checkJob, s.ID, s.Revision)
	if boundary == "accept_commit" {
		if err == nil {
			t.Fatal("actual accepted TX fault was ignored")
		}
	} else if err != nil && boundary != "revision_after_add_import" && boundary != "removed_after_add_import" {
		t.Fatal(err)
	}
	raw, err := a.Store.List(ctx, "intent", 10, 0)
	if err != nil || len(raw) != 1 {
		t.Fatal("actual quality download", len(raw), err)
	}
	var intent Intent
	if err = json.Unmarshal(raw[0], &intent); err != nil {
		t.Fatal(err)
	}
	var current Subscription
	downloadJob := reorgJob(t, a, "subscription_download", intent.ID)
	if boundary == "accept_commit" || boundary == "rejected" {
		if err = a.Store.Get(ctx, "subscription", s.ID, &current); err != nil || current.CurrentPriority != nil || current.SourceNote != nil {
			t.Fatal("unaccepted transaction advanced quality", current, err)
		}
		if boundary == "rejected" {
			if adds.Load() != 1 || intent.State != "failed" {
				t.Fatal("rejected actual Add", adds.Load(), intent.State)
			}
			return
		}
		if _, err = a.Store.DB.Exec(`DROP TRIGGER quality_accept_failure`); err != nil {
			t.Fatal(err)
		}
		if err = a.checkSubscription(ctx, checkJob, s.ID, s.Revision); err != nil {
			t.Fatal("sealed post-Add SQL recovery", err)
		}
	}
	if boundary == "revision_after_add_import" {
		if err = a.Store.Get(ctx, "subscription", s.ID, &current); err != nil || current.Revision != s.Revision+1 || current.Policy.Include != "NEW-RULE-ONLY" || current.SourceNote != nil || current.CurrentPriority != nil {
			t.Fatal("old accepted receipt changed new subscription rules", current.Revision, current.SourceNote, err)
		}
	} else if boundary == "removed_after_add_import" {
		var count int
		if err = a.Store.DB.QueryRow(`SELECT COUNT(*) FROM records WHERE kind='subscription' AND id=?`, s.ID).Scan(&count); err != nil || count != 0 {
			t.Fatal("accepted receipt recreated removed subscription", count, err)
		}
	} else if err = a.Store.Get(ctx, "subscription", s.ID, &current); err != nil || current.CurrentPriority == nil || *current.CurrentPriority != 100 || !bytes.Equal(mustCleanSeedJSON(current.SourceNote), []byte(`[1]`)) {
		t.Fatal("actual Add did not record source priority/note", current, err)
	}
	expectedAdds := int64(1)
	if boundary == "duplicate" {
		expectedAdds = 0
	}
	if adds.Load() != expectedAdds {
		t.Fatal("actual downloader Add count", adds.Load())
	}
	if got, err := os.ReadFile(oldAsset.Path); err != nil || !bytes.Equal(got, oldBytes) {
		t.Fatal("old video changed before completed import", err)
	}
	importWrite(t, newSource, newBytes)
	if boundary == "custom_audio" {
		importWrite(t, strings.TrimSuffix(newSource, filepath.Ext(newSource))+".audio", []byte("new actual custom audio"))
	}
	if subtitles {
		importWrite(t, newSubtitle, newSubtitleBytes)
	}
	if boundary == "subtitle_unmanaged" {
		target := strings.TrimSuffix(oldAsset.Path, filepath.Ext(oldAsset.Path)) + ".zh.srt"
		var prior managedFile
		if err = a.Store.Get(ctx, "managed_file", tokenHash(target), &prior); err != nil {
			t.Fatal(err)
		}
		prior.AssetID = "foreign_asset"
		if err = a.Store.Put(ctx, "managed_file", tokenHash(target), prior); err != nil {
			t.Fatal(err)
		}
	}
	complete.Store(true)
	if err = a.pollDownloads(ctx); err != nil {
		t.Fatal(err)
	}
	if err = a.Store.Get(ctx, "intent", intent.ID, &intent); err != nil || intent.ImportPlanID == "" {
		t.Fatal("actual completed download did not plan replacement", intent.Error, err)
	}
	importJob := reorgJob(t, a, "import", intent.ImportPlanID)
	var plan ImportPlan
	if err = a.Store.Get(ctx, "import_plan", intent.ImportPlanID, &plan); err != nil {
		t.Fatal(err)
	}
	targetSubtitle := strings.TrimSuffix(oldAsset.Path, filepath.Ext(oldAsset.Path)) + ".zh.srt"
	if boundary == "subtitle_unmanaged" {
		importJob = qualityRunningJob(t, a, importJob)
		if err = a.runImport(ctx, importJob, plan.ID, nil); err == nil {
			t.Fatal("corrupt managed subtitle owner was accepted")
		}
		if got, er := os.ReadFile(oldAsset.Path); er != nil || !bytes.Equal(got, oldBytes) {
			t.Fatal("invalid sidecar owner changed video before preflight", er)
		}
		return
	}
	if boundary == "subtitle_plan_tamper" || boundary == "subtitle_old_changed" || boundary == "subtitle_source_changed" {
		if boundary == "subtitle_plan_tamper" {
			var meta importMetadata
			if err = a.Store.Get(ctx, "import_meta", plan.ID, &meta); err != nil {
				t.Fatal(err)
			}
			meta.Sidecars[plan.Files[0].ID][0].Source = oldSubtitle
			if err = a.Store.Put(ctx, "import_meta", plan.ID, meta); err != nil {
				t.Fatal(err)
			}
		} else if boundary == "subtitle_old_changed" {
			importWrite(t, targetSubtitle, []byte("externally replaced subtitle"))
		} else {
			importWrite(t, newSubtitle, []byte("externally replaced source subtitle"))
		}
		importJob = qualityRunningJob(t, a, importJob)
		if err = a.runImport(ctx, importJob, intent.ImportPlanID, nil); err == nil {
			t.Fatal("changed subtitle evidence allowed video replacement")
		}
		if got, e := os.ReadFile(oldAsset.Path); e != nil || !bytes.Equal(got, oldBytes) {
			t.Fatal("subtitle conflict changed original video", e)
		}
		return
	}
	if boundary == "swap_commit_cold" || boundary == "subtitle_swap_cold" || boundary == "subtitle_cleanup_changed" {
		if _, err = a.Store.DB.Exec(`CREATE TRIGGER quality_swap_failure BEFORE INSERT ON records WHEN NEW.kind='asset' AND json_extract(NEW.payload,'$.intent_id')!='' BEGIN SELECT RAISE(ABORT,'owned_quality_swap_boundary'); END`); err != nil {
			t.Fatal(err)
		}
		importJob = qualityRunningJob(t, a, importJob)
		if err = a.runImport(ctx, importJob, intent.ImportPlanID, nil); err == nil {
			t.Fatal("actual post-swap SQL commit did not fail")
		}
		if got, e := os.ReadFile(oldAsset.Path); e != nil || !bytes.Equal(got, newBytes) {
			t.Fatal("commit failure did not cross actual atomic swap", e)
		}
		if subtitles {
			if got, e := os.ReadFile(targetSubtitle); e != nil || !bytes.Equal(got, newSubtitleBytes) {
				t.Fatal("subtitle was not exchanged before actual SQL failure", e)
			}
		}
		if boundary == "subtitle_cleanup_changed" {
			importWrite(t, targetSubtitle, []byte("externally edited new subtitle"))
		}
		if _, err = a.Store.DB.Exec(`DROP TRIGGER quality_swap_failure`); err != nil {
			t.Fatal(err)
		}
		config, dir := a.Settings(), a.Dir
		a.Close()
		a, err = New(Options{DataDir: dir})
		if err != nil {
			t.Fatal(err)
		}
		defer a.Close()
		a.config = config
		// Retrying the real confirmation must accept only the persisted exchange
		// orientation, even when the prior worker never committed its asset.
		tx, e := a.Store.DB.BeginTx(ctx, nil)
		if e != nil {
			t.Fatal(e)
		}
		err = a.importReserve(ctx, tx, plan, plan.Files[0])
		_ = tx.Rollback()
		if err != nil && boundary != "subtitle_cleanup_changed" {
			t.Fatal("cold retry rejected authenticated published exchange", err)
		}
	}
	if boundary == "subtitle_cleanup_changed" {
		importJob = qualityRunningJob(t, a, importJob)
		if err = a.runImport(ctx, importJob, plan.ID, nil); err == nil {
			t.Fatal("changed new subtitle allowed cold cleanup")
		}
		for _, key := range []string{plan.ID + "/" + plan.Files[0].ID, qualitySidecarKey(plan, plan.Files[0], 0)} {
			op, er := a.readDirectoryOverwriteOperation(ctx, key)
			if er != nil || len(op.Targets) != 1 {
				t.Fatal("actual original backup evidence missing", er)
			}
			for _, old := range op.Targets {
				stamp, er := snapshotPath(filepath.Join(filepath.Dir(old.Path), old.Backup))
				if er != nil || stamp != old.Snapshot {
					t.Fatal("changed new subtitle lost original backup", er)
				}
			}
		}
		return
	}
	reorgRunJob(t, a, importJob)
	var replacement Asset
	if err = a.assetAtPath(ctx, oldAsset.Path, &replacement); err != nil || replacement.ID == oldAsset.ID || replacement.IntentID != intent.ID {
		t.Fatal("replacement asset did not take actual movie slot", replacement, err)
	}
	if got, err := os.ReadFile(replacement.Path); err != nil || !bytes.Equal(got, newBytes) {
		t.Fatal("replacement did not publish the new video", err)
	}
	stamp, err := snapshotPath(replacement.Path)
	if err != nil || stamp == oldVideo {
		t.Fatal("old in-place receipt was reused", stamp, err)
	}
	if err = a.Store.Get(ctx, "import_plan", intent.ImportPlanID, &plan); err != nil {
		t.Fatal(err)
	}
	cleanupID := plan.ID + "/" + plan.Files[0].ID
	if boundary == "auto_exdev" {
		p, e := a.qualityPlanProof(ctx, a.Store.DB, plan)
		if e != nil || p == nil || p.InputSHA != qualityImportSHA(plan) || plan.Files[0].Method != "copy" {
			t.Fatal("actual auto copy did not reseal quality identity", e)
		}
		sourceStamp, e := snapshotPath(newSource)
		if e != nil || sameInode(sourceStamp, stamp) {
			t.Fatal("quality actual copy reused source inode", e)
		}
	}
	qualityRequireDirectoryOverwriteComplete(t, a, cleanupID)
	reorgRunJob(t, a, reorgJob(t, a, "scrape", replacement.ID))
	if err = a.Store.Get(ctx, "asset", oldAsset.ID, &oldAsset); err != nil || oldAsset.State != "superseded" {
		t.Fatal("old quality asset state", oldAsset.State, err)
	}
	if err = a.Store.Get(ctx, "asset", replacement.ID, &replacement); err != nil || replacement.ScrapeState != "scraped" {
		t.Fatal("actual replacement scrape", replacement.ScrapeState, err)
	}
	for path, want := range map[string][]byte{oldSource: oldBytes, newSource: newBytes, replacement.Path: newBytes} {
		got, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(got, want) {
			t.Fatal("download/seeding source or replacement changed", path, err)
		}
	}
	if subtitles {
		for path, want := range map[string][]byte{oldSubtitle: oldSubtitleBytes, newSubtitle: newSubtitleBytes, targetSubtitle: newSubtitleBytes} {
			if got, e := os.ReadFile(path); e != nil || !bytes.Equal(got, want) {
				t.Fatal("subtitle or seeding source changed", path, e)
			}
		}
		var managed managedFile
		if err = a.Store.Get(ctx, "managed_file", tokenHash(targetSubtitle), &managed); err != nil || managed.AssetID != replacement.ID || managed.Kind != "subtitle" {
			t.Fatal("new subtitle did not take managed ownership", managed, err)
		}
		qualityRequireDirectoryOverwriteComplete(t, a, qualitySidecarKey(plan, plan.Files[0], 0))
	}
	if err = a.runDownload(ctx, downloadJob, intent.ID); err != nil || adds.Load() != expectedAdds {
		t.Fatal("accepted cold-safe replay repeated Add", adds.Load(), err)
	}
	if boundary == "revision_after_add_import" {
		var latest Subscription
		if err = a.Store.Get(ctx, "subscription", s.ID, &latest); err != nil || latest.Revision != s.Revision+1 || latest.Policy.Include != "NEW-RULE-ONLY" || latest.SourceNote != nil || latest.CurrentPriority != nil {
			t.Fatal("old import overwrote new subscription policy/progress", latest, err)
		}
	}
	if boundary == "removed_after_add_import" {
		var count int
		if err = a.Store.DB.QueryRow(`SELECT COUNT(*) FROM records WHERE kind='subscription' AND id=?`, s.ID).Scan(&count); err != nil || count != 0 {
			t.Fatal("old import resurrected deleted subscription", count, err)
		}
	}
	for _, callback := range completion {
		var latest Subscription
		if err = a.Store.Get(ctx, "subscription", s.ID, &latest); err != nil {
			t.Fatal(err)
		}
		callback(a, latest)
	}
}

func qualityRunningJob(t *testing.T, a *App, j Job) Job {
	t.Helper()
	if _, e := a.Store.DB.Exec(`UPDATE jobs SET state='running',generation=generation+1 WHERE id=?`, j.ID); e != nil {
		t.Fatal(e)
	}
	j, e := scanJob(a.Store.DB.QueryRow(`SELECT `+jobColumns+` FROM jobs WHERE id=?`, j.ID))
	if e != nil {
		t.Fatal(e)
	}
	return j
}
