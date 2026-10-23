package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"light-media/internal/catalog"
	"light-media/internal/plugins"
	"light-media/internal/plugins/subscribegroup"
	"light-media/internal/sites"
)

// This fixture uses the private host adapter, not the SDK/worker catalog:
// SubscribeGroup remains unavailable until its download producer and UI exist.
func subscribeGroupInstall(t *testing.T, a *App, config any) PluginRun {
	t.Helper()
	_, err := a.pluginStore.InstallAndQueue(context.Background(), PluginInstallation{
		InstanceID: "SubscribeGroup", PluginID: "SubscribeGroup", Version: "2.8.7", ManifestSchemaVersion: 1,
		Manifest: json.RawMessage(`{"plugin_id":"SubscribeGroup","name":"订阅规则","runtime":"go-executable"}`),
	}, config)
	if err != nil {
		t.Fatal(err)
	}
	return subscribeGroupClaim(t, a)
}
func subscribeGroupClaim(t *testing.T, a *App) PluginRun {
	t.Helper()
	run, err := a.pluginStore.ClaimRun(context.Background())
	if err != nil || run.InstanceID != "SubscribeGroup" {
		t.Fatal("missing actual invocation", run, err)
	}
	return run
}
func subscribeGroupConfigure(t *testing.T, a *App, run PluginRun) PluginRun {
	t.Helper()
	claimedRun := run
	result, err := a.configureSubscribeGroup(context.Background(), &run)
	if err != nil {
		t.Fatal(err)
	}
	if err = finishClaimedFixtureRun(context.Background(), a.pluginStore, claimedRun, "succeeded", "", result); err != nil {
		t.Fatal(err)
	}
	return run
}
func subscribeGroupConfig(rule string) subscribegroup.Configuration {
	return subscribegroup.Configuration{Category: true, UpdateConfs: &rule}
}
func subscribeGroupCreate(t *testing.T, a *App) (Subscription, PluginRun, string) {
	return subscribeGroupCreateKnown(t, a, true)
}
func subscribeGroupCreateKnown(t *testing.T, a *App, known bool) (Subscription, PluginRun, string) {
	t.Helper()
	ctx := context.Background()
	media := catalog.Media{ID: 206484, MediaType: "tv", Title: "诛仙", OriginalTitle: "Jade Dynasty", Year: 2022, OriginalLanguage: "zh", OriginCountry: []string{"CN"}, GenreIDs: []int64{16}, CategoryMetadataKnown: known}
	if err := a.Store.Put(ctx, "cache", "media:tv:206484", map[string]any{"at": time.Now(), "data": media}); err != nil {
		t.Fatal(err)
	}
	request := subscriptionRequest(t)
	// Use an explicit owned directory, as the UI does. No downloader RPC is
	// needed after restarting with shared-container-path settings.
	settings := a.Settings()
	if len(settings.AllowedRoots) == 0 {
		settings.AllowedRoots = []string{t.TempDir()}
		if err := a.saveSettings(ctx, settings); err != nil {
			t.Fatal(err)
		}
	}
	request.DownloadPath = settings.AllowedRoots[0]
	request.RootID = ""
	request.Policy = Policy{Resolution: "1080p", Source: "BluRay", Codec: "h264", HDR: "sdr", Include: "old", Exclude: "legacy"}
	sub, err := a.saveSubscription(ctx, "", request)
	if err != nil {
		t.Fatal(err)
	}
	if err = a.dispatchPluginEvents(ctx, time.Now()); err != nil {
		t.Fatal(err)
	}
	run := subscribeGroupClaim(t, a)
	var invocation plugins.Invocation
	var input struct {
		BatchID string `json:"batch_id"`
	}
	if json.Unmarshal(run.Input, &invocation) != nil || json.Unmarshal(invocation.Input, &input) != nil || input.BatchID == "" {
		t.Fatal("missing bound event batch")
	}
	return sub, run, input.BatchID
}
func subscribeGroupRead(t *testing.T, a *App, id string) Subscription {
	t.Helper()
	var sub Subscription
	if err := a.Store.Get(context.Background(), "subscription", id, &sub); err != nil {
		t.Fatal(err)
	}
	return sub
}

func TestSubscribeGroupAddedActualOutboxFieldsHistoryAndSearch(t *testing.T) {
	a := subscriptionTestApp(t)
	ctx := context.Background()
	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/torrents.php" {
			t.Error("unexpected material request", r.URL.Path)
		}
		io.WriteString(w, keywordFixtureListing(
			strings.ReplaceAll(workflowHTML("Jade.Dynasty.S04E01.2160p.WEB-DL.HDR10", "", true), "id=101", "id=1"),
			strings.ReplaceAll(workflowHTML("Jade.Dynasty.S04E01.1080p.BluRay.SDR", "", true), "id=101", "id=2")))
	}))
	defer site.Close()
	c := a.Settings()
	c.Sites = []SiteConfig{{Config: sites.Config{ID: "fixture", Name: "合成站", Kind: "jiayuan", BaseURL: site.URL, Cookie: "synthetic-cookie"}, Enabled: true, DailyBudget: 100}}
	if err := a.saveSettings(ctx, c); err != nil {
		t.Fatal(err)
	}
	subscribeGroupConfigure(t, a, subscribeGroupInstall(t, a, subscribeGroupConfig("category:国漫#include:Jade#exclude:NEVER#resolution:2160p#quality:WEB-DL#effect:HDR10#sites:合成站,合成站#savepath:/downloads/{name}")))
	// Unknown/mixed legacy entries must survive the append without coercion.
	legacy := json.RawMessage(`[{"unknown":{"private":"synthetic-history-private"},"name":17},null,"old"]`)
	if _, err := a.pluginStore.SaveData(ctx, "SubscribeGroup", "history", 0, legacy); err != nil {
		t.Fatal(err)
	}
	sub, run, batch := subscribeGroupCreate(t, a)
	job, err := a.enqueue(ctx, "subscription", map[string]any{"id": sub.ID, "revision": sub.Revision}, "synthetic-old-job")
	if err != nil {
		t.Fatal(err)
	}
	result, err := a.applySubscribeGroupAdded(ctx, run, batch)
	if err != nil {
		t.Fatal(err)
	}
	updated := subscribeGroupRead(t, a, sub.ID)
	p := updated.Policy.FilterParams
	if updated.Revision != sub.Revision+1 || p == nil || *p.Resolution != "4K|2160p|x2160" || *p.Quality != "WEB-?DL|WEB-?RIP" || *p.Effect != "[\\s.]+HDR[\\s.]+|HDR10|HDR10\\+" || *p.Include != "Jade" || *p.Exclude != "NEVER" || updated.Policy.Source != "" || updated.Policy.Codec != "" || updated.Policy.Include != "" || updated.DownloadPath != "/downloads/诛仙 (2022)" || updated.RootID != "" || updated.DirectoryID != "" || updated.DownloaderID != sub.DownloaderID || !reflect.DeepEqual(updated.SiteIDs, []string{"fixture", "fixture"}) {
		t.Fatal("actual subscription differs from source category override", updated)
	}
	var state string
	var generation int
	if err := a.Store.DB.QueryRow(`SELECT state,generation FROM jobs WHERE id=?`, job.ID).Scan(&state, &generation); err != nil || state != "cancelled" || generation != 1 {
		t.Fatal("old job retained stale policy", state, generation, err)
	}
	history, err := a.pluginStore.Data(ctx, "SubscribeGroup", "history")
	var entries []json.RawMessage
	if err != nil || json.Unmarshal(history.Value, &entries) != nil || len(entries) != 4 {
		t.Fatal("history missing", err)
	}
	var old []json.RawMessage
	json.Unmarshal(legacy, &old)
	for i := range old {
		if !bytes.Equal(entries[i], old[i]) {
			t.Fatal("legacy history rewritten", i)
		}
	}
	var entry subscribegroup.History
	if json.Unmarshal(entries[3], &entry) != nil || entry.Name != "诛仙" || entry.Type != "二级分类自定义配置 国漫" || !strings.Contains(entry.Content, `"save_path": "/downloads/`) {
		t.Fatal("source history missing", entry)
	}
	var sealed []byte
	if err := a.Store.DB.QueryRow(`SELECT sealed FROM plugin_values WHERE instance_id='SubscribeGroup' AND item_key='history'`).Scan(&sealed); err != nil || bytes.Contains(sealed, []byte("synthetic-history-private")) || json.Valid(sealed) {
		t.Fatal("history stored in plaintext", err)
	}
	if _, err = a.pluginStore.Data(ctx, "SubscribeGroup", "history_handle"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("added branch altered download dedup", err)
	}
	// A retry while still running returns the receipt without a second update.
	again, err := a.applySubscribeGroupAdded(ctx, run, batch)
	if err != nil || !bytes.Equal(result, again) || subscribeGroupRead(t, a, sub.ID).Revision != updated.Revision {
		t.Fatal("repeat mutated subscription", err)
	}
	after, _ := a.pluginStore.Data(ctx, "SubscribeGroup", "history")
	if after.Revision != history.Revision {
		t.Fatal("repeat appended history")
	}
	out, err := a.searchResources(ctx, SearchRequest{Query: "Jade Dynasty", Policy: updated.Policy})
	if err != nil || !reflect.DeepEqual(priorityCandidateIDs(out), []string{"1"}) {
		t.Fatal("host fields did not reach actual search", priorityCandidateIDs(out), err)
	}
	if err = finishClaimedFixtureRun(ctx, a.pluginStore, run, "succeeded", "", result); err != nil {
		t.Fatal(err)
	}
}

func TestSubscribeGroupInitClearIsAtomicAndSourceFullReplacement(t *testing.T) {
	a := subscriptionTestApp(t)
	ctx := context.Background()
	run := subscribeGroupInstall(t, a, map[string]any{"category": true, "enabled": false, "clear": true, "clear_handle": true, "update_confs": "category:国漫#include:new", "unknown": "drop-on-source-update"})
	for _, key := range []string{"history", "history_handle"} {
		if _, err := a.pluginStore.SaveData(ctx, "SubscribeGroup", key, 0, []string{"synthetic-private"}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := a.Store.DB.Exec(`CREATE TRIGGER deny_group_receipt BEFORE INSERT ON plugin_subscribe_group_operations BEGIN SELECT RAISE(ABORT,'synthetic late failure'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := a.configureSubscribeGroup(ctx, &run); err == nil {
		t.Fatal("failed receipt accepted")
	}
	config, _ := a.pluginStore.Config(ctx, "SubscribeGroup")
	if config.Revision != 1 || run.ConfigRevision != 1 || len(a.subscribeGroupCaches) != 0 || pluginEventCount(t, a, "plugin_event_subscriptions") != 0 {
		t.Fatal("partial failed init published")
	}
	for _, key := range []string{"history", "history_handle"} {
		if _, err := a.pluginStore.Data(ctx, "SubscribeGroup", key); err != nil {
			t.Fatal("clear escaped rollback", key, err)
		}
	}
	if _, err := a.Store.DB.Exec(`DROP TRIGGER deny_group_receipt`); err != nil {
		t.Fatal(err)
	}
	result, err := a.configureSubscribeGroup(ctx, &run)
	if err != nil {
		t.Fatal(err)
	}
	config, _ = a.pluginStore.Config(ctx, "SubscribeGroup")
	var fields map[string]json.RawMessage
	json.Unmarshal(config.Value, &fields)
	if config.Revision != 3 || run.ConfigRevision != 3 || len(fields) != 6 || string(fields["clear"]) != "false" || string(fields["clear_handle"]) != "false" || string(fields["update_details"]) != "[]" {
		t.Fatal("source two writes/full replacement not retained", config.Revision, string(config.Value))
	}
	for _, key := range []string{"history", "history_handle"} {
		if _, err := a.pluginStore.Data(ctx, "SubscribeGroup", key); !errors.Is(err, sql.ErrNoRows) {
			t.Fatal("source clear missing", key, err)
		}
	}
	if _, err = a.configureSubscribeGroup(ctx, &run); err != nil {
		t.Fatal("receipt retry failed", err)
	}
	config, _ = a.pluginStore.Config(ctx, "SubscribeGroup")
	if config.Revision != 3 {
		t.Fatal("flags replayed")
	}
	// Actual close/reopen before FinishRun: commit and its final revision win.
	dir := a.Dir
	if err = a.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := New(Options{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	finished, err := reopened.pluginStore.Run(ctx, run.ID)
	if err != nil || finished.State != "succeeded" || !bytes.Equal(finished.Result, result) || len(reopened.subscribeGroupCaches) != 0 {
		t.Fatal("committed initialization not recovered", finished.State, err)
	}
}

func TestSubscribeGroupAddedLateFailureAndRestartRetry(t *testing.T) {
	a := subscriptionTestApp(t)
	ctx := context.Background()
	subscribeGroupConfigure(t, a, subscribeGroupInstall(t, a, subscribeGroupConfig("category:国漫#include:Jade")))
	sub, run, batch := subscribeGroupCreate(t, a)
	if _, err := a.Store.DB.Exec(`CREATE TRIGGER deny_group_receipt BEFORE INSERT ON plugin_subscribe_group_operations WHEN NEW.run_id='` + run.ID + `' BEGIN SELECT RAISE(ABORT,'synthetic late failure'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := a.applySubscribeGroupAdded(ctx, run, batch); err == nil {
		t.Fatal("late failure accepted")
	}
	if got := subscribeGroupRead(t, a, sub.ID); !reflect.DeepEqual(got, sub) {
		t.Fatal("subscription escaped rollback")
	}
	if _, err := a.pluginStore.Data(ctx, "SubscribeGroup", "history"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("history escaped rollback", err)
	}
	if _, err := a.Store.DB.Exec(`DROP TRIGGER deny_group_receipt`); err != nil {
		t.Fatal(err)
	}
	dir := a.Dir
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := New(Options{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	run = subscribeGroupClaim(t, reopened)
	result, err := reopened.applySubscribeGroupAdded(ctx, run, batch)
	if err != nil {
		t.Fatal("cold retry failed", err)
	}
	updated := subscribeGroupRead(t, reopened, sub.ID)
	history, _ := reopened.pluginStore.Data(ctx, "SubscribeGroup", "history")
	if updated.Revision != 2 || updated.Policy.FilterParams == nil || *updated.Policy.FilterParams.Include != "Jade" {
		t.Fatal("cold retry lost source rule")
	}
	if err = reopened.Close(); err != nil {
		t.Fatal(err)
	}
	again, err := New(Options{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer again.Close()
	finished, err := again.pluginStore.Run(ctx, run.ID)
	if err != nil || finished.State != "succeeded" || !bytes.Equal(finished.Result, result) {
		t.Fatal("committed event not reconciled", finished.State, err)
	}
	finalHistory, _ := again.pluginStore.Data(ctx, "SubscribeGroup", "history")
	if finalHistory.Revision != history.Revision || subscribeGroupRead(t, again, sub.ID).Revision != updated.Revision {
		t.Fatal("recovery duplicated committed writes")
	}
}

func TestSubscribeGroupAddedEmptyPatchAndLegacyHistoryTruthiness(t *testing.T) {
	for _, legacy := range []string{`null`, `false`, `0`, `""`, `{}`, `[]`, `{"bad":true}`} {
		t.Run(legacy, func(t *testing.T) {
			a := subscriptionTestApp(t)
			ctx := context.Background()
			subscribeGroupConfigure(t, a, subscribeGroupInstall(t, a, subscribeGroupConfig("category:国漫")))
			if _, err := a.pluginStore.SaveData(ctx, "SubscribeGroup", "history", 0, json.RawMessage(legacy)); err != nil {
				t.Fatal(err)
			}
			sub, run, batch := subscribeGroupCreate(t, a)
			_, err := a.applySubscribeGroupAdded(ctx, run, batch)
			if legacy == `{"bad":true}` {
				if pluginErrorCode(err) != "INVALID_PLUGIN_DATA" {
					t.Fatal("truthy nonlist accepted", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got := subscribeGroupRead(t, a, sub.ID); !reflect.DeepEqual(got, sub) {
				t.Fatal("empty patch changed current row")
			}
			history, err := a.pluginStore.Data(ctx, "SubscribeGroup", "history")
			var entries []subscribegroup.History
			if err != nil || json.Unmarshal(history.Value, &entries) != nil || len(entries) != 1 || entries[0].Content != "{}" {
				t.Fatal("empty source update/history lost", string(history.Value), err)
			}
		})
	}
}

func TestSubscribeGroupAddedRejectsUnownedAndInvalidatedRuns(t *testing.T) {
	for _, change := range []string{"batch", "instance", "generation", "revision", "action", "input", "handler", "cancelled", "staged", "uninstalled", "config"} {
		t.Run(change, func(t *testing.T) {
			a := subscriptionTestApp(t)
			ctx := context.Background()
			subscribeGroupConfigure(t, a, subscribeGroupInstall(t, a, subscribeGroupConfig("category:国漫#include:Jade")))
			sub, run, batch := subscribeGroupCreate(t, a)
			var err error
			switch change {
			case "batch":
				batch = "other"
			case "instance":
				run.InstanceID = "other"
			case "generation":
				run.Generation++
			case "revision":
				run.ConfigRevision++
			case "action":
				run.Action = "init"
			case "input":
				// A caller's forged input cannot replace the persisted batch.
				run.Input = json.RawMessage(`{"input":{"batch_id":"other"}}`)
				batch = "other"
			case "handler":
				_, err = a.Store.DB.Exec(`UPDATE plugin_event_batches SET handler_key='other' WHERE id=?`, batch)
			case "cancelled":
				err = a.cancelSubscription(ctx, sub.ID)
			case "staged":
				_, err = a.Store.DB.Exec(`UPDATE plugin_installations SET desired_enabled=0 WHERE instance_id='SubscribeGroup'`)
			case "uninstalled":
				err = a.pluginStore.Uninstall(ctx, "SubscribeGroup")
			case "config":
				_, _, err = a.pluginStore.SaveConfigAndQueue(ctx, "SubscribeGroup", run.ConfigRevision, subscribeGroupConfig("category:国漫#include:edited"))
			}
			if err != nil {
				t.Fatal(err)
			}
			before := subscribeGroupRead(t, a, sub.ID)
			if _, err = a.applySubscribeGroupAdded(ctx, run, batch); err == nil {
				t.Fatal("unowned/stale run wrote core")
			}
			if after := subscribeGroupRead(t, a, sub.ID); !reflect.DeepEqual(before, after) {
				t.Fatal("rejected call wrote subscription")
			}
			if _, err = a.pluginStore.Data(ctx, "SubscribeGroup", "history"); !errors.Is(err, sql.ErrNoRows) {
				t.Fatal("rejected call wrote history", err)
			}
		})
	}
}

func TestSubscribeGroupCategoryCacheWarmReloadAndColdRestart(t *testing.T) {
	for _, cold := range []bool{false, true} {
		t.Run(map[bool]string{false: "warm", true: "cold"}[cold], func(t *testing.T) {
			a := subscriptionTestApp(t)
			ctx := context.Background()
			// Equal names use the first active site, regardless of its priority.
			settings := a.Settings()
			settings.AllowedRoots = []string{t.TempDir()}
			if err := a.saveSettings(ctx, settings); err != nil {
				t.Fatal(err)
			}
			a.config.Sites = []SiteConfig{
				{Config: sites.Config{ID: "disabled", Name: "同名"}, Enabled: false},
				{Config: sites.Config{ID: "first", Name: "同名"}, Enabled: true, SiteOptions: SiteOptions{Priority: 99}},
				{Config: sites.Config{ID: "second", Name: "同名"}, Enabled: true, SiteOptions: SiteOptions{Priority: 0}},
			}
			subscribeGroupConfigure(t, a, subscribeGroupInstall(t, a, subscribeGroupConfig("category:国漫#include:retained#sites:同名")))
			config, _ := a.pluginStore.Config(ctx, "SubscribeGroup")
			if _, _, err := a.pluginStore.SaveConfigAndQueue(ctx, "SubscribeGroup", config.Revision, subscribeGroupConfig("category:欧美剧#include:new")); err != nil {
				t.Fatal(err)
			}
			subscribeGroupConfigure(t, a, subscribeGroupClaim(t, a))
			// Original rules cache does not re-resolve an old category's sites
			// during events, including after the active site list changes.
			a.config.Sites = a.config.Sites[2:]
			if cold {
				dir := a.Dir
				if err := a.Close(); err != nil {
					t.Fatal(err)
				}
				reopened, err := New(Options{DataDir: dir})
				if err != nil {
					t.Fatal(err)
				}
				defer reopened.Close()
				a = reopened
			}
			sub, run, batch := subscribeGroupCreate(t, a)
			if _, err := a.applySubscribeGroupAdded(ctx, run, batch); err != nil {
				t.Fatal(err)
			}
			updated := subscribeGroupRead(t, a, sub.ID)
			if cold {
				if !reflect.DeepEqual(updated, sub) {
					t.Fatal("cold start retained an unconfigured category")
				}
			} else if updated.Policy.FilterParams == nil || *updated.Policy.FilterParams.Include != "retained" || !reflect.DeepEqual(updated.SiteIDs, []string{"first"}) {
				t.Fatal("warm reload discarded or re-resolved source cache", updated.Policy, updated.SiteIDs)
			}
		})
	}
}

func TestSubscribeGroupAddedReadsCurrentRowAfterMetadataAndHonorsCancellation(t *testing.T) {
	for _, cancel := range []bool{false, true} {
		t.Run(map[bool]string{false: "edit", true: "cancel"}[cancel], func(t *testing.T) {
			a := subscriptionTestApp(t)
			ctx := context.Background()
			subscribeGroupConfigure(t, a, subscribeGroupInstall(t, a, subscribeGroupConfig("category:国漫#include:Jade#savepath:/downloads/{name}")))
			sub, run, batch := subscribeGroupCreateKnown(t, a, false)
			entered, release := make(chan struct{}), make(chan struct{})
			defer func() {
				select {
				case <-release:
				default:
					close(release)
				}
			}()
			transport := imageTransport(func(request *http.Request) (*http.Response, error) {
				if request.URL.Host != "api.themoviedb.org" || request.URL.Path != "/3/tv/206484" {
					return nil, errors.New("unexpected synthetic metadata request")
				}
				close(entered)
				select {
				case <-release:
				case <-request.Context().Done():
					return nil, request.Context().Err()
				}
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"id":206484,"name":"诛仙","original_name":"Jade Dynasty","overview":"合成简介","first_air_date":"2022-01-01","original_language":"zh","origin_country":["CN"],"genres":[{"id":16}],"credits":{"cast":[]},"recommendations":{"results":[]}}`))}, nil
			})
			previous := http.DefaultTransport
			http.DefaultTransport = transport
			a.config.TMDB = TMDBConfig{APIKey: "synthetic-group-key"}
			a.catClient = catalog.New(catalog.Credentials{APIKey: a.config.TMDB.APIKey})
			a.catCreds = a.config.TMDB
			http.DefaultTransport = previous
			done := make(chan error, 1)
			go func() { _, err := a.applySubscribeGroupAdded(ctx, run, batch); done <- err }()
			select {
			case <-entered:
			case <-time.After(5 * time.Second):
				t.Fatal("host did not resolve actual metadata")
			}
			if cancel {
				if err := a.cancelSubscription(ctx, sub.ID); err != nil {
					t.Fatal(err)
				}
			} else {
				changed := sub.Media
				changed.Title = "用户编辑后的名称"
				changed.Year = 2026
				if err := a.Store.Put(ctx, "cache", "media:tv:206484", map[string]any{"at": time.Now(), "data": changed}); err != nil {
					t.Fatal(err)
				}
				request := subscriptionRequest(t)
				request.Revision = sub.Revision
				request.DownloadPath = sub.DownloadPath
				request.RootID = ""
				request.Keyword = "用户搜索词"
				request.Policy.Include = "user-override"
				if _, err := a.saveSubscription(ctx, sub.ID, request); err != nil {
					t.Fatal(err)
				}
			}
			before := subscribeGroupRead(t, a, sub.ID)
			close(release)
			var resultErr error
			select {
			case resultErr = <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("host did not finish after metadata release")
			}
			updated := subscribeGroupRead(t, a, sub.ID)
			if cancel {
				if pluginErrorCode(resultErr) != "REVIEW_PLUGIN_SUBSCRIPTION" || !reflect.DeepEqual(updated, before) {
					t.Fatal("canceled row overwritten", resultErr)
				}
				if _, err := a.pluginStore.Data(ctx, "SubscribeGroup", "history"); !errors.Is(err, sql.ErrNoRows) {
					t.Fatal("canceled event wrote history", err)
				}
			} else if resultErr != nil || updated.Revision != before.Revision+1 || updated.Media.Title != before.Media.Title || updated.Keyword != before.Keyword || updated.DownloadPath != "/downloads/用户编辑后的名称 (2026)" || updated.Policy.FilterParams == nil || *updated.Policy.FilterParams.Include != "Jade" {
				t.Fatal("stale planned row replaced current edit", updated, resultErr)
			}
		})
	}
}
