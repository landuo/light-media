package app

import (
	"context"
	"encoding/json"
	"errors"
	"light-media/internal/plugins/autosignin"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	durable "light-media/internal/plugins/autosignin/durable"
)

func signinSourceRequest(t *testing.T, f *signinSQLFixture, kind, payload string) (*pluginAutoSignInSourcePort, durable.SourceRequest) {
	t.Helper()
	ctx := context.Background()
	s := newAutoSignInSourcePort(f.b).(*pluginAutoSignInSourcePort)
	cfg, err := s.Bind(ctx, f.o.binding, f.run.ID)
	if err != nil {
		t.Fatal(err)
	}
	req := durable.SourceRequest{Identity: durable.SourceIdentity{Version: 1, Scope: f.o.binding.Scope, Epoch: f.o.binding.Epoch, RunID: f.run.ID, Lane: "source-test", Kind: kind, PayloadDigest: tokenHash(payload), Index: 0, Config: cfg}, Payload: json.RawMessage(payload)}
	if _, _, err = f.j.BeginSource(ctx, f.o.binding, req); err != nil {
		t.Fatalf("begin=%v request=%+v state=%+v", err, req.Identity, f.state(t).Runs[f.run.ID])
	}
	return s, req
}
func TestAutoSignInSourceEventAcceptanceIsNotHandlerCompletion(t *testing.T) {
	f := signinSQLSetup(t)
	f.start(t)
	s, req := signinSourceRequest(t, f, "source.emit", `{"event":"EventType.PluginAction","data":{"action":"site_refresh","site_id":3}}`)
	ctx := context.Background()
	if err := s.Invoke(ctx, f.o.binding, req); err != nil {
		t.Fatal(err)
	}
	result, found, err := s.ReadResult(ctx, f.o.binding, req.Identity)
	if err != nil || !found || result.Outcome != durable.SourceNormal {
		t.Fatalf("result=%+v found=%v err=%v", result, found, err)
	}
	var count int
	if err = f.a.Store.DB.QueryRow(`SELECT COUNT(*) FROM plugin_signin_source_events`).Scan(&count); err != nil || count != 1 {
		t.Fatal(count, err)
	}
	if err = s.Invoke(ctx, f.o.binding, req); !errors.Is(err, durable.ErrUncertain) {
		t.Fatalf("repeat=%v", err)
	}
	// Loss of independent proof never authorizes another queue insertion.
	if _, err = f.a.Store.DB.Exec(`DELETE FROM plugin_signin_source_results`); err != nil {
		t.Fatal(err)
	}
	if _, found, err = s.ReadResult(ctx, f.o.binding, req.Identity); err != nil || found {
		t.Fatal(found, err)
	}
	if err = s.Invoke(ctx, f.o.binding, req); !errors.Is(err, durable.ErrUncertain) {
		t.Fatal(err)
	}
	if err = f.a.Store.DB.QueryRow(`SELECT COUNT(*) FROM plugin_signin_source_events`).Scan(&count); err != nil || count != 1 {
		t.Fatal(count, err)
	}
}
func TestAutoSignInSourceRejectsChangedRequestAndEpoch(t *testing.T) {
	f := signinSQLSetup(t)
	f.start(t)
	s, req := signinSourceRequest(t, f, "source.emit", `{"event":"EventType.PluginAction","data":{"action":"cloudflare_speedtest"}}`)
	ctx := context.Background()
	changed := req
	changed.Payload = json.RawMessage(`{"event":"EventType.PluginAction","data":{"action":"site_refresh","site_id":9}}`)
	if err := s.Invoke(ctx, f.o.binding, changed); !errors.Is(err, durable.ErrConflict) {
		t.Fatal(err)
	}
	changed = req
	changed.Identity.Epoch = "replacement-owner"
	if err := s.Invoke(ctx, f.o.binding, changed); !errors.Is(err, durable.ErrStale) {
		t.Fatal(err)
	}
	if err := s.Invoke(ctx, f.o.binding, req); err != nil {
		t.Fatal(err)
	}
	// Mutable journal alone cannot substitute for the immutable accepted event.
	if _, err := f.a.Store.DB.Exec(`DELETE FROM plugin_signin_source_events`); err != nil {
		t.Fatal(err)
	}
	if _, found, err := s.ReadResult(ctx, f.o.binding, req.Identity); err == nil || found {
		t.Fatal(found, err)
	}
}
func TestAutoSignInSourceNoticeUsesRouterAndIndependentTerminal(t *testing.T) {
	f := signinSQLSetup(t)
	f.start(t)
	s, req := signinSourceRequest(t, f, "source.notify", `{"title":"synthetic signin","mtype":"NotificationType.SiteMessage","text":"no accounts or external recipients"}`)
	ctx := context.Background()
	if err := s.Invoke(ctx, f.o.binding, req); err != nil {
		t.Fatal(err)
	}
	result, found, err := s.ReadResult(ctx, f.o.binding, req.Identity)
	if err != nil || !found || result.Outcome != durable.SourceNormal {
		t.Fatalf("result=%+v found=%v err=%v", result, found, err)
	}
	box, err := f.a.pluginStore.readAutoSignInAcceptance(ctx, f.a.Store.DB, req.Identity)
	if err != nil {
		t.Fatal(err)
	}
	route, steps, err := f.a.pluginStore.readNoticeRoute(ctx, f.a.Store.DB, box.RouteID)
	if err != nil || !route.Complete || len(steps) == 0 {
		t.Fatal(route, steps, err)
	}
	if box.HistoryID == "" || box.RouteInitialSHA == "" {
		t.Fatal("missing trusted notice anchors")
	}
	if _, err = f.a.Store.DB.Exec(`DELETE FROM plugin_signin_source_results`); err != nil {
		t.Fatal(err)
	}
	if _, found, err = s.ReadResult(ctx, f.o.binding, req.Identity); err != nil || found {
		t.Fatal(found, err)
	}
	if err = s.Invoke(ctx, f.o.binding, req); !errors.Is(err, durable.ErrUncertain) {
		t.Fatal(err)
	}
}

func TestAutoSignInSourceControllerActualEventWrapper(t *testing.T) {
	ctx := context.Background()
	f := signinSQLSetup(t, map[string]any{"enabled": true, "notify": true, "onlyonce": false, "sign_sites": []any{1}, "login_sites": []any{}, "queue_cnt": 1, "cron": "2"})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(`<input type="password">`)) }))
	defer server.Close()
	grant, err := autosignin.BindSiteHTTP(autosignin.SiteHTTPBinding{URL: server.URL + "/", Cookie: "sid=synthetic-only"})
	if err != nil {
		t.Fatal(err)
	}
	deps := durable.Dependencies{Source: newAutoSignInSourcePort(f.b), Timezone: "UTC", Now: func() time.Time { return time.Date(2026, 1, 2, 12, 0, 0, 0, time.UTC) }, RandomInt: func(a, b int) (int, error) { return a, nil }, Inventory: func(context.Context) (autosignin.Inventory, error) {
		site := autosignin.Object{"id": 1, "name": "Owned source test"}
		return autosignin.Inventory{Configured: []autosignin.Object{site}, Indexers: []autosignin.Object{site}}, nil
	}, Factory: func(context.Context, autosignin.Object) (durable.BoundSite, error) {
		return durable.BoundSite{Site: autosignin.Object{"id": 1, "name": "Owned source test", "url": server.URL + "/", "cookie": "sid=synthetic-only"}, Hooks: autosignin.SiteVisitHooks{HTTP: grant}, Revision: "source-owned-loopback"}, nil
	}}
	controller := durable.Controller{Journal: f.j, Binding: f.o.binding, DigestKey: [32]byte{1, 2, 3}, Dependencies: deps}
	if _, err = controller.Execute(ctx, f.run.ID, durable.Command{Action: "cold_init"}); err != nil {
		t.Fatal(err)
	}
	if err = f.a.pluginStore.FinishRun(ctx, f.run.ID, "succeeded", "", json.RawMessage(`{}`)); err != nil {
		t.Fatal(err)
	}
	run, err := f.a.pluginStore.EnqueueRun(ctx, "AutoSignIn", "service", json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.a.Store.DB.Exec(`UPDATE plugin_runs SET state='running' WHERE id=?`, run.ID); err != nil {
		t.Fatal(err)
	}
	run, err = f.a.pluginStore.Run(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	backend, err := f.o.backend(&run)
	if err != nil {
		t.Fatal(err)
	}
	journal, err := durable.NewStateJournal(backend)
	if err != nil {
		t.Fatal(err)
	}
	controller.Journal = journal
	controller.Dependencies.Source = signinTraceSourcePort{SourcePort: newAutoSignInSourcePort(backend), t: t}
	if _, err = controller.Execute(ctx, run.ID, durable.Command{Action: "service"}); err != nil {
		t.Fatal(err)
	}
	var count int
	if err = f.a.Store.DB.QueryRow(`SELECT COUNT(*) FROM plugin_signin_source_events`).Scan(&count); err != nil || count != 1 {
		t.Fatal("actual source event", count, err)
	}
	state, err := journal.State(ctx, f.o.binding)
	if err != nil {
		t.Fatal(err)
	}
	events, notices := 0, 0
	for _, entries := range state.Runs[run.ID].Lanes {
		for _, e := range entries {
			if e.Kind == "source.emit" {
				events++
			}
			if e.Kind == "source.notify" {
				notices++
			}
		}
	}
	if events != 1 || notices != 1 {
		t.Fatal("source controller boundaries", events, notices)
	}
}

func TestAutoSignInSourceConstructorValidationExceptionHasZeroRouterEffects(t *testing.T) {
	f := signinSQLSetup(t)
	f.start(t)
	s, req := signinSourceRequest(t, f, "source.notify", `{"title":"synthetic","channel":"not-a-real-channel"}`)
	ctx := context.Background()
	if err := s.Invoke(ctx, f.o.binding, req); err != nil {
		t.Fatal(err)
	}
	result, found, err := s.ReadResult(ctx, f.o.binding, req.Identity)
	if err != nil || !found || result.Outcome != durable.SourceException || result.ErrorClass != "ValidationError" {
		t.Fatal(result, found, err)
	}
	for _, query := range []string{`SELECT COUNT(*) FROM plugin_notice_routes`, `SELECT COUNT(*) FROM plugin_message_queue`, `SELECT COUNT(*) FROM records WHERE kind='notification'`} {
		var count int
		if err = f.a.Store.DB.QueryRow(query).Scan(&count); err != nil || count != 0 {
			t.Fatal(query, count, err)
		}
	}
	if _, err = f.a.Store.DB.Exec(`DELETE FROM plugin_signin_source_results`); err != nil {
		t.Fatal(err)
	}
	if _, found, err = s.ReadResult(ctx, f.o.binding, req.Identity); err != nil || found {
		t.Fatal(found, err)
	}
	if err = s.Invoke(ctx, f.o.binding, req); !errors.Is(err, durable.ErrUncertain) {
		t.Fatal(err)
	}
}

type signinTraceSourcePort struct {
	durable.SourcePort
	t *testing.T
}

func (p signinTraceSourcePort) Invoke(ctx context.Context, b durable.Binding, r durable.SourceRequest) error {
	err := p.SourcePort.Invoke(ctx, b, r)
	p.t.Logf("source invoke kind=%s err=%v", r.Identity.Kind, err)
	return err
}
func (p signinTraceSourcePort) ReadResult(ctx context.Context, b durable.Binding, id durable.SourceIdentity) (durable.SourceResult, bool, error) {
	r, f, e := p.SourcePort.ReadResult(ctx, b, id)
	p.t.Logf("source read kind=%s found=%v err=%v", id.Kind, f, e)
	return r, f, e
}

func TestAutoSignInSourceUnknownIntentCannotBeReinvoked(t *testing.T) {
	f := signinSQLSetup(t)
	f.start(t)
	s, req := signinSourceRequest(t, f, "source.emit", `{"event":"EventType.PluginAction","data":{"action":"cloudflare_speedtest"}}`)
	ctx := context.Background()
	if err := f.j.UnknownSource(ctx, f.o.binding, req); err != nil {
		t.Fatal(err)
	}
	if err := s.Invoke(ctx, f.o.binding, req); !errors.Is(err, durable.ErrUncertain) {
		t.Fatal(err)
	}
	var count int
	if err := f.a.Store.DB.QueryRow(`SELECT COUNT(*) FROM plugin_signin_source_events`).Scan(&count); err != nil || count != 0 {
		t.Fatal(count, err)
	}
}
