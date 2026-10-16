package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"light-media/internal/plugins"
)

const refreshTestLibrary = "11111111111111111111111111111111"
const refreshTestMovie = "22222222222222222222222222222222"

type refreshFixture struct {
	app           *App
	cookie        *http.Cookie
	root          string
	scans, logins atomic.Int32
	path          atomic.Value
	reject        atomic.Bool
}

func newRefreshFixture(t *testing.T, delay int) *refreshFixture {
	t.Helper()
	a, _, root := importTestApp(t)
	useBuiltInPluginProcess(t, a)
	f := &refreshFixture{app: a, root: root}
	f.path.Store(filepath.Join(root, "movie.mkv"))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v/api/v1/sys/version":
			fmt.Fprint(w, `{"code":0,"data":{"version":"1.0-synthetic","mediasrvVersion":"1.0-synthetic"}}`)
		case "/v/api/v1/login":
			f.logins.Add(1)
			fmt.Fprint(w, `{"code":0,"data":{"token":"synthetic-media-session"}}`)
		case "/v/api/v1/user/info":
			fmt.Fprint(w, `{"code":0,"data":{"is_admin":1}}`)
		case "/v/api/v1/mdb/list":
			fmt.Fprintf(w, `{"code":0,"data":[{"guid":%q,"name":"测试库","category":"Mix","dir_list":[%q]}]}`, refreshTestLibrary, root)
		case "/v/api/v1/task/running":
			fmt.Fprint(w, `{"code":0,"data":null}`)
		case "/v/api/v1/mdb/scan/" + refreshTestLibrary, "/v/api/v1/mdb/scanall":
			body, _ := io.ReadAll(r.Body)
			if r.Method != "POST" || len(body) != 0 {
				t.Error("unexpected core duplicate or non-original scan body")
			}
			f.scans.Add(1)
			fmt.Fprintf(w, `{"code":0,"data":%t}`, !f.reject.Load())
		case "/v/api/v1/search/list":
			fmt.Fprintf(w, `{"code":0,"data":[{"guid":%q,"type":"Movie","title":"测试影片"}]}`, refreshTestMovie)
		case "/v/api/v1/item/" + refreshTestMovie:
			fmt.Fprintf(w, `{"code":0,"data":{"guid":%q,"ancestor_guid":%q,"parent_guid":"","type":"Movie","title":"测试影片"}}`, refreshTestMovie, refreshTestLibrary)
		case "/v/api/v1/media/itemfile/" + refreshTestMovie:
			fmt.Fprintf(w, `{"code":0,"data":[{"item_guid":%q,"file_path":%q}]}`, refreshTestMovie, f.path.Load().(string))
		default:
			t.Error("unexpected media request", r.URL.Path)
			fmt.Fprint(w, `{"code":-1}`)
		}
	}))
	t.Cleanup(server.Close)
	a.config.FNOS = FNConfig{Enabled: true, Host: server.URL + "/v", Username: "synthetic-user", Password: "synthetic-fnos-password"}
	f.cookie = setupTestAdmin(t, a)
	w := callTest(t, a.Handler(), "POST", "/api/plugins/install", `{"plugin_id":"MediaServerRefresh"}`, f.cookie, "")
	if w.Code != 200 {
		t.Fatal("install refresh", w.Code, w.Body.String())
	}
	executeNextRefreshRun(t, a)
	w = callTest(t, a.Handler(), "PUT", "/api/plugins/MediaServerRefresh/config", fmt.Sprintf(`{"revision":1,"config":{"enabled":true,"delay":%d,"mediaservers":["fnos"]}}`, delay), f.cookie, "")
	if w.Code != 200 {
		t.Fatal("configure refresh", w.Code, w.Body.String())
	}
	executeNextRefreshRun(t, a)
	return f
}

func executeNextRefreshRun(t *testing.T, a *App) PluginRun {
	t.Helper()
	run, err := a.pluginStore.claimRunForInstance(context.Background(), "MediaServerRefresh")
	if err != nil {
		t.Fatal(err)
	}
	a.executePluginRun(context.Background(), run)
	run, err = a.pluginStore.Run(context.Background(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	return run
}

func (f *refreshFixture) importMovie(t *testing.T) Asset {
	t.Helper()
	a := f.app
	importFixture(t, a, "movie")
	source := filepath.Join(a.Settings().AllowedRoots[0], "Test.2024.mkv")
	importWrite(t, source, []byte("owned-synthetic-video"))
	plan, err := a.previewImport(context.Background(), ImportRequest{MediaType: "movie", TMDBID: 123, RootID: "root", Mode: "hardlink", Files: []ImportFileRequest{{Path: source}}})
	if err != nil {
		t.Fatal(err)
	}
	job, err := a.executeImport(context.Background(), plan.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	job.State, job.Generation = "running", 1
	if _, err := a.Store.DB.Exec(`UPDATE jobs SET state='running',generation=1 WHERE id=?`, job.ID); err != nil {
		t.Fatal(err)
	}
	if err := a.runImport(context.Background(), job, plan.ID, nil); err != nil {
		t.Fatal(err)
	}
	var saved ImportPlan
	if err := a.Store.Get(context.Background(), "import_plan", plan.ID, &saved); err != nil {
		t.Fatal(err)
	}
	var asset Asset
	if err := a.Store.Get(context.Background(), "asset", saved.Files[0].AssetID, &asset); err != nil {
		t.Fatal(err)
	}
	scrape, err := scanJob(a.Store.DB.QueryRow(`SELECT ` + jobColumns + ` FROM jobs WHERE kind='scrape' LIMIT 1`))
	if err != nil {
		t.Fatal(err)
	}
	scrape.State, scrape.Generation = "running", 1
	if _, err := a.Store.DB.Exec(`UPDATE jobs SET state='running',generation=1 WHERE id=?`, scrape.ID); err != nil {
		t.Fatal(err)
	}
	if err := a.runScrape(context.Background(), scrape, asset.ID); err != nil {
		t.Fatal(err)
	}
	if err := a.Store.Get(context.Background(), "asset", asset.ID, &asset); err != nil {
		t.Fatal(err)
	}
	f.path.Store(asset.Path)
	return asset
}

func TestPluginMediaRefreshActualImportSDKDebounceAndVisibilityWithoutDuplicateScan(t *testing.T) {
	f := newRefreshFixture(t, 3600)
	a := f.app
	asset := f.importMovie(t)
	at := time.Now()
	if err := a.dispatchPluginEvents(context.Background(), at); err != nil {
		t.Fatal(err)
	}
	syncJob, err := scanJob(a.Store.DB.QueryRow(`SELECT ` + jobColumns + ` FROM jobs WHERE kind='fnos_sync' LIMIT 1`))
	if err != nil {
		t.Fatal(err)
	}
	syncJob.State, syncJob.Generation, syncJob.Attempt = "running", 1, 1
	if _, err := a.Store.DB.Exec(`UPDATE jobs SET state='running',generation=1,attempt=1 WHERE id=?`, syncJob.ID); err != nil {
		t.Fatal(err)
	}
	err = a.runFNOSSync(context.Background(), syncJob, asset.ID)
	var pending *pluginRefreshWait
	if !errors.As(err, &pending) || f.logins.Load() != 0 || f.scans.Load() != 0 {
		t.Fatal("core bypassed delay", err, f.scans.Load())
	}
	if _, err := a.finishJob(context.Background(), syncJob, "retry_wait", "PLUGIN_REFRESH_PENDING", pending.Error(), pending.Until.Unix()); err != nil {
		t.Fatal(err)
	}
	var attempt int
	if err := a.Store.DB.QueryRow(`SELECT attempt FROM jobs WHERE id=?`, syncJob.ID).Scan(&attempt); err != nil || attempt != 0 {
		t.Fatal("waiting consumed failure budget", attempt, err)
	}
	if err := a.dispatchPluginEvents(context.Background(), at.Add(time.Hour+time.Second)); err != nil {
		t.Fatal(err)
	}
	run := executeNextRefreshRun(t, a)
	if run.State != "succeeded" || f.scans.Load() != 1 {
		t.Fatal("real SDK did not request one scan", run.State, run.ErrorCode, f.scans.Load())
	}
	if _, err := a.Store.DB.Exec(`UPDATE jobs SET state='running',generation=1 WHERE id=?`, syncJob.ID); err != nil {
		t.Fatal(err)
	}
	if err := a.runFNOSSync(context.Background(), syncJob, asset.ID); err != nil {
		t.Fatal(err)
	}
	if f.scans.Load() != 1 {
		t.Fatal("core repeated the accepted plugin scan")
	}
	if err := a.Store.Get(context.Background(), "asset", asset.ID, &asset); err != nil || asset.FNOSState != "visible" || asset.FNOSGUID != refreshTestMovie || asset.FNOSScanScope != "plugin:MediaServerRefresh" {
		t.Fatal("actual visibility not confirmed", asset.FNOSState, err)
	}
}

func TestPluginMediaRefreshConfigQueueDoesNotLoseImportsBeforeWorkerInit(t *testing.T) {
	f := newRefreshFixture(t, 0)
	a := f.app
	w := callTest(t, a.Handler(), "PUT", "/api/plugins/MediaServerRefresh/config", `{"revision":2,"config":{"enabled":true,"delay":0,"mediaservers":["fnos"]}}`, f.cookie, "")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	f.importMovie(t)
	if run := executeNextRefreshRun(t, a); run.State != "succeeded" {
		t.Fatal(run.State, run.ErrorCode)
	}
	if err := a.dispatchPluginEvents(context.Background(), time.Now()); err != nil {
		t.Fatal(err)
	}
	if run := executeNextRefreshRun(t, a); run.State != "succeeded" || f.scans.Load() != 1 {
		t.Fatal("import committed after config was lost before init", run.State, run.ErrorCode, f.scans.Load())
	}
}

func TestPluginMediaRefreshHandoffRetainsFactsCommittedDuringDrain(t *testing.T) {
	for _, action := range []string{"reload", "config"} {
		t.Run(action, func(t *testing.T) {
			f := newRefreshFixture(t, 0)
			a := f.app
			installation, err := a.pluginStore.Installation(context.Background(), "MediaServerRefresh")
			if err != nil {
				t.Fatal(err)
			}
			a.pluginLifecycleMu.Lock()
			err = a.drainMediaRefresh(context.Background(), installation)
			a.pluginLifecycleMu.Unlock()
			if err != nil {
				t.Fatal(err)
			}
			// A real import commits after drain's frozen high water and before
			// the replacement initialization can be queued.
			f.importMovie(t)
			if action == "reload" {
				_, err = a.pluginStore.EnqueueRun(context.Background(), "MediaServerRefresh", "reload", nil)
			} else {
				_, _, err = a.pluginStore.SaveConfigAndQueue(context.Background(), "MediaServerRefresh", 2, map[string]any{"enabled": true, "delay": 0, "mediaservers": []string{"fnos"}})
			}
			if err != nil {
				t.Fatal(err)
			}
			if run := executeNextRefreshRun(t, a); run.State != "succeeded" {
				t.Fatal(run.State, run.ErrorCode)
			}
			if err := a.dispatchPluginEvents(context.Background(), time.Now()); err != nil {
				t.Fatal(err)
			}
			if run := executeNextRefreshRun(t, a); run.State != "succeeded" || f.scans.Load() != 1 {
				t.Fatal("handoff lost a committed import", run.State, run.ErrorCode, f.scans.Load())
			}
		})
	}
}

func TestPluginMediaRefreshReenableDoesNotReplayDisabledHistory(t *testing.T) {
	f := newRefreshFixture(t, 0)
	a := f.app
	for index, enabled := range []bool{false, true} {
		w := callTest(t, a.Handler(), "PUT", "/api/plugins/MediaServerRefresh/config", fmt.Sprintf(`{"revision":%d,"config":{"enabled":%t,"delay":0,"mediaservers":["fnos"]}}`, index+2, enabled), f.cookie, "")
		if w.Code != 200 || executeNextRefreshRun(t, a).State != "succeeded" {
			t.Fatal("toggle failed", w.Code)
		}
		if !enabled {
			f.importMovie(t)
		}
	}
	if err := a.dispatchPluginEvents(context.Background(), time.Now()); err != nil {
		t.Fatal(err)
	}
	if pluginEventCount(t, a, "plugin_event_deliveries") != 0 || f.scans.Load() != 0 {
		t.Fatal("disabled history was silently replayed")
	}
}

func TestPluginMediaRefreshDisableAndUninstallFlushOriginalDelay(t *testing.T) {
	for _, action := range []string{"disable", "uninstall", "reload", "reset"} {
		t.Run(action, func(t *testing.T) {
			f := newRefreshFixture(t, 3600)
			a := f.app
			f.importMovie(t)
			if err := a.dispatchPluginEvents(context.Background(), time.Now()); err != nil {
				t.Fatal(err)
			}
			stale := callTest(t, a.Handler(), "PUT", "/api/plugins/MediaServerRefresh/config", `{"revision":1,"config":{"enabled":false,"delay":0,"mediaservers":["fnos"]}}`, f.cookie, "")
			if stale.Code != 400 || f.scans.Load() != 0 {
				t.Fatal("stale form changed stop timing", stale.Code)
			}
			method, path, body := "POST", "/api/plugins/MediaServerRefresh/"+action, `{}`
			if action == "disable" {
				method, path, body = "PUT", "/api/plugins/MediaServerRefresh/config", `{"revision":2,"config":{"enabled":false,"delay":3600,"mediaservers":["fnos"]}}`
			}
			result := callTest(t, a.Handler(), method, path, body, f.cookie, "")
			if result.Code != 200 || f.scans.Load() != 1 {
				t.Fatal("stop did not flush pending content", result.Code, result.Body.String(), f.scans.Load())
			}
			if action == "disable" || action == "reload" {
				if run := executeNextRefreshRun(t, a); run.State != "succeeded" {
					t.Fatal(run.State, run.ErrorCode)
				}
			}
		})
	}
}

func TestPluginMediaRefreshUncertainSubmitIsNotReplayedAndConfirmedReceiptIsReused(t *testing.T) {
	for _, state := range []string{"submitting", "confirmed"} {
		t.Run(state, func(t *testing.T) {
			f := newRefreshFixture(t, 0)
			a := f.app
			f.importMovie(t)
			if err := a.dispatchPluginEvents(context.Background(), time.Now()); err != nil {
				t.Fatal(err)
			}
			run, err := a.pluginStore.claimRunForInstance(context.Background(), "MediaServerRefresh")
			if err != nil {
				t.Fatal(err)
			}
			installation, err := a.pluginStore.Installation(context.Background(), run.InstanceID)
			if err != nil {
				t.Fatal(err)
			}
			var invocation plugins.Invocation
			if json.Unmarshal(run.Input, &invocation) != nil {
				t.Fatal("invalid invocation")
			}
			if state == "submitting" {
				if _, err := a.Store.DB.Exec(`INSERT INTO plugin_media_refresh_operations VALUES(?,?,?,'submitting',NULL,?)`, run.ID, run.InstanceID, mediaRefreshFingerprint(a.Settings().FNOS), now()); err != nil {
					t.Fatal(err)
				}
			} else {
				if _, err := a.pluginMediaHost(context.Background(), installation, &run, "media.refresh", invocation.Input); err != nil {
					t.Fatal(err)
				}
				if f.scans.Load() != 1 {
					t.Fatal("fixture scan not accepted")
				}
			}
			if err := a.pluginStore.recoverMediaRefreshRuns(context.Background()); err != nil {
				t.Fatal(err)
			}
			if state == "submitting" {
				recovered, err := a.pluginStore.Run(context.Background(), run.ID)
				if err != nil || recovered.State != "reconcile_pending" {
					t.Fatal("uncertain submission was not held for review", recovered.State, err)
				}
				if _, err := a.pluginMediaHost(context.Background(), installation, &run, "media.refresh", invocation.Input); pluginErrorCode(err) != "REVIEW_PLUGIN_MEDIA_REFRESH" {
					t.Fatal("uncertain scan repeated", err)
				}
				if f.scans.Load() != 0 {
					t.Fatal("unconfirmed operation resent")
				}
			} else {
				if result := executeNextRefreshRun(t, a); result.State != "succeeded" || f.scans.Load() != 1 {
					t.Fatal("confirmed scan was not reused", result.State, result.ErrorCode, f.scans.Load())
				}
			}
		})
	}
}

func TestPluginMediaRefreshHTTP200RejectionAndChangedAssetNeverClaimSuccess(t *testing.T) {
	for _, scenario := range []string{"rejected", "identity", "root"} {
		t.Run(scenario, func(t *testing.T) {
			f := newRefreshFixture(t, 0)
			a := f.app
			asset := f.importMovie(t)
			if scenario == "rejected" {
				f.reject.Store(true)
			}
			if scenario == "identity" {
				asset.Media.ID = 999
				if err := a.Store.Put(context.Background(), "asset", asset.ID, asset); err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "root" {
				a.config.Roots[0].Path = filepath.Join(f.root, "other")
			}
			if err := a.dispatchPluginEvents(context.Background(), time.Now()); err != nil {
				t.Fatal(err)
			}
			run := executeNextRefreshRun(t, a)
			if run.State != "failed" || run.ErrorCode == "" {
				t.Fatal("invalid refresh claimed success", run.State, run.ErrorCode)
			}
			if scenario != "rejected" && f.scans.Load() != 0 {
				t.Fatal("changed identity/scope was scanned")
			}
			public := callTest(t, a.Handler(), "GET", "/api/plugins/MediaServerRefresh/runs", "", f.cookie, "")
			if strings.Contains(public.Body.String(), "synthetic-fnos-password") || strings.Contains(public.Body.String(), asset.Path) {
				t.Fatal("private refresh state exposed")
			}
		})
	}
}

func TestPluginMediaRefreshFailedScanStillRequiresIndependentVisibilityProof(t *testing.T) {
	f := newRefreshFixture(t, 0)
	a := f.app
	asset := f.importMovie(t)
	f.reject.Store(true)
	if err := a.dispatchPluginEvents(context.Background(), time.Now()); err != nil {
		t.Fatal(err)
	}
	run := executeNextRefreshRun(t, a)
	if run.State != "failed" {
		t.Fatal("server rejection was not preserved", run.State)
	}
	scans := f.scans.Load()
	job, err := scanJob(a.Store.DB.QueryRow(`SELECT ` + jobColumns + ` FROM jobs WHERE kind='fnos_sync' LIMIT 1`))
	if err != nil {
		t.Fatal(err)
	}
	job.State, job.Generation = "running", 1
	if _, err := a.Store.DB.Exec(`UPDATE jobs SET state='running',generation=1 WHERE id=?`, job.ID); err != nil {
		t.Fatal(err)
	}
	if err := a.runFNOSSync(context.Background(), job, asset.ID); err != nil {
		t.Fatal("server's independently visible asset could not be verified", err)
	}
	if f.scans.Load() != scans {
		t.Fatal("core resent a failed or uncertain plugin scan")
	}
	if err := a.Store.Get(context.Background(), "asset", asset.ID, &asset); err != nil || asset.FNOSState != "visible" || asset.FNOSGUID != refreshTestMovie {
		t.Fatal("actual visibility proof was not persisted", asset.FNOSState, err)
	}
	if saved, err := a.pluginStore.Run(context.Background(), run.ID); err != nil || saved.State != "failed" {
		t.Fatal("visibility proof rewrote failed plugin execution", saved.State, err)
	}
}

func TestPluginMediaRefreshDurableBoundariesCannotBeSkipped(t *testing.T) {
	for _, state := range []string{"submitting", "confirmed"} {
		t.Run(state, func(t *testing.T) {
			f := newRefreshFixture(t, 0)
			a := f.app
			f.importMovie(t)
			if err := a.dispatchPluginEvents(context.Background(), time.Now()); err != nil {
				t.Fatal(err)
			}
			if _, err := a.Store.DB.Exec(`CREATE TRIGGER ignore_refresh_boundary BEFORE UPDATE OF state ON plugin_media_refresh_operations WHEN NEW.state='` + state + `' BEGIN SELECT RAISE(IGNORE); END`); err != nil {
				t.Fatal(err)
			}
			run := executeNextRefreshRun(t, a)
			if run.State != "failed" || run.ErrorCode != "PLUGIN_REFRESH_STATE_CONFLICT" {
				t.Fatal("missing durable receipt reported success", run.State, run.ErrorCode)
			}
			wantScans := int32(0)
			if state == "confirmed" {
				wantScans = 1
			}
			if f.scans.Load() != wantScans {
				t.Fatal("remote write crossed the wrong persistence boundary", f.scans.Load())
			}
			var savedState string
			if err := a.Store.DB.QueryRow(`SELECT state FROM plugin_media_refresh_operations WHERE run_id=?`, run.ID).Scan(&savedState); err != nil || savedState == "confirmed" {
				t.Fatal("unconfirmed state was lost", savedState, err)
			}
			a.controlMu.Lock()
			writes := a.activeWrites
			a.controlMu.Unlock()
			if writes != 0 {
				t.Fatal("write guard was not released", writes)
			}
		})
	}
}

func TestPluginMediaRefreshCloseFlushesCollectedAndUncollectedFacts(t *testing.T) {
	for _, collected := range []bool{false, true} {
		t.Run(fmt.Sprint(collected), func(t *testing.T) {
			f := newRefreshFixture(t, 3600)
			a := f.app
			f.importMovie(t)
			if collected {
				if err := a.dispatchPluginEvents(context.Background(), time.Now()); err != nil {
					t.Fatal(err)
				}
			}
			// Exercise Close after ordinary worker shutdown; this fixture drives
			// jobs explicitly so no unrelated core work starts during the test.
			canceled := false
			a.cancel = func() { canceled = true }
			if err := a.Close(); err != nil {
				t.Fatal(err)
			}
			if !canceled || f.scans.Load() != 1 {
				t.Fatal("shutdown abandoned the original pending delay", canceled, f.scans.Load())
			}
			a.cancel = nil // The fixture cleanup may safely close the DB again.
		})
	}
}
