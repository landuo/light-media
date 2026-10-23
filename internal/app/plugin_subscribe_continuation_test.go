package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"light-media/internal/catalog"
	"light-media/internal/metainfo"
	"light-media/internal/plugins"
	"light-media/internal/plugins/subscribegroup"
	"light-media/internal/sites"
)

type subscribeContinuation struct {
	app       *App
	sub       Subscription
	request   SubscriptionRequest
	adds      atomic.Int32
	gate      atomic.Bool
	entered   chan struct{}
	release   chan struct{}
	enterOnce sync.Once
	freeOnce  sync.Once
	listing   atomic.Value
	seedTask  func(Intent, float64)
}

func subscribeContinuationFixture(t *testing.T) *subscribeContinuation {
	t.Helper()
	return subscribeContinuationConfigFixture(t, subscribeDownloadConfig())
}

func subscribeContinuationConfigFixture(t *testing.T, config subscribegroup.Configuration, configure ...func(*subscribeContinuation)) *subscribeContinuation {
	t.Helper()
	f := &subscribeContinuation{app: subscriptionTestApp(t), entered: make(chan struct{}), release: make(chan struct{})}
	a, ctx := f.app, context.Background()
	materials, hashes, titles := map[string][]byte{}, map[string]string{}, map[string]string{}
	fragments := []string{}
	for episode := 1; episode <= 3; episode++ {
		key := fmt.Sprint(100 + episode)
		title := fmt.Sprintf("Jade.Dynasty.S04E%02d.2160p.WEB-DL.HDR10-FixtureTeam", episode)
		titles[key] = title
		materials[key], hashes[key] = workflowTorrent(title + ".mkv")
		fragments = append(fragments, strings.ReplaceAll(workflowHTML(title, "", true), "id=101", "id="+key))
	}
	var tasksMu sync.Mutex
	tasks := map[string]map[string]any{}
	f.seedTask = func(in Intent, progress float64) {
		tasksMu.Lock()
		defer tasksMu.Unlock()
		task := workflowTask(in.Hash, progress)
		task["save_path"], task["name"] = in.DownloadPath, in.ResourceTitle+".mkv"
		tasks[in.Hash] = task
	}
	qb := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/torrents/info":
			tasksMu.Lock()
			defer tasksMu.Unlock()
			found := []any{}
			for hash, task := range tasks {
				if filter := r.URL.Query().Get("hashes"); filter == "" || filter == hash {
					found = append(found, task)
				}
			}
			workflowJSON(w, found)
		case "/api/v2/torrents/add":
			if err := r.ParseMultipartForm(4 << 20); err != nil {
				t.Error(err)
				http.Error(w, "bad form", 400)
				return
			}
			file, _, err := r.FormFile("torrents")
			if err != nil {
				t.Error(err)
				http.Error(w, "missing material", 400)
				return
			}
			raw, err := io.ReadAll(file)
			file.Close()
			material, parseErr := metainfo.ParseMaterial(raw)
			if err != nil || parseErr != nil {
				t.Error("invalid actual submission material", err, parseErr)
				http.Error(w, "invalid material", 400)
				return
			}
			tasksMu.Lock()
			if _, duplicate := tasks[material.Hash]; duplicate {
				t.Error("same torrent was added twice")
			}
			task := workflowTask(material.Hash, 0)
			task["save_path"] = r.FormValue("savepath")
			task["name"] = material.Name
			tasks[material.Hash] = task
			tasksMu.Unlock()
			f.adds.Add(1)
			io.WriteString(w, "Ok.")
		case "/api/v2/torrents/files":
			tasksMu.Lock()
			defer tasksMu.Unlock()
			if task := tasks[r.URL.Query().Get("hash")]; task != nil {
				workflowJSON(w, []any{map[string]any{"index": 0, "name": task["name"], "size": 1024, "progress": task["progress"], "priority": 1}})
			} else {
				workflowJSON(w, []any{})
			}
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(qb.Close)
	f.listing.Store(keywordFixtureListing(fragments...))
	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/torrents.php":
			if f.gate.Load() {
				f.enterOnce.Do(func() { close(f.entered) })
				select {
				case <-f.release:
				case <-r.Context().Done():
					return
				}
			}
			io.WriteString(w, f.listing.Load().(string))
		case "/download.php":
			if raw := materials[r.URL.Query().Get("id")]; raw != nil {
				w.Write(raw)
			} else {
				http.NotFound(w, r)
			}
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(site.Close)
	t.Cleanup(func() { f.freeOnce.Do(func() { close(f.release) }) })
	settings := a.Settings()
	settings.Downloaders[0].BaseURL = qb.URL
	source, pathErr := filepath.EvalSymlinks(t.TempDir())
	if pathErr != nil {
		t.Fatal(pathErr)
	}
	settings.AllowedRoots = []string{source}
	for i := range settings.Roots {
		settings.Roots[i].Path, pathErr = filepath.EvalSymlinks(settings.Roots[i].Path)
		if pathErr != nil {
			t.Fatal(pathErr)
		}
	}
	settings.Sites = []SiteConfig{{Config: sites.Config{ID: "continuation-site", Name: "合成站点", Kind: "jiayuan", BaseURL: site.URL, Cookie: "synthetic-cookie"}, Enabled: true, DailyBudget: 10000}}
	settings.RSSSites = []string{"continuation-site"}
	if err := a.saveSettings(ctx, settings); err != nil {
		t.Fatal(err)
	}
	if _, err := a.saveResourceMetadataSettings(ctx, 0, resourceMetadataConfiguration{SourceMode: "rust", ReleaseGroups: []string{"FixtureTeam"}}); err != nil {
		t.Fatal(err)
	}
	useBuiltInPluginProcess(t, a)
	cookie := setupTestAdmin(t, a)
	if response := callTest(t, a.Handler(), "POST", "/api/plugins/install", `{"plugin_id":"SubscribeGroup"}`, cookie, ""); response.Code != 200 {
		t.Fatal(response.Body.String())
	}
	subscribeSDKExecute(t, a, subscribeGroupClaim(t, a))
	raw, _ := json.Marshal(map[string]any{"revision": 1, "config": config})
	if response := callTest(t, a.Handler(), "PUT", "/api/plugins/SubscribeGroup/config", string(raw), cookie, ""); response.Code != 200 {
		t.Fatal(response.Body.String())
	}
	subscribeSDKExecute(t, a, subscribeGroupClaim(t, a))
	f.request = subscriptionRequest(t)
	f.request.Keyword, f.request.DownloadPath, f.request.RootID = "Jade Dynasty", settings.AllowedRoots[0], ""
	f.request.Episodes, f.request.TotalEpisodes = []int{1, 2}, 2
	if config.Category {
		media := catalog.Media{ID: f.request.TMDBID, MediaType: "tv", Title: "诛仙", OriginalTitle: "Jade Dynasty", Year: 2022, OriginalLanguage: "zh", OriginCountry: []string{"CN"}, GenreIDs: []int64{16}, CategoryMetadataKnown: true}
		if err := a.Store.Put(ctx, "cache", "media:tv:206484", map[string]any{"at": time.Now(), "data": media}); err != nil {
			t.Fatal(err)
		}
	}
	for _, setup := range configure {
		setup(f)
	}
	var err error
	f.sub, err = a.saveSubscription(ctx, "", f.request)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.enqueue(ctx, "subscription", map[string]any{"id": f.sub.ID, "revision": f.sub.Revision}, "wf_continuation"); err != nil {
		t.Fatal(err)
	}
	check, err := a.claim(ctx)
	if err != nil || check.Kind != "subscription" {
		t.Fatal("missing actual subscription check", err)
	}
	if err = a.safeRun(ctx, check); err != nil {
		t.Fatal("actual subscription did not select resources", err)
	}
	if changed, err := a.finishJob(ctx, check, "succeeded", "", "", time.Now().Unix()); err != nil || !changed {
		t.Fatal("check did not finish", err)
	}
	selected, err := a.Store.List(ctx, "intent", 100, 0)
	if err != nil || len(selected) != 2 {
		t.Fatal("check did not create two actual selected intents", len(selected), err)
	}
	for _, raw := range selected {
		var in Intent
		if json.Unmarshal(raw, &in) != nil || in.SubscriptionRevision != f.sub.Revision || in.SubscriptionID != f.sub.ID || len(in.Episodes) != 1 || in.Hash != hashes[in.ResourceID] || in.ResourceTitle != titles[in.ResourceID] {
			t.Fatal("selected intent not bound to actual subscription/resource", in.ID)
		}
	}
	return f
}

func subscribeContinuationClaim(t *testing.T, f *subscribeContinuation) (Job, Intent) {
	t.Helper()
	job, err := f.app.claim(context.Background())
	var body struct {
		ID string `json:"id"`
	}
	var in Intent
	if err != nil || job.Kind != "download" || json.Unmarshal(job.Payload, &body) != nil || f.app.Store.Get(context.Background(), "intent", body.ID, &in) != nil {
		t.Fatal("missing selected core download", job.Kind, err)
	}
	return job, in
}

func subscribeContinuationRun(t *testing.T, f *subscribeContinuation, job Job) {
	t.Helper()
	ctx := context.Background()
	if err := f.app.safeRun(ctx, job); err != nil {
		t.Fatal("selected core download failed", err)
	}
	if changed, err := f.app.finishJob(ctx, job, "succeeded", "", "", time.Now().Unix()); err != nil || !changed {
		t.Fatal("selected download did not finish", err)
	}
}

func subscribeContinuationEvent(t *testing.T, a *App) (PluginRun, string) {
	t.Helper()
	if err := a.dispatchPluginEvents(context.Background(), time.Now()); err != nil {
		t.Fatal(err)
	}
	run := subscribeGroupClaim(t, a)
	var invocation plugins.Invocation
	var input struct {
		BatchID string `json:"batch_id"`
	}
	if json.Unmarshal(run.Input, &invocation) != nil || json.Unmarshal(invocation.Input, &input) != nil || input.BatchID == "" {
		t.Fatal("accepted event has no bound batch")
	}
	return run, input.BatchID
}

func TestSubscribeGroupFillKeepsActualSelectedIntentBatchUsable(t *testing.T) {
	f := subscribeContinuationFixture(t)
	a, ctx := f.app, context.Background()
	firstJob, first := subscribeContinuationClaim(t, f)
	subscribeContinuationRun(t, f, firstJob)
	secondJob, selected := subscribeContinuationClaim(t, f)
	run, _ := subscribeContinuationEvent(t, a)
	subscribeSDKExecute(t, a, run)
	updated := subscribeGroupRead(t, a, f.sub.ID)
	var continued Intent
	if err := a.Store.Get(ctx, "intent", selected.ID, &continued); err != nil || updated.Revision != f.sub.Revision+1 || continued.SubscriptionRevision != updated.Revision {
		t.Fatal("selected choice was invalidated by internal fill", err)
	}
	unchanged := continued
	unchanged.SubscriptionRevision, unchanged.UpdatedAt = selected.SubscriptionRevision, selected.UpdatedAt
	if !reflect.DeepEqual(unchanged, selected) {
		t.Fatal("internal fill changed approved hash/request/path", continued)
	}
	subscribeContinuationRun(t, f, secondJob)
	if f.adds.Load() != 2 {
		t.Fatal("two selected resources did not reach downloader exactly once", f.adds.Load())
	}
	coverage, err := a.subscriptionCoverage(ctx, updated, []int{1, 2})
	if err != nil || len(coverage.Missing) != 0 || !reflect.DeepEqual(coverage.Reserved, []int{1, 2}) {
		t.Fatal("selected episodes became stranded or unreserved", coverage, err)
	}
	for _, key := range []string{first.ID, selected.ID} {
		var in Intent
		if err := a.Store.Get(ctx, "intent", key, &in); err != nil || in.State != "accepted" {
			t.Fatal("reserved intent is not actually accepted", key, in.State, err)
		}
	}
	before, _ := a.domainEvents(ctx, 0, 100)
	// The submit job can recover after its acceptance committed but before its
	// own completion write. It must not fail the now newer subscription guard.
	if err := a.runDownload(ctx, firstJob, first.ID); err != nil {
		t.Fatal("committed acceptance was not idempotent", err)
	}
	after, _ := a.domainEvents(ctx, 0, 100)
	if f.adds.Load() != 2 || !reflect.DeepEqual(before.Events, after.Events) {
		t.Fatal("committed job replay resubmitted or republished acceptance")
	}
}

func TestSubscribeGroupFillCategoryKeepsAlreadySelectedIntentBatchUsable(t *testing.T) {
	f := subscribeContinuationConfigFixture(t, subscribeGroupConfig("category:国漫#include:Jade"))
	a, ctx := f.app, context.Background()
	firstJob, first := subscribeContinuationClaim(t, f)
	secondJob, second := subscribeContinuationClaim(t, f)
	// Retriable submissions retain the original approval; committed or terminal
	// records must not be rewritten merely because their subscription changed.
	states := []string{"submitting", "reconcile_pending", "accepted", "downloading", "downloaded", "imported", "cancelled"}
	clones := map[string]Intent{}
	for _, state := range states {
		clone := first
		clone.ID, clone.State = "continuation-state-"+state, state
		if err := a.Store.Put(ctx, "intent", clone.ID, clone); err != nil {
			t.Fatal(err)
		}
		clones[clone.ID] = clone
	}
	run, batch := subscribeContinuationEvent(t, a)
	if _, err := a.Store.DB.Exec(`CREATE TRIGGER deny_category_continuation BEFORE INSERT ON plugin_subscribe_group_operations BEGIN SELECT RAISE(ABORT,'synthetic late category receipt failure'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := a.applySubscribeGroupAdded(ctx, run, batch); err == nil {
		t.Fatal("late category receipt failure was hidden")
	}
	for _, selected := range append([]Intent{first, second}, mapIntentValues(clones)...) {
		var unchanged Intent
		if err := a.Store.Get(ctx, "intent", selected.ID, &unchanged); err != nil || !reflect.DeepEqual(unchanged, selected) {
			t.Fatal("category continuation escaped its failed transaction", selected.ID, err)
		}
	}
	if subscribeGroupRead(t, a, f.sub.ID).Revision != f.sub.Revision {
		t.Fatal("category subscription revision escaped failed transaction")
	}
	if _, err := a.pluginStore.Data(ctx, "SubscribeGroup", "history"); !missingPluginValue(err) {
		t.Fatal("category history escaped failed transaction", err)
	}
	if _, err := a.Store.DB.Exec(`DROP TRIGGER deny_category_continuation`); err != nil {
		t.Fatal(err)
	}
	// The exact persisted event now runs in the actual Go worker process.
	subscribeSDKExecute(t, a, run)
	updated := subscribeGroupRead(t, a, f.sub.ID)
	if updated.Revision != f.sub.Revision+1 || updated.Policy.FilterParams == nil || updated.Policy.FilterParams.Include == nil || *updated.Policy.FilterParams.Include != "Jade" {
		t.Fatal("category source rule was not applied", updated.Revision)
	}
	for _, selected := range append([]Intent{first, second}, mapIntentValues(clones)...) {
		var continued Intent
		if err := a.Store.Get(ctx, "intent", selected.ID, &continued); err != nil {
			t.Fatal(err)
		}
		want := selected
		if selected.State == "planned" || selected.State == "submitting" || selected.State == "reconcile_pending" {
			want.SubscriptionRevision = updated.Revision
		}
		if !reflect.DeepEqual(continued, want) {
			t.Fatal("category changed selected material or wrong lifecycle state", selected.ID)
		}
	}
	subscribeContinuationRun(t, f, firstJob)
	subscribeContinuationRun(t, f, secondJob)
	if f.adds.Load() != 2 {
		t.Fatal("category stranded previously selected downloads", f.adds.Load())
	}
}

func mapIntentValues(values map[string]Intent) []Intent {
	out := make([]Intent, 0, len(values))
	for _, value := range values {
		out = append(out, value)
	}
	return out
}

func TestSubscribeGroupFillIntentContinuationRollsBackWithLateReceiptFailure(t *testing.T) {
	f := subscribeContinuationFixture(t)
	a, ctx := f.app, context.Background()
	firstJob, _ := subscribeContinuationClaim(t, f)
	subscribeContinuationRun(t, f, firstJob)
	secondJob, selected := subscribeContinuationClaim(t, f)
	run, batch := subscribeContinuationEvent(t, a)
	if _, err := a.Store.DB.Exec(`CREATE TRIGGER deny_continuation_receipt BEFORE INSERT ON plugin_subscribe_group_operations BEGIN SELECT RAISE(ABORT,'synthetic late receipt failure'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := a.applySubscribeGroupDownload(ctx, run, batch); err == nil {
		t.Fatal("late continuation receipt failure was hidden")
	}
	var unchanged Intent
	if err := a.Store.Get(ctx, "intent", selected.ID, &unchanged); err != nil || !reflect.DeepEqual(unchanged, selected) || subscribeGroupRead(t, a, f.sub.ID).Revision != f.sub.Revision {
		t.Fatal("intent/subscription revisions escaped failed transaction", err)
	}
	for _, key := range []string{"history", "history_handle"} {
		if _, err := a.pluginStore.Data(ctx, "SubscribeGroup", key); !missingPluginValue(err) {
			t.Fatal("plugin KV escaped failed continuation", key, err)
		}
	}
	if _, err := a.Store.DB.Exec(`DROP TRIGGER deny_continuation_receipt`); err != nil {
		t.Fatal(err)
	}
	result, err := a.applySubscribeGroupDownload(ctx, run, batch)
	if err != nil {
		t.Fatal("same exact continuation could not retry", err)
	}
	if err = finishClaimedFixtureRun(ctx, a.pluginStore, run, "succeeded", "", result); err != nil {
		t.Fatal(err)
	}
	subscribeContinuationRun(t, f, secondJob)
	if f.adds.Load() != 2 {
		t.Fatal("retry did not retain unique selected submissions")
	}
}

func TestSubscribeGroupFillDoesNotBlessIntentsInvalidatedByUserEdit(t *testing.T) {
	f := subscribeContinuationFixture(t)
	a, ctx := f.app, context.Background()
	firstJob, _ := subscribeContinuationClaim(t, f)
	subscribeContinuationRun(t, f, firstJob)
	oldJob, old := subscribeContinuationClaim(t, f)
	edit := f.request
	edit.Revision, edit.Keyword = f.sub.Revision, "user-edited-query"
	edit.Episodes, edit.TotalEpisodes, edit.AllowEarly = []int{1, 2, 3}, 3, []int{3}
	current, err := a.saveSubscription(ctx, f.sub.ID, edit)
	if err != nil {
		t.Fatal(err)
	}
	preview, err := a.previewDownload(subscriptionContext(ctx, current.ID, current.Revision), PreviewRequest{SearchRequest: SearchRequest{Query: "Jade Dynasty", MediaType: "tv", TMDBID: current.Media.ID, Season: 4, Episodes: []int{3}, AllowEarly: []int{3}}, SiteID: "continuation-site", ResourceID: "103", DownloaderID: current.DownloaderID, DownloadPath: current.DownloadPath, SubscriptionID: current.ID, Revision: current.Revision}, true)
	if err != nil {
		t.Fatal("new current revision could not select resource", err)
	}
	fresh, err := a.confirmDownload(ctx, preview.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	run, _ := subscribeContinuationEvent(t, a)
	subscribeSDKExecute(t, a, run)
	var kept, moved Intent
	if err = a.Store.Get(ctx, "intent", old.ID, &kept); err != nil || kept.SubscriptionRevision != old.SubscriptionRevision {
		t.Fatal("plugin blessed older user-invalidated revision", err)
	}
	if err = a.Store.Get(ctx, "intent", fresh.ID, &moved); err != nil || moved.SubscriptionRevision != current.Revision+1 {
		t.Fatal("plugin did not preserve newly selected current revision", err)
	}
	if err = a.runDownload(ctx, oldJob, old.ID); pluginErrorCode(err) != "REVIEW_SUBSCRIPTION_CHANGED" || f.adds.Load() != 1 {
		t.Fatal("unsubmitted obsolete choice bypassed subscription guard", err)
	}
	if _, err = a.finishJob(ctx, oldJob, "failed", "REVIEW_SUBSCRIPTION_CHANGED", "test user edit", time.Now().Unix()); err != nil {
		t.Fatal(err)
	}
	// The source now also queues completion preparation after actual Add.
	// Claim this already selected real download, rather than assuming it is the
	// only job in the generic queue.
	freshJob := qualityRunningJob(t, a, reorgJob(t, a, "download", fresh.ID))
	var choice Intent
	if err = a.Store.Get(ctx, "intent", fresh.ID, &choice); err != nil {
		t.Fatal(err)
	}
	if choice.ID != fresh.ID {
		t.Fatal("wrong current selection claimed")
	}
	subscribeContinuationRun(t, f, freshJob)
	if f.adds.Load() != 2 {
		t.Fatal("current selected revision did not continue")
	}
	if _, err = a.confirmDownload(ctx, preview.ID, false); pluginErrorCode(err) != "SUBSCRIPTION_CHANGED" {
		t.Fatal("obsolete preview created another reservation", err)
	}
}

func TestSubscribeGroupFillWaitsForRunningSelectedDownloadContext(t *testing.T) {
	f := subscribeContinuationFixture(t)
	a, ctx := f.app, context.Background()
	firstJob, _ := subscribeContinuationClaim(t, f)
	subscribeContinuationRun(t, f, firstJob)
	secondJob, _ := subscribeContinuationClaim(t, f)
	f.gate.Store(true)
	coreDone := make(chan error, 1)
	coreExited := make(chan struct{})
	var pluginExited <-chan struct{}
	t.Cleanup(func() {
		f.freeOnce.Do(func() { close(f.release) })
		select {
		case <-coreExited:
		case <-time.After(5 * time.Second):
			t.Error("core fixture did not stop")
		}
		if pluginExited != nil {
			select {
			case <-pluginExited:
			case <-time.After(5 * time.Second):
				t.Error("plugin fixture did not stop")
			}
		}
	})
	go func() { defer close(coreExited); coreDone <- a.safeRun(ctx, secondJob) }()
	select {
	case <-f.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("second selected download did not enter network recheck")
	}
	run, _ := subscribeContinuationEvent(t, a)
	pluginDone := make(chan struct{})
	pluginExited = pluginDone
	go func() { a.executePluginRun(ctx, run); close(pluginDone) }()
	deadline := time.Now().Add(5 * time.Second)
	for a.pluginMu.TryLock() {
		a.pluginMu.Unlock()
		if time.Now().After(deadline) {
			t.Fatal("actual plugin did not reach its host operation")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if current := subscribeGroupRead(t, a, f.sub.ID); current.Revision != f.sub.Revision {
		t.Fatal("plugin invalidated a live selected download context")
	}
	f.freeOnce.Do(func() { close(f.release) })
	select {
	case err := <-coreDone:
		if err != nil {
			t.Fatal("live core context was rejected or overwritten", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("core download did not finish")
	}
	select {
	case <-pluginDone:
	case <-time.After(5 * time.Second):
		t.Fatal("plugin did not finish after selected download released its lock")
	}
	finished, err := a.pluginStore.Run(ctx, run.ID)
	if err != nil || finished.State != "succeeded" || f.adds.Load() != 2 || subscribeGroupRead(t, a, f.sub.ID).Revision != f.sub.Revision+1 {
		t.Fatal("running context continuation failed", finished.State, finished.ErrorCode, f.adds.Load(), err)
	}
	if changed, err := a.finishJob(ctx, secondJob, "succeeded", "", "", time.Now().Unix()); err != nil || !changed {
		t.Fatal("plugin changed the running download job generation", err)
	}
}
