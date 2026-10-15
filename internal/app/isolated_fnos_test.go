package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"light-media/internal/catalog"
	"light-media/internal/fnos"
)

// This opt-in integration test accepts the exact owned fixture and credentials
// on stdin. It never creates/deletes a library or touches an existing library.
func TestIsolatedFNOSProductWorkflow(t *testing.T) {
	if os.Getenv("LIGHT_MEDIA_FNOS_PRODUCT_TEST") != "1" {
		t.Skip("requires explicitly prepared UUID fixture and private stdin")
	}
	var in struct{ Host, Username, Password, ExperimentID, LibraryGUID, Root, Stage string }
	if e := json.NewDecoder(io.LimitReader(os.Stdin, 64<<10)).Decode(&in); e != nil {
		t.Fatal("private integration input invalid")
	}
	uuid := regexp.MustCompile(`^[a-f0-9]{8}-[a-f0-9]{4}-4[a-f0-9]{3}-[89ab][a-f0-9]{3}-[a-f0-9]{12}$`)
	if !uuid.MatchString(in.ExperimentID) || in.Root != "/vol2/1000/media/light-media-m0-test-"+in.ExperimentID || in.Stage != "/tmp/light-media-fnos-product-"+in.ExperimentID || in.Host != "https://nas.accidia.top:8080/v" {
		t.Fatal("fixture scope invalid")
	}
	for _, p := range []string{in.Root, in.Stage, filepath.Join(in.Stage, "source"), filepath.Join(in.Stage, "source", "clip.mkv")} {
		st, e := os.Lstat(p)
		if e != nil || st.Mode()&os.ModeSymlink != 0 {
			t.Fatal("owned fixture absent or symlink")
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	service, e := fnos.NewService(fnos.Config{Host: in.Host, Username: in.Username, Password: in.Password})
	if e != nil {
		t.Fatal(e)
	}
	libraries, e := service.Libraries(ctx)
	if e != nil {
		t.Fatal(e)
	}
	owned := false
	for _, l := range libraries {
		if l.GUID == in.LibraryGUID && len(l.Directories) == 1 && l.Directories[0] == in.Root && l.Name == "light-media-m0-test-"+in.ExperimentID {
			owned = true
		}
	}
	if !owned {
		t.Fatal("exact owned test library not confirmed")
	}
	a, e := New(Options{DataDir: filepath.Join(in.Stage, "app")})
	if e != nil {
		t.Fatal(e)
	}
	defer a.Close()
	cfg := Settings{AllowedRoots: []string{filepath.Join(in.Stage, "source")}, Roots: []Root{{ID: "owned", Path: in.Root, Kind: "mixed", FNOSLibraryGUID: in.LibraryGUID}}, FNOS: FNConfig{Host: in.Host, Username: in.Username, Password: in.Password, Enabled: true, Mappings: []PathMapping{{From: in.Root, To: in.Root}}}}
	if e = a.saveSettings(ctx, cfg); e != nil {
		t.Fatal(e)
	}
	prefix := "light-media-m0-test-" + in.ExperimentID
	identities := []catalog.Media{{ID: 899990001, MediaType: "movie", Title: prefix + "-Movie", Year: 2024, Overview: "Owned generated integration fixture"}, {ID: 899990002, MediaType: "tv", Title: prefix + "-Series", Year: 2024}, {ID: 899990003, MediaType: "tv", Title: prefix + "-Animation", Year: 2024}}
	var assets []Asset
	for i, m := range identities {
		importCache(t, a, fmt.Sprintf("media:%s:%d", m.MediaType, m.ID), m)
		file := ImportFileRequest{Path: filepath.Join(in.Stage, "source", "clip.mkv")}
		if m.MediaType == "tv" {
			file.Season = 1
			file.Episodes = []int{1}
			importCache(t, a, fmt.Sprintf("season:%d:1", m.ID), catalog.Season{ID: 899991000 + int64(i), SeasonNumber: 1, Episodes: []catalog.Episode{{ID: 899992000 + int64(i), SeasonNumber: 1, EpisodeNumber: 1, Name: prefix + "-Episode", AirDate: "2024-01-01"}}})
		}
		plan, err := a.previewImport(ctx, ImportRequest{MediaType: m.MediaType, TMDBID: m.ID, RootID: "owned", Mode: "copy", Files: []ImportFileRequest{file}})
		if err != nil || len(plan.Files) != 1 || plan.Files[0].Status != "ready" {
			t.Fatalf("owned preview %d failed: %v", i, err)
		}
		job, err := a.executeImport(ctx, plan.ID, nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = a.Store.DB.ExecContext(ctx, `UPDATE jobs SET state='running',generation=1 WHERE id=?`, job.ID); err != nil {
			t.Fatal(err)
		}
		job.Generation = 1
		job.State = "running"
		if err = a.runImport(a.traceContext(ctx, job.WorkflowID, job.ID, ""), job, plan.ID, nil); err != nil {
			t.Fatal(err)
		}
		a.Store.DB.ExecContext(ctx, `UPDATE jobs SET state='succeeded' WHERE id=?`, job.ID)
		var completed ImportPlan
		if err = a.Store.Get(ctx, "import_plan", plan.ID, &completed); err != nil {
			t.Fatal(err)
		}
		assetID := completed.Files[0].AssetID
		scrape, err := scanJob(a.Store.DB.QueryRowContext(ctx, `SELECT `+jobColumns+` FROM jobs WHERE kind='scrape' AND json_extract(payload,'$.id')=?`, assetID))
		if err != nil {
			t.Fatal(err)
		}
		a.Store.DB.ExecContext(ctx, `UPDATE jobs SET state='running',generation=1 WHERE id=?`, scrape.ID)
		scrape.Generation = 1
		scrape.State = "running"
		if err = a.runScrape(a.traceContext(ctx, scrape.WorkflowID, scrape.ID, ""), scrape, assetID); err != nil {
			t.Fatal(err)
		}
		a.Store.DB.ExecContext(ctx, `UPDATE jobs SET state='succeeded' WHERE id=?`, scrape.ID)
		var asset Asset
		if err = a.Store.Get(ctx, "asset", assetID, &asset); err != nil {
			t.Fatal(err)
		}
		src, _ := os.Stat(file.Path)
		dst, err := os.Stat(asset.Path)
		if err != nil || os.SameFile(src, dst) {
			t.Fatal("actual NAS copy/source retention failed")
		}
		if _, err = os.Stat(strings.TrimSuffix(asset.Path, filepath.Ext(asset.Path)) + ".nfo"); err != nil {
			t.Fatal("NFO not published")
		}
		assets = append(assets, asset)
	}
	for _, asset := range assets {
		job, err := scanJob(a.Store.DB.QueryRowContext(ctx, `SELECT `+jobColumns+` FROM jobs WHERE kind='fnos_sync' AND json_extract(payload,'$.id')=?`, asset.ID))
		if err != nil {
			t.Fatal(err)
		}
		a.Store.DB.ExecContext(ctx, `UPDATE jobs SET state='running',generation=1 WHERE id=?`, job.ID)
		job.Generation = 1
		job.State = "running"
		var syncErr error
		for attempt := 0; attempt < 8; attempt++ {
			syncErr = a.runFNOSSync(a.traceContext(ctx, job.WorkflowID, job.ID, ""), job, asset.ID)
			if syncErr == nil {
				break
			}
			var current Asset
			_ = a.Store.Get(ctx, "asset", asset.ID, &current)
			if current.FNOSState == "visible" {
				t.Fatal("failed verify falsely marked visible")
			}
			select {
			case <-ctx.Done():
				t.Fatal("owned FNOS visibility deadline")
			case <-time.After(5 * time.Second):
			}
		}
		if syncErr != nil {
			t.Fatalf("owned %s FNOS visibility not verified: %v", asset.Media.MediaType, syncErr)
		}
		var current Asset
		if err = a.Store.Get(ctx, "asset", asset.ID, &current); err != nil || current.FNOSState != "visible" || current.FNOSGUID == "" {
			t.Fatal("verified FNOS identity not persisted")
		}
		a.Store.DB.ExecContext(ctx, `UPDATE jobs SET state='succeeded' WHERE id=?`, job.ID)
		t.Logf("verified own %s: imported copy, source retained, NFO, FNOS actual item-file association", asset.Media.MediaType)
	}
	if _, err := service.Verify(ctx, in.LibraryGUID, identities[0].Title, "Movie", filepath.Join(in.Root, "never-created.mkv")); err == nil {
		t.Fatal("missing file falsely verified")
	}
	t.Log("verified missing file is never marked visible; only owned library was scanned")
}
