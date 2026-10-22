package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sync/atomic"
	"testing"

	"light-media/internal/sites"
)

// Mutation occurs after the real history transaction commits, before the next
// Router effect. The owned HTTP fixture is the only site contacted by this test.
func TestSiteMessageCurrentGrantCheckedAfterHistoryBeforeFurtherEffects(t *testing.T) {
	for _, mode := range []string{"unchanged", "rotate_api_key", "disable_site"} {
		t.Run(mode, func(t *testing.T) {
			a, _, _, _ := pluginHTTPFixture(t)
			calls := siteMessageHTTPFixture(t, a, true)
			ctx := context.Background()
			var changed atomic.Bool
			var sends atomic.Int32
			a.pluginStore.notices.send = func(context.Context, pluginNoticeQueueEntry) (int, error) { sends.Add(1); return 1, nil }
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
				if mode == "rotate_api_key" {
					settings.APIToken = "owned-rotated-api-key-after-history"
				} else {
					settings.Sites[0].Enabled = false
				}
				return a.saveSettings(ctx, settings)
			}
			run := enqueueSyntheticPluginHTTP(t, a)
			a.executePluginRun(ctx, run)
			finished, err := a.pluginStore.Run(ctx, run.ID)
			if err != nil || !changed.Load() || calls.Load() != 7 {
				t.Fatal("actual post-history boundary not reached", finished.State, changed.Load(), calls.Load(), err)
			}
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
			if len(siteMessageHistory(t, a)) != wantHistory || statisticRouterCount(t, a, "plugin_notice_publications") != wantEffects || statisticRouterCount(t, a, "plugin_message_queue") != wantEffects || int(sends.Load()) != wantEffects {
				t.Fatal("revoked source grant admitted a later effect", len(siteMessageHistory(t, a)), statisticRouterCount(t, a, "plugin_notice_publications"), statisticRouterCount(t, a, "plugin_message_queue"), sends.Load())
			}
			if mode == "unchanged" {
				if finished.State != "succeeded" {
					t.Fatal("unchanged grant failed positive control", finished.State, finished.ErrorCode)
				}
			} else {
				if finished.State == "succeeded" {
					t.Fatal("revoked source API returned success")
				}
				if _, err = a.pluginStore.siteStatisticReceipt(ctx, run); !errors.Is(err, sql.ErrNoRows) {
					t.Fatal("revoked source published API success receipt", err)
				}
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
			cold.pluginStore.notices.send = func(context.Context, pluginNoticeQueueEntry) (int, error) { sends.Add(1); return 1, nil }
			_ = cold.resumePluginNoticeRoutes(ctx, 200)
			if calls.Load() != 7 || len(siteMessageHistory(t, cold)) != wantHistory || statisticRouterCount(t, cold, "plugin_notice_publications") != wantEffects || statisticRouterCount(t, cold, "plugin_message_queue") != wantEffects || int(sends.Load()) != wantEffects {
				t.Fatal("cold replay bypassed revoked grant or repeated existing effect", calls.Load(), sends.Load())
			}
		})
	}
}

func TestSiteMessageDeletedUIHistoryDoesNotInvalidateOriginalAPICompletion(t *testing.T) {
	a, _, _, _ := pluginHTTPFixture(t)
	calls := siteMessageHTTPFixture(t, a, true)
	ctx := context.Background()
	var sends atomic.Int32
	a.pluginStore.notices.send = func(context.Context, pluginNoticeQueueEntry) (int, error) { sends.Add(1); return 1, nil }
	if _, err := a.Store.DB.Exec(`CREATE TRIGGER owned_v3_final_api_failure BEFORE INSERT ON plugin_site_statistic_operations BEGIN SELECT RAISE(ABORT,'owned final return receipt failure'); END`); err != nil {
		t.Fatal(err)
	}
	run := enqueueSyntheticPluginHTTP(t, a)
	a.executePluginRun(ctx, run)
	finished, err := a.pluginStore.Run(ctx, run.ID)
	if err != nil || finished.State != "reconcile_pending" {
		t.Fatal("local final receipt failure lost recoverable execution", finished.State, finished.ErrorCode, err)
	}
	history := siteMessageHistory(t, a)
	if len(history) != 2 || sends.Load() != 2 || calls.Load() != 7 {
		t.Fatal("messages were not actually committed before final receipt failure", len(history), sends.Load(), calls.Load())
	}
	if _, err = a.Store.DB.Exec(`DROP TRIGGER owned_v3_final_api_failure`); err != nil {
		t.Fatal(err)
	}
	// Delete only mutable UI history in this test's private SQLite database.
	// Authenticated Router steps remain to prove the original successful posts.
	for _, n := range history {
		if _, err = a.Store.DB.Exec(`DELETE FROM records WHERE kind='notification' AND id=?`, n.ID); err != nil {
			t.Fatal(err)
		}
	}
	dir := a.Dir
	if err = a.Close(); err != nil {
		t.Fatal(err)
	}
	cold, err := New(Options{DataDir: dir})
	if err != nil {
		t.Fatal("cold open incorrectly required deleted UI history", err)
	}
	defer cold.Close()
	cold.pluginStore.notices.send = func(context.Context, pluginNoticeQueueEntry) (int, error) { sends.Add(1); return 1, nil }
	if err = cold.resumePluginNoticeRoutes(ctx, 200); err != nil {
		t.Fatal("authenticated original completion did not survive history deletion", err)
	}
	recovered, err := cold.pluginStore.Run(ctx, run.ID)
	if err != nil || recovered.State != "succeeded" {
		t.Fatal("original API did not settle from immutable history proof", recovered.State, recovered.ErrorCode, err)
	}
	if _, err = cold.pluginStore.siteStatisticReceipt(ctx, run); err != nil {
		t.Fatal("API completion receipt missing", err)
	}
	if calls.Load() != 7 || sends.Load() != 2 || len(siteMessageHistory(t, cold)) != 0 {
		t.Fatal("recovery recollected, resent, or recreated deleted history", calls.Load(), sends.Load())
	}
	if statisticRouterCount(t, cold, "plugin_notice_publications") != 2 || statisticRouterCount(t, cold, "plugin_message_queue") != 2 {
		t.Fatal("cold recovery duplicated accepted effects")
	}
}

func TestSiteMessageSealedPendingBusinessAbsentOrNullAndRoutingRejectsNull(t *testing.T) {
	for _, mode := range []string{"absent", "null", "routing_null"} {
		t.Run(mode, func(t *testing.T) {
			a := newTestApp(t)
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
				if len(siteMessageHistory(t, a)) != 0 || statisticRouterCount(t, a, "plugin_notice_routes") != 0 {
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
