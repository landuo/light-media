package app

import (
	"context"
	"encoding/json"
	"fmt"
	"light-media/internal/sites"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type siteSystemFixture struct {
	Name, Kind, Body string
	ModuleErrors     int `json:"module_errors"`
}

func siteSystemFixtures(t *testing.T) []siteSystemFixture {
	t.Helper()
	raw, err := os.ReadFile("../../testdata/plugins/site-account-outcomes-original-oracle.json")
	if err != nil {
		t.Fatal(err)
	}
	var f struct{ Cases []siteSystemFixture }
	if json.Unmarshal(raw, &f) != nil {
		t.Fatal("invalid oracle")
	}
	return f.Cases
}

func installSystemFailureSite(t *testing.T, a *App, kind, body string, gate func()) *atomic.Int32 {
	t.Helper()
	calls := &atomic.Int32{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if gate != nil {
			gate()
		}
		fmt.Fprint(w, body)
	}))
	t.Cleanup(server.Close)
	baseURL := server.URL
	if kind == "mteam" {
		// Preserve production's fixed M-Team origin validation. All resulting I/O is
		// explicitly redirected to this owned HTTP server, never the real service.
		baseURL = "https://api.m-team.cc"
		local, err := url.Parse(server.URL)
		if err != nil {
			t.Fatal(err)
		}
		previous := http.DefaultTransport
		transport := &http.Transport{}
		http.DefaultTransport = siteOutcomeRoundTrip(func(r *http.Request) (*http.Response, error) {
			if r.URL.Scheme != "https" || r.URL.Host != "api.m-team.cc" {
				return nil, fmt.Errorf("owned transport denied unexpected origin")
			}
			owned := r.Clone(r.Context())
			target := *r.URL
			target.Scheme, target.Host = local.Scheme, local.Host
			owned.URL, owned.Host, owned.RequestURI = &target, local.Host, ""
			return transport.RoundTrip(owned)
		})
		t.Cleanup(func() { transport.CloseIdleConnections(); http.DefaultTransport = previous })
	}
	c := a.Settings()
	c.Sites = []SiteConfig{{Config: sites.Config{ID: "known", Kind: kind, Name: "合成站点", BaseURL: baseURL, Cookie: "owned-synthetic-cookie", APIKey: "owned-synthetic-key"}, SiteOptions: SiteOptions{Domain: "known.example"}, Enabled: true}}
	if err := a.saveSettings(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	return calls
}

func systemErrorReceipts(t *testing.T, a *App) []siteSystemErrorReceipt {
	t.Helper()
	rows, err := a.Store.DB.Query(`SELECT id FROM records WHERE kind='site_system_error' ORDER BY rowid`)
	if err != nil {
		t.Fatal(err)
	}
	var keys []string
	for rows.Next() {
		var key string
		if err = rows.Scan(&key); err != nil {
			t.Fatal(err)
		}
		keys = append(keys, key)
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	rows.Close()
	var out []siteSystemErrorReceipt
	for _, key := range keys {
		r, e := a.pluginStore.systemErrorReceipt(context.Background(), a.Store.DB, key)
		if e != nil {
			t.Fatal(e)
		}
		out = append(out, r)
	}
	return out
}

func requireSystemErrorCount(t *testing.T, a *App, want int) {
	t.Helper()
	var count int
	if err := a.Store.DB.QueryRow(`SELECT count(*) FROM domain_events WHERE json_extract(payload,'$.event_type')='system.error'`).Scan(&count); err != nil || count != want {
		t.Fatal("typed event count", count, want, err)
	}
	if got := len(systemErrorReceipts(t, a)); got != want {
		t.Fatal("sealed source count", got, want)
	}
}

func TestSiteSystemErrorsFourNativeParsersThroughRefreshJob(t *testing.T) {
	for _, f := range siteSystemFixtures(t) {
		if f.ModuleErrors != 2 || f.Kind == "throws" || f.Kind == "unsupported" {
			continue
		}
		t.Run(f.Name, func(t *testing.T) {
			a := newTestApp(t)
			calls := installSystemFailureSite(t, a, f.Kind, f.Body, nil)
			if _, err := a.enqueue(context.Background(), "site_refresh", map[string]string{"id": "site_refresh"}, ""); err != nil {
				t.Fatal(err)
			}
			job, err := a.claim(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if err = a.safeRun(context.Background(), job); pluginErrorCode(err) != "SITE_STATISTICS_REFRESH_FAILED" {
				t.Fatal("missing native no-data outcome", err)
			}
			requireSystemErrorCount(t, a, 1)
			r := systemErrorReceipts(t, a)[0]
			if r.Origin.Kind != "job" || r.Origin.ID != job.ID || r.Origin.Instance != "" || calls.Load() == 0 {
				t.Fatal("job origin invented plugin owner", r.Origin)
			}
			if len(a.systemMessages.get("")) == 0 {
				t.Fatal("job did not notify shared helper")
			}
		})
	}
}

func TestSystemErrorDomainEventHasExclusiveTypedReference(t *testing.T) {
	key := "sye_" + strings.Repeat("a", 64)
	base := DomainEvent{Type: "system.error", SystemErrorID: key, EventID: domainEventID("system.error", key)}
	if _, err := validateDomainEvent(base); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*DomainEvent){func(e *DomainEvent) { e.SiteID = "known" }, func(e *DomainEvent) { e.JobID = "job_owned" }, func(e *DomainEvent) { e.NoticeID = "notice_other" }, func(e *DomainEvent) { e.Type = "site.refreshed"; e.SiteID = "known" }, func(e *DomainEvent) { e.Origin = "plugin" }, func(e *DomainEvent) { e.MediaID = "movie:1" }, func(e *DomainEvent) { e.SystemErrorID = "request-injected" }} {
		e := base
		change(&e)
		if _, err := validateDomainEvent(e); err == nil {
			t.Fatal("mixed/untrusted event accepted", e)
		}
	}
	a := newTestApp(t)
	tx, err := a.Store.DB.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err = recordDomainEventTx(context.Background(), tx, base); err == nil {
		t.Fatal("event accepted without typed sealed backing")
	}
}

func TestSiteSystemErrorDistinctNativeKindsKeepOriginalHelperDedup(t *testing.T) {
	var messages systemMessageRuntime
	at := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	for _, kind := range []string{"hh-missing-panel", "mt-invalid-json", "mt-null-data", "mt-invalid-integer"} {
		text := siteNativeModuleErrorText(kind)
		if text == "" || !messages.put("system", systemMessageTitle(siteSystemModuleName+"发生了错误"), text, nil, at) {
			t.Fatal("distinct trusted classes merged", kind)
		}
		if messages.put("system", systemMessageTitle(siteSystemModuleName+"发生了错误"), text, nil, at) {
			t.Fatal("same source error bypassed original dedup", kind)
		}
	}
	if len(messages.queue) != 4 {
		t.Fatal("changed helper queue semantics", len(messages.queue))
	}
	for range 4 {
		var value systemMessage
		if json.Unmarshal(messages.get(""), &value) != nil || string(value.Note) != "null" {
			t.Fatal("note was used to evade dedup", value)
		}
	}
}

func TestSiteSystemErrorsFourCoreParsersThroughJobAndSSE(t *testing.T) {
	cases := 0
	for _, f := range siteSystemFixtures(t) {
		if f.ModuleErrors != 2 || f.Kind == "throws" || f.Kind == "unsupported" {
			continue
		}
		cases++
		t.Run(f.Name, func(t *testing.T) {
			a := newTestApp(t)
			cookie := setupTestAdmin(t, a)
			calls := installSystemFailureSite(t, a, f.Kind, f.Body, nil)
			job := siteMessageClaimBatch(t, a)
			err := a.runSiteRefresh(context.Background(), job)
			if pluginErrorCodeInternal(err) != "SITE_STATISTICS_REFRESH_FAILED" || calls.Load() == 0 {
				t.Fatal("real core invocation did not return no-data", err)
			}
			requireSystemErrorCount(t, a, 1)
			r := systemErrorReceipts(t, a)[0]
			if r.FailureKind != f.Name || r.Origin.Kind != "job" || r.Origin.ID != job.ID || r.Origin.Instance != "" || r.Data.Type != "module" || r.Data.ModuleID != "IndexerModule" || r.Data.ModuleMethod != "refresh_userdata" || r.ModuleNameSource != siteSystemModuleNameSource {
				t.Fatal("lost typed origin", r)
			}
			raw, _ := json.Marshal(r.Data)
			var fields map[string]any
			json.Unmarshal(raw, &fields)
			if len(fields) != 6 || !strings.HasPrefix(r.Data.Traceback, siteNativeDiagnosticHeader) || strings.Contains(string(raw), "owned-synthetic") || strings.Contains(string(raw), "/workspace/") || strings.Contains(string(raw), f.Body) && len(f.Body) > 5 {
				t.Fatal("unsafe or invented diagnostic", string(raw))
			}
			resp, reader := openSystemStream(t, a, cookie, "/api/system/message")
			var message systemMessage
			if json.Unmarshal([]byte(readSystemFrame(t, reader)), &message) != nil || message.Type != "system" || message.Title == nil || *message.Title != "站点索引发生了错误" || message.Text != siteNativeModuleErrorText(f.Name) || string(message.Note) != "null" {
				t.Fatal("actual SSE lost helper payload", message)
			}
			resp.Body.Close()
			if len(a.systemMessages.get("")) != 0 {
				t.Fatal("native failure emitted duplicate helper")
			}
			// Cold recovery retains the typed source fact, not the ephemeral UI message.
			requests := calls.Load()
			dir := a.Dir
			if err = a.Close(); err != nil {
				t.Fatal(err)
			}
			cold, err := New(Options{DataDir: dir})
			if err != nil {
				t.Fatal(err)
			}
			defer cold.Close()
			requireSystemErrorCount(t, cold, 1)
			if calls.Load() != requests || len(cold.systemMessages.get("")) != 0 {
				t.Fatal("cold receipt replayed parser/SSE")
			}
		})
	}
	if cases != 4 {
		t.Fatal("oracle four branches changed", cases)
	}
}

func TestSiteSystemErrorsCoreConnectionDTOAndUntrustedFailuresDoNotEmit(t *testing.T) {
	for _, f := range []siteSystemFixture{{Name: "connection-test", Kind: "mteam", Body: "{"}, {Name: "DTO-login", Kind: "jiayuan", Body: `<input type="password">`}, {Name: "DTO-business", Kind: "mteam", Body: `{"code":"1","message":"owned business denial"}`}, {Name: "unsupported-shape", Kind: "mteam", Body: `{"code":"0","data":[]}`}, {Name: "safety-limit", Kind: "mteam", Body: `{"code":"0","data":{"memberCount":{"uploaded":"9223372036854775808"}}}`}} {
		t.Run(f.Name, func(t *testing.T) {
			a := newTestApp(t)
			installSystemFailureSite(t, a, f.Kind, f.Body, nil)
			if f.Name == "connection-test" {
				_, _ = a.refreshManagedSite(context.Background(), "known")
			} else {
				job := siteMessageClaimBatch(t, a)
				_ = a.runSiteRefresh(context.Background(), job)
			}
			requireSystemErrorCount(t, a, 0)
			if len(a.systemMessages.get("")) != 0 {
				t.Fatal("non-module failure became system message")
			}
		})
	}
}

func TestSiteSystemErrorCoreRollbackDoesNotLeakHelperOrDedup(t *testing.T) {
	a := newTestApp(t)
	calls := installSystemFailureSite(t, a, "mteam", "{", nil)
	if _, err := a.Store.DB.Exec(`CREATE TRIGGER owned_fail_system_event BEFORE INSERT ON domain_events WHEN json_extract(NEW.payload,'$.event_type')='system.error' BEGIN SELECT RAISE(ABORT,'owned event rollback'); END`); err != nil {
		t.Fatal(err)
	}
	job := siteMessageClaimBatch(t, a)
	_ = a.runSiteRefresh(context.Background(), job)
	requireSystemErrorCount(t, a, 0)
	if len(a.systemMessages.get("")) != 0 || len(a.systemMessages.recent) != 0 {
		t.Fatal("rolled-back helper or duplicate key escaped")
	}
	requests := calls.Load()
	if _, err := a.Store.DB.Exec(`DROP TRIGGER owned_fail_system_event`); err != nil {
		t.Fatal(err)
	}
	// Existing admitted operation remains unknown and must not be recollected.
	_ = a.runSiteRefresh(context.Background(), job)
	if calls.Load() != requests {
		t.Fatal("unknown collection repeated network read")
	}
	queued, err := a.enqueue(context.Background(), "site_refresh", map[string]string{"id": "next-system-error-after-rollback"}, "")
	if err != nil {
		t.Fatal(err)
	}
	next, err := a.claim(context.Background())
	if err != nil || next.ID != queued.ID {
		t.Fatal("independent core job not claimed", next.ID, err)
	}
	_ = a.runSiteRefresh(context.Background(), next)
	requireSystemErrorCount(t, a, 1)
	if len(a.systemMessages.get("")) == 0 {
		t.Fatal("rollback left duplicate suppression behind")
	}
}

func TestSiteSystemErrorCoreRevocationAndCancellationBeforeCommit(t *testing.T) {
	for _, mode := range []string{"site", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			a := newTestApp(t)
			entered, release := make(chan struct{}), make(chan struct{})
			var released atomic.Bool
			unblock := func() {
				if released.CompareAndSwap(false, true) {
					close(release)
				}
			}
			defer unblock()
			var once atomic.Bool
			installSystemFailureSite(t, a, "mteam", "{", func() {
				if once.CompareAndSwap(false, true) {
					close(entered)
					<-release
				}
			})
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			job := siteMessageClaimBatch(t, a)
			done := make(chan struct{})
			go func() { _ = a.runSiteRefresh(ctx, job); close(done) }()
			select {
			case <-entered:
			case <-time.After(5 * time.Second):
				t.Fatal("parser did not request owned fixture")
			}
			switch mode {
			case "cancel":
				cancel()
			default:
				c := a.Settings()
				c.Sites[0].Enabled = false
				if err := a.saveSettings(context.Background(), c); err != nil {
					t.Fatal(err)
				}
			}
			unblock()
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("revoked invocation did not finish")
			}
			requireSystemErrorCount(t, a, 0)
			if len(a.systemMessages.get("")) != 0 {
				t.Fatal("revoked native failure escaped")
			}
		})
	}
}
