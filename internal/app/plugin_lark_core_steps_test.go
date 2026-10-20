package app

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"light-media/internal/catalog"
)

func larkStartedCoreFixture(t *testing.T, a *App) (*pluginLarkCoreExecution, context.Context) {
	t.Helper()
	j, id := larkCoreFixture(t, a, "owned/steps")
	core := &pluginLarkCoreExecution{a: a, job: j}
	ctx := context.Background()
	if _, e := a.larkInboxForJob(ctx, j, id, core); e != nil {
		t.Fatal(e)
	}
	t.Cleanup(core.release)
	return core, context.WithValue(ctx, pluginLarkCoreKey{}, core)
}
func TestPluginLarkCoreReadFreezeAndAuthenticatedSession(t *testing.T) {
	a := newTestApp(t)
	installUnregisteredLarkConfig(t, a, map[string]any{"enabled": true})
	core, ctx := larkStartedCoreFixture(t, a)
	var calls atomic.Int64
	query := func(context.Context) ([]catalog.Media, error) {
		calls.Add(1)
		return []catalog.Media{{ID: 42, MediaType: "movie", Title: "Owned"}}, nil
	}
	got, e := larkCoreRead(ctx, a, core, "owned_search", "Owned", query)
	if e != nil || len(got) != 1 {
		t.Fatal(got, e, "calls", calls.Load())
	}
	session := pluginLarkMediaSession{Request: "owned-request", User: "ou_owned", Phase: "media", Medias: got}
	saved, e := a.larkSaveMediaSession(ctx, core, session)
	if e != nil {
		t.Fatal(e)
	}
	core.ordinal = 0
	got, e = larkCoreRead(ctx, a, core, "owned_search", "Owned", func(context.Context) ([]catalog.Media, error) {
		t.Fatal("read replay dispatched new query")
		return nil, nil
	})
	if e != nil || len(got) != 1 || calls.Load() != 1 {
		t.Fatal(got, e, calls.Load())
	}
	replay, e := a.larkSaveMediaSession(ctx, core, session)
	if e != nil || replay.Sequence != saved.Sequence || replay.Updated != saved.Updated {
		t.Fatal("session replay rewrote accepted TTL or sequence", e)
	}
	current, e := a.larkCurrentMediaSession(ctx, core, "ou_owned")
	if e != nil || current == nil || current.Request != session.Request {
		t.Fatal(current, e)
	}
	missing, e := a.larkCurrentMediaSession(ctx, core, "ou_other")
	if e != nil || missing != nil {
		t.Fatal("session escaped user scope", missing, e)
	}
	// An entire encrypted cell deletion is detected against the accepted inbox
	// head, rather than interpreted as a new empty interaction.
	if _, e = a.Store.DB.Exec(`DELETE FROM plugin_lark_inbox_steps WHERE inbox_id=? AND ordinal=1`, core.inbox.ID); e != nil {
		t.Fatal(e)
	}
	if _, e = a.larkCurrentMediaSession(ctx, core, "ou_owned"); pluginErrorCodeInternal(e) != "LARK_EVIDENCE_MISSING" {
		t.Fatal("deleted session recreated", e)
	}
}
func TestPluginLarkCoreLocalWriteLostAcknowledgementNeverReplays(t *testing.T) {
	a := newTestApp(t)
	installUnregisteredLarkConfig(t, a, map[string]any{"enabled": true})
	core, ctx := larkStartedCoreFixture(t, a)
	var writes atomic.Int64
	_, e := larkCoreWrite(ctx, a, core, "owned_p0_write", "source-42", func(ctx context.Context) (string, error) {
		writes.Add(1)
		if _, e := a.Store.DB.ExecContext(ctx, `INSERT INTO events(time,workflow_id,job_id,level,module,event,message) VALUES('owned','','','INFO','owned','effect','accepted')`); e != nil {
			return "", e
		}
		return "", errors.New("owned ACK lost after committed effect")
	})
	if pluginErrorCodeInternal(e) != "LARK_UNCONFIRMED" {
		t.Fatal(e)
	}
	_, e = larkCoreWrite(ctx, a, core, "owned_p0_write", "source-42", func(context.Context) (string, error) { writes.Add(1); return "duplicate", nil })
	if pluginErrorCodeInternal(e) != "LARK_UNCONFIRMED" || writes.Load() != 1 {
		t.Fatal("write re-dispatched", writes.Load(), e)
	}
	core.release()
	cold, e := NewPluginStore(context.Background(), a.Store)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = cold.EnqueueRun(context.Background(), "LarkMessager", "module", []byte(`{"call_id":"new"}`)); pluginErrorCodeInternal(e) != "LARK_UNCONFIRMED" {
		t.Fatal("cold source bypassed unknown write", e)
	}
}
