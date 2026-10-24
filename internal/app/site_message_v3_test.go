package app

import (
	"context"
	"encoding/json"
	"light-media/internal/sites"
	"sync/atomic"
	"testing"
)

func TestSiteMessageSealedPendingBusinessAbsentOrNullAndRoutingRejectsNull(t *testing.T) {
	for _, mode := range []string{"absent", "null", "routing_null"} {
		t.Run(mode, func(t *testing.T) {
			a := newTestApp(t)
			sent := siteMessageOwnedFIFOSender(t, a)
			wantSends := 1
			if mode == "routing_null" {
				wantSends = 0
			}
			t.Cleanup(func() {
				if got := len(sent()); got != wantSends {
					t.Errorf("owned HTTP sender count = %d, want %d", got, wantSends)
				}
			})
			job := siteMessageClaimBatch(t, a)
			r := siteMessageSeedCollectedDTO(t, a, job, "pending-roundtrip", &sites.SourceUserData{MessageUnread: 1, Messages: [][]any{{"head", "date", "body"}}})
			var document map[string]json.RawMessage
			if err := json.Unmarshal(mustCleanSeedJSON(r), &document); err != nil {
				t.Fatal(err)
			}
			var steps []map[string]json.RawMessage
			if err := json.Unmarshal(document["Steps"], &steps); err != nil || len(steps) != 1 {
				t.Fatal("bad synthetic step fixture", err)
			}
			if mode == "absent" {
				delete(steps[0], "Business")
			} else {
				steps[0]["Business"] = json.RawMessage(`null`)
			}
			if mode == "routing_null" {
				steps[0]["State"] = json.RawMessage(`"routing"`)
			}
			encoded, err := json.Marshal(steps)
			if err != nil {
				t.Fatal(err)
			}
			document["Steps"] = encoded
			encoded, err = json.Marshal(document)
			if err != nil {
				t.Fatal(err)
			}
			sealed, err := a.pluginStore.seal("site_message_collection", "system", r.ID, encoded)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = a.Store.DB.Exec(`UPDATE records SET payload=? WHERE kind='site_message_collection' AND id=?`, sealed, r.ID); err != nil {
				t.Fatal(err)
			}
			loaded, err := a.pluginStore.readSiteMessageCollection(context.Background(), a.Store.DB, r.ID)
			if mode == "routing_null" {
				if err == nil {
					t.Fatal("routing ordinal accepted missing Business proof")
				}
				if len(siteMessageHistory(t, a)) != 0 || siteMessageCoreTableCount(t, a, "plugin_notice_routes") != 0 {
					t.Fatal("invalid routing proof reached a side effect")
				}
				return
			}
			if err != nil || loaded.Steps[0].State != "pending" || len(loaded.Steps[0].Business) != 0 {
				t.Fatal("pending absent/null business did not roundtrip as unconstructed", loaded.Steps, err)
			}
			finished, err := a.finishSiteMessageCollection(context.Background(), r.ID)
			if err != nil || finished.Stage != "complete" || len(siteMessageHistory(t, a)) != 1 {
				t.Fatal("valid unconstructed ordinal could not resume", finished.Stage, err)
			}
		})
	}
}

func siteMessageCoreTableCount(t *testing.T, a *App, table string) int {
	t.Helper()
	var count int
	if err := a.Store.DB.QueryRow(`SELECT count(*) FROM ` + table).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func TestSiteMessageCoreGrantCheckedAfterHistoryBeforeFurtherEffects(t *testing.T) {
	for _, mode := range []string{"unchanged", "connection", "disable_site"} {
		t.Run(mode, func(t *testing.T) {
			a := newTestApp(t)
			calls := siteMessageHTTPFixture(t, a, true)
			ctx := context.Background()
			var changed atomic.Bool
			sent := siteMessageOwnedFIFOSender(t, a)
			changedURL := siteMessageRevokedConnectionFixture(t)
			a.pluginStore.notices.afterCommit = func(stage string) error {
				if stage != "route_step" || changed.Load() {
					return nil
				}
				var histories int
				if err := a.Store.DB.QueryRow(`SELECT count(*) FROM records WHERE kind='notification'`).Scan(&histories); err != nil {
					return err
				}
				if histories == 0 || !changed.CompareAndSwap(false, true) {
					return nil
				}
				if mode == "unchanged" {
					return nil
				}
				a.settingsMu.Lock()
				defer a.settingsMu.Unlock()
				settings := a.Settings()
				if mode == "connection" {
					settings.Sites[0].BaseURL = changedURL
				} else {
					settings.Sites[0].Enabled = false
				}
				return a.saveSettings(ctx, settings)
			}
			job := siteMessageClaimBatch(t, a)
			runErr := a.runSiteRefresh(ctx, job)
			if !changed.Load() || calls.Load() != 7 {
				t.Fatal("actual post-history boundary not reached", changed.Load(), calls.Load(), runErr)
			}
			var err error
			var snapshot siteSnapshot
			if err = a.Store.Get(ctx, "site_connection", "known", &snapshot); err != nil || snapshot.SourceData == nil {
				t.Fatal("later authority change rolled back collected fact", err)
			}
			var events int
			if err = a.Store.DB.QueryRow(`SELECT count(*) FROM domain_events WHERE json_extract(payload,'$.event_type')='site.refreshed' AND json_extract(payload,'$.site_id')='known'`).Scan(&events); err != nil || events != 1 {
				t.Fatal("original source event lost", events, err)
			}
			wantHistory, wantEffects := 1, 0
			if mode == "unchanged" {
				wantHistory, wantEffects = 2, 2
			}
			if len(siteMessageHistory(t, a)) != wantHistory || siteMessageCoreTableCount(t, a, "plugin_notice_publications") != wantEffects || siteMessageCoreTableCount(t, a, "plugin_message_queue") != wantEffects || len(sent()) != wantEffects {
				t.Fatal("revoked source grant admitted a later effect", len(siteMessageHistory(t, a)), siteMessageCoreTableCount(t, a, "plugin_notice_publications"), siteMessageCoreTableCount(t, a, "plugin_message_queue"), len(sent()))
			}
			if (mode == "unchanged") != (runErr == nil) {
				t.Fatal("core grant positive/negative control failed", mode, runErr)
			}

			dir := a.Dir
			if err = a.Close(); err != nil {
				t.Fatal(err)
			}
			cold, err := New(Options{DataDir: dir})
			if err != nil {
				t.Fatal(err)
			}
			defer cold.Close()
			coldSent := siteMessageOwnedFIFOSender(t, cold)
			_ = cold.resumePluginNoticeRoutes(ctx, 200)
			if calls.Load() != 7 || len(siteMessageHistory(t, cold)) != wantHistory || siteMessageCoreTableCount(t, cold, "plugin_notice_publications") != wantEffects || siteMessageCoreTableCount(t, cold, "plugin_message_queue") != wantEffects || len(sent()) != wantEffects || len(coldSent()) != 0 {
				t.Fatal("cold replay bypassed revoked grant or repeated existing effect", calls.Load(), len(sent()))
			}
		})
	}
}

func TestSiteMessageDeletedUIHistoryPreservesCoreCompletion(t *testing.T) {
	a := newTestApp(t)
	calls := siteMessageHTTPFixture(t, a, true)
	ctx := context.Background()
	sent := siteMessageOwnedFIFOSender(t, a)
	job := siteMessageClaimBatch(t, a)
	if err := a.runSiteRefresh(ctx, job); err != nil {
		t.Fatal(err)
	}
	history := siteMessageHistory(t, a)
	if len(history) != 2 || len(sent()) != 2 || calls.Load() != 7 {
		t.Fatal("messages were not actually committed", len(history), len(sent()), calls.Load())
	}
	for _, item := range history {
		if _, err := a.Store.DB.Exec(`DELETE FROM records WHERE kind='notification' AND id=?`, item.ID); err != nil {
			t.Fatal(err)
		}
	}
	dir := a.Dir
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	cold, err := New(Options{DataDir: dir})
	if err != nil {
		t.Fatal("cold open incorrectly required deleted UI history", err)
	}
	defer cold.Close()
	coldSent := siteMessageOwnedFIFOSender(t, cold)
	if err = cold.resumePluginNoticeRoutes(ctx, 200); err != nil {
		t.Fatal("authenticated core completion did not survive history deletion", err)
	}
	id := cold.pluginStore.noticeID("site-collection", "job", job.ID, "known")
	collection, err := cold.pluginStore.readSiteMessageCollection(ctx, cold.Store.DB, id)
	if err != nil || collection.Stage != "complete" {
		t.Fatal("immutable core completion missing", collection.Stage, err)
	}
	tx, err := cold.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err = cold.requireSiteCollectionCompleteTx(ctx, tx, collection); err != nil {
		t.Fatal("deleted UI history invalidated immutable post proof", err)
	}
	if err = tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 7 || len(sent()) != 2 || len(coldSent()) != 0 || len(siteMessageHistory(t, cold)) != 0 {
		t.Fatal("recovery recollected, resent, or recreated deleted history")
	}
	if siteMessageCoreTableCount(t, cold, "plugin_notice_publications") != 2 || siteMessageCoreTableCount(t, cold, "plugin_message_queue") != 2 {
		t.Fatal("cold recovery duplicated accepted effects")
	}
}
