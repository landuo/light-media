package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"light-media/internal/catalog"
)

// Opt-in, loopback-only browser fixture. It serves the built application and
// real queue workers, but has no schedulers, plugins, NAS or downloader accounts.
func TestOrganizeEpisodeGroupBrowserFixture(t *testing.T) {
	base := os.Getenv("LM_EPISODE_GROUP_BROWSER_FIXTURE")
	if base == "" {
		t.Skip("opt-in browser fixture")
	}
	root, err := filepath.Abs("../..")
	if err != nil || !filepath.IsAbs(base) || !within(filepath.Join(root, "work"), base) {
		t.Fatal("use an exclusive absolute workspace work directory")
	}
	source, target := filepath.Join(base, "source"), filepath.Join(base, "library")
	for _, dir := range []string{source, target} {
		if err = os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	a, err := New(Options{DataDir: filepath.Join(base, "data"), Web: http.FileServer(http.Dir(filepath.Join(root, "web/dist"))), SafeMode: true})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	off, on := false, true
	// Keep the directory rule disabled: an empty destination demonstrates the
	// retained-draft error case, then the UI explicitly selects this test root.
	a.config = Settings{AllowedRoots: []string{source}, Roots: []Root{{ID: "fixture-root", Path: target, Kind: "mixed"}}, Directories: []DirectoryRule{{ID: "fixture-directory", Name: "独占剧集组样本", SourcePath: source, LibraryPath: target, RootID: "fixture-root", MediaType: "mixed", MonitorType: "none", Mode: "hardlink", Renaming: &on, Scraping: &off, LibraryTypeFolder: &off}}}
	secret := make([]byte, 24)
	if _, err = rand.Read(secret); err != nil {
		t.Fatal(err)
	}
	auth, _ := json.Marshal(map[string]string{"username": "episode-group-fixture", "password": hex.EncodeToString(secret)})
	response := callTest(t, a.Handler(), "POST", "/api/auth/setup", string(auth), nil, "")
	if response.Code != 200 || len(response.Result().Cookies()) == 0 {
		t.Fatal("fixture setup failed")
	}
	cookie := response.Result().Cookies()[0]
	organizeGroupFixture(t, a)
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	media := catalog.Media{ID: 123, MediaType: "tv", Title: "Group Fixture", Year: 2024, CategoryMetadataKnown: true}
	importCache(t, a, "media:tv:123", media)
	for i := 1; i <= 2; i++ {
		path := filepath.Join(source, fmt.Sprintf("Sample.S01E%02d.mkv", i))
		if err = os.WriteFile(path, []byte(fmt.Sprintf("synthetic group episode %d", i)), 0600); err != nil {
			t.Fatal(err)
		}
		key, fileID := fmt.Sprintf("group-plan-%d", i), fmt.Sprintf("group-file-%d", i)
		stamp := time.Now().Add(-time.Duration(i) * time.Minute).UTC().Format(time.RFC3339Nano)
		plan := ImportPlan{ID: key, WorkflowID: "fixture-work", RootID: "fixture-root", Media: media, Mode: "hardlink", Files: []PlanFile{{ID: fileID, Source: path, Target: filepath.Join(target, fmt.Sprintf("Unpublished.%d.mkv", i)), Method: "hardlink", Season: 1, Episodes: []int{i}, Status: "error", Reason: "合成记录：待重新整理"}}, State: "failed", CreatedAt: stamp}
		if err = a.Store.Put(ctx, "import_plan", key, plan); err != nil {
			t.Fatal(err)
		}
		payload, _ := json.Marshal(map[string]any{"id": key, "file_ids": []string{fileID}})
		if _, err = a.Store.DB.Exec(`INSERT INTO jobs(id,workflow_id,kind,state,payload,run_at,created_at,updated_at,error_message) VALUES(?,?,'import','failed',?,?,?,?,?)`, fmt.Sprintf("group-job-%d", i), plan.WorkflowID, payload, time.Now().Unix(), stamp, stamp, "合成记录：待重新整理"); err != nil {
			t.Fatal(err)
		}
	}
	response = callTest(t, a.Handler(), "PUT", "/api/files/episode-format/rules", `{"revision":0,"rules":[{"name":"停用的合成规则","enabled":false,"order":0,"pattern":"^Never(?P<ep>\\d+)$","min_file_size_mb":0}]}`, cookie, "")
	if response.Code != 200 {
		t.Fatalf("fixture rule setup: %d %s", response.Code, response.Body)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/__fixture", func(w http.ResponseWriter, r *http.Request) {
		http.SetCookie(w, cookie)
		http.Redirect(w, r, "/#history", http.StatusSeeOther)
	})
	mux.HandleFunc("/__fixture/stop", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			w.WriteHeader(405)
			return
		}
		w.WriteHeader(204)
		cancel()
	})
	mux.Handle("/", a.Handler())
	server := &http.Server{Addr: "127.0.0.1:18081", Handler: mux}
	a.Start(ctx)
	for _, kinds := range []jobLane{generalJobs, importJobs} {
		kinds := kinds
		a.wg.Add(1)
		go func() { defer a.wg.Done(); a.jobWorker(ctx, kinds) }()
	}
	go func() {
		<-ctx.Done()
		stop, c := context.WithTimeout(context.Background(), 3*time.Second)
		defer c()
		server.Shutdown(stop)
	}()
	fmt.Println("fixture_ready http://127.0.0.1:18081/__fixture synthetic_records=2 disabled_rule=1")
	if err = server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		t.Fatal(err)
	}
}
