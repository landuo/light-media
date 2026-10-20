package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
)

func TestPluginNoticeV2ActualQualityAcceptedPrepareHistoryDeleteNoticeColdAndGuard(t *testing.T) {
	for _, mode := range []string{"normal", "accept_reply_lost", "history_failure"} {
		t.Run(mode, func(t *testing.T) {
			qualityActualMovieWorkflow(t, "normal", func(a *App, s Subscription) {
				ctx := context.Background()
				fixture := installNoticeSourceFixtures(t, a, s.Media, 0, 0)
				var lost atomic.Bool
				if mode == "accept_reply_lost" {
					a.pluginStore.notices.afterCommit = func(operation string) error {
						if operation == "business_accept" && lost.CompareAndSwap(false, true) {
							return errors.New("owned completion route Commit ACK lost")
						}
						return nil
					}
				}
				prepare, e := scanJob(a.Store.DB.QueryRow(`SELECT `+jobColumns+` FROM jobs WHERE kind='subscription_completion_prepare' AND json_extract(payload,'$.id')=? ORDER BY rowid LIMIT 1`, s.ID))
				if e != nil {
					t.Fatal("real accepted fact did not enqueue completion prepare", e)
				}
				prepare = qualityRunningJob(t, a, prepare)
				if mode == "history_failure" {
					if _, e = a.Store.DB.Exec(`CREATE TRIGGER completion_history_failure BEFORE INSERT ON records WHEN NEW.kind='subscription_history' BEGIN SELECT RAISE(ABORT,'owned history write failure'); END`); e != nil {
						t.Fatal(e)
					}
				}
				e = a.prepareSubscriptionCompletion(ctx, prepare, s.ID, s.Revision)
				if mode == "history_failure" {
					if e == nil {
						t.Fatal("history failure deleted source subscription")
					}
					var n, jobs int
					a.Store.DB.QueryRow(`SELECT count(*) FROM records WHERE kind='subscription' AND id=?`, s.ID).Scan(&n)
					a.Store.DB.QueryRow(`SELECT count(*) FROM jobs WHERE kind='subscription_completion'`).Scan(&jobs)
					if n != 1 || jobs != 0 || fixture.Sends.Load() != 0 {
						t.Fatal("history/check/delete transaction was partial", n, jobs, fixture.Sends.Load())
					}
					return
				}
				if e != nil {
					t.Fatal("real full source factory/check/history/delete failed", e)
				}
				var n int
				if e = a.Store.DB.QueryRow(`SELECT count(*) FROM records WHERE kind='subscription' AND id=?`, s.ID).Scan(&n); e != nil || n != 0 {
					t.Fatal("source row not deleted after original history", n, e)
				}
				complete, e := scanJob(a.Store.DB.QueryRow(`SELECT ` + jobColumns + ` FROM jobs WHERE kind='subscription_completion' ORDER BY rowid LIMIT 1`))
				if e != nil {
					t.Fatal(e)
				}
				complete = qualityRunningJob(t, a, complete)
				var ref struct {
					ID string `json:"id"`
				}
				if e = json.Unmarshal(complete.Payload, &ref); e != nil {
					t.Fatal(e)
				}
				e = a.runSubscriptionCompletion(ctx, complete, ref.ID)
				if e != nil {
					t.Fatal("actual native Notice/statistics admission failed", e)
				}
				if mode == "accept_reply_lost" && !lost.Load() {
					t.Fatal("lost ACK did not cross real route SQL Commit")
				}
				var receipt subscriptionCompletionReceipt
				if e = a.readQualitySealed(ctx, a.Store.DB, "subscription_completion", ref.ID, &receipt); e != nil || receipt.Stage != "complete" {
					t.Fatal("successful statistics admission did not commit completion", receipt.Stage, e)
				}
				statsJob, statsReceipt := subscriptionStatsOnly(t, a)
				statsJob = qualityRunningJob(t, a, statsJob)
				if e = a.safeRun(ctx, statsJob); pluginErrorCodeInternal(e) != "SUBSCRIPTION_STATS_DEPENDENCY" {
					t.Fatal("missing source settings were invented or blocked parent", e)
				}
				if e = a.readQualitySealed(ctx, a.Store.DB, "subscription_stats", statsReceipt.ID, &statsReceipt); e != nil || statsReceipt.Stage != "dependency" {
					t.Fatal("child dependency not durable", e)
				}
				r := noticeV2OnlyRoute(t, a)
				rendered, e := a.pluginStore.noticeBusinessRender(ctx, r.ID)
				if e != nil || !r.Complete || r.SourceError != "" || r.BusinessV2.Binding.Kind != "subscription_completion" || r.BusinessV2.Binding.Completion == nil || *r.BusinessV2.Binding.Completion != receipt.Identity || !strings.Contains(rendered.Snapshot.Fields["title"].(string), "已完成洗版") || fixture.Metadata.Load() != 1 || fixture.Sends.Load() != 1 {
					t.Fatal("quality completion used fixed text or lacked actual authenticated route", e, fixture.Metadata.Load(), fixture.Sends.Load())
				}
				var histories, events int
				a.Store.DB.QueryRow(`SELECT count(*) FROM records WHERE kind='subscription_history'`).Scan(&histories)
				a.Store.DB.QueryRow(`SELECT count(*) FROM records WHERE kind='source_subscribe_complete'`).Scan(&events)
				if histories != 1 || events != 1 {
					t.Fatal("history or SourceComplete missing after durable stats admission", histories, events)
				}
				result, e := a.postSubscriptionCompletionRawNotice(ctx, receipt.Identity, receipt.Input.Source.Business)
				if e != nil || fixture.Sends.Load() != 1 {
					t.Fatal("actual complete Notice result repeated delivery", e)
				}
				wrong := receipt.Identity
				wrong.SourceSHA = strings.Repeat("0", 64)
				if _, e = a.postSubscriptionCompletionRawNotice(ctx, wrong, receipt.Input.Source.Business); e == nil || fixture.Sends.Load() != 1 {
					t.Fatal("unrelated completion identity reused send authority", e)
				}
				tx, e := a.Store.DB.BeginTx(ctx, nil)
				if e != nil {
					t.Fatal(e)
				}
				if e = a.requireSubscriptionCompletionRawNoticeTx(ctx, tx, receipt.Identity, receipt.Input.Source.Business, result); e != nil {
					t.Fatal("actual final source guard rejected sealed native completion", e)
				}
				tx.Rollback()
				var falseResult pluginNoticeV2Result
				cleanupJournalStrict(result, &falseResult)
				falseResult.SourceReturned = false
				tx, e = a.Store.DB.BeginTx(ctx, nil)
				if e != nil {
					t.Fatal(e)
				}
				if e = a.requireSubscriptionCompletionRawNoticeTx(ctx, tx, receipt.Identity, receipt.Input.Source.Business, mustCleanSeedJSON(falseResult)); e == nil {
					t.Fatal("false source return bypassed final event guard")
				}
				tx.Rollback()

				var publication string
				if e = a.Store.DB.QueryRow(`SELECT id FROM plugin_notice_publications WHERE route_id=?`, r.ID).Scan(&publication); e != nil {
					t.Fatal(e)
				}
				if _, e = a.Store.DB.Exec(`UPDATE jobs SET state='retry_wait' WHERE id=?`, complete.ID); e != nil {
					t.Fatal(e)
				}
				message, e := a.pluginStore.noticeMessage(ctx, a.Store.DB, publication)
				if e != nil || !strings.Contains(message["title"].(string), "已完成洗版") {
					t.Fatal("immutable committed publication disappeared when source job waits", e)
				}
				if _, e = a.postSubscriptionCompletionRawNotice(ctx, receipt.Identity, receipt.Input.Source.Business); e != nil || fixture.Sends.Load() != 1 {
					t.Fatal("completed readback repeated send", e)
				}
				if e = a.runNoticeRoute(ctx, r.ID); e != nil || fixture.Sends.Load() != 1 {
					t.Fatal("complete route read repeated HTTP", e)
				}
				settings, dir := a.Settings(), a.Dir
				if e = a.Close(); e != nil {
					t.Fatal(e)
				}
				cold, e := New(Options{DataDir: dir})
				if e != nil {
					t.Fatal(e)
				}
				defer cold.Close()
				cold.config = settings
				cold.pluginStore.notices.send = func(context.Context, pluginNoticeQueueEntry) (int, error) {
					t.Fatal("cold completion repeated owned HTTP")
					return 0, errors.New("ungranted")
				}
				coldJob := qualityRunningJob(t, cold, complete)
				if e = cold.runSubscriptionCompletion(ctx, coldJob, ref.ID); e != nil {
					t.Fatal("cold source completion did not consume same frozen Notice result", e)
				}
				if fixture.Sends.Load() != 1 || fixture.Metadata.Load() != 1 || cold.pluginStore.noticeTemplateHelper().Cache.Get(rendered.SourceCache.Key) != nil {
					t.Fatal("cold replay repeated fresh metadata/send or reset ephemeral cache")
				}
				if _, e = cold.Store.DB.Exec(`DELETE FROM records WHERE kind='download_added'`); e != nil {
					t.Fatal(e)
				}
				if _, _, e = cold.pluginStore.readNoticeRoute(ctx, cold.Store.DB, r.ID); e == nil || errors.Is(e, sql.ErrNoRows) {
					t.Fatal("missing actual accepted download closure was replaced by completion bit", e)
				}
			})
		})
	}
}
