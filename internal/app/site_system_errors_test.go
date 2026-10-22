package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"light-media/internal/plugins"
	"light-media/internal/sites"
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
func enqueueSystemFailureHTTP(t *testing.T, a *App) PluginRun {
	t.Helper()
	ctx := context.Background()
	installation, err := a.pluginStore.Installation(ctx, "SiteStatistic")
	if err != nil {
		t.Fatal(err)
	}
	input := plugins.HTTPRequest{Key: "refresh_by_domain", Method: "GET", Path: "/refresh_by_domain", Params: map[string]string{}, Query: map[string][]string{"domain": {siteStatisticDomain(a.Settings().Sites[0])}}, CredentialMatches: map[string]bool{"apikey": true}, Headers: map[string]string{}}
	a.pluginMu.Lock()
	_, err = a.enqueuePluginHTTP(ctx, installation, input, pluginAPIScope{Key: input.Key, Method: input.Method, Authority: pluginAPIAuthority{Kind: "key", Proof: a.pluginAPIKeyProof(a.Settings().APIToken)}})
	a.pluginMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	run, err := a.pluginStore.ClaimRun(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return run
}

// Exercise the actual authenticated HTTP router as well as the real SDK host.
// The owned test drives its queued worker explicitly, avoiding unrelated timers.
func executeSystemFailureAPI(t *testing.T, a *App, cookie *http.Cookie, instance string) PluginRun {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	server := httptest.NewServer(a.Handler())
	defer func() { cancel(); server.Close() }()
	target := server.URL + "/light-media/api/plugins/" + instance + "/api/refresh_by_domain?domain=" + url.QueryEscape(siteStatisticDomain(a.Settings().Sites[0])) + "&apikey=" + url.QueryEscape(a.Settings().APIToken) + "&instance_id=request-cannot-own-origin"
	request, err := http.NewRequestWithContext(ctx, "GET", target, nil)
	if err != nil {
		t.Fatal(err)
	}
	request.AddCookie(cookie)
	type result struct {
		response *http.Response
		err      error
	}
	finished := make(chan result, 1)
	go func() { response, err := server.Client().Do(request); finished <- result{response, err} }()
	var run PluginRun
	for {
		run, err = a.pluginStore.claimRunForInstance(ctx, instance)
		if err == nil {
			break
		}
		if !errors.Is(err, sql.ErrNoRows) {
			t.Fatal(err)
		}
		select {
		case result := <-finished:
			if result.response != nil {
				result.response.Body.Close()
			}
			t.Fatal("HTTP API did not enqueue owned worker", result.err)
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(10 * time.Millisecond):
		}
	}
	a.executePluginRun(ctx, run)
	select {
	case result := <-finished:
		if result.err != nil {
			t.Fatal(result.err)
		}
		defer result.response.Body.Close()
		body, err := io.ReadAll(result.response.Body)
		var response map[string]any
		if err != nil || result.response.StatusCode != http.StatusOK || json.Unmarshal(body, &response) != nil || response["success"] != false {
			t.Fatal("native HTTP source no-data contract", result.response.StatusCode, string(body), err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	return run
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
func TestSiteSystemErrorsFourNativeParsersThroughAPIAndSSE(t *testing.T) {
	cases := 0
	for _, f := range siteSystemFixtures(t) {
		if f.ModuleErrors != 2 || f.Kind == "throws" || f.Kind == "unsupported" {
			continue
		}
		cases++
		t.Run(f.Name, func(t *testing.T) {
			a, cookie, _, _ := pluginHTTPFixture(t)
			calls := installSystemFailureSite(t, a, f.Kind, f.Body, nil)
			run := executeSystemFailureAPI(t, a, cookie, "SiteStatistic")
			result, err := a.pluginStore.Run(context.Background(), run.ID)
			if err != nil || result.State != "succeeded" || calls.Load() == 0 {
				t.Fatal("real API invocation did not return no-data", result.State, err)
			}
			requireSystemErrorCount(t, a, 1)
			r := systemErrorReceipts(t, a)[0]
			if r.FailureKind != f.Name || r.Origin.ID != run.ID || r.Origin.Instance != run.InstanceID || r.Data.Type != "module" || r.Data.ModuleID != "IndexerModule" || r.Data.ModuleMethod != "refresh_userdata" || r.ModuleNameSource != siteSystemModuleNameSource {
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
func TestSiteSystemErrorsConnectionDTOAndUntrustedFailuresDoNotEmit(t *testing.T) {
	for _, f := range []siteSystemFixture{{Name: "connection-test", Kind: "mteam", Body: "{"}, {Name: "DTO-login", Kind: "jiayuan", Body: `<input type="password">`}, {Name: "DTO-business", Kind: "mteam", Body: `{"code":"1","message":"owned business denial"}`}, {Name: "unsupported-shape", Kind: "mteam", Body: `{"code":"0","data":[]}`}, {Name: "safety-limit", Kind: "mteam", Body: `{"code":"0","data":{"memberCount":{"uploaded":"9223372036854775808"}}}`}} {
		t.Run(f.Name, func(t *testing.T) {
			a, _, _, _ := pluginHTTPFixture(t)
			installSystemFailureSite(t, a, f.Kind, f.Body, nil)
			if f.Name == "connection-test" {
				_, _ = a.refreshManagedSite(context.Background(), "known")
			} else {
				run := enqueueSystemFailureHTTP(t, a)
				a.executePluginRun(context.Background(), run)
			}
			requireSystemErrorCount(t, a, 0)
			if len(a.systemMessages.get("")) != 0 {
				t.Fatal("non-module failure became system message")
			}
		})
	}
}
func TestSiteSystemErrorRollbackDoesNotLeakHelperOrDedup(t *testing.T) {
	a, _, _, _ := pluginHTTPFixture(t)
	calls := installSystemFailureSite(t, a, "mteam", "{", nil)
	if _, err := a.Store.DB.Exec(`CREATE TRIGGER owned_fail_system_event BEFORE INSERT ON domain_events WHEN json_extract(NEW.payload,'$.event_type')='system.error' BEGIN SELECT RAISE(ABORT,'owned event rollback'); END`); err != nil {
		t.Fatal(err)
	}
	run := enqueueSystemFailureHTTP(t, a)
	a.executePluginRun(context.Background(), run)
	requireSystemErrorCount(t, a, 0)
	if len(a.systemMessages.get("")) != 0 || len(a.systemMessages.recent) != 0 {
		t.Fatal("rolled-back helper or duplicate key escaped")
	}
	requests := calls.Load()
	if _, err := a.Store.DB.Exec(`DROP TRIGGER owned_fail_system_event`); err != nil {
		t.Fatal(err)
	}
	// Existing admitted operation remains unknown and must not be recollected.
	a.executePluginRun(context.Background(), run)
	if calls.Load() != requests {
		t.Fatal("unknown collection repeated network read")
	}
	next := enqueueSystemFailureHTTP(t, a)
	a.executePluginRun(context.Background(), next)
	requireSystemErrorCount(t, a, 1)
	if len(a.systemMessages.get("")) == 0 {
		t.Fatal("rollback left duplicate suppression behind")
	}
}
func TestSiteSystemErrorRevocationAndCancellationBeforeCommit(t *testing.T) {
	for _, mode := range []string{"key", "site", "installation", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			a, _, _, _ := pluginHTTPFixture(t)
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
			run := enqueueSystemFailureHTTP(t, a)
			done := make(chan struct{})
			go func() { a.executePluginRun(ctx, run); close(done) }()
			select {
			case <-entered:
			case <-time.After(5 * time.Second):
				t.Fatal("parser did not request owned fixture")
			}
			switch mode {
			case "cancel":
				cancel()
			case "installation":
				if _, err := a.Store.DB.Exec(`UPDATE plugin_installations SET desired_enabled=0 WHERE instance_id=?`, run.InstanceID); err != nil {
					t.Fatal(err)
				}
			default:
				c := a.Settings()
				if mode == "key" {
					c.APIToken = "owned-revoked-api-key"
				} else {
					c.Sites[0].Enabled = false
				}
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

func TestSiteSystemErrorCloneOriginComesFromVerifiedRun(t *testing.T) {
	a, cookie, _, _ := pluginHTTPFixture(t)
	installSystemFailureSite(t, a, "mteam", "{", nil)
	const instance = "SiteStatistic_owned_clone"
	statisticRouterInstall(t, a, instance)
	run := executeSystemFailureAPI(t, a, cookie, instance)
	requireSystemErrorCount(t, a, 1)
	receipt := systemErrorReceipts(t, a)[0]
	if receipt.Origin.Instance != instance || receipt.Origin.ID != run.ID {
		t.Fatal("clone/request origin mismatch", receipt.Origin, run.ID)
	}
	// Clone identity is evidence, not a broadcast subscription or a separate FIFO.
	if len(a.systemMessages.get("unrelated-instance")) == 0 || len(a.systemMessages.get(instance)) != 0 {
		t.Fatal("clone helper was not shared destructive FIFO")
	}
}
