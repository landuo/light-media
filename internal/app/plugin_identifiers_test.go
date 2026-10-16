package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"light-media/internal/catalog"
	"light-media/internal/decision"
	"light-media/internal/media"
	"light-media/internal/media/words"
	"light-media/internal/plugins/builtin"
	"light-media/internal/plugins/schedule"
	"light-media/internal/sites"
)

func identifiersExecuteNext(t *testing.T, a *App) PluginRun {
	t.Helper()
	run, err := a.pluginStore.ClaimRun(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	a.executePluginRun(context.Background(), run)
	run, err = a.pluginStore.Run(context.Background(), run.ID)
	if err != nil || run.State != "succeeded" {
		t.Fatal("identifiers process failed", run.State, run.ErrorCode, err)
	}
	return run
}

func identifiersConfigure(t *testing.T, a *App, cookie *http.Cookie, revision int64, config builtin.RemoteIdentifiersConfiguration) {
	t.Helper()
	raw, _ := json.Marshal(map[string]any{"revision": revision, "config": config})
	response := callTest(t, a.Handler(), "PUT", "/api/plugins/RemoteIdentifiers/config", string(raw), cookie, "")
	if response.Code != 200 {
		t.Fatal("identifiers config", response.Code, response.Body.String())
	}
}

func identifiersDue(t *testing.T, a *App) time.Time {
	t.Helper()
	var due string
	if err := a.Store.DB.QueryRow(`SELECT next_due FROM plugin_schedules WHERE service_id='RemoteIdentifiers.once' AND active=1`).Scan(&due); err != nil {
		t.Fatal(err)
	}
	at, err := time.Parse(time.RFC3339Nano, due)
	if err != nil {
		t.Fatal(err)
	}
	return at
}

func TestPluginIdentifiersRealSDKOnceAppliesRecognitionAndPreservesPhysicalPath(t *testing.T) {
	a, source, _ := importTestApp(t)
	useBuiltInPluginProcess(t, a)
	cookie := setupTestAdmin(t, a)
	ctx := context.Background()
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		fmt.Fprint(w, "Legacy => Test Film\nOld => 测试剧集 && E <> \\.mkv >> EP-2\n")
	}))
	defer server.Close()
	if err := a.saveRecognitionWords(ctx, 0, []string{"USER-PREFIX => 用户词", "#RemoteIdentifiers old", "Old => WRONG"}); err != nil {
		t.Fatal(err)
	}
	if response := callTest(t, a.Handler(), "POST", "/api/plugins/install", `{"plugin_id":"RemoteIdentifiers"}`, cookie, ""); response.Code != 200 {
		t.Fatal(response.Code, response.Body.String())
	}
	identifiersExecuteNext(t, a)
	if requests.Load() != 0 {
		t.Fatal("disabled default fetched remote words")
	}
	form := callTest(t, a.Handler(), "GET", "/api/plugins/RemoteIdentifiers/form", "", cookie, "")
	for _, required := range []string{"启用插件", "过滤空白行", "立即运行一次", "定时任务周期", "远程文件地址（一行一个）", builtin.RemoteIdentifiersCron} {
		if !strings.Contains(form.Body.String(), required) {
			t.Fatal("original field missing", required)
		}
	}
	config := builtin.RemoteIdentifiersConfiguration{OnlyOnce: true, Flitter: true, Cron: builtin.RemoteIdentifiersCron, FileURLs: server.URL + "/words?passkey=synthetic-private-url"}
	identifiersConfigure(t, a, cookie, 1, config)
	before := time.Now()
	identifiersExecuteNext(t, a)
	due := identifiersDue(t, a)
	if due.Before(before.Add(2*time.Second)) || due.After(time.Now().Add(4*time.Second)) {
		t.Fatal("one-shot is not anchored to actual initialization", due)
	}
	stored, _ := a.pluginStore.Config(ctx, "RemoteIdentifiers")
	var saved builtin.RemoteIdentifiersConfiguration
	json.Unmarshal(stored.Value, &saved)
	if saved.OnlyOnce || saved.Enable {
		t.Fatal("one-shot flag not atomically cleared", saved.OnlyOnce, saved.Enable)
	}
	if count, err := a.pluginStore.enqueueDuePluginSchedules(ctx, due); err != nil || count != 1 {
		t.Fatal("once not dispatched", count, err)
	}
	run := identifiersExecuteNext(t, a)
	if requests.Load() != 1 {
		t.Fatal("unexpected fetch count", requests.Load())
	}
	revision, rules, err := a.recognitionWords(ctx)
	if err != nil || revision != 2 || len(rules) != 4 || rules[0] != "USER-PREFIX => 用户词" || rules[1] != remoteIdentifiersMarker {
		t.Fatal("user prefix or remote replacement incorrect", revision, len(rules), err)
	}
	name := "Legacy (2024).mkv"
	input := filepath.Join(source, name)
	importWrite(t, input, []byte("owned synthetic video"))
	importFixture(t, a, "movie")
	parsed := a.parseMediaName(name)
	if parsed.Status != media.Parsed || parsed.Title != "Test Film" {
		t.Fatal("words not used by file recognition", parsed)
	}
	plan, err := a.previewImport(ctx, ImportRequest{MediaType: "movie", TMDBID: 123, RootID: "root", Mode: "hardlink", Files: []ImportFileRequest{{Path: input}}})
	if err != nil || len(plan.Files) != 1 || plan.Files[0].Source != input || plan.Files[0].Status != "ready" {
		t.Fatal("words broke physical import plan", err, plan.Files)
	}
	if _, err := os.Stat(input); err != nil {
		t.Fatal("recognition modified physical source", err)
	}
	season, episodes := a.resourceNumberingSuggestion("Old.S01E09.mkv")
	if season != 1 || !reflect.DeepEqual(episodes, []int{7}) {
		t.Fatal("resource numbering ignored words", season, episodes)
	}
	target := decision.Target{Media: catalog.Media{ID: 42, MediaType: "tv", Title: "测试剧集"}, Season: 1, Episodes: []int{7}, KnownEpisodes: []int{7}, AiredEpisodes: []int{7}, Auto: true}
	choice := a.evaluateResource(sites.Resource{Title: "Old.S01E09.mkv", Seeders: 1, Size: 1 << 30}, target, decision.Policy{})
	if !choice.Accepted || !reflect.DeepEqual(choice.Episodes, []int{7}) {
		t.Fatal("subscription/download choice ignored words", choice)
	}
	for _, path := range []string{"/api/plugins/RemoteIdentifiers/form", "/api/plugins/RemoteIdentifiers/runs", "/api/plugins"} {
		response := callTest(t, a.Handler(), "GET", path, "", cookie, "")
		if strings.Contains(response.Body.String(), "synthetic-private-url") {
			t.Fatal("public plugin API disclosed source credential", path)
		}
	}
	projected := callTest(t, a.Handler(), "GET", "/api/plugins/RemoteIdentifiers/form", "", cookie, "")
	if !strings.Contains(projected.Body.String(), privateIdentifiersURLs) {
		t.Fatal("private source preservation marker missing")
	}
	if count, err := a.pluginStore.enqueueDuePluginSchedules(ctx, due.Add(time.Hour)); err != nil || count != 0 {
		t.Fatal("consumed date repeated", count, err)
	}
	// Crash after the atomic word publication and before a worker acknowledgement.
	a.Store.DB.Exec(`UPDATE plugin_runs SET state='running',result=NULL WHERE id=?`, run.ID)
	a.Store.DB.Exec(`UPDATE plugin_schedule_occurrences SET state='running' WHERE run_id=?`, run.ID)
	if err := a.pluginStore.recoverPluginRuns(ctx); err != nil {
		t.Fatal(err)
	}
	recovered, _ := a.pluginStore.Run(ctx, run.ID)
	if recovered.State != "succeeded" || requests.Load() != 1 {
		t.Fatal("confirmed update was refetched on recovery", recovered.State, requests.Load())
	}
	var state string
	a.Store.DB.QueryRow(`SELECT state FROM plugin_schedule_occurrences WHERE run_id=?`, run.ID).Scan(&state)
	if state != "succeeded" {
		t.Fatal("recovered occurrence not terminal", state)
	}
	// The public marker retains encrypted source URLs and does not replay once.
	saved.FileURLs = privateIdentifiersURLs
	identifiersConfigure(t, a, cookie, stored.Revision, saved)
	identifiersExecuteNext(t, a)
	stored, _ = a.pluginStore.Config(ctx, "RemoteIdentifiers")
	json.Unmarshal(stored.Value, &saved)
	if !strings.Contains(saved.FileURLs, "synthetic-private-url") {
		t.Fatal("saving projection lost original private source")
	}
}

func TestPluginIdentifiersRegistrationAndWordPublicationRollback(t *testing.T) {
	a := newTestApp(t)
	useBuiltInPluginProcess(t, a)
	ctx := context.Background()
	cookie := setupTestAdmin(t, a)
	callTest(t, a.Handler(), "POST", "/api/plugins/install", `{"plugin_id":"RemoteIdentifiers"}`, cookie, "")
	identifiersExecuteNext(t, a)
	config := builtin.RemoteIdentifiersConfiguration{OnlyOnce: true, Cron: builtin.RemoteIdentifiersCron}
	identifiersConfigure(t, a, cookie, 1, config)
	if _, err := a.Store.DB.Exec(`CREATE TRIGGER fail_identifiers_flag BEFORE UPDATE ON plugin_values WHEN OLD.instance_id='RemoteIdentifiers' AND OLD.kind='config' BEGIN SELECT RAISE(ABORT,'synthetic flag write failure'); END`); err != nil {
		t.Fatal(err)
	}
	run, err := a.pluginStore.ClaimRun(ctx)
	if err != nil {
		t.Fatal(err)
	}
	a.executePluginRun(ctx, run)
	var count int
	a.Store.DB.QueryRow(`SELECT COUNT(*) FROM plugin_schedules WHERE service_id='RemoteIdentifiers.once'`).Scan(&count)
	if count != 0 {
		t.Fatal("date registration survived failed flag commit")
	}
	stored, _ := a.pluginStore.Config(ctx, "RemoteIdentifiers")
	var saved builtin.RemoteIdentifiersConfiguration
	json.Unmarshal(stored.Value, &saved)
	if !saved.OnlyOnce {
		t.Fatal("failed registration cleared one-shot")
	}
	a.Store.DB.Exec(`DROP TRIGGER fail_identifiers_flag`)
	if _, err := a.pluginStore.EnqueueRun(ctx, "RemoteIdentifiers", "reload", nil); err != nil {
		t.Fatal(err)
	}
	identifiersExecuteNext(t, a)
	due := identifiersDue(t, a)
	a.pluginStore.enqueueDuePluginSchedules(ctx, due)
	service, err := a.pluginStore.ClaimRun(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.saveRecognitionWords(ctx, 0, []string{"User => Original"}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Store.DB.Exec(`CREATE TRIGGER fail_word_receipt BEFORE INSERT ON recognition_word_updates BEGIN SELECT RAISE(ABORT,'synthetic receipt failure'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := a.replaceRemoteIdentifiersForRun(ctx, service, []string{remoteIdentifiersMarker, "User => Changed"}); err == nil {
		t.Fatal("receipt failure ignored")
	}
	revision, rules, err := a.recognitionWords(ctx)
	if err != nil || revision != 1 || !reflect.DeepEqual(rules, []string{"User => Original"}) || a.prepareRecognitionTitle("User") != "Original" {
		t.Fatal("failed receipt published words", revision, rules, err)
	}
	if _, err := a.Store.DB.Exec(`DROP TRIGGER fail_word_receipt`); err != nil {
		t.Fatal(err)
	}
	// A user edit completed during network fetch is read at publication time.
	if err := a.saveRecognitionWords(ctx, 1, []string{"Edited => Kept", remoteIdentifiersMarker, "Old => Removed"}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.replaceRemoteIdentifiersForRun(ctx, service, []string{remoteIdentifiersMarker, "Remote => New"}); err != nil {
		t.Fatal(err)
	}
	_, rules, _ = a.recognitionWords(ctx)
	if !reflect.DeepEqual(rules, []string{"Edited => Kept", remoteIdentifiersMarker, "Remote => New"}) {
		t.Fatal("concurrent user prefix overwritten")
	}
	if err := a.saveRecognitionWords(ctx, 1, []string{"stale"}); pluginErrorCode(err) != "RECOGNITION_REVISION_CONFLICT" {
		t.Fatal("stale user edit overwritten words", err)
	}
}

func TestIdentifiersFetcherOriginalOrderFilteringRetriesAndFailures(t *testing.T) {
	var base string
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		switch r.URL.Path {
		case "/list.json":
			fmt.Fprintf(w, `{"a":%q,"b":%q,"a":%q}`, base+"/old", base+"/second", base+"/first")
		case "/first":
			fmt.Fprint(w, "A => B\n\n \r\n")
		case "/second":
			fmt.Fprint(w, "C => D")
		case "/etherpad/p/test/export/txt":
			fmt.Fprint(w, "E => F")
		case "/badjson.json":
			fmt.Fprint(w, `{"a":42}`)
		case "/html":
			fmt.Fprint(w, "<!doctype html><title>login</title>")
		case "/limit":
			fmt.Fprint(w, "try again later")
		case "/encoding":
			w.Write([]byte{255, 254})
		default:
			w.WriteHeader(403)
		}
	}))
	defer server.Close()
	base = server.URL
	delays := []time.Duration{}
	f := identifiersFetcher{client: &http.Client{Timeout: time.Second, CheckRedirect: identifiersRedirect}, sleep: func(ctx context.Context, d time.Duration) error { delays = append(delays, d); return nil }}
	config := builtin.RemoteIdentifiersConfiguration{FileURLs: base + "/list.json\n" + base + "/etherpad/p/test", Flitter: true}
	got, err := f.fetch(context.Background(), config)
	expected := []string{remoteIdentifiersMarker, "A => B", " \r", "C => D", "E => F"}
	if err != nil || !reflect.DeepEqual(got, expected) {
		t.Fatal("index insertion order or exact empty filtering changed", got, err)
	}
	config.Flitter = false
	got, err = f.fetch(context.Background(), config)
	if err != nil || len(got) != 7 || got[2] != "" || got[4] != "" {
		t.Fatal("unfiltered empty lines changed", got, err)
	}
	for _, path := range []string{"/badjson.json", "/html", "/limit", "/encoding", "/forbidden"} {
		t.Run(path, func(t *testing.T) {
			before := requests.Load()
			delays = nil
			config.FileURLs = base + path + ""
			_, err := f.fetch(context.Background(), config)
			if err == nil || requests.Load()-before != 3 || !reflect.DeepEqual(delays, []time.Duration{5 * time.Second, 10 * time.Second}) {
				t.Fatal("whole collection retry differs", requests.Load()-before, delays, err)
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := f.fetch(ctx, config); err == nil {
		t.Fatal("canceled fetch ignored")
	}
}

func TestIdentifiersPrivateRedirectAndGenericAPISecrets(t *testing.T) {
	var foreign atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { foreign.Add(1); fmt.Fprint(w, "never") }))
	defer target.Close()
	from := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, http.StatusFound) }))
	defer from.Close()
	f := identifiersFetcher{client: &http.Client{CheckRedirect: identifiersRedirect}}
	if _, err := f.text(context.Background(), from.URL+"/?token=synthetic-url-token"); err == nil || strings.Contains(err.Error(), "synthetic-url-token") || foreign.Load() != 0 {
		t.Fatal("private redirect reached foreign source or exposed token", err, foreign.Load())
	}
	for _, raw := range []string{"http://169.254.169.254/latest", "file:///etc/passwd", "http://0.0.0.0/", "https://"} {
		if _, err := identifiersURL(raw); err == nil {
			t.Fatal("invalid source accepted", raw)
		}
	}
}

func TestRecognitionWordsEncryptedRestartAndFileScanUsesSharedMatcher(t *testing.T) {
	a, source, _ := importTestApp(t)
	ctx := context.Background()
	rules := []string{"Legacy => Test Film", "E <> \\.mkv >> EP-2"}
	if err := a.saveRecognitionWords(ctx, 0, rules); err != nil {
		t.Fatal(err)
	}
	var sealed []byte
	a.Store.DB.QueryRow(`SELECT sealed FROM recognition_words`).Scan(&sealed)
	if strings.Contains(string(sealed), "Legacy") {
		t.Fatal("recognition words stored in plaintext")
	}
	for _, name := range []string{"Legacy (2024).mkv", "Legacy.S01E09.mkv"} {
		importWrite(t, filepath.Join(source, name), []byte("owned media fixture"))
	}
	scan, err := a.scanFiles(ctx, source, false)
	if err != nil {
		t.Fatal(err)
	}
	files := scan.(map[string]any)["files"].([]importScanFile)
	if len(files) != 2 || files[0].Path != filepath.Join(source, "Legacy (2024).mkv") || files[0].Parsed.Title != "Test Film" || !reflect.DeepEqual(files[1].Parsed.Original.Episodes, []int{7}) {
		t.Fatal("scan words or source path mismatch", files)
	}
	dir := a.Dir
	a.Close()
	b, err := New(Options{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	if b.parseMediaName("Legacy.S01E09.mkv").Original.Episodes[0] != 7 {
		t.Fatal("recognition snapshot not restored")
	}
}

func TestIdentifiersCronEnableAndManualServiceUseActualBoundConfig(t *testing.T) {
	a := newTestApp(t)
	useBuiltInPluginProcess(t, a)
	ctx := context.Background()
	cookie := setupTestAdmin(t, a)
	callTest(t, a.Handler(), "POST", "/api/plugins/install", `{"plugin_id":"RemoteIdentifiers"}`, cookie, "")
	identifiersExecuteNext(t, a)
	config := builtin.RemoteIdentifiersConfiguration{Enable: true, Cron: "30 4 * * *"}
	identifiersConfigure(t, a, cookie, 1, config)
	identifiersExecuteNext(t, a)
	var due string
	a.Store.DB.QueryRow(`SELECT next_due FROM plugin_schedules WHERE service_id='RemoteIdentifiers' AND active=1`).Scan(&due)
	trigger, _ := schedule.New(schedule.Spec{Kind: "cron", Timezone: identifiersTimezone(), Cron: config.Cron}, time.Now())
	if due != schedule.FormatTime(trigger.First(time.Now())) {
		t.Fatal("configured cron not registered", due)
	}
	run, err := a.pluginStore.enqueueManualPluginService(ctx, "RemoteIdentifiers", "RemoteIdentifiers", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	finished := identifiersExecuteNext(t, a)
	if finished.ID != run.ID {
		t.Fatal("manual service not delivered")
	}
	var after string
	a.Store.DB.QueryRow(`SELECT next_due FROM plugin_schedules WHERE service_id='RemoteIdentifiers'`).Scan(&after)
	if after != due {
		t.Fatal("manual service moved cron")
	}
	config.Enable = false
	stored, _ := a.pluginStore.Config(ctx, "RemoteIdentifiers")
	identifiersConfigure(t, a, cookie, stored.Revision, config)
	identifiersExecuteNext(t, a)
	if _, err := a.pluginStore.enqueueManualPluginService(ctx, "RemoteIdentifiers", "RemoteIdentifiers", time.Now()); pluginErrorCode(err) != "PLUGIN_SERVICE_NOT_FOUND" {
		t.Fatal("disabled service callable", err)
	}
}

func TestPublicRemoteIdentifiersCorpus(t *testing.T) {
	if os.Getenv("LIGHT_MEDIA_TEST_PUBLIC_IDENTIFIERS") != "1" {
		t.Skip("explicit public-network corpus check")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	f := identifiersFetcher{client: &http.Client{Timeout: 15 * time.Second, CheckRedirect: identifiersRedirect}}
	rules, err := f.fetch(ctx, builtin.RemoteIdentifiersConfiguration{FileURLs: builtin.RemoteIdentifiersURLs, Flitter: true})
	if err != nil {
		t.Fatal(err)
	}
	a := newTestApp(t)
	if err := a.saveRecognitionWords(ctx, 0, rules); err != nil {
		t.Fatal(err)
	}
	samples := []string{"龙珠.Dragon.Ball.Daima.S01E01.1080p.WEB-DL.mkv", "斗罗大陆2绝世唐门 EP177 1080p.mkv", "非凡测试.S01E01.mkv", "Legacy (2024).mkv"}
	type result struct {
		Title, GoResult string
		Failures        []int
	}
	results := []result{}
	for _, sample := range samples {
		got := a.recognitionMatcher.Load().Prepare(sample)
		if got.Limited {
			t.Fatal("public word list exceeded per-filename budget")
		}
		t.Logf("public corpus: %d rules; failed rules %d; applied rules %d", len(rules), got.Failed, len(got.Applied))
		failures := []int{}
		for i, rule := range rules {
			m, _ := words.New([]string{rule})
			if m.Prepare(sample).Failed > 0 {
				failures = append(failures, i)
			}
		}
		results = append(results, result{sample, got.Title, failures})
	}
	// Only these two public, credential-free lists are written for the read-only
	// reference calculation. User configuration never enters this artifact.
	raw, _ := json.Marshal(map[string]any{"words": rules, "results": results})
	if err := os.WriteFile("../../work/public-remote-identifiers.json", raw, 0600); err != nil {
		t.Fatal(err)
	}
}

func TestIdentifiersPendingOnceRestartPreservesAndReloadStops(t *testing.T) {
	a := newTestApp(t)
	useBuiltInPluginProcess(t, a)
	ctx := context.Background()
	cookie := setupTestAdmin(t, a)
	callTest(t, a.Handler(), "POST", "/api/plugins/install", `{"plugin_id":"RemoteIdentifiers"}`, cookie, "")
	identifiersExecuteNext(t, a)
	identifiersConfigure(t, a, cookie, 1, builtin.RemoteIdentifiersConfiguration{OnlyOnce: true})
	identifiersExecuteNext(t, a)
	original := identifiersDue(t, a)
	if err := a.initializeNativePlugins(ctx); err != nil {
		t.Fatal(err)
	}
	identifiersExecuteNext(t, a)
	if !identifiersDue(t, a).Equal(original) {
		t.Fatal("startup reset pending one-shot anchor")
	}
	if _, err := a.pluginStore.EnqueueRun(ctx, "RemoteIdentifiers", "reload", nil); err != nil {
		t.Fatal(err)
	}
	identifiersExecuteNext(t, a)
	if n, err := a.pluginStore.enqueueDuePluginSchedules(ctx, original); err != nil || n != 0 {
		t.Fatal("explicit original-style reload retained old one-shot", n, err)
	}
}

func TestRecognitionWordsLimitsDoNotUseUnadjustedIdentity(t *testing.T) {
	a := newTestApp(t)
	if err := a.saveRecognitionWords(context.Background(), 0, []string{`(a+)+$ => X`}); err != nil {
		t.Fatal(err)
	}
	parsed := a.parseMediaName(strings.Repeat("a", 80) + ".S01E09.mkv")
	if parsed.Status == media.Parsed || parsed.Original != nil {
		t.Fatal("incomplete preprocessing accepted the original unadjusted episode")
	}
}
