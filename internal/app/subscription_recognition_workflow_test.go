package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"light-media/internal/catalog"
	"light-media/internal/decision"
	"light-media/internal/download"
	"light-media/internal/metainfo"
	"light-media/internal/priorityrules"
	"light-media/internal/releasemeta"
	"light-media/internal/sites"
)

func TestSubscriptionRecognitionRawReplacementAndConcurrentIsolation(t *testing.T) {
	a, ctx := subscriptionTestApp(t), context.Background()
	if err := a.saveRecognitionWords(ctx, 0, []string{"Raw.Show => Global.Show"}); err != nil {
		t.Fatal(err)
	}
	request := subscriptionRequest(t)
	var sub Subscription
	for _, raw := range []string{"Raw.Show => Local.Show\n\n  ", " ", "\n", ""} {
		request.Revision, request.CustomWords = sub.Revision, subscriptionWordsRaw(t, raw)
		var err error
		sub, err = a.saveSubscription(ctx, sub.ID, request)
		if err != nil {
			t.Fatal(err)
		}
		scoped, err := a.subscriptionRecognitionContext(ctx, sub.ID, sub.Revision)
		if err != nil {
			t.Fatal(err)
		}
		want := strings.Split(raw, "\n")
		if raw == "" {
			want = []string{"Raw.Show => Global.Show"}
		}
		actual := scoped.Value(resourceRecognitionKey{}).(resourceRecognition)
		if !reflect.DeepEqual(actual.options.CustomWords, want) {
			t.Fatal("raw local words were trimmed, merged or filtered")
		}
		prepared := a.prepareRecognitionTitleContext(scoped, "Raw.Show.S04E01")
		if raw == "" && prepared != "Global.Show.S04E01" || raw == "Raw.Show => Local.Show\n\n  " && prepared != "Local.Show.S04E01" || (raw == " " || raw == "\n") && prepared != "Raw.Show.S04E01" {
			t.Fatal("source nonempty replacement/fallback semantics changed", prepared)
		}
	}
	left, err := withResourceRecognition(ctx, releasemeta.Options{CustomWords: []string{"Raw.Show => Left.Show"}})
	if err != nil {
		t.Fatal(err)
	}
	right, err := withResourceRecognition(ctx, releasemeta.Options{CustomWords: []string{"Raw.Show => Right.Show"}})
	if err != nil {
		t.Fatal(err)
	}
	var workers sync.WaitGroup
	for _, item := range []struct {
		ctx  context.Context
		want string
	}{{left, "Left.Show.S04E01"}, {right, "Right.Show.S04E01"}, {ctx, "Global.Show.S04E01"}} {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for range 40 {
				if got := a.prepareRecognitionTitleContext(item.ctx, "Raw.Show.S04E01"); got != item.want {
					t.Error("one subscription replaced another's matcher", got)
					return
				}
			}
		}()
	}
	workers.Wait()
	if _, err := a.subscriptionRecognitionContext(ctx, sub.ID, sub.Revision-1); pluginErrorCode(err) != "REVIEW_SUBSCRIPTION_CHANGED" {
		t.Fatal("local words escaped subscription revision guard", err)
	}
}

func TestSubscriptionRecognitionPriorityFilterUsesGlobalsWhileSortAndPackUseLocalScope(t *testing.T) {
	a, ctx := subscriptionTestApp(t), context.Background()
	if err := a.saveRecognitionWords(ctx, 0, []string{"S04E01-E04 => S04E01-E03"}); err != nil {
		t.Fatal(err)
	}
	options := releasemeta.Options{SourceMode: "rust", CustomWords: []string{"Alias => Jade.Dynasty", "S04E01-E04 => S04E01-E02"}}
	ctx, err := withResourceRecognition(ctx, options)
	if err != nil {
		t.Fatal(err)
	}
	identity := catalog.Media{ID: 206484, MediaType: "tv", Title: "诛仙", OriginalTitle: "Jade Dynasty", Year: 2022}
	resource := sites.Resource{ID: "four", Title: "Alias.S04E01-E04.2160p.WEB-DL", Size: 4 << 30}
	positive := sites.Resource{ID: "two", Title: "Alias.S04E01-E02.2160p.WEB-DL", Size: 4 << 30}
	globalCount, globalErr := a.priorityEpisodeCount(resource)
	localCount, localErr := a.priorityEpisodeCountContext(ctx, resource)
	if globalErr != nil || localErr != nil || globalCount != 3 || localCount != 2 {
		t.Fatal("fixture must distinguish global filter count from local candidate count", globalCount, localCount, globalErr, localErr)
	}
	settings := a.Settings()
	settings.PriorityRules = &priorityrules.Configuration{Mode: priorityrules.Rust, Groups: []priorityrules.Group{{Name: "scope", RuleString: priorityText("ScopeSize")}}, CustomRules: []priorityrules.Definition{{"id": "ScopeSize", "name": "scope", "include": "Alias", "size_range": ">1500"}}}
	in := SearchRequest{priorityMedia: &identity, Policy: Policy{FilterGroups: []string{"scope"}}}
	filter, err := a.preparePriorityFilter(ctx, in, settings)
	if err != nil {
		t.Fatal(err)
	}
	out, err := filter.apply(ctx, a, SearchOutput{Candidates: []Candidate{{Resource: resource, Accepted: true}, {Resource: positive, Accepted: true}}})
	if err != nil || len(out.Candidates) != 1 || out.Candidates[0].Resource.ID != "two" || out.Candidates[0].Resource.Title != positive.Title {
		t.Fatal("priority size resolver inherited local words or changed raw include text", err)
	}
	out, err = filter.apply(context.Background(), a, SearchOutput{Candidates: []Candidate{{Resource: resource, Accepted: true}, {Resource: positive, Accepted: true}}})
	if err != nil || len(out.Candidates) != 1 || out.Candidates[0].Resource.ID != "two" {
		t.Fatal("priority filtering did not use the same global scope with and without a local candidate context", err)
	}
	sorter, err := a.preparePrioritySorter(ctx, in, settings)
	if err != nil {
		t.Fatal(err)
	}
	candidates := []Candidate{{Resource: resource, Accepted: true}, {Resource: sites.Resource{ID: "three", Title: "Jade.Dynasty.S04E01-E03"}, Accepted: true}}
	sorter.sort(a, candidates, Policy{})
	if candidates[0].Resource.ID != "three" {
		t.Fatal("priority sorter reparsed with the global matcher")
	}
	target := decision.Target{Media: identity, Season: 4, Episodes: []int{1, 2}, KnownEpisodes: []int{1, 2}, AiredEpisodes: []int{1, 2}, Auto: true}
	pack := sites.Resource{Title: "Alias.S04.2160p.WEB-DL", Size: 1 << 30}
	if !a.eligibleAutomaticSeasonPackContext(ctx, pack, target, Policy{}) || a.eligibleAutomaticSeasonPack(pack, target, Policy{}) {
		t.Fatal("automatic pack eligibility lost its selection scope")
	}
	material := metainfo.Material{Files: []metainfo.File{{Path: "Alias.S04E01.mkv", Size: 1024}, {Path: "Alias.S04E02.mkv", Size: 1024}}}
	if err = a.validateAutomaticSeasonPackFilesContext(ctx, material, target, "scope"); err != nil {
		t.Fatal("actual pack files ignored local recognition", err)
	}
	if err = a.validateAutomaticSeasonPackFiles(material, target, "scope"); err == nil {
		t.Fatal("pack fixture no longer distinguishes local recognition")
	}
}

func TestSubscriptionRecognitionActualSelectionContextSurvivesRestartAndRecheck(t *testing.T) {
	for _, existingHash := range []bool{false, true} {
		t.Run(map[bool]string{false: "new_add", true: "existing_hash"}[existingHash], func(t *testing.T) {
			ctx := context.Background()
			// Use a no-op local list to suppress a global episode-changing rule.
			// The complete original titles/files must pass real automatic checks.
			local := "# synthetic-private-selected-word\nJade[.]Dynasty => Jade.Dynasty\n\n "
			f := newRecognitionSelectionFixture(t, func(f *recognitionSelectionFixture) {
				f.request.CustomWords = subscriptionWordsRaw(t, local)
				if err := f.app.saveRecognitionWords(ctx, 0, []string{"S04E0[12] => S04E01-E04"}); err != nil {
					t.Fatal(err)
				}
				// Source FilterModule size_range independently parses global words:
				// 1.5 GiB / 4 is below 500 MiB. Local candidate words keep one
				// actual episode, whose 1.5 GiB would reject both selections if
				// that local Context were incorrectly reused for the denominator.
				settings := f.app.Settings()
				settings.PriorityRules = &priorityrules.Configuration{Mode: priorityrules.Rust, Groups: []priorityrules.Group{{Name: "global-size", RuleString: priorityText("GlobalSize")}}, CustomRules: []priorityrules.Definition{{"id": "GlobalSize", "name": "global size denominator", "include": "Jade.Dynasty", "size_range": "<500"}}}
				if err := f.app.saveSettings(ctx, settings); err != nil {
					t.Fatal(err)
				}
				f.request.Policy.FilterGroups = []string{"global-size"}
				if _, err := f.app.saveResourceMetadataSettings(ctx, 1, resourceMetadataConfiguration{SourceMode: "rust", ReleaseGroups: []string{"FixtureTeam"}, Customization: []string{"FrozenMarker"}}); err != nil {
					t.Fatal(err)
				}
			})
			a := f.app
			selected, err := a.Store.List(ctx, "intent", 10, 0)
			if err != nil || len(selected) != 2 {
				t.Fatal("actual subscription did not select both episodes with local words", err)
			}
			original := map[string]downloadContextSnapshot{}
			for _, raw := range selected {
				var in Intent
				if json.Unmarshal(raw, &in) != nil {
					t.Fatal("bad selected intent")
				}
				source, err := a.loadDownloadContext(ctx, "intent", in.ID, in.Hash)
				if err != nil || source == nil || !reflect.DeepEqual(source.Options.CustomWords, strings.Split(local, "\n")) || source.Options.SourceMode != "rust" || !reflect.DeepEqual(source.Options.ReleaseGroups, []string{"FixtureTeam"}) || !reflect.DeepEqual(source.Options.Customization, []string{"FrozenMarker"}) {
					t.Fatal("check/search/preview/confirm did not freeze complete selected options", err)
				}
				original[in.ID] = *source
				var preview DownloadPreview
				if err := a.Store.Get(ctx, "download_preview", in.PreviewID, &preview); err != nil {
					t.Fatal(err)
				}
				public, _ := json.Marshal(preview)
				if bytes.Contains(raw, []byte("synthetic-private-selected-word")) || bytes.Contains(public, []byte("synthetic-private-selected-word")) || bytes.Contains(raw, []byte("custom_words")) || bytes.Contains(public, []byte("custom_words")) {
					t.Fatal("private selected word list leaked into ordinary intent or preview/request")
				}
			}
			if err := a.saveRecognitionWords(ctx, 1, []string{"S04E0[12] => S04E99"}); err != nil {
				t.Fatal(err)
			}
			if _, err := a.saveResourceMetadataSettings(ctx, 2, resourceMetadataConfiguration{SourceMode: "legacy", ReleaseGroups: []string{"ChangedTeam"}, Customization: []string{"ChangedMarker"}}); err != nil {
				t.Fatal(err)
			}
			f.listing.Store(strings.ReplaceAll(f.listing.Load().(string), ".HDR10-", ".HDR10.FrozenMarker-"))
			dir := a.Dir
			if err := a.Close(); err != nil {
				t.Fatal(err)
			}
			a, err = New(Options{DataDir: dir})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = a.Close() })
			f.app = a
			firstJob, first := recognitionSelectionClaim(t, f)
			if existingHash {
				f.seedTask(first, 0)
			}
			recognitionSelectionRun(t, f, firstJob)
			secondJob, second := recognitionSelectionClaim(t, f)
			recognitionSelectionRun(t, f, secondJob)
			wantAdds := int32(2)
			if existingHash {
				wantAdds = 1
			}
			if f.adds.Load() != wantAdds {
				t.Fatal("restart/recheck did not preserve unique selected downloads", f.adds.Load())
			}
			page, err := a.domainEvents(ctx, 0, 100)
			if err != nil {
				t.Fatal(err)
			}
			facts := 0
			for _, event := range page.Events {
				if event.Type != "download.accepted" {
					continue
				}
				snapshot, err := a.downloadAddedSnapshot(ctx, event)
				if err != nil || snapshot.Context == nil || !reflect.DeepEqual(snapshot.Context.Options, original[event.IntentID].Options) {
					t.Fatal("accepted fact lost complete chosen options", err)
				}
				wantTitle := original[event.IntentID].Title
				if event.IntentID != first.ID || !existingHash {
					wantTitle = strings.ReplaceAll(wantTitle, ".HDR10-", ".HDR10.FrozenMarker-")
				}
				if snapshot.Context.Title != wantTitle {
					t.Fatal("fresh source title or existing-hash snapshot was replaced")
				}
				facts++
			}
			if facts != 2 {
				t.Fatal("both actual accepted facts were not committed", facts)
			}
			// Completed file parsing also follows the selected snapshot, after
			// the live global words and metadata options changed.
			if err := a.Store.Get(ctx, "intent", second.ID, &second); err != nil {
				t.Fatal(err)
			}
			f.seedTask(second, 1)
			files, err := a.downloadFiles(ctx, second.ID)
			if err != nil || len(files.Files) != 1 || files.Files[0].Parsed.Original == nil || !reflect.DeepEqual(files.Files[0].Parsed.Original.Episodes, second.Episodes) {
				t.Fatal("downloaded file review reverted to current global numbering", err)
			}
			renaming, scraping := true, false
			if _, err := a.saveManagedDirectory(ctx, DirectoryRule{Name: "owned fixture", SourcePath: second.DownloadPath, RootID: a.Settings().Roots[0].ID, MediaType: "tv", Mode: "hardlink", MonitorType: "monitor", Enabled: true, Renaming: &renaming, Scraping: &scraping}); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(files.Files[0].ApplicationPath, make([]byte, 1024), 0600); err != nil {
				t.Fatal(err)
			}
			d, err := a.downloader(second.DownloaderID)
			if err != nil {
				t.Fatal(err)
			}
			client, err := download.New(d.Config)
			if err != nil {
				t.Fatal(err)
			}
			if err = a.planCompletedDownload(ctx, client, d, &second, files.Task); err != nil {
				t.Fatal("completed download could not plan selected local numbering", err)
			}
			var plan ImportPlan
			if err = a.Store.Get(ctx, "import_plan", second.ImportPlanID, &plan); err != nil || len(plan.Files) != 1 || !reflect.DeepEqual(plan.Files[0].Episodes, second.Episodes) {
				t.Fatal("import plan lost selected local numbering", err)
			}
			// A manual/native request with the same TMDB ID owns current global
			// words, even while the stored subscription still has local words.
			manual := PreviewRequest{SearchRequest: SearchRequest{Query: "Jade Dynasty", MediaType: "tv", TMDBID: f.sub.Media.ID, Season: 4, Episodes: []int{3}, AllowEarly: []int{3}}, SiteID: "continuation-site", ResourceID: "103", DownloaderID: second.DownloaderID, DownloadPath: second.DownloadPath}
			globalRev, _, err := a.recognitionWords(ctx)
			if err != nil || a.saveRecognitionWords(ctx, globalRev, []string{"S04E03 => S04E99"}) != nil {
				t.Fatal("could not set distinct manual global words", err)
			}
			// Manual previews allow a numbering-review decision to reach actual
			// file coverage. Current globals rewrite E03 to E99 there; borrowing
			// this subscription's no-op list would incorrectly make it succeed.
			if _, err = a.previewDownload(ctx, manual, false); pluginErrorCode(err) != "REVIEW_TORRENT_COVERAGE" {
				t.Fatal("manual preview borrowed local subscription words", err)
			}
			native, err := a.addNativeDownload(ctx, NativeDownloadRequest{SiteID: manual.SiteID, ResourceID: manual.ResourceID, Query: manual.Query, DownloaderID: manual.DownloaderID, DownloadPath: manual.DownloadPath, MediaType: "tv", TMDBID: manual.TMDBID})
			if err != nil {
				t.Fatal("native request could not use its own current globals", err)
			}
			nativeContext, err := a.loadDownloadContext(ctx, "native_download", native.ID, native.Hash)
			if err != nil || nativeContext == nil || !reflect.DeepEqual(nativeContext.Options.CustomWords, []string{"S04E03 => S04E99"}) || nativeContext.Options.SourceMode != "legacy" || !reflect.DeepEqual(nativeContext.Options.ReleaseGroups, []string{"ChangedTeam"}) {
				t.Fatal("native request inherited a matching subscription's context", err)
			}
		})
	}
}

func TestIntentRecognitionPreservesExplicitEmptyExtensionsAndLegacyGlobalFallback(t *testing.T) {
	a, ctx := subscriptionTestApp(t), context.Background()
	options := releasemeta.Options{SourceMode: "rust", CustomWords: []string{"Raw => Frozen"}, ReleaseGroups: []string{"FrozenTeam"}, Customization: []string{"FrozenMarker"}, MediaExts: []string{}}
	scoped, err := withResourceRecognition(ctx, options)
	if err != nil {
		t.Fatal(err)
	}
	_, hash := workflowTorrent("Raw.S04E01.mkv")
	source, err := a.prepareDownloadContext(scoped, "intent", "scope-fixture", hash, sites.Resource{Title: "Raw.S04E01.mkv"})
	if err != nil {
		t.Fatal(err)
	}
	tx, err := a.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err = a.saveDownloadContextTx(ctx, tx, source); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	loaded, err := a.intentRecognitionContext(ctx, Intent{ID: source.DownloadID, Hash: hash})
	if err != nil || !reflect.DeepEqual(loaded.Value(resourceRecognitionKey{}).(resourceRecognition).options, options) {
		t.Fatal("private context changed nil versus explicit empty options", err)
	}
	if err = a.saveRecognitionWords(ctx, 0, []string{"Raw => CurrentGlobal"}); err != nil {
		t.Fatal(err)
	}
	legacy, err := a.intentRecognitionContext(ctx, Intent{ID: "pre-producer", Hash: hash})
	if err != nil || a.prepareRecognitionTitleContext(legacy, "Raw.S04E01") != "CurrentGlobal.S04E01" {
		t.Fatal("legacy intent did not retain documented global fallback", err)
	}
	if _, err = a.intentRecognitionContext(ctx, Intent{ID: source.DownloadID, Hash: strings.Repeat("b", 40)}); err == nil {
		t.Fatal("selected context could be rebound to another hash")
	}
}

// Neutral HTTP/core portion of the former continuation fixture. No plugin
// setup, worker or event consumer participates in this core recognition test.
type recognitionSelectionFixture struct {
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

func newRecognitionSelectionFixture(t *testing.T, configure ...func(*recognitionSelectionFixture)) *recognitionSelectionFixture {
	t.Helper()
	f := &recognitionSelectionFixture{app: subscriptionTestApp(t), entered: make(chan struct{}), release: make(chan struct{})}
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
	f.request = subscriptionRequest(t)
	f.request.Keyword, f.request.DownloadPath, f.request.RootID = "Jade Dynasty", settings.AllowedRoots[0], ""
	f.request.Episodes, f.request.TotalEpisodes = []int{1, 2}, 2
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

func recognitionSelectionClaim(t *testing.T, f *recognitionSelectionFixture) (Job, Intent) {
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

func recognitionSelectionRun(t *testing.T, f *recognitionSelectionFixture, job Job) {
	t.Helper()
	ctx := context.Background()
	if err := f.app.safeRun(ctx, job); err != nil {
		t.Fatal("selected core download failed", err)
	}
	if changed, err := f.app.finishJob(ctx, job, "succeeded", "", "", time.Now().Unix()); err != nil || !changed {
		t.Fatal("selected download did not finish", err)
	}
}
