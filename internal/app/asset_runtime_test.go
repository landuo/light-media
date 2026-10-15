package app

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"light-media/internal/catalog"
)

func TestAssetNFOUsesOwnEpisodeIDAndActorRoles(t *testing.T) {
	identity := catalog.Media{ID: 206484, MediaType: "tv", Title: "诛仙", Year: 2022, Overview: "剧集简介", ReleaseDate: "2022-08-02", Cast: []catalog.CastMember{{ID: 5, Name: "演员", Character: "小白", Order: 0}}, Genres: []catalog.Genre{{ID: 16, Name: "动画"}}}
	episode := catalog.Episode{ID: 7706553, SeasonNumber: 4, EpisodeNumber: 5, Name: "偶遇小白", Overview: "单集简介", AirDate: "2026-09-04"}
	raw, err := assetNFO(identity, &episode)
	if err != nil {
		t.Fatal(err)
	}
	var parsed assetLocalNFO
	if err = xml.Unmarshal(raw, &parsed); err != nil {
		t.Fatal(err)
	}
	if parsed.XMLName.Local != "episodedetails" || parsed.TMDBID != "7706553" || parsed.UniqueIDs[0].Value != "7706553" || *parsed.Season != 4 || *parsed.Episode != 5 || parsed.Aired != "2026-09-04" || parsed.Actors[0].Role != "小白" {
		t.Fatalf("wrong episode NFO: %s", raw)
	}
	show, err := assetNFO(identity, nil)
	if err != nil || !strings.Contains(string(show), "<tmdbid>206484</tmdbid>") {
		t.Fatalf("show identity lost: %s %v", show, err)
	}
	episode.AirDate = ""
	unknown, err := assetNFO(identity, &episode)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(unknown), "<year>") || strings.Contains(string(unknown), "<aired>") {
		t.Fatal("unknown episode date was inferred from series")
	}
}

func TestStandaloneScrapePersistsNFOAndCanRetry(t *testing.T) {
	a, source, _ := importTestApp(t)
	identity := importFixture(t, a, "movie")
	input := filepath.Join(source, "Test (2024).mkv")
	importWrite(t, input, []byte("video"))
	plan, err := a.previewImport(context.Background(), ImportRequest{MediaType: "movie", TMDBID: identity.ID, RootID: "root", Mode: "hardlink", Files: []ImportFileRequest{{Path: input}}})
	if err != nil {
		t.Fatal(err)
	}
	job, err := a.executeImport(context.Background(), plan.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	a.Store.DB.Exec(`UPDATE jobs SET state='running',generation=1 WHERE id=?`, job.ID)
	job.Generation = 1
	job.State = "running"
	if err = a.runImport(context.Background(), job, plan.ID, nil); err != nil {
		t.Fatal(err)
	}
	var imported ImportPlan
	if err = a.Store.Get(context.Background(), "import_plan", plan.ID, &imported); err != nil {
		t.Fatal(err)
	}
	var scrape Job
	scrape, err = scanJob(a.Store.DB.QueryRow(`SELECT ` + jobColumns + ` FROM jobs WHERE kind='scrape' LIMIT 1`))
	if err != nil {
		t.Fatal(err)
	}
	a.Store.DB.Exec(`UPDATE jobs SET state='running',generation=1 WHERE id=?`, scrape.ID)
	scrape.Generation = 1
	scrape.State = "running"
	if err = a.runScrape(context.Background(), scrape, imported.Files[0].AssetID); err != nil {
		t.Fatal(err)
	}
	if err = a.runScrape(context.Background(), scrape, imported.Files[0].AssetID); err != nil {
		t.Fatal("retry lost existing own metadata", err)
	}
	var asset Asset
	if err = a.Store.Get(context.Background(), "asset", imported.Files[0].AssetID, &asset); err != nil {
		t.Fatal(err)
	}
	if asset.ScrapeState != "scraped" || asset.FNOSState != "disabled" {
		t.Fatalf("states %+v", asset)
	}
	nfoName := strings.TrimSuffix(asset.Path, ".mkv") + ".nfo"
	nfo, ownID, err := readAssetNFO(nfoName)
	if err != nil || ownID != identity.ID || nfo.Actors[0].Role != "角色" {
		t.Fatalf("nfo=%+v id=%d err=%v", nfo, ownID, err)
	}
}

func TestAssetRemovalRequiresPreviewAndKeepsDownloadSource(t *testing.T) {
	a, source, _ := importTestApp(t)
	importFixture(t, a, "movie")
	input := filepath.Join(source, "Test (2024).mkv")
	importWrite(t, input, []byte("source"))
	plan, err := a.previewImport(context.Background(), ImportRequest{MediaType: "movie", TMDBID: 123, RootID: "root", Mode: "hardlink", Files: []ImportFileRequest{{Path: input}}})
	if err != nil {
		t.Fatal(err)
	}
	job, err := a.executeImport(context.Background(), plan.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	a.Store.DB.Exec(`UPDATE jobs SET state='running',generation=1 WHERE id=?`, job.ID)
	job.Generation = 1
	job.State = "running"
	if err = a.runImport(context.Background(), job, plan.ID, nil); err != nil {
		t.Fatal(err)
	}
	var asset Asset
	if err = a.assetAtPath(context.Background(), plan.Files[0].Target, &asset); err != nil {
		t.Fatal(err)
	}
	preview, err := a.previewAssetRemoval(context.Background(), asset)
	if err != nil || len(preview.Files) != 1 {
		t.Fatalf("preview=%+v err=%v", preview, err)
	}
	if err = a.removeAsset(context.Background(), asset.ID, "wrong-preview", true, true); err == nil {
		t.Fatal("missing preview accepted")
	}
	if err = a.removeAsset(context.Background(), asset.ID, preview.ID, true, true); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Lstat(asset.Path); !os.IsNotExist(err) {
		t.Fatal("published target remained")
	}
	if raw, err := os.ReadFile(input); err != nil || string(raw) != "source" {
		t.Fatal("download source changed")
	}
	if err = a.ensureNotExcluded(context.Background(), asset.Media, 0, nil); err == nil {
		t.Fatal("remove triggered reacquire")
	}
	if err = a.removeAsset(context.Background(), asset.ID, preview.ID, true, true); err == nil {
		t.Fatal("single use preview reused")
	}
	if err = a.resolveMissingAsset(context.Background(), asset, "allow_reacquire"); err != nil {
		t.Fatal(err)
	}
	if err = a.ensureNotExcluded(context.Background(), asset.Media, 0, nil); err != nil {
		t.Fatal("explicit reacquire not honored", err)
	}
}

func TestAssetRemovalRejectsChangedInode(t *testing.T) {
	a, source, target := importTestApp(t)
	identity := importFixture(t, a, "movie")
	path := filepath.Join(target, "movie.mkv")
	importWrite(t, path, []byte("ours"))
	locked, _ := snapshotPath(path)
	asset := Asset{ID: "asset-test", RootID: "root", Media: identity, Path: path, SourcePath: filepath.Join(source, "source.mkv"), State: "present"}
	a.Store.Put(context.Background(), "asset", asset.ID, asset)
	a.Store.Put(context.Background(), "managed_file", tokenHash(path), managedFile{Path: path, RootID: "root", AssetID: asset.ID, Kind: "video", Snapshot: locked})
	preview, err := a.previewAssetRemoval(context.Background(), asset)
	if err != nil {
		t.Fatal(err)
	}
	replacement := path + ".new"
	importWrite(t, replacement, []byte("external"))
	if err = os.Rename(replacement, path); err != nil {
		t.Fatal(err)
	}
	if err = a.removeAsset(context.Background(), asset.ID, preview.ID, true, true); err == nil {
		t.Fatal("changed inode deleted")
	}
	raw, _ := os.ReadFile(path)
	if string(raw) != "external" {
		t.Fatal("external replacement removed")
	}
}

func TestMissingRequiresReachableRootAndTwoChecks(t *testing.T) {
	a, _, target := importTestApp(t)
	identity := importFixture(t, a, "movie")
	path := filepath.Join(target, "movie.mkv")
	importWrite(t, path, []byte("video"))
	asset := Asset{ID: "asset-test", Media: identity, RootID: "root", Path: path, State: "external"}
	a.Store.Put(context.Background(), "asset", asset.ID, asset)
	a.Store.DB.Exec(`INSERT INTO asset_slots VALUES(?,?,?,?)`, mediaKey(identity), 0, 0, asset.ID)
	moved := target + ".offline"
	if err := os.Rename(target, moved); err != nil {
		t.Fatal(err)
	}
	if err := a.checkAsset(context.Background(), asset.ID); err == nil {
		t.Fatal("unavailable root ignored")
	}
	var current Asset
	a.Store.Get(context.Background(), "asset", asset.ID, &current)
	if current.State != "external" || current.MissingSeenAt != "" {
		t.Fatal("unavailable mount became missing")
	}
	os.Rename(moved, target)
	os.Remove(path)
	if err := a.checkAsset(context.Background(), asset.ID); err != nil {
		t.Fatal(err)
	}
	a.Store.Get(context.Background(), "asset", asset.ID, &current)
	if current.State != "missing_suspected" {
		t.Fatal(current.State)
	}
	if err := a.resolveMissingAsset(context.Background(), current, "confirm_missing"); err == nil {
		t.Fatal("one check considered final")
	}
	a.Store.Put(context.Background(), "asset_presence", asset.ID, assetPresence{Count: 1, Last: time.Now().Add(-time.Minute).UTC().Format(time.RFC3339Nano)})
	if err := a.checkAsset(context.Background(), asset.ID); err != nil {
		t.Fatal(err)
	}
	a.Store.Get(context.Background(), "asset", asset.ID, &current)
	if current.State != "missing_pending" {
		t.Fatal(current.State)
	}
	var count int
	a.Store.DB.QueryRow(`SELECT COUNT(*) FROM asset_slots WHERE asset_id=?`, asset.ID).Scan(&count)
	if count != 1 {
		t.Fatal("unconfirmed missing released slot")
	}
	if err := a.resolveMissingAsset(context.Background(), current, "confirm_missing"); err != nil {
		t.Fatal(err)
	}
	if err := a.ensureNotExcluded(context.Background(), identity, 0, nil); err == nil {
		t.Fatal("confirm missing allowed automatic refill")
	}
}

func TestLibraryScanBuildsExternalEpisodeSlotsWithoutOwningVideo(t *testing.T) {
	a, _, target := importTestApp(t)
	identity := importFixture(t, a, "tv")
	series := filepath.Join(target, "测试剧集 (2024)")
	video := filepath.Join(series, "Season 4", "测试剧集 S04E05.mkv")
	importWrite(t, video, []byte("external video"))
	episode := catalog.Episode{ID: 7000, SeasonNumber: 4, EpisodeNumber: 5, Name: "第五集", AirDate: "2024-05-06"}
	episodeNFO, err := assetNFO(identity, &episode)
	if err != nil {
		t.Fatal(err)
	}
	showNFO, err := assetNFO(identity, nil)
	if err != nil {
		t.Fatal(err)
	}
	importWrite(t, strings.TrimSuffix(video, ".mkv")+".nfo", episodeNFO)
	importWrite(t, filepath.Join(series, "tvshow.nfo"), showNFO)
	job := importClaim(t, a, "library_scan", "unused")
	if err = a.scanLibrary(context.Background(), job, "root"); err != nil {
		t.Fatal(err)
	}
	var asset Asset
	if err = a.assetAtPath(context.Background(), video, &asset); err != nil {
		t.Fatal(err)
	}
	if asset.Media.ID != identity.ID || asset.Media.ID == episode.ID || asset.Season != 4 || asset.Episodes[0] != 5 || asset.State != "external" || asset.SourcePath != "" {
		t.Fatalf("scan conflated ID: %+v", asset)
	}
	var count int
	a.Store.DB.QueryRow(`SELECT COUNT(*) FROM asset_slots WHERE media_key=? AND season=4 AND episode=5`, mediaKey(identity)).Scan(&count)
	if count != 1 {
		t.Fatal("external episode did not reserve library slot")
	}
	preview, err := a.previewAssetRemoval(context.Background(), asset)
	if err != nil || len(preview.Files) != 0 {
		t.Fatalf("scan claimed file ownership %+v %v", preview, err)
	}
	var scan map[string]any
	if err = a.Store.Get(context.Background(), "library_scan", "root", &scan); err != nil || scan["status"] != "complete" {
		t.Fatalf("scan status %+v %v", scan, err)
	}
}

func TestNFOConflictingIDsAndIncompleteScanDoNotInventIdentity(t *testing.T) {
	a, _, target := importTestApp(t)
	nfo := filepath.Join(target, "bad.nfo")
	importWrite(t, nfo, []byte(`<movie><title>x</title><tmdbid>10</tmdbid><uniqueid type="tmdb" default="true">11</uniqueid></movie>`))
	if _, _, err := readAssetNFO(nfo); err == nil {
		t.Fatal("conflicting standard IDs accepted")
	}
	job := importClaim(t, a, "library_scan", "unused")
	a.Store.DB.Exec(`UPDATE jobs SET state='cancelled',generation=generation+1 WHERE id=?`, job.ID)
	if err := a.scanLibrary(context.Background(), job, "root"); err == nil {
		t.Fatal("canceled scan completed")
	}
	var scan map[string]any
	a.Store.Get(context.Background(), "library_scan", "root", &scan)
	if scan["status"] != "incomplete" {
		raw, _ := json.Marshal(scan)
		t.Fatalf("canceled scan status %s", raw)
	}
}

func TestRemovalResumesQuarantineWithoutDeletingExternalReplacement(t *testing.T) {
	a, source, target := importTestApp(t)
	identity := importFixture(t, a, "movie")
	path := filepath.Join(target, "movie.mkv")
	importWrite(t, path, []byte("ours"))
	locked, _ := snapshotPath(path)
	asset := Asset{ID: "quarantine-test", RootID: "root", Media: identity, Path: path, SourcePath: filepath.Join(source, "source.mkv"), State: "present"}
	a.Store.Put(context.Background(), "asset", asset.ID, asset)
	a.Store.Put(context.Background(), "managed_file", tokenHash(path), managedFile{Path: path, RootID: "root", AssetID: asset.ID, Kind: "video", Snapshot: locked})
	preview, err := a.previewAssetRemoval(context.Background(), asset)
	if err != nil {
		t.Fatal(err)
	}
	preview.State = "deleting"
	preview.Files[0].Quarantine = ".light-media-remove-" + tokenHash(preview.ID + path)[:32] + ".part"
	quarantine := filepath.Join(filepath.Dir(path), preview.Files[0].Quarantine)
	if err = os.Rename(path, quarantine); err != nil {
		t.Fatal(err)
	}
	// Simulate a crash immediately after the exclusive rename, before the
	// quarantined flag is persisted, followed by an unrelated new public file.
	if err = a.Store.Put(context.Background(), "removal_preview", preview.ID, preview); err != nil {
		t.Fatal(err)
	}
	importWrite(t, path, []byte("external replacement"))
	if err = a.removeAsset(context.Background(), asset.ID, preview.ID, true, true); err != nil {
		t.Fatal(err)
	}
	if raw, err := os.ReadFile(path); err != nil || string(raw) != "external replacement" {
		t.Fatal("external replacement touched", err)
	}
	if _, err = os.Lstat(quarantine); !os.IsNotExist(err) {
		t.Fatal("own quarantine not removed")
	}
}
