package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// Expected order is fixed chain/subscribe.py:2805-2867: synchronous check,
// history, delete, post_message, complete broadcast, asynchronous statistics.
// Network peers and media are owned protocol fixtures; no external delivery
// or production downloader acceptance is claimed by this composed test.
func TestStandardSubscriptionCompletionActualCoreSDKVetoFailureAndRestart(t *testing.T) {
	oracleRaw, err := os.ReadFile("../../testdata/standard-subscription-events/source-oracle.json")
	if err != nil {
		t.Fatal(err)
	}
	var oracle struct {
		Defaults map[string]json.RawMessage `json:"completion_check_defaults"`
		Source   struct {
			Calls []struct{ Name string } `json:"ordered_calls"`
		} `json:"completion_source"`
	}
	if json.Unmarshal(oracleRaw, &oracle) != nil {
		t.Fatal("invalid fixed source oracle")
	}
	calls := []string{}
	for _, call := range oracle.Source.Calls {
		calls = append(calls, call.Name)
	}
	if !reflect.DeepEqual(calls, []string{"ChainEventType.SubscribeCompletionCheck", "add_history", "delete", "post_message", "EventType.SubscribeComplete", "sub_done_async"}) {
		t.Fatal("fixed original finish source changed", calls)
	}
	ordinaryMovieDownloadWorkflow(t, "movie", func(a *App, s Subscription) {
		ctx := context.Background()
		cookie := installOwnedEventFixture(t, a)
		response := callTest(t, a.Handler(), "POST", "/api/plugins/"+ownedEventFixtureID+"/clone", `{"suffix":"Completion"}`, cookie, "")
		assertOriginalRegistrySuccess(t, response.Code, response.Body.Bytes())
		a.wg.Wait()
		clone := ownedEventFixtureID + "completion"
		setOwnedEventFixtureConfig(t, a, cookie, clone, true, false)
		checker := installOrderWorker(t, a, cookie, os.Getenv("OWNED_COMPLETION_CHECK_FIXTURE_BINARY"))
		trace := filepath.Join(t.TempDir(), "completion-check.jsonl")
		configure := func(mode string) {
			raw, _ := json.Marshal(map[string]any{"enabled": true, "mode": mode, "source": "Owned completion veto", "reason": "owned unfinished season", "trace_path": trace})
			response := callTest(t, a.Handler(), "PUT", "/api/plugins/"+checker.PluginID, string(raw), cookie, "")
			assertOriginalRegistrySuccess(t, response.Code, response.Body.Bytes())
			a.wg.Wait()
		}
		notice := installNoticeSourceFixtures(t, a, s.Media, 0, 0)
		prepare, err := scanJob(a.Store.DB.QueryRow(`SELECT `+jobColumns+` FROM jobs WHERE kind='subscription_completion_prepare' AND json_extract(payload,'$.id')=? ORDER BY rowid LIMIT 1`, s.ID))
		if err != nil {
			t.Fatal(err)
		}
		prepare = qualityRunningJob(t, a, prepare)
		configure("cancel_error")
		if err = a.prepareSubscriptionCompletion(ctx, prepare, s.ID, s.Revision); err != nil {
			t.Fatal(err)
		}
		var current Subscription
		if err = a.Store.Get(ctx, "subscription", s.ID, &current); err != nil || current.State != "active" || current.LastReason != "完成被 Owned completion veto 否决：owned unfinished season" {
			t.Fatal("real chain veto did not stop before history/delete", current.LastReason, err)
		}
		var histories int
		a.Store.DB.QueryRow(`SELECT count(*) FROM records WHERE kind='subscription_history'`).Scan(&histories)
		if histories != 0 || notice.Sends.Load() != 0 {
			t.Fatal("veto allowed finish side effects")
		}
		requireBusinessEventCount(t, a, clone, "subscribe.complete", 0)
		rows := readOrderTrace(t, trace)
		if len(rows) != 2 {
			t.Fatal("actual declared chain methods were not both called", len(rows))
		}
		var check struct{ Data map[string]json.RawMessage }
		if json.Unmarshal(rows[0].Input, &check) != nil || string(check.Data["cancel"]) != "false" || string(check.Data["source"]) != `"未知来源"` || len(check.Data["subscribe"]) == 0 || len(check.Data["mediainfo"]) == 0 || len(check.Data["meta"]) == 0 {
			t.Fatal("source check schema/defaults missing")
		}
		for _, key := range []string{"cancel", "source", "reason"} {
			if !originalBaseJSONEqual(check.Data[key], oracle.Defaults[key]) {
				t.Fatal("check differs from fixed Pydantic defaults", key)
			}
		}
		// An ordinary return containing cancel=true must not replace shared data.
		configure("ordinary")
		if _, err = a.Store.DB.Exec(`CREATE TRIGGER owned_finish_history_rollback BEFORE INSERT ON records WHEN NEW.kind='subscription_history' BEGIN SELECT RAISE(ABORT,'owned history rollback'); END`); err != nil {
			t.Fatal(err)
		}
		if err = a.prepareSubscriptionCompletion(ctx, prepare, s.ID, s.Revision); err == nil {
			t.Fatal("ordinary return vetoed or history failure ignored")
		}
		requireBusinessEventCount(t, a, clone, "subscribe.complete", 0)
		if _, err = a.Store.DB.Exec(`DROP TRIGGER owned_finish_history_rollback`); err != nil {
			t.Fatal(err)
		}
		if err = a.prepareSubscriptionCompletion(ctx, prepare, s.ID, s.Revision); err != nil {
			t.Fatal(err)
		}
		if err = a.Store.Get(ctx, "subscription", s.ID, &current); err == nil {
			t.Fatal("actual source history/delete did not finish")
		}
		complete, err := scanJob(a.Store.DB.QueryRow(`SELECT ` + jobColumns + ` FROM jobs WHERE kind='subscription_completion' ORDER BY rowid LIMIT 1`))
		if err != nil {
			t.Fatal(err)
		}
		complete = qualityRunningJob(t, a, complete)
		var ref struct {
			ID string `json:"id"`
		}
		if json.Unmarshal(complete.Payload, &ref) != nil {
			t.Fatal("invalid actual completion job")
		}
		if _, err = a.Store.DB.Exec(`CREATE TRIGGER owned_finish_event_rollback BEFORE INSERT ON records WHEN NEW.kind='source_subscribe_complete' BEGIN SELECT RAISE(ABORT,'owned event rollback'); END`); err != nil {
			t.Fatal(err)
		}
		if err = a.runSubscriptionCompletion(ctx, complete, ref.ID); err == nil {
			t.Fatal("failed final transaction accepted")
		}
		requireBusinessEventCount(t, a, clone, "subscribe.complete", 0)
		if notice.Sends.Load() != 1 {
			t.Fatal("source notification was not actually attempted before completion broadcast")
		}
		if _, err = a.Store.DB.Exec(`DROP TRIGGER owned_finish_event_rollback`); err != nil {
			t.Fatal(err)
		}
		setOwnedEventFixtureConfig(t, a, cookie, ownedEventFixtureID, true, true)
		if err = a.runSubscriptionCompletion(ctx, complete, ref.ID); err != nil {
			t.Fatal("callback failure changed committed finish", err)
		}
		events := requireBusinessEventCount(t, a, clone, "subscribe.complete", 1)
		var receipt subscriptionCompletionReceipt
		if err = a.readQualitySealed(ctx, a.Store.DB, "subscription_completion", ref.ID, &receipt); err != nil || receipt.Stage != "complete" {
			t.Fatal(err)
		}
		var got struct {
			ID        string          `json:"subscribe_id"`
			Subscribe json.RawMessage `json:"subscribe_info"`
			Media     json.RawMessage `json:"mediainfo"`
		}
		if json.Unmarshal(events[0].Data, &got) != nil || got.ID != s.ID || !originalBaseJSONEqual(got.Subscribe, receipt.Input.Source.Subscribe) || !originalBaseJSONEqual(got.Media, receipt.Input.Source.MediaInfo) {
			t.Fatal("broadcast replaced full original source objects with compact data")
		}
		if err = a.runSubscriptionCompletion(ctx, complete, ref.ID); err != nil {
			t.Fatal(err)
		}
		requireBusinessEventCount(t, a, clone, "subscribe.complete", 1)
		settings, dir := a.Settings(), a.Dir
		if err = a.Close(); err != nil {
			t.Fatal(err)
		}
		cold, err := New(Options{DataDir: dir})
		if err != nil {
			t.Fatal(err)
		}
		defer cold.Close()
		cold.config = settings
		if err = cold.initializeOriginalPlugins(ctx); err != nil {
			t.Fatal(err)
		}
		complete = qualityRunningJob(t, cold, complete)
		if err = cold.runSubscriptionCompletion(ctx, complete, ref.ID); err != nil {
			t.Fatal("cold completion failed", err)
		}
		requireBusinessEventCount(t, cold, clone, "subscribe.complete", 1)
		if notice.Sends.Load() != 1 {
			t.Fatal("retry/cold recovery repeated successful source notification")
		}
	})
}
