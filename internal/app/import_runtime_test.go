package app

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"light-media/internal/catalog"
)

func importTestApp(t *testing.T) (*App, string, string) {
	t.Helper()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	source, target := filepath.Join(base, "source"), filepath.Join(base, "library")
	for _, dir := range []string{source, target} {
		if err = os.MkdirAll(dir, 0750); err != nil {
			t.Fatal(err)
		}
	}
	app, err := New(Options{DataDir: filepath.Join(base, "data")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { app.Close() })
	app.config = Settings{AllowedRoots: []string{source}, Roots: []Root{{ID: "root", Path: target, Kind: "mixed"}}}
	return app, source, target
}

func importCache(t *testing.T, a *App, key string, value any) {
	t.Helper()
	if err := a.Store.Put(context.Background(), "cache", key, map[string]any{"at": time.Now(), "data": value}); err != nil {
		t.Fatal(err)
	}
}
func importFixture(t *testing.T, a *App, kind string) catalog.Media {
	t.Helper()
	identity := catalog.Media{ID: 123, MediaType: kind, Title: "测试影片", OriginalTitle: "Test Film", Year: 2024, Overview: "测试简介", Cast: []catalog.CastMember{{ID: 1, Name: "演员", Character: "角色", Order: 0}}, Genres: []catalog.Genre{{ID: 18, Name: "剧情"}}}
	if kind == "tv" {
		identity.Title = "测试剧集"
	}
	importCache(t, a, "media:"+kind+":123", identity)
	return identity
}
func importWrite(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0640); err != nil {
		t.Fatal(err)
	}
}
func importClaim(t *testing.T, a *App, kind, id string) Job {
	t.Helper()
	job, err := a.enqueue(context.Background(), kind, map[string]string{"id": id}, "wf-test")
	if err != nil {
		t.Fatal(err)
	}
	_, err = a.Store.DB.Exec(`UPDATE jobs SET state='running',generation=1 WHERE id=?`, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	job.State = "running"
	job.Generation = 1
	return job
}

func TestImportPreviewPublishResumeAndSourceRetention(t *testing.T) {
	for _, method := range []string{"hardlink", "copy", "auto"} {
		t.Run(method, func(t *testing.T) {
			a, source, _ := importTestApp(t)
			importFixture(t, a, "movie")
			input := filepath.Join(source, "Test (2024).mkv")
			importWrite(t, input, []byte("synthetic video bytes"))
			importWrite(t, filepath.Join(source, "Test (2024).zh.srt"), []byte("字幕"))
			plan, err := a.previewImport(context.Background(), ImportRequest{MediaType: "movie", TMDBID: 123, RootID: "root", Mode: method, Files: []ImportFileRequest{{Path: input}}})
			if err != nil || plan.Files[0].Status != "ready" {
				t.Fatalf("plan=%+v err=%v", plan, err)
			}
			if plan.Files[0].Method != method && !(method == "auto" && plan.Files[0].Method == "hardlink") {
				t.Fatalf("actual preview method missing: %+v", plan.Files[0])
			}
			job, err := a.executeImport(context.Background(), plan.ID, nil)
			if err != nil {
				t.Fatal(err)
			}
			_, err = a.Store.DB.Exec(`UPDATE jobs SET state='running',generation=1 WHERE id=?`, job.ID)
			if err != nil {
				t.Fatal(err)
			}
			job.State = "running"
			job.Generation = 1
			if err = a.runImport(context.Background(), job, plan.ID, nil); err != nil {
				t.Fatal(err)
			}
			if err = a.runImport(context.Background(), job, plan.ID, nil); err != nil {
				t.Fatal("idempotent resume", err)
			}
			srcInfo, _ := os.Stat(input)
			destInfo, err := os.Stat(plan.Files[0].Target)
			if err != nil {
				t.Fatal(err)
			}
			if method == "copy" && os.SameFile(srcInfo, destInfo) {
				t.Fatal("copy reused source inode")
			}
			if method != "copy" && !os.SameFile(srcInfo, destInfo) {
				t.Fatal("hardlink was not used")
			}
			if _, err = os.Stat(input); err != nil {
				t.Fatal("source removed", err)
			}
			subtitle := strings.TrimSuffix(plan.Files[0].Target, ".mkv") + ".zh.srt"
			if raw, err := os.ReadFile(subtitle); err != nil || string(raw) != "字幕" {
				t.Fatal("subtitle lost", err)
			}
			var assets, slots, reservations int
			_ = a.Store.DB.QueryRow(`SELECT COUNT(*) FROM records WHERE kind='asset'`).Scan(&assets)
			_ = a.Store.DB.QueryRow(`SELECT COUNT(*) FROM asset_slots`).Scan(&slots)
			_ = a.Store.DB.QueryRow(`SELECT COUNT(*) FROM reservations`).Scan(&reservations)
			if assets != 1 || slots != 1 || reservations != 0 {
				t.Fatalf("assets=%d slots=%d reservations=%d", assets, slots, reservations)
			}
		})
	}
}

func TestImportRejectsChangedSourceAndNoOverwrite(t *testing.T) {
	for _, change := range []string{"source", "target", "symlink-parent"} {
		t.Run(change, func(t *testing.T) {
			a, source, target := importTestApp(t)
			importFixture(t, a, "movie")
			input := filepath.Join(source, "Test (2024).mkv")
			importWrite(t, input, []byte("source"))
			plan, err := a.previewImport(context.Background(), ImportRequest{MediaType: "movie", TMDBID: 123, RootID: "root", Mode: "copy", Files: []ImportFileRequest{{Path: input}}})
			if err != nil {
				t.Fatal(err)
			}
			switch change {
			case "source":
				importWrite(t, input, []byte("modified"))
			case "target":
				importWrite(t, plan.Files[0].Target, []byte("must stay"))
			case "symlink-parent":
				outside := filepath.Join(filepath.Dir(target), "outside")
				os.MkdirAll(outside, 0750)
				if err = os.Symlink(outside, filepath.Dir(plan.Files[0].Target)); err != nil {
					t.Fatal(err)
				}
			}
			job, err := a.executeImport(context.Background(), plan.ID, nil)
			if err != nil {
				t.Fatal(err)
			}
			a.Store.DB.Exec(`UPDATE jobs SET state='running',generation=1 WHERE id=?`, job.ID)
			job.Generation = 1
			job.State = "running"
			if err = a.runImport(context.Background(), job, plan.ID, nil); err == nil {
				t.Fatal("unsafe publication succeeded")
			}
			if change == "target" {
				raw, _ := os.ReadFile(plan.Files[0].Target)
				if string(raw) != "must stay" {
					t.Fatal("existing target overwritten")
				}
			}
			if change == "symlink-parent" {
				entries, _ := os.ReadDir(filepath.Join(filepath.Dir(target), "outside"))
				if len(entries) != 0 {
					t.Fatal("wrote through symlink")
				}
			}
		})
	}
}

func TestImportEpisodeRequiresAuthoritativeSingleEpisode(t *testing.T) {
	a, source, _ := importTestApp(t)
	importFixture(t, a, "tv")
	importCache(t, a, "season:123:4", catalog.Season{ID: 40, SeriesID: 123, SeasonNumber: 4, Episodes: []catalog.Episode{{ID: 7706553, SeasonNumber: 4, EpisodeNumber: 5, Name: "单集名", AirDate: "2026-09-04"}}})
	input := filepath.Join(source, "Series S04E05.mkv")
	importWrite(t, input, []byte("video"))
	for _, episodes := range [][]int{{5}, {6}, {5, 6}} {
		plan, err := a.previewImport(context.Background(), ImportRequest{MediaType: "tv", TMDBID: 123, RootID: "root", Mode: "copy", Files: []ImportFileRequest{{Path: input, Season: 4, Episodes: episodes}}})
		if err != nil {
			t.Fatal(err)
		}
		if (len(episodes) == 1 && episodes[0] == 5) != (plan.Files[0].Status == "ready") {
			t.Fatalf("episodes=%v status=%s", episodes, plan.Files[0].Status)
		}
	}
}

func TestImportCopyCancellationAndReceiptReconciliation(t *testing.T) {
	a, source, target := importTestApp(t)
	input := filepath.Join(source, "video.mkv")
	importWrite(t, input, []byte(strings.Repeat("x", 2<<20)))
	locked, err := snapshotPath(input)
	if err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(target, "movie", "video.mkv")
	calls := 0
	_, err = a.publishImport(context.Background(), "cancellation-test", input, output, locked, "copy", func() error {
		calls++
		if calls >= 3 {
			return context.Canceled
		}
		return nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("wanted cancellation, got %v", err)
	}
	if _, err = os.Lstat(output); !os.IsNotExist(err) {
		t.Fatal("partial file published")
	}
	receipt, err := a.publishImport(context.Background(), "cancellation-test", input, output, locked, "copy", func() error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	first, _ := os.Stat(output)
	receipt.State = "staged"
	if err = a.Store.Put(context.Background(), "import_receipt", receipt.ID, receipt); err != nil {
		t.Fatal(err)
	}
	if _, err = a.publishImport(context.Background(), "cancellation-test", input, output, locked, "copy", func() error { return nil }); err != nil {
		t.Fatal("crash-after-publish not reconciled", err)
	}
	second, _ := os.Stat(output)
	if !os.SameFile(first, second) {
		t.Fatal("resume republished video")
	}
	if _, err = os.Stat(input); err != nil {
		t.Fatal("source lost")
	}
}

func TestImportSourceSymlinkAndScanContainment(t *testing.T) {
	a, source, _ := importTestApp(t)
	identity := importFixture(t, a, "movie")
	outside := filepath.Join(filepath.Dir(source), "outside.mkv")
	importWrite(t, outside, []byte("outside"))
	link := filepath.Join(source, "Alias (2024).mkv")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	plan, err := a.previewImport(context.Background(), ImportRequest{MediaType: "movie", TMDBID: identity.ID, RootID: "root", Mode: "hardlink", Files: []ImportFileRequest{{Path: link}}})
	if err != nil || plan.Files[0].Status != "blocked" {
		t.Fatalf("symlink not blocked %+v %v", plan, err)
	}
	if _, err = a.scanFiles(context.Background(), filepath.Dir(source), true); err == nil {
		t.Fatal("source escape scan accepted")
	}
	result, err := a.scanFiles(context.Background(), source, true)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(result)
	if strings.Contains(string(raw), "Alias") {
		t.Fatal("scan followed symlink")
	}
}

func TestImportOriginalIntentCompletesEvenWhenUnselectedExtrasRemain(t *testing.T) {
	a, source, _ := importTestApp(t)
	identity := importFixture(t, a, "movie")
	main, extra := filepath.Join(source, "Main (2024).mkv"), filepath.Join(source, "Extra (2024).mkv")
	importWrite(t, main, []byte("main"))
	importWrite(t, extra, []byte("extra"))
	intent := Intent{ID: "intent-main", Media: identity, RootID: "root", State: "downloaded"}
	a.Store.Put(context.Background(), "intent", intent.ID, intent)
	plan, err := a.previewImport(context.Background(), ImportRequest{MediaType: "movie", TMDBID: identity.ID, RootID: "root", Mode: "copy", IntentID: intent.ID, Files: []ImportFileRequest{{Path: main}, {Path: extra}}})
	if err != nil {
		t.Fatal(err)
	}
	job, err := a.executeImport(context.Background(), plan.ID, []string{plan.Files[0].ID})
	if err != nil {
		t.Fatal(err)
	}
	a.Store.DB.Exec(`UPDATE jobs SET state='running',generation=1 WHERE id=?`, job.ID)
	job.Generation = 1
	job.State = "running"
	if err = a.runImport(context.Background(), job, plan.ID, []string{plan.Files[0].ID}); err != nil {
		t.Fatal(err)
	}
	a.Store.Get(context.Background(), "intent", intent.ID, &intent)
	if intent.State != "imported" {
		t.Fatalf("main target was imported, intent=%s", intent.State)
	}
	var after ImportPlan
	a.Store.Get(context.Background(), "import_plan", plan.ID, &after)
	if after.State != "partial" || after.Files[1].Status != plan.Files[1].Status {
		t.Fatal("unselected extra was changed")
	}
}

func TestImportExclusionRechecksAtomicallyBeforeQueue(t *testing.T) {
	a, source, _ := importTestApp(t)
	identity := importFixture(t, a, "movie")
	input := filepath.Join(source, "Test (2024).mkv")
	importWrite(t, input, []byte("video"))
	plan, err := a.previewImport(context.Background(), ImportRequest{MediaType: "movie", TMDBID: identity.ID, RootID: "root", Mode: "copy", Files: []ImportFileRequest{{Path: input}}})
	if err != nil {
		t.Fatal(err)
	}
	a.Store.Put(context.Background(), "exclusion", "movie:123:0:0", map[string]bool{"blocked": true})
	if _, err = a.executeImport(context.Background(), plan.ID, nil); err == nil {
		t.Fatal("excluded import queued")
	}
	var count int
	a.Store.DB.QueryRow(`SELECT COUNT(*) FROM jobs`).Scan(&count)
	if count != 0 {
		t.Fatal("job escaped failed transaction")
	}
	a.Store.DB.QueryRow(`SELECT COUNT(*) FROM reservations`).Scan(&count)
	if count != 0 {
		t.Fatal("reservation escaped failed transaction")
	}
}
