package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"light-media/internal/catalog"
	"light-media/internal/plugins"
	"light-media/internal/plugins/subscribegroup"
	"light-media/internal/torrentfilter"
)

// These tests exercise the private host transaction through committed SQL
// facts and the real outbox dispatcher. They do not register a worker/catalog
// adapter or claim that the unfinished plugin UI is available to users.
func subscribeDownloadSetup(t *testing.T, a *App, config subscribegroup.Configuration) {
	t.Helper()
	run := subscribeGroupInstall(t, a, config)
	result, err := a.configureSubscribeGroup(context.Background(), &run)
	if err != nil {
		t.Fatal(err)
	}
	if err = a.pluginStore.configureEventSubscription(context.Background(), run, "downloaded", []string{"download.accepted"}, 0, true); err != nil {
		t.Fatal(err)
	}
	if err = a.pluginStore.FinishRun(context.Background(), run.ID, "succeeded", "", result); err != nil {
		t.Fatal(err)
	}
}

func subscribeDownloadConfig() subscribegroup.Configuration {
	return subscribegroup.Configuration{Enabled: true, UpdateDetails: []string{"分辨率", "资源质量", "特效", "制作组", "站点"}}
}

func subscribeDownloadSub(t *testing.T, a *App, key string, season int, state string, policy Policy, sites []string) Subscription {
	t.Helper()
	sub := Subscription{ID: key, Media: catalog.Media{ID: 206484, MediaType: "tv", Title: key, Year: 2022}, Season: season, State: state, Revision: 3, Policy: policy, SiteIDs: sites, DownloaderID: "test-qb", DownloadPath: "/downloads/retained", CreatedAt: now()}
	if err := a.Store.Put(context.Background(), "subscription", key, sub); err != nil {
		t.Fatal(err)
	}
	return sub
}

func subscribeDownloadSnapshot() downloadAddedSnapshot {
	return downloadAddedSnapshot{DownloadKind: "intent", Hash: strings.Repeat("a", 40), SiteID: "rss-fixture", MediaType: "tv", TMDBID: 206484, Seasons: "S04", HasContext: true, HasHistory: true,
		Metadata: &subscribegroup.Metadata{Resolution: "2160p", Quality: "WEB-DL", Effect: "HDR10", Team: "Team", Customization: "Custom"}}
}

func subscribeDownloadCommit(t *testing.T, a *App, snapshot downloadAddedSnapshot) DomainEvent {
	t.Helper()
	ctx := context.Background()
	snapshot.DownloadID = id("intent_")
	snapshot.DownloadKind = "intent"
	in := Intent{ID: snapshot.DownloadID, WorkflowID: "wf_" + snapshot.DownloadID, Media: catalog.Media{ID: snapshot.TMDBID, MediaType: snapshot.MediaType}, Hash: snapshot.Hash, SiteID: snapshot.SiteID, State: "accepted", Purpose: "core_download"}
	event := DomainEvent{EventID: domainEventID("download.accepted", in.ID), Type: "download.accepted", WorkflowID: in.WorkflowID, JobID: "job_" + in.ID, MediaID: mediaKey(in.Media), IntentID: in.ID, Purpose: in.Purpose}
	tx, err := a.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err = txPut(ctx, tx, "intent", in.ID, in); err != nil {
		t.Fatal(err)
	}
	if err = a.recordDownloadAddedTx(ctx, tx, event, snapshot); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	return event
}

func subscribeDownloadRun(t *testing.T, a *App, snapshot downloadAddedSnapshot) (PluginRun, string) {
	t.Helper()
	subscribeDownloadCommit(t, a, snapshot)
	if err := a.dispatchPluginEvents(context.Background(), time.Now()); err != nil {
		t.Fatal(err)
	}
	run := subscribeGroupClaim(t, a)
	var invocation plugins.Invocation
	var input struct {
		BatchID string `json:"batch_id"`
	}
	if json.Unmarshal(run.Input, &invocation) != nil || json.Unmarshal(invocation.Input, &input) != nil || input.BatchID == "" {
		t.Fatal("download batch not bound to persisted invocation")
	}
	return run, input.BatchID
}

func subscribeDownloadData(t *testing.T, a *App, key string) ([]json.RawMessage, PluginStoredValue) {
	t.Helper()
	stored, err := a.pluginStore.Data(context.Background(), "SubscribeGroup", key)
	var entries []json.RawMessage
	if err != nil || json.Unmarshal(stored.Value, &entries) != nil {
		t.Fatal("missing plugin list", key, err)
	}
	return entries, stored
}

func TestSubscribeGroupDownloadRangeFillCurrentRowsHistoryAndDedupAtomic(t *testing.T) {
	a := subscriptionTestApp(t)
	ctx := context.Background()
	a.config.RSSSites = []string{"rss-fixture"}
	subscribeDownloadSetup(t, a, subscribeDownloadConfig())
	// The original includes paused/completed subscriptions and processes stored
	// identity order. Deliberately make lexical key order differ from insertion.
	first := subscribeDownloadSub(t, a, "z-first-paused", 4, "paused", Policy{Exclude: "preserved"}, nil)
	kept := subscribeDownloadSub(t, a, "a-already-filled", 5, "completed", Policy{Resolution: "1080p", Source: "BluRay", HDR: "sdr", Include: "keep"}, []string{"keep-site"})
	third := subscribeDownloadSub(t, a, "b-third-completed", 6, "completed", Policy{Include: "keep", Source: "BluRay", HDR: "sdr"}, nil)
	cancelled := subscribeDownloadSub(t, a, "c-cancelled", 4, "cancelled", Policy{}, nil)
	other := subscribeDownloadSub(t, a, "other-media", 4, "active", Policy{}, nil)
	other.Media.ID = 99
	if err := a.Store.Put(ctx, "subscription", other.ID, other); err != nil {
		t.Fatal(err)
	}
	jobs := map[string]Job{}
	for _, sub := range []Subscription{first, kept, third} {
		job, err := a.enqueue(ctx, "subscription", map[string]any{"id": sub.ID, "revision": sub.Revision}, "wf_"+sub.ID)
		if err != nil {
			t.Fatal(err)
		}
		jobs[sub.ID] = job
	}
	legacyHistory := json.RawMessage(`[{"unknown":{"private":"synthetic-download-private","huge":` + strings.Repeat("9", 400) + `},"number":1.00},true]`)
	legacyHandled := json.RawMessage(`[7,{"key":"电视剧:206484","huge":` + strings.Repeat("9", 400) + `},null,["电视剧:206484"],"other"]`)
	for key, raw := range map[string]json.RawMessage{"history": legacyHistory, "history_handle": legacyHandled} {
		if _, err := a.pluginStore.SaveData(ctx, "SubscribeGroup", key, 0, raw); err != nil {
			t.Fatal(err)
		}
	}
	snapshot := subscribeDownloadSnapshot()
	snapshot.Seasons = "S04-S06"
	run, batch := subscribeDownloadRun(t, a, snapshot)
	result, err := a.applySubscribeGroupDownload(ctx, run, batch)
	if err != nil {
		t.Fatal(err)
	}
	var counts map[string]int
	if json.Unmarshal(result, &counts) != nil || counts["updated"] != 2 || counts["history_added"] != 2 || counts["handled_added"] != 2 {
		t.Fatal("range failed source update count", string(result))
	}
	gotFirst := subscribeGroupRead(t, a, first.ID)
	p := gotFirst.Policy.FilterParams
	if gotFirst.Revision != first.Revision+1 || p == nil || *p.Include != "Custom.+Team" || *p.Resolution != "4K|2160p|x2160" || *p.Quality != "WEB-?DL|WEB-?RIP" || *p.Effect != `[\s.]+HDR[\s.]+|HDR10|HDR10\+` || gotFirst.Policy.Exclude != "preserved" || !reflect.DeepEqual(gotFirst.SiteIDs, []string{"rss-fixture"}) || gotFirst.State != first.State || gotFirst.DownloadPath != first.DownloadPath || gotFirst.DownloaderID != first.DownloaderID {
		t.Fatal("download failed actual subscription fill", gotFirst)
	}
	gotThird := subscribeGroupRead(t, a, third.ID)
	if gotThird.Revision != third.Revision+1 || gotThird.Policy.Include != "keep" || gotThird.Policy.Source != "BluRay" || gotThird.Policy.HDR != "sdr" || gotThird.Policy.FilterParams == nil || gotThird.Policy.FilterParams.Include != nil || gotThird.Policy.FilterParams.Quality != nil || gotThird.Policy.FilterParams.Effect != nil || !reflect.DeepEqual(gotThird.SiteIDs, []string{"rss-fixture"}) {
		t.Fatal("download overwrote populated fields", gotThird)
	}
	for _, want := range []Subscription{kept, cancelled, other} {
		if got := subscribeGroupRead(t, a, want.ID); !reflect.DeepEqual(got, want) {
			t.Fatal("unrelated or already populated subscription changed", want.ID)
		}
	}
	for key, job := range jobs {
		wantState, wantGeneration := "cancelled", 1
		if key == kept.ID {
			wantState, wantGeneration = "queued", 0
		}
		var state string
		var generation int
		if err = a.Store.DB.QueryRow(`SELECT state,generation FROM jobs WHERE id=?`, job.ID).Scan(&state, &generation); err != nil || state != wantState || generation != wantGeneration {
			t.Fatal("job invalidation scope differs", key, state, generation, err)
		}
	}
	history, hstored := subscribeDownloadData(t, a, "history")
	handled, dstored := subscribeDownloadData(t, a, "history_handle")
	var oldHistory, oldHandled []json.RawMessage
	json.Unmarshal(legacyHistory, &oldHistory)
	json.Unmarshal(legacyHandled, &oldHandled)
	if len(history) != len(oldHistory)+2 || len(handled) != len(oldHandled)+2 {
		t.Fatal("missing per-subscription history/handled duplicates")
	}
	for i := range oldHistory {
		if !bytes.Equal(oldHistory[i], history[i]) {
			t.Fatal("old history fields rewritten", i)
		}
	}
	for i := range oldHandled {
		if !bytes.Equal(oldHandled[i], handled[i]) {
			t.Fatal("old handled values coerced", i)
		}
	}
	for i, name := range []string{first.ID, third.ID} {
		var entry subscribegroup.History
		if json.Unmarshal(history[len(oldHistory)+i], &entry) != nil || entry.Name != name || entry.Type != "种子下载自定义配置" || !strings.Contains(entry.Content, `"resolution": "4K|2160p|x2160"`) {
			t.Fatal("source history content/order missing", entry)
		}
		if string(handled[len(oldHandled)+i]) != `"电视剧:206484"` {
			t.Fatal("handled append did not preserve per-subscription duplicates")
		}
	}
	var sealed []byte
	if err = a.Store.DB.QueryRow(`SELECT sealed FROM plugin_values WHERE instance_id='SubscribeGroup' AND item_key='history'`).Scan(&sealed); err != nil || bytes.Contains(sealed, []byte("synthetic-download-private")) || json.Valid(sealed) {
		t.Fatal("history was not encrypted", err)
	}
	again, err := a.applySubscribeGroupDownload(ctx, run, batch)
	if err != nil || !bytes.Equal(again, result) {
		t.Fatal("same running invocation did not return receipt", err)
	}
	_, afterHistory := subscribeDownloadData(t, a, "history")
	_, afterHandled := subscribeDownloadData(t, a, "history_handle")
	if afterHistory.Revision != hstored.Revision || afterHandled.Revision != dstored.Revision {
		t.Fatal("receipt replay duplicated plugin KV")
	}
	if err = a.pluginStore.FinishRun(ctx, run.ID, "succeeded", "", result); err != nil {
		t.Fatal(err)
	}
	// A separate accepted download in a different season is still deduplicated
	// by the original cross-season type:tmdbid key.
	extra := subscribeDownloadSub(t, a, "late-season", 8, "active", Policy{}, nil)
	snapshot.Seasons = "S08"
	run, batch = subscribeDownloadRun(t, a, snapshot)
	if _, err = a.applySubscribeGroupDownload(ctx, run, batch); err != nil || !reflect.DeepEqual(subscribeGroupRead(t, a, extra.ID), extra) {
		t.Fatal("handled key was incorrectly scoped to season", err)
	}
	_, finalHandled := subscribeDownloadData(t, a, "history_handle")
	if finalHandled.Revision != dstored.Revision {
		t.Fatal("cross-season duplicate appended handled KV")
	}
}

func TestSubscribeGroupDownloadSingleSeasonAndRSSScope(t *testing.T) {
	for _, siteInRSS := range []bool{false, true} {
		t.Run(fmt.Sprint(siteInRSS), func(t *testing.T) {
			a := subscriptionTestApp(t)
			if siteInRSS {
				a.config.RSSSites = []string{"rss-fixture"}
			}
			subscribeDownloadSetup(t, a, subscribeDownloadConfig())
			selected := subscribeDownloadSub(t, a, "selected", 4, "active", Policy{}, nil)
			otherSeason := subscribeDownloadSub(t, a, "other-season", 5, "active", Policy{}, nil)
			run, batch := subscribeDownloadRun(t, a, subscribeDownloadSnapshot())
			if _, err := a.applySubscribeGroupDownload(context.Background(), run, batch); err != nil {
				t.Fatal(err)
			}
			got := subscribeGroupRead(t, a, selected.ID)
			if got.Revision != selected.Revision+1 || (len(got.SiteIDs) > 0) != siteInRSS || !reflect.DeepEqual(subscribeGroupRead(t, a, otherSeason.ID), otherSeason) {
				t.Fatal("season or RSS scope escaped", got)
			}
		})
	}
}

func TestSubscribeGroupDownloadRangeMeansAllSeasonsAndBadSingleSeasonRollsBack(t *testing.T) {
	for _, seasons := range []string{"S04-S06", "", "S04,S05"} {
		t.Run(seasons, func(t *testing.T) {
			a := subscriptionTestApp(t)
			subscribeDownloadSetup(t, a, subscribeDownloadConfig())
			// Source treats a hyphen as season=None; it does not bound the range.
			sub := subscribeDownloadSub(t, a, "outside-range", 8, "paused", Policy{}, nil)
			snapshot := subscribeDownloadSnapshot()
			snapshot.Seasons = seasons
			run, batch := subscribeDownloadRun(t, a, snapshot)
			_, err := a.applySubscribeGroupDownload(context.Background(), run, batch)
			got := subscribeGroupRead(t, a, sub.ID)
			if seasons == "S04,S05" {
				if pluginErrorCode(err) != "INVALID_PLUGIN_DOWNLOAD_SEASON" || !reflect.DeepEqual(got, sub) {
					t.Fatal("invalid single season was coerced or partially changed", err)
				}
				if _, err := a.pluginStore.Data(context.Background(), "SubscribeGroup", "history_handle"); !errors.Is(err, sql.ErrNoRows) {
					t.Fatal("invalid season wrote handled history", err)
				}
			} else if err != nil || got.Revision != sub.Revision+1 {
				t.Fatal("source season=None did not cover all stored seasons", err)
			}
		})
	}
}

func TestSubscribeGroupDownloadLateSQLFailureRollsBackAndExactRetry(t *testing.T) {
	a := subscriptionTestApp(t)
	ctx := context.Background()
	subscribeDownloadSetup(t, a, subscribeDownloadConfig())
	sub := subscribeDownloadSub(t, a, "target", 4, "active", Policy{}, nil)
	job, err := a.enqueue(ctx, "subscription", map[string]any{"id": sub.ID, "revision": sub.Revision}, "wf_target")
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"history", "history_handle"} {
		if _, err = a.pluginStore.SaveData(ctx, "SubscribeGroup", key, 0, []string{"old"}); err != nil {
			t.Fatal(err)
		}
	}
	run, batch := subscribeDownloadRun(t, a, subscribeDownloadSnapshot())
	if _, err = a.Store.DB.Exec(`CREATE TRIGGER deny_download_receipt BEFORE INSERT ON plugin_subscribe_group_operations BEGIN SELECT RAISE(ABORT,'synthetic receipt failure'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err = a.applySubscribeGroupDownload(ctx, run, batch); err == nil {
		t.Fatal("late receipt failure reported success")
	}
	if !reflect.DeepEqual(subscribeGroupRead(t, a, sub.ID), sub) {
		t.Fatal("subscription escaped failed transaction")
	}
	for _, key := range []string{"history", "history_handle"} {
		entries, stored := subscribeDownloadData(t, a, key)
		if stored.Revision != 1 || len(entries) != 1 || string(entries[0]) != `"old"` {
			t.Fatal("KV escaped failed transaction", key)
		}
	}
	var state string
	var generation int
	if err = a.Store.DB.QueryRow(`SELECT state,generation FROM jobs WHERE id=?`, job.ID).Scan(&state, &generation); err != nil || state != "queued" || generation != 0 {
		t.Fatal("job invalidation escaped failed transaction", state, generation, err)
	}
	if _, err = a.pluginStore.subscribeGroupReceipt(ctx, run); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("failed transaction left receipt", err)
	}
	if _, err = a.Store.DB.Exec(`DROP TRIGGER deny_download_receipt`); err != nil {
		t.Fatal(err)
	}
	dir, originalRunID := a.Dir, run.ID
	if err = a.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := New(Options{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	run = subscribeGroupClaim(t, reopened)
	if run.ID != originalRunID {
		t.Fatal("cold recovery replaced the original invocation")
	}
	result, err := reopened.applySubscribeGroupDownload(ctx, run, batch)
	if err != nil || subscribeGroupRead(t, reopened, sub.ID).Revision != sub.Revision+1 {
		t.Fatal("same invocation could not retry after rollback and restart", err)
	}
	if err = reopened.Close(); err != nil {
		t.Fatal(err)
	}
	again, err := New(Options{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer again.Close()
	finished, err := again.pluginStore.Run(ctx, originalRunID)
	if err != nil || finished.State != "succeeded" || !bytes.Equal(finished.Result, result) || subscribeGroupRead(t, again, sub.ID).Revision != sub.Revision+1 {
		t.Fatal("committed receipt was not recovered without duplicate updates", err)
	}
	for _, key := range []string{"history", "history_handle"} {
		entries, stored := subscribeDownloadData(t, again, key)
		if stored.Revision != 2 || len(entries) != 2 {
			t.Fatal("recovery duplicated committed plugin KV", key)
		}
	}
}

func TestSubscribeGroupDownloadKVFalseyAndTruthyScalarBehavior(t *testing.T) {
	for _, scalarKey := range []string{"history", "history_handle"} {
		for _, value := range []string{`null`, `false`, `0`, `0.0`, `""`, `{}`, `[]`, `"nonempty"`, `true`, `1`, `{"old":1}`} {
			t.Run(scalarKey+"/"+value, func(t *testing.T) {
				a := subscriptionTestApp(t)
				ctx := context.Background()
				subscribeDownloadSetup(t, a, subscribeDownloadConfig())
				sub := subscribeDownloadSub(t, a, "target", 4, "active", Policy{}, nil)
				for _, key := range []string{"history", "history_handle"} {
					raw := json.RawMessage(`[]`)
					if key == scalarKey {
						raw = json.RawMessage(value)
					}
					if _, err := a.pluginStore.SaveData(ctx, "SubscribeGroup", key, 0, raw); err != nil {
						t.Fatal(err)
					}
				}
				run, batch := subscribeDownloadRun(t, a, subscribeDownloadSnapshot())
				_, err := a.applySubscribeGroupDownload(ctx, run, batch)
				truthy := value == `"nonempty"` || value == `true` || value == `1` || value == `{"old":1}`
				if truthy {
					if err == nil || !reflect.DeepEqual(subscribeGroupRead(t, a, sub.ID), sub) {
						t.Fatal("truthy scalar KV silently coerced or partially updated", err)
					}
					return
				}
				if err != nil {
					t.Fatal("falsey source KV was not normalized on append", err)
				}
				for _, key := range []string{"history", "history_handle"} {
					entries, stored := subscribeDownloadData(t, a, key)
					if stored.Revision != 2 || len(entries) != 1 {
						t.Fatal("falsey KV did not become one-element list", key)
					}
				}
			})
		}
	}
}

// Independent cases follow original 2.8.7 download_notice's order: membership,
// movie/no-subscriber/no-empty-field exits, then append. A non-list container
// is not itself an error until Python would need an unsupported operation.
func TestSubscribeGroupDownloadHandledContainerMembershipAndEarlyExits(t *testing.T) {
	cases := []struct {
		name       string
		handled    string
		mediaType  string
		seasons    string
		noSub      bool
		full       bool
		corruptRow bool
		wantError  bool
	}{
		{"string contains key", `"prefix电视剧:206484suffix"`, "tv", "not-a-season", false, false, true, false},
		{"dictionary key with false value", `{"电视剧:206484":false}`, "tv", "not-a-season", false, false, true, false},
		{"list string key", `[17,{"key":"电视剧:206484"},"电视剧:206484"]`, "tv", "not-a-season", false, false, true, false},
		{"string misses key but movie", `"old"`, "movie", "not-a-season", false, false, true, false},
		{"dictionary misses key but movie", `{"电视剧:206484":true}`, "movie", "not-a-season", false, false, true, false},
		{"string misses key but no subscriptions", `"old"`, "tv", "S04", true, false, false, false},
		{"dictionary misses key but no subscriptions", `{"old":true}`, "tv", "S04", true, false, false, false},
		{"string misses key but fields full", `"old"`, "tv", "S04", false, true, false, false},
		{"dictionary misses key but fields full", `{"old":true}`, "tv", "S04", false, true, false, false},
		{"string cannot append actual change", `"old"`, "tv", "S04", false, false, false, true},
		{"dictionary cannot append actual change", `{"old":true}`, "tv", "S04", false, false, false, true},
		{"truthy bool membership fails before movie", `true`, "movie", "S04", false, false, true, true},
		{"truthy number membership fails before movie", `17`, "movie", "S04", false, false, true, true},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			a := subscriptionTestApp(t)
			ctx := context.Background()
			subscribeDownloadSetup(t, a, subscribeDownloadConfig())
			var sub Subscription
			if !test.noSub {
				policy, sites := Policy{}, []string(nil)
				if test.full {
					policy = Policy{Resolution: "1080p", Source: "BluRay", HDR: "sdr", Include: "retained"}
					sites = []string{"retained-site"}
				}
				sub = subscribeDownloadSub(t, a, "target", 4, "active", policy, sites)
			}
			if test.corruptRow {
				if err := a.Store.Put(ctx, "subscription", "unrelated-broken-row", "not-a-subscription"); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := a.pluginStore.SaveData(ctx, "SubscribeGroup", "history_handle", 0, json.RawMessage(test.handled)); err != nil {
				t.Fatal(err)
			}
			// Any accidental history read would fail. Source no-op exits never
			// append this unrelated old value and must preserve it untouched.
			if _, err := a.pluginStore.SaveData(ctx, "SubscribeGroup", "history", 0, json.RawMessage(`{"untouched":"old-history"}`)); err != nil {
				t.Fatal(err)
			}
			snapshot := subscribeDownloadSnapshot()
			snapshot.MediaType, snapshot.Seasons = test.mediaType, test.seasons
			run, batch := subscribeDownloadRun(t, a, snapshot)
			result, err := a.applySubscribeGroupDownload(ctx, run, batch)
			if test.wantError {
				if pluginErrorCode(err) != "INVALID_PLUGIN_DATA" {
					t.Fatal("original membership/append error was hidden", err)
				}
				if _, receiptErr := a.pluginStore.subscribeGroupReceipt(ctx, run); !errors.Is(receiptErr, sql.ErrNoRows) {
					t.Fatal("failed append left a success receipt", receiptErr)
				}
			} else {
				var counts map[string]int
				if err != nil || json.Unmarshal(result, &counts) != nil || counts["updated"] != 0 || counts["history_added"] != 0 || counts["handled_added"] != 0 {
					t.Fatal("source early exit did not commit a no-op receipt", string(result), err)
				}
				if receipt, receiptErr := a.pluginStore.subscribeGroupReceipt(ctx, run); receiptErr != nil || !bytes.Equal(receipt, result) {
					t.Fatal("no-op was not durable", receiptErr)
				}
			}
			if sub.ID != "" && !reflect.DeepEqual(subscribeGroupRead(t, a, sub.ID), sub) {
				t.Fatal("membership/no-op/failed append changed subscription")
			}
			for key, want := range map[string]string{"history_handle": test.handled, "history": `{"untouched":"old-history"}`} {
				stored, err := a.pluginStore.Data(ctx, "SubscribeGroup", key)
				if err != nil || stored.Revision != 1 || string(stored.Value) != want {
					t.Fatal("source early exit or failed append changed KV", key, err)
				}
			}
		})
	}
}

func TestSubscribeGroupDownloadProjectionPreservesSourceEmptyPresence(t *testing.T) {
	text := func(value string) *string { return &value }
	cases := []struct {
		name   string
		policy Policy
		filled bool
	}{
		{"legacy codec is populated quality", Policy{Codec: "h264"}, true},
		{"explicit empty replaces legacy", Policy{Source: "BluRay", Codec: "h264", Include: "old", FilterParams: &torrentfilter.Parameters{Quality: text(""), Include: text("")}}, true},
		{"explicit populated remains untouched", Policy{Source: "BluRay", Codec: "h264", Include: "old", FilterParams: &torrentfilter.Parameters{Quality: text("custom-quality"), Include: text("custom-include")}}, false},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			a := subscriptionTestApp(t)
			config := subscribegroup.Configuration{Enabled: true, UpdateDetails: []string{"资源质量", "制作组"}}
			subscribeDownloadSetup(t, a, config)
			sub := subscribeDownloadSub(t, a, "target", 4, "active", test.policy, nil)
			run, batch := subscribeDownloadRun(t, a, subscribeDownloadSnapshot())
			if _, err := a.applySubscribeGroupDownload(context.Background(), run, batch); err != nil {
				t.Fatal(err)
			}
			got := subscribeGroupRead(t, a, sub.ID)
			if !test.filled {
				if !reflect.DeepEqual(got, sub) {
					t.Fatal("populated original fields overwritten", got)
				}
				if _, err := a.pluginStore.Data(context.Background(), "SubscribeGroup", "history_handle"); !errors.Is(err, sql.ErrNoRows) {
					t.Fatal("no-change download marked handled", err)
				}
				return
			}
			if got.Revision != sub.Revision+1 || got.Policy.FilterParams == nil || got.Policy.FilterParams.Include == nil || *got.Policy.FilterParams.Include != "Custom.+Team" {
				t.Fatal("source blank include not filled", got)
			}
			if sub.Policy.FilterParams == nil {
				if got.Policy.Codec != "h264" || got.Policy.FilterParams.Quality != nil {
					t.Fatal("legacy codec was treated as blank source quality", got)
				}
			} else if got.Policy.Source != "" || got.Policy.Codec != "" || got.Policy.Include != "" || got.Policy.FilterParams.Quality == nil || *got.Policy.FilterParams.Quality != "WEB-?DL|WEB-?RIP" {
				t.Fatal("explicit blank source fields did not replace legacy constraints", got)
			}
		})
	}
}

func TestSubscribeGroupDownloadPrerequisitesDoNotReadHistoryOrMarkHandled(t *testing.T) {
	cases := map[string]func(*subscribegroup.Configuration, *downloadAddedSnapshot){
		"disabled download branch": func(c *subscribegroup.Configuration, _ *downloadAddedSnapshot) { c.Enabled = false },
		"empty selected fields":    func(c *subscribegroup.Configuration, _ *downloadAddedSnapshot) { c.UpdateDetails = nil },
		"missing context":          func(_ *subscribegroup.Configuration, s *downloadAddedSnapshot) { s.HasContext = false },
		"missing download history": func(_ *subscribegroup.Configuration, s *downloadAddedSnapshot) { s.HasHistory = false },
	}
	for name, alter := range cases {
		t.Run(name, func(t *testing.T) {
			a := subscriptionTestApp(t)
			config, snapshot := subscribeDownloadConfig(), subscribeDownloadSnapshot()
			alter(&config, &snapshot)
			subscribeDownloadSetup(t, a, config)
			sub := subscribeDownloadSub(t, a, "target", 4, "active", Policy{}, nil)
			for _, key := range []string{"history", "history_handle"} {
				if _, err := a.pluginStore.SaveData(context.Background(), "SubscribeGroup", key, 0, json.RawMessage(`{"untouched":true}`)); err != nil {
					t.Fatal(err)
				}
			}
			run, batch := subscribeDownloadRun(t, a, snapshot)
			if _, err := a.applySubscribeGroupDownload(context.Background(), run, batch); err != nil || !reflect.DeepEqual(subscribeGroupRead(t, a, sub.ID), sub) {
				t.Fatal("source early exit altered subscription or read invalid KV", err)
			}
			for _, key := range []string{"history", "history_handle"} {
				stored, err := a.pluginStore.Data(context.Background(), "SubscribeGroup", key)
				if err != nil || stored.Revision != 1 || string(stored.Value) != `{"untouched":true}` {
					t.Fatal("source early exit touched KV", key, err)
				}
			}
		})
	}
}

func TestSubscribeGroupDownloadScopesAndCurrentExecutionAuthority(t *testing.T) {
	cases := map[string]func(*App, *PluginRun, *string){
		"different batch": func(_ *App, _ *PluginRun, batch *string) { *batch = "another-batch" },
		"forged version":  func(_ *App, run *PluginRun, _ *string) { run.Version = "9.9.9" },
		"forged revision": func(_ *App, run *PluginRun, _ *string) { run.ConfigRevision++ },
		"forged generation": func(_ *App, run *PluginRun, _ *string) {
			run.Generation++
		},
		"wrong handler": func(a *App, run *PluginRun, _ *string) {
			if _, err := a.Store.DB.Exec(`UPDATE plugin_event_batches SET handler_key='added' WHERE run_id=?`, run.ID); err != nil {
				panic(err)
			}
		},
		"disabled": func(a *App, _ *PluginRun, _ *string) {
			if _, err := a.Store.DB.Exec(`UPDATE plugin_installations SET desired_enabled=0 WHERE instance_id='SubscribeGroup'`); err != nil {
				panic(err)
			}
		},
		"stopped run": func(a *App, run *PluginRun, _ *string) {
			if err := a.pluginStore.FinishRun(context.Background(), run.ID, "failed", "TEST", nil); err != nil {
				panic(err)
			}
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			a := subscriptionTestApp(t)
			subscribeDownloadSetup(t, a, subscribeDownloadConfig())
			sub := subscribeDownloadSub(t, a, "target", 4, "active", Policy{}, nil)
			run, batch := subscribeDownloadRun(t, a, subscribeDownloadSnapshot())
			mutate(a, &run, &batch)
			if _, err := a.applySubscribeGroupDownload(context.Background(), run, batch); err == nil || !reflect.DeepEqual(subscribeGroupRead(t, a, sub.ID), sub) {
				t.Fatal("invalid invocation acquired subscription authority", err)
			}
			for _, key := range []string{"history", "history_handle"} {
				if _, err := a.pluginStore.Data(context.Background(), "SubscribeGroup", key); !errors.Is(err, sql.ErrNoRows) {
					t.Fatal("denied invocation wrote KV", key, err)
				}
			}
		})
	}
}

func subscribeDownloadWriterFixture(kind string) (DomainEvent, downloadAddedSnapshot, map[string]any) {
	snapshot := subscribeDownloadSnapshot()
	snapshot.DownloadKind, snapshot.DownloadID = kind, kind+"_writer_fixture"
	event := DomainEvent{Type: "download.accepted", WorkflowID: "wf_writer_fixture", JobID: "job_writer_fixture", MediaID: "tv:206484"}
	if kind == "intent" {
		event.IntentID = snapshot.DownloadID
		event.EventID = domainEventID(event.Type, snapshot.DownloadID)
	} else {
		event.NativeDownloadID = snapshot.DownloadID
		event.EventID = domainEventID(event.Type, "native_download:"+snapshot.DownloadID)
	}
	row := map[string]any{"id": snapshot.DownloadID, "hash": snapshot.Hash, "site_id": snapshot.SiteID, "state": "planned"}
	return event, snapshot, row
}

func TestDownloadAddedWriterRejectsPlannedAndRequiresBusinessTransaction(t *testing.T) {
	for _, kind := range []string{"intent", "native_download"} {
		t.Run(kind, func(t *testing.T) {
			a := newTestApp(t)
			ctx := context.Background()
			event, snapshot, planned := subscribeDownloadWriterFixture(kind)
			if err := a.recordDownloadAddedTx(ctx, nil, event, snapshot); pluginErrorCode(err) != "DOMAIN_EVENT_TX_REQUIRED" {
				t.Fatal("publication did not require owning business transaction", err)
			}
			if _, err := a.domainEvents(ctx, 0, 10); err != nil {
				t.Fatal(err)
			}
			tx, err := a.Store.DB.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			if err = txPut(ctx, tx, kind, snapshot.DownloadID, planned); err != nil {
				tx.Rollback()
				t.Fatal(err)
			}
			err = a.recordDownloadAddedTx(ctx, tx, event, snapshot)
			if rollbackErr := tx.Rollback(); rollbackErr != nil {
				t.Fatal(rollbackErr)
			}
			if pluginErrorCode(err) != "INVALID_DOWNLOAD_EVENT" {
				t.Fatal("planned downloader row was published as accepted", err)
			}
			page, err := a.domainEvents(ctx, 0, 10)
			var snapshots int
			if err != nil || len(page.Events) != 0 {
				t.Fatal("planned row left a download fact", err)
			}
			if err = a.Store.DB.QueryRow(`SELECT COUNT(*) FROM records WHERE kind='download_added'`).Scan(&snapshots); err != nil || snapshots != 0 {
				t.Fatal("planned row left a frozen download snapshot", err)
			}
		})
	}
}

func TestDownloadAddedOutboxFailureRollsBackAcceptedRowAndFrozenSnapshot(t *testing.T) {
	for _, kind := range []string{"intent", "native_download"} {
		t.Run(kind, func(t *testing.T) {
			a := newTestApp(t)
			ctx := context.Background()
			event, snapshot, row := subscribeDownloadWriterFixture(kind)
			if err := a.Store.Put(ctx, kind, snapshot.DownloadID, row); err != nil {
				t.Fatal(err)
			}
			if _, err := a.domainEvents(ctx, 0, 10); err != nil {
				t.Fatal(err)
			}
			if _, err := a.Store.DB.Exec(`CREATE TRIGGER deny_download_fact BEFORE INSERT ON domain_events BEGIN SELECT RAISE(ABORT,'synthetic outbox failure'); END`); err != nil {
				t.Fatal(err)
			}
			row["state"] = "accepted"
			tx, err := a.Store.DB.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			if err = txPut(ctx, tx, kind, snapshot.DownloadID, row); err != nil {
				tx.Rollback()
				t.Fatal(err)
			}
			err = a.recordDownloadAddedTx(ctx, tx, event, snapshot)
			if rollbackErr := tx.Rollback(); rollbackErr != nil {
				t.Fatal(rollbackErr)
			}
			if err == nil {
				t.Fatal("late outbox failure reported successful publication")
			}
			var kept map[string]any
			if err = a.Store.Get(ctx, kind, snapshot.DownloadID, &kept); err != nil || kept["state"] != "planned" {
				t.Fatal("accepted state escaped rollback", err)
			}
			var snapshots int
			if err = a.Store.DB.QueryRow(`SELECT COUNT(*) FROM records WHERE kind='download_added'`).Scan(&snapshots); err != nil || snapshots != 0 {
				t.Fatal("private snapshot escaped failed outbox transaction", err)
			}
			if page, err := a.domainEvents(ctx, 0, 10); err != nil || len(page.Events) != 0 {
				t.Fatal("failed outbox left a visible fact", err)
			}
			if _, err := a.Store.DB.Exec(`DROP TRIGGER deny_download_fact`); err != nil {
				t.Fatal(err)
			}
			tx, err = a.Store.DB.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			if err = txPut(ctx, tx, kind, snapshot.DownloadID, row); err != nil {
				t.Fatal(err)
			}
			if err = a.recordDownloadAddedTx(ctx, tx, event, snapshot); err != nil {
				t.Fatal("same accepted fact could not be retried", err)
			}
			if err = tx.Commit(); err != nil {
				t.Fatal(err)
			}
			if page, err := a.domainEvents(ctx, 0, 10); err != nil || len(page.Events) != 1 {
				t.Fatal("successful retry did not publish exactly one fact", err)
			}
		})
	}
}
