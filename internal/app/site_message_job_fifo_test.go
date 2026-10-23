package app

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"light-media/internal/sites"
)

func TestSiteMessageAcceptedOrdinalSurvivesRealJobCompletionStates(t *testing.T) {
	for _, state := range []string{"retry_wait", "waiting_paused", "failed", "cancelled"} {
		t.Run(state, func(t *testing.T) {
			a := newTestApp(t)
			ctx := context.Background()
			calls := siteMessageHTTPFixture(t, a, true, true)
			clock := siteMessageClosedFIFO(t, a)
			sent := siteMessageOwnedFIFOSender(t, a)
			// Both rows come from the actual owned HTTP parser fixture. Only the
			// second Router publication is faulted; its earlier history commit is
			// deliberately preserved. No artificial collection is used for this job.
			if _, err := a.Store.DB.Exec(`CREATE TRIGGER owned_second_message_unknown BEFORE INSERT ON plugin_notice_publications WHEN (SELECT count(*) FROM plugin_notice_publications)=1 BEGIN SELECT RAISE(ABORT,'owned second ordinal publication unknown'); END`); err != nil {
				t.Fatal(err)
			}
			job := siteMessageClaimBatch(t, a)
			originalGeneration := job.Generation
			collectionID := a.pluginStore.noticeID("site-collection", "job", job.ID, "known")
			attempts := 1
			if state == "failed" {
				attempts = 3
			}
			for attempt := 1; attempt <= attempts; attempt++ {
				err := a.runSiteRefresh(ctx, job)
				var post *siteMessagePostError
				if !errors.As(err, &post) {
					t.Fatal("second ordinal unknown did not stop real batch", attempt, err)
				}
				receipt, readErr := a.pluginStore.readSiteMessageCollection(ctx, a.Store.DB, collectionID)
				if readErr != nil || receipt.Stage != "collected" || len(receipt.Steps) != 3 || receipt.Steps[0].State != "complete" || receipt.Steps[1].State != "routing" || receipt.Steps[2].State != "pending" {
					t.Fatal("unknown second ordinal fabricated a source result", receipt, readErr)
				}
				if receipt.Origin.Generation != int64(originalGeneration) || calls.Load() != 7 || len(sent()) != 0 {
					t.Fatal("retry changed original fact, recollected, or sent during quiet hours", receipt.Origin, calls.Load(), sent())
				}
				nextState, code, runAt := state, "TASK_FAILED", time.Now().Add(time.Hour).Unix()
				if state == "failed" && attempt < 3 {
					nextState = "retry_wait"
					runAt = time.Now().Unix()
				}
				if state == "waiting_paused" {
					code = "AUTOMATION_PAUSED"
				}
				if state == "cancelled" {
					// Exercise the production cancellation CAS and generation increment.
					req := httptest.NewRequest(http.MethodPost, "/api/jobs/"+job.ID+"/cancel", nil)
					if _, err = a.jobAPI(httptest.NewRecorder(), req, job.ID+"/cancel"); err != nil {
						t.Fatal(err)
					}
				} else {
					// This is the production worker's transactional completion function,
					// not an SQL rewrite of job.state or a mock worker. For the pause case
					// the pause outcome is supplied explicitly; the scheduler is not run.
					changed, finishErr := a.finishJob(ctx, job, nextState, code, err.Error(), runAt)
					if finishErr != nil || !changed {
						t.Fatal("real completion transition failed", nextState, changed, finishErr)
					}
				}
				stored, readErr := scanJob(a.Store.DB.QueryRowContext(ctx, `SELECT `+jobColumns+` FROM jobs WHERE id=?`, job.ID))
				if readErr != nil || stored.State != nextState {
					t.Fatal("original job was left running", stored.State, nextState, readErr)
				}
				if state == "failed" && attempt < 3 {
					next, claimErr := a.claim(ctx)
					if claimErr != nil || next.ID != job.ID || next.Attempt != attempt+1 || next.Generation <= job.Generation {
						t.Fatal("retry did not obtain real next worker lease", next, claimErr)
					}
					job = next
				}
			}
			if state == "failed" {
				var failures int
				if err := a.Store.DB.QueryRow(`SELECT count(*) FROM domain_events WHERE json_extract(payload,'$.event_type')='job.failed' AND json_extract(payload,'$.job_id')=?`, job.ID).Scan(&failures); err != nil || failures != 1 {
					t.Fatal("terminal third attempt lacks real job.failed fact", failures, err)
				}
			}
			ids := siteMessageFIFOIDs(t, a)
			if len(ids) != 1 || len(siteMessageHistory(t, a)) != 2 {
				t.Fatal("second unknown post was enqueued, or its earlier history lost", ids)
			}
			// Remove only the SQL fault, not the original unknown receipt. A separate
			// job and enabled site provide an independent FIFO successor. enqueue is
			// the core producer; its distinct payload avoids global refresh dedup.
			if _, err := a.Store.DB.Exec(`DROP TRIGGER owned_second_message_unknown`); err != nil {
				t.Fatal(err)
			}
			queued, err := a.enqueue(ctx, "site_refresh", map[string]string{"id": "independent-fifo-successor"}, "")
			if err != nil {
				t.Fatal(err)
			}
			independent, err := a.claim(ctx)
			if err != nil || independent.ID != queued.ID {
				t.Fatal("independent worker grant missing", independent, err)
			}
			next := siteMessageSeedCollectedDTO(t, a, independent, "independent-after-job-outcome", &sites.SourceUserData{MessageUnread: 1, Messages: [][]any{{"next legal message", "date", "body"}}})
			complete, err := a.finishSiteMessageCollection(ctx, next.ID)
			if err != nil || complete.Stage != "complete" {
				t.Fatal("independent source failed", complete.Stage, err)
			}
			changed, err := a.finishJob(ctx, independent, "succeeded", "", "", time.Now().Unix())
			if err != nil || !changed {
				t.Fatal("successor left artificially running", changed, err)
			}
			ids = siteMessageFIFOIDs(t, a)
			if len(ids) != 2 || len(sent()) != 0 {
				t.Fatal("quiet hours or independent FIFO order lost", ids, sent())
			}
			clock.Store(time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC).UnixNano())
			if err = a.drainPluginMessages(ctx, 200); err != nil {
				t.Fatal("job scheduling outcome blocked previously accepted ordinal or successor", state, err)
			}
			wantState := "consumed"
			if state == "cancelled" {
				wantState = "revoked"
				if len(sent()) != 1 || sent()[0] != ids[1] {
					t.Fatal("explicit cancellation sent or starved successor", ids, sent())
				}
			} else if len(sent()) != 2 || sent()[0] != ids[0] || sent()[1] != ids[1] {
				t.Fatal("retry, pause or failure incorrectly revoked accepted ordinal", state, ids, sent())
			}
			first, err := a.pluginStore.readPluginMessage(ctx, a.Store.DB, ids[0])
			if err != nil || first.State != wantState {
				t.Fatal("first FIFO lacks correct authenticated terminal receipt", first.State, wantState, err)
			}
			successor, err := a.pluginStore.readPluginMessage(ctx, a.Store.DB, ids[1])
			if err != nil || successor.State != "consumed" {
				t.Fatal("successor did not complete", successor.State, err)
			}
			receipt, err := a.pluginStore.readSiteMessageCollection(ctx, a.Store.DB, collectionID)
			if err != nil || receipt.Stage != "collected" || receipt.Steps[1].State != "routing" || receipt.Steps[2].State != "pending" || calls.Load() != 7 {
				t.Fatal("FIFO delivery resumed unknown later ordinal or recollected", receipt, err, calls.Load())
			}
			if len(siteMessageFIFOIDs(t, a)) != 2 || len(siteMessageHistory(t, a)) != 3 {
				t.Fatal("unknown second ordinal or ratio gained new side effects")
			}
			siteMessageColdDrainNoResend(t, a)
			if calls.Load() != 7 {
				t.Fatal("cold drain recollected original source", calls.Load())
			}
		})
	}
}
