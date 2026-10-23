package app

import (
	"context"
	"encoding/json"
	"testing"

	"light-media/internal/plugins"
)

// These tests cover actual SQL inbox/job and queue admission boundaries while
// Lark remains unregistered. They do not substitute for the final SDK workflow.
func larkCoreFixture(t *testing.T, a *App, source string) (Job, string) {
	t.Helper()
	ctx := context.Background()
	installation, e := a.pluginStore.Installation(ctx, "LarkMessager")
	if e != nil {
		t.Fatal(e)
	}
	request := plugins.HTTPRequest{RequestID: id("ph_"), Key: "webhook", Method: "POST", Path: "/webhook"}
	run, e := a.enqueuePluginHTTP(ctx, installation, request, pluginAPIScope{Key: request.Key, Method: request.Method, RequestID: request.RequestID, Authority: pluginAPIAuthority{Kind: "anonymous"}})
	if e != nil {
		t.Fatal(e)
	}
	queued := run
	run, e = a.pluginStore.claimRunForInstance(ctx, queued.InstanceID)
	if e != nil || run.ID != queued.ID {
		t.Fatal("exact webhook claim", e)
	}
	if e = finishClaimedFixtureRun(ctx, a.pluginStore, run, "succeeded", "", nil); e != nil {
		t.Fatal(e)
	}
	var inv plugins.Invocation
	if e = json.Unmarshal(run.Input, &inv); e != nil {
		t.Fatal(e)
	}
	tx, e := a.Store.DB.BeginTx(ctx, nil)
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback()
	inboxID := id("li_")
	j, e := enqueueTx(ctx, tx, "lark_message", map[string]string{"id": inboxID}, inboxID)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = tx.ExecContext(ctx, `UPDATE jobs SET state='running',generation=1 WHERE id=?`, j.ID); e != nil {
		t.Fatal(e)
	}
	j.State, j.Generation = "running", 1
	root, e := a.pluginStore.larkRootRead(ctx, tx, run.InstanceID, run.Generation)
	if e != nil {
		t.Fatal(e)
	}
	f := pluginLarkInbox{Version: 1, ID: inboxID, Instance: run.InstanceID, Generation: run.Generation, Revision: run.ConfigRevision, OriginRun: run.ID, InputSHA: deferredInputSHA(run.Input), ConfigSHA: tokenHash(string(inv.Config)), Job: j.ID, JobInputSHA: tokenHash(string(j.Payload)), SourceID: source, State: "queued"}
	if e = a.pluginStore.larkSaveInboxTx(ctx, tx, root, f); e != nil {
		t.Fatal(e)
	}
	if e = tx.Commit(); e != nil {
		t.Fatal(e)
	}
	return j, inboxID
}
func TestPluginLarkConcurrentLiveCoreQueueAndColdUnknown(t *testing.T) {
	for _, boundary := range []string{"release", "cancel", "cold", "missing", "http_scope_missing", "http_scope_substituted"} {
		t.Run(boundary, func(t *testing.T) {
			a := newTestApp(t)
			installUnregisteredLarkConfig(t, a, map[string]any{"enabled": true, "app_id": "owned", "app_secret": "owned"})
			j1, id1 := larkCoreFixture(t, a, "owned/first")
			j2, id2 := larkCoreFixture(t, a, "owned/second")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			first := &pluginLarkCoreExecution{a: a, job: j1}
			second := &pluginLarkCoreExecution{a: a, job: j2}
			if _, e := a.larkInboxForJob(ctx, j1, id1, first); e != nil {
				t.Fatal(e)
			}
			defer first.release()
			if _, e := a.larkInboxForJob(ctx, j2, id2, second); e != nil {
				t.Fatal(e)
			}
			defer second.release()
			// Both authenticated live threads may enqueue child calls. Neither can
			// reset configuration while either source workflow is active.
			if _, e := a.pluginStore.EnqueueRun(ctx, "LarkMessager", "module", json.RawMessage(`{"call_id":"owned-child"}`)); e != nil {
				t.Fatal("two live source threads blocked each other", e)
			}
			if e := a.pluginStore.Reset(ctx, "LarkMessager"); pluginErrorCodeInternal(e) != "LARK_UNCONFIRMED" {
				t.Fatal("reset bypassed active workflows", e)
			}
			if _, _, e := a.pluginStore.SaveConfigAndQueue(ctx, "LarkMessager", first.inbox.Revision, map[string]any{}); pluginErrorCodeInternal(e) != "LARK_UNCONFIRMED" {
				t.Fatal("config bypassed active workflows", e)
			}
			want := "LARK_UNCONFIRMED"
			p := a.pluginStore
			switch boundary {
			case "release":
				first.release()
			case "cancel":
				cancel()
			case "cold":
				var e error
				p, e = NewPluginStore(context.Background(), a.Store)
				if e != nil {
					t.Fatal(e)
				}
			case "missing":
				_, e := a.Store.DB.Exec(`DELETE FROM plugin_lark_inbox WHERE inbox_id=?`, id1)
				if e != nil {
					t.Fatal(e)
				}
				want = "LARK_EVIDENCE_MISSING"
			case "http_scope_missing":
				_, e := a.Store.DB.Exec(`DELETE FROM plugin_api_calls WHERE run_id=?`, first.inbox.OriginRun)
				if e != nil {
					t.Fatal(e)
				}
				want = "LARK_EVIDENCE_MISSING"
			case "http_scope_substituted":
				_, e := a.Store.DB.Exec(`UPDATE plugin_api_calls SET scope=(SELECT scope FROM plugin_api_calls WHERE run_id=?) WHERE run_id=?`, second.inbox.OriginRun, first.inbox.OriginRun)
				if e != nil {
					t.Fatal(e)
				}
				want = "LARK_EVIDENCE_MISSING"
			}
			if _, e := p.EnqueueRun(context.Background(), "LarkMessager", "module", json.RawMessage(`{"call_id":"must-not-replay"}`)); pluginErrorCodeInternal(e) != want {
				t.Fatal("inactive or missing source proof was trusted", boundary, e)
			}
		})
	}
}
