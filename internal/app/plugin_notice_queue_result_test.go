package app

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
)

func TestPluginNoticeQueueActualResultCASAndSourceChanges(t *testing.T) {
	for _, mode := range []string{"normal", "cancel_context", "parent_stopped", "route_removed", "resealed_intent", "identity_changed", "finish_sql_failure", "intent_write_tamper", "finish_write_tamper", "finish_ack_lost"} {
		t.Run(mode, func(t *testing.T) {
			a, in, fixture := uiSourceAddFixture(t)
			subscriptionStatsInstallSettings(t, a, "http://127.0.0.1:1", false)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			actualSend := a.pluginStore.notices.send
			var lost atomic.Bool
			if mode == "finish_ack_lost" {
				a.pluginStore.notices.afterCommit = func(op string) error {
					if op == "queue_finish" && lost.CompareAndSwap(false, true) {
						return errors.New("owned committed exact result ACK lost")
					}
					return nil
				}
			}
			if mode == "intent_write_tamper" {
				if _, e := a.Store.DB.Exec(`CREATE TRIGGER owned_intent_tamper AFTER UPDATE ON plugin_message_queue WHEN NEW.state='intent' BEGIN UPDATE plugin_message_queue SET sealed=x'00' WHERE id=NEW.id; END`); e != nil {
					t.Fatal(e)
				}
			}
			a.pluginStore.notices.send = func(ctx context.Context, q pluginNoticeQueueEntry) (int, error) {
				modules, cause := actualSend(ctx, q)
				if cause != nil {
					t.Error(cause)
					return modules, cause
				}
				var e error
				switch mode {
				case "cancel_context":
					cancel()
				case "parent_stopped":
					_, e = a.Store.DB.Exec(`UPDATE jobs SET state='cancelled',generation=generation+1 WHERE kind='subscription_add' AND state='running'`)
				case "route_removed":
					_, e = a.Store.DB.Exec(`DELETE FROM plugin_notice_routes WHERE id=?`, q.RouteID)
				case "resealed_intent":
					var sealed []byte
					var stored pluginNoticeQueueEntry
					e = a.Store.DB.QueryRow(`SELECT sealed FROM plugin_message_queue WHERE id=?`, q.ID).Scan(&sealed)
					if e == nil {
						e = a.pluginStore.noticeUnseal("queue", q.ID, sealed, &stored)
					}
					if e == nil {
						sealed, e = a.pluginStore.noticeSeal("queue", q.ID, stored)
					}
					if e == nil {
						_, e = a.Store.DB.Exec(`UPDATE plugin_message_queue SET sealed=? WHERE id=?`, sealed, q.ID)
					}
				case "identity_changed":
					_, e = a.Store.DB.Exec(`UPDATE plugin_message_queue SET ordinal=ordinal+1 WHERE id=?`, q.ID)
				case "finish_sql_failure":
					_, e = a.Store.DB.Exec(`CREATE TRIGGER owned_finish_failure BEFORE UPDATE ON plugin_message_queue WHEN NEW.state='consumed' BEGIN SELECT RAISE(ABORT,'owned final receipt SQL failure'); END`)
				case "finish_write_tamper":
					_, e = a.Store.DB.Exec(`CREATE TRIGGER owned_finish_tamper AFTER UPDATE ON plugin_message_queue WHEN NEW.state='consumed' BEGIN UPDATE plugin_message_queue SET sealed=x'00' WHERE id=NEW.id; END`)
				}
				if e != nil {
					t.Error(e)
				}
				return modules, cause
			}
			_, cause := a.createUISubscription(ctx, in, "owned-native-admin")
			ids := noticeRouteTestIDs(t, a, "plugin_message_queue")
			if len(ids) != 1 {
				t.Fatal("expected one actual queue entry", len(ids))
			}
			var state string
			var sealed []byte
			if e := a.Store.DB.QueryRow(`SELECT state,sealed FROM plugin_message_queue WHERE id=?`, ids[0]).Scan(&state, &sealed); e != nil {
				t.Fatal(e)
			}
			var queue pluginNoticeQueueEntry
			if e := a.pluginStore.noticeUnseal("queue", ids[0], sealed, &queue); e != nil {
				t.Fatal("tamper was committed rather than rolled back", e)
			}
			wantState, wantSends, wantAdded := "consumed", int64(1), 0
			switch mode {
			case "normal", "finish_ack_lost":
				wantAdded = 1
				if cause != nil || mode == "finish_ack_lost" && !lost.Load() {
					t.Fatal("exact normal result failed to continue", cause)
				}
			case "intent_write_tamper":
				wantState, wantSends = "queued", 0
			case "resealed_intent", "identity_changed", "finish_sql_failure", "finish_write_tamper":
				wantState = "intent"
			}
			if state != wantState || queue.State != state || fixture.Sends.Load() != wantSends || noticeV2AddedCount(t, a) != wantAdded {
				t.Fatal("known result or exact CAS condition lost", state, queue.State, fixture.Sends.Load(), noticeV2AddedCount(t, a), cause)
			}
			if wantAdded == 0 && cause == nil {
				t.Fatal("changed source/intent admitted another business action")
			}
			if state == "consumed" && (queue.Modules != 1 || queue.ErrorCode != "" || queue.FinishedAt == "") {
				t.Fatal("known actual sender result was overwritten", queue.State, queue.Modules, queue.ErrorCode)
			}
			if mode == "finish_sql_failure" {
				if _, e := a.Store.DB.Exec(`DROP TRIGGER owned_finish_failure`); e != nil {
					t.Fatal(e)
				}
			}
			_ = a.attemptPluginMessage(context.Background(), ids[0])
			if fixture.Sends.Load() != wantSends {
				t.Fatal("changed or already sent intent reissued HTTP")
			}
		})
	}
}
