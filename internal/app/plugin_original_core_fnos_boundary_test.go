package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
)

const ownedCoreFNLibrary = "11111111111111111111111111111111"

const ownedCoreFNMovie = "22222222222222222222222222222222"

type ownedCoreFNFixture struct {
	app           *App
	root          string
	scans, logins atomic.Int32
	path          atomic.Value
	reject        atomic.Bool
}

func newOwnedCoreFNFixture(t *testing.T) *ownedCoreFNFixture {
	t.Helper()
	a, _, root := importTestApp(t)
	f := &ownedCoreFNFixture{app: a, root: root}
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
			fmt.Fprintf(w, `{"code":0,"data":[{"guid":%q,"name":"测试库","category":"Mix","dir_list":[%q]}]}`, ownedCoreFNLibrary, root)
		case "/v/api/v1/task/running":
			fmt.Fprint(w, `{"code":0,"data":null}`)
		case "/v/api/v1/mdb/scan/" + ownedCoreFNLibrary, "/v/api/v1/mdb/scanall":
			var body struct {
				Directories []string `json:"dir_list"`
			}
			if r.Method != "POST" || json.NewDecoder(r.Body).Decode(&body) != nil || len(body.Directories) != 1 || body.Directories[0] != filepath.ToSlash(filepath.Dir(f.path.Load().(string))) {
				t.Error("core scan did not carry the actual owned directory")
			}
			f.scans.Add(1)
			fmt.Fprint(w, `{"code":0,"data":true}`)
		case "/v/api/v1/search/list":
			fmt.Fprintf(w, `{"code":0,"data":[{"guid":%q,"type":"Movie","title":"测试影片"}]}`, ownedCoreFNMovie)
		case "/v/api/v1/item/" + ownedCoreFNMovie:
			fmt.Fprintf(w, `{"code":0,"data":{"guid":%q,"ancestor_guid":%q,"parent_guid":"","type":"Movie","title":"测试影片"}}`, ownedCoreFNMovie, ownedCoreFNLibrary)
		case "/v/api/v1/media/itemfile/" + ownedCoreFNMovie:
			actual := f.path.Load().(string)
			if f.reject.Load() {
				actual = filepath.Join(filepath.Dir(actual), "different-owned-file.mkv")
			}
			fmt.Fprintf(w, `{"code":0,"data":[{"item_guid":%q,"file_path":%q}]}`, ownedCoreFNMovie, actual)
		default:
			t.Error("unexpected media request", r.URL.Path)
			fmt.Fprint(w, `{"code":-1}`)
		}
	}))
	t.Cleanup(server.Close)
	a.config.FNOS = FNConfig{Enabled: true, Host: server.URL + "/v", Username: "synthetic-user", Password: "synthetic-fnos-password"}
	return f
}

func (f *ownedCoreFNFixture) importMovie(t *testing.T) Asset {
	t.Helper()
	a := f.app
	importFixture(t, a, "movie")
	source := filepath.Join(a.Settings().AllowedRoots[0], "Test.2024.mkv")
	importWrite(t, source, []byte("owned-synthetic-video"))
	plan, err := a.previewImport(context.Background(), ImportRequest{MediaType: "movie", TMDBID: 123, RootID: "root", Mode: "hardlink", Files: []ImportFileRequest{{Path: source}}})
	if err != nil || len(plan.Files) != 1 || plan.Files[0].Status != "ready" {
		t.Fatal("actual owned import precondition", plan, err)
	}
	job, err := a.executeImport(context.Background(), plan.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	job = claimOwnedCoreFNJob(t, a, job, "import")
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
	scrape = claimOwnedCoreFNJob(t, a, scrape, "scrape")
	if err := a.runScrape(context.Background(), scrape, asset.ID); err != nil {
		t.Fatal(err)
	}
	if err := a.Store.Get(context.Background(), "asset", asset.ID, &asset); err != nil {
		t.Fatal(err)
	}
	f.path.Store(asset.Path)
	return asset
}

func TestOriginalCoreFNOSScanAndVisibilityWithoutNativeGate(t *testing.T) {
	for _, wrongPath := range []bool{false, true} {
		t.Run(fmt.Sprintf("wrong_file_%t", wrongPath), func(t *testing.T) {
			f := newOwnedCoreFNFixture(t)
			a := f.app
			ctx := context.Background()
			asset := f.importMovie(t)
			if asset.State != "present" || asset.ScrapeState != "scraped" || !within(f.root, asset.Path) {
				t.Fatal("import/scrape preconditions", asset)
			}
			if _, err := os.Stat(asset.Path); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(filepath.Join(a.Settings().AllowedRoots[0], "Test.2024.mkv")); err != nil {
				t.Fatal("original source not retained", err)
			}
			f.reject.Store(wrongPath)
			job, err := scanJob(a.Store.DB.QueryRow(`SELECT ` + jobColumns + ` FROM jobs WHERE kind='fnos_sync' LIMIT 1`))
			if err != nil {
				t.Fatal(err)
			}
			job = claimOwnedCoreFNJob(t, a, job, "fnos_sync")
			err = a.safeRun(ctx, job)
			if wrongPath && err == nil {
				t.Fatal("wrong actual file accepted")
			}
			if !wrongPath && err != nil {
				t.Fatal("ordinary core was blocked", err)
			}
			var saved Asset
			if readErr := a.Store.Get(ctx, "asset", asset.ID, &saved); readErr != nil {
				t.Fatal(readErr)
			}
			if f.scans.Load() != 1 || f.logins.Load() == 0 {
				t.Fatal("actual core Scan not reached", f.scans.Load(), f.logins.Load())
			}
			if wrongPath {
				if saved.FNOSState == "visible" || saved.FNOSGUID != "" {
					t.Fatal("false visibility", saved)
				}
			} else if saved.FNOSState != "visible" || saved.FNOSGUID != ownedCoreFNMovie {
				t.Fatal("verified parent/file result not persisted", saved)
			}
			var runs int
			if err = a.Store.DB.QueryRow(`SELECT COUNT(*) FROM plugin_runs`).Scan(&runs); err != nil || runs != 0 {
				t.Fatal("legacy plugin run", runs, err)
			}
			if _, ok := a.pluginRunningInstances.Load("MediaServerRefresh"); ok {
				t.Fatal("old adapter loaded")
			}
		})
	}
}

func claimOwnedCoreFNJob(t *testing.T, a *App, queued Job, kind string) Job {
	t.Helper()
	if queued.Kind != kind || queued.State != "queued" {
		t.Fatalf("expected real queued %s: %+v", kind, queued)
	}
	claimed, err := a.claim(context.Background())
	if err != nil || claimed.ID != queued.ID || claimed.Kind != kind || claimed.State != "running" || claimed.Generation <= queued.Generation {
		t.Fatalf("actual core claim did not select queued %s/%s: %+v %v", kind, queued.ID, claimed, err)
	}
	return claimed
}
