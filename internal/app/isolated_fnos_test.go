package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"light-media/internal/catalog"
	"light-media/internal/fnos"
)

type isolatedFNOSTransport func(*http.Request) (*http.Response, error)

func (f isolatedFNOSTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// This opt-in integration test accepts the exact owned fixture and credentials
// on stdin. It never creates/deletes a library or touches an existing library.
func TestIsolatedFNOSProductWorkflow(t *testing.T) {
	if os.Getenv("LIGHT_MEDIA_FNOS_PRODUCT_TEST") != "1" {
		t.Skip("requires explicitly prepared UUID fixture and private stdin")
	}
	var in struct {
		Host, Username, Password, ExperimentID, LibraryGUID, Root, Stage string
		GoPluginRefresh, ResumeRefresh                                   bool
	}
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
	for _, library := range libraries {
		if library.GUID == in.LibraryGUID {
			continue
		}
		for _, directory := range library.Directories {
			if within(directory, in.Root) || within(in.Root, directory) {
				t.Fatal("owned test library overlaps another library")
			}
		}
	}
	expectedScans := int32(1)
	if in.GoPluginRefresh {
		base := http.DefaultTransport
		var scans, denied atomic.Int32
		http.DefaultTransport = isolatedFNOSTransport(func(r *http.Request) (*http.Response, error) {
			if strings.HasPrefix(r.URL.Path, "/v/api/v1/mdb/scan") {
				if r.Method != "POST" || r.URL.Path != "/v/api/v1/mdb/scan/"+in.LibraryGUID {
					denied.Add(1)
					return nil, errors.New("isolated test forbids non-owned library writes")
				}
				scans.Add(1)
			}
			response, err := base.RoundTrip(r)
			if err == nil && r.URL.Path == "/v/api/v1/mdb/scan/"+in.LibraryGUID {
				raw, readErr := io.ReadAll(io.LimitReader(response.Body, 64<<10))
				response.Body.Close()
				response.Body = io.NopCloser(bytes.NewReader(raw))
				var envelope struct {
					Code *int `json:"code"`
					Data any  `json:"data"`
				}
				if readErr == nil && json.Unmarshal(raw, &envelope) == nil && envelope.Code != nil {
					t.Logf("owned scan response: HTTP %d, business code %d, data type %T", response.StatusCode, *envelope.Code, envelope.Data)
				}
			}
			return response, err
		})
		defer func() {
			http.DefaultTransport = base
			if denied.Load() != 0 || (!t.Failed() && scans.Load() != expectedScans) {
				t.Error("owned refresh scope/count differs", scans.Load(), denied.Load())
			}
		}()
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
	if in.GoPluginRefresh {
		useBuiltInPluginProcess(t, a)
	}
	if in.GoPluginRefresh && !in.ResumeRefresh {
		cookie := setupTestAdmin(t, a)
		installed := callTest(t, a.Handler(), "POST", "/api/plugins/install", `{"plugin_id":"MediaServerRefresh"}`, cookie, "")
		if installed.Code != 200 || executeNextRefreshRun(t, a).State != "succeeded" {
			t.Fatal("owned refresh plugin installation failed")
		}
		configured := callTest(t, a.Handler(), "PUT", "/api/plugins/MediaServerRefresh/config", `{"revision":1,"config":{"enabled":true,"delay":3600,"mediaservers":["fnos"]}}`, cookie, "")
		if configured.Code != 200 || executeNextRefreshRun(t, a).State != "succeeded" {
			t.Fatal("owned refresh plugin configuration failed")
		}
	}
	// Keep ownership unique without accidental E21/E47 tokens inside UUIDs:
	// fnOS can parse those as episode numbers before reaching S01E01.
	prefix := "light-media-m0-test-" + strings.ReplaceAll(in.ExperimentID, "e", "x")
	identities := []catalog.Media{{ID: 899990001, MediaType: "movie", Title: prefix + "-Movie", Year: 2024, Overview: "Owned generated integration fixture"}, {ID: 899990002, MediaType: "tv", Title: prefix + "-Series", Year: 2024}, {ID: 899990003, MediaType: "tv", Title: prefix + "-Animation", Year: 2024}}
	var assets []Asset
	if in.ResumeRefresh {
		if !in.GoPluginRefresh {
			t.Fatal("resume requires the Go refresh fixture")
		}
		// Exercise recovery of this exact fixture. Preparing never crossed
		// the write boundary; confirmed reuses its receipt with zero writes.
		// Never replay an uncertain submitting operation.
		var runID, operation string
		err := a.Store.DB.QueryRowContext(ctx, `SELECT r.id,o.state FROM plugin_runs r JOIN plugin_media_refresh_operations o ON o.run_id=r.id WHERE r.instance_id='MediaServerRefresh' AND r.state IN ('failed','succeeded') AND r.action='event' AND o.state IN ('preparing','confirmed')`).Scan(&runID, &operation)
		if err != nil {
			t.Fatal("no safe owned fixture operation to resume")
		}
		if operation == "confirmed" {
			expectedScans = 0
		}
		changed, err := a.Store.DB.ExecContext(ctx, `UPDATE plugin_runs SET state='pending' WHERE id=? AND state IN ('failed','succeeded')`, runID)
		if err != nil {
			t.Fatal(err)
		}
		if count, _ := changed.RowsAffected(); count != 1 {
			t.Fatal("no single safe owned fixture run to resume")
		}
		stored, err := a.Store.List(ctx, "asset", 10, 0)
		if err != nil || len(stored) != 3 {
			t.Fatal("owned fixture assets missing")
		}
		for _, raw := range stored {
			var asset Asset
			if json.Unmarshal(raw, &asset) != nil || !within(in.Root, asset.Path) {
				t.Fatal("owned fixture asset scope invalid")
			}
			assets = append(assets, asset)
		}
	} else {
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
	}
	if in.GoPluginRefresh {
		at := time.Now()
		if err := a.dispatchPluginEvents(ctx, at); err != nil {
			t.Fatal(err)
		}
		first, err := scanJob(a.Store.DB.QueryRowContext(ctx, `SELECT `+jobColumns+` FROM jobs WHERE kind='fnos_sync' AND json_extract(payload,'$.id')=?`, assets[0].ID))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := a.Store.DB.ExecContext(ctx, `UPDATE jobs SET state='running',generation=1 WHERE id=?`, first.ID); err != nil {
			t.Fatal(err)
		}
		first.State, first.Generation = "running", 1
		if expectedScans != 0 {
			var waiting *pluginRefreshWait
			if err := a.runFNOSSync(ctx, first, assets[0].ID); !errors.As(err, &waiting) {
				t.Fatal("owned core sync did not respect plugin delay")
			}
		}
		if err := a.dispatchPluginEvents(ctx, at.Add(time.Hour+time.Second)); err != nil {
			t.Fatal(err)
		}
		run := executeNextRefreshRun(t, a)
		var result struct {
			ScanRequested bool               `json:"scan_requested"`
			Result        fnos.RefreshResult `json:"result"`
		}
		if run.State != "succeeded" || json.Unmarshal(run.Result, &result) != nil || !result.ScanRequested || result.Result.AllLibraries || len(result.Result.LibraryGUIDs) != 1 || result.Result.LibraryGUIDs[0] != in.LibraryGUID {
			t.Fatal("Go plugin did not confirm only the owned library scan", run.State, run.ErrorCode)
		}
		if expectedScans == 0 {
			t.Log("Go SDK reused the confirmed receipt with zero new scans; core still verified actual visibility")
		} else {
			t.Log("Go SDK consumed the actual import batch; FNOS accepted one owned-library refresh; core respected delay")
		}
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
		if in.GoPluginRefresh && current.FNOSScanScope != "plugin:MediaServerRefresh" {
			t.Fatal("core did not reuse the plugin scan receipt")
		}
		a.Store.DB.ExecContext(ctx, `UPDATE jobs SET state='succeeded' WHERE id=?`, job.ID)
		t.Logf("verified own %s: imported copy, source retained, NFO, FNOS actual item-file association", asset.Media.MediaType)
	}
	if _, err := service.Verify(ctx, in.LibraryGUID, identities[0].Title, "Movie", filepath.Join(in.Root, "never-created.mkv")); err == nil {
		t.Fatal("missing file falsely verified")
	}
	t.Log("verified missing file is never marked visible; only owned library was scanned")
}
