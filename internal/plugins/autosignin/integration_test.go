package autosignin

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestServiceEventToRealLoopbackVisitAndHistory(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Header.Get("Cookie") != "sid=synthetic-only" {
			t.Error("bound host credentials absent")
		}
		w.Header().Set("Content-Type", "text/html; charset=UTF-8")
		switch {
		case strings.HasPrefix(r.URL.Path, "/signed/"):
			io.WriteString(w, `<a href="logout.php">退出</a><div>签到成功</div>`)
		case strings.HasPrefix(r.URL.Path, "/pending/"):
			io.WriteString(w, `<a href="logout.php">退出</a><button>签到</button>`)
		case strings.HasPrefix(r.URL.Path, "/expired/"):
			io.WriteString(w, `<input type="password">`)
		}
	}))
	defer server.Close()
	sites := []Object{{"id": json.Number("1"), "name": "Signed", "url": server.URL + "/signed/"}, {"id": json.Number("2"), "name": "Pending", "url": server.URL + "/pending/"}, {"id": json.Number("3"), "name": "Expired", "url": server.URL + "/expired/"}}
	configured := []Object{{"id": json.Number("1"), "name": "Signed"}, {"id": json.Number("2"), "name": "Pending"}, {"id": json.Number("3"), "name": "Expired"}}
	e := New(Inventory{Configured: configured, Indexers: sites}, func() time.Time { return time.Date(2026, 1, 2, 12, 0, 0, 0, time.UTC) })
	if err := e.Init(Object{"enabled": true, "sign_sites": []any{1, 2, 3}, "login_sites": []any{1}, "queue_cnt": 2, "retry_keyword": "Cookie已失效"}); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	stats := []Object{}
	events, messages := []Object{}, []Object{}
	hooks := RunHooks{Visit: func(ctx context.Context, metadata Object, op string, loaded bool) (Result, error) {
		trusted := clone(metadata).(Object)
		trusted["cookie"] = "sid=synthetic-only"
		binding, err := BindSiteHTTP(SiteHTTPBinding{URL: textValue(trusted["url"]), Cookie: "sid=synthetic-only", UserAgent: "synthetic-UA"})
		if err != nil {
			return Result{}, err
		}
		v := SiteVisitor{Hooks: SiteVisitHooks{HTTP: binding, Stats: func(_ context.Context, domain string, ok bool, seconds int) error {
			mu.Lock()
			stats = append(stats, Object{"domain": domain, "success": ok, "seconds": seconds})
			mu.Unlock()
			return nil
		}}}
		return v.Visit(ctx, trusted, op, loaded)
	}, Emit: func(_ context.Context, kind string, data Object) error {
		events = append(events, mergeObject(Object{"kind": kind}, data))
		return nil
	}, Notify: func(_ context.Context, data Object) error { messages = append(messages, data); return nil }}
	keys := func() []string {
		out := []string{}
		for k := range e.Data {
			out = append(out, k)
		}
		sort.Strings(out)
		return out
	}
	event := &EventEnvelope{Data: Object{"action": "site_signin", "channel": "synthetic", "user": "synthetic"}}
	if err := e.SignIn(context.Background(), event, EntryHooks{Run: hooks, HistoryKeys: keys}); err != nil {
		t.Fatal(err)
	}
	if requests.Load() != 5 {
		t.Fatalf("source visit/fallback count got %d want5", requests.Load())
	}
	daily := asObject(e.Data["签到-2026-01-02"])
	results := asObject(daily["results"])
	for _, c := range []struct {
		id             string
		success, login bool
	}{{"1", true, true}, {"2", false, true}, {"3", false, false}} {
		r := asObject(results[c.id])
		if r["success"] != c.success || r["logged_in"] != c.login {
			t.Fatalf("result %s=%v", c.id, r)
		}
	}
	if got := asList(daily["retry"]); len(got) != 1 || !equal(got[0], 3) {
		t.Fatal("retry not based on completed/login evidence", got)
	}
	if len(events) != 1 || events[0]["action"] != "site_refresh" || !equal(events[0]["site_id"], 3) {
		t.Fatal("Cookie refresh event absent", events)
	}
	if len(messages) != 3 || messages[0]["title"] != "开始站点签到 ..." || messages[1]["title"] != "站点签到完成！" || messages[2]["title"] != "站点登录完成！" {
		t.Fatal("source event messages", messages)
	}
	mu.Lock()
	if len(stats) != 4 {
		t.Error("fallback must not count twice", stats)
	}
	mu.Unlock()
	raw, _ := json.Marshal(Object{"history": e.Data, "config": e.Config, "messages": messages})
	if strings.Contains(string(raw), "synthetic-only") {
		t.Fatal("credential entered plugin state/history/messages")
	}
	if err := e.SignIn(context.Background(), nil, EntryHooks{Run: hooks, HistoryKeys: keys}); err != nil {
		t.Fatal(err)
	}
	if requests.Load() != 7 {
		t.Fatal("same-day retry must only revisit incomplete site", requests.Load())
	}
}
func TestVisitorSchemaLifecycleAndCancellation(t *testing.T) {
	calls := 0
	pages := &syntheticPages{site: syntheticSite, responses: []any{Object{"text": `<a href="logout.php">Logout</a>`}}, calls: []any{}}
	v := SiteVisitor{Adapters: []SiteAdapter{{Match: func(string) (bool, error) { calls++; return true, nil }, SigninIsLogin: true, Signin: func(context.Context, Object, SiteVisitHooks) (AdapterResult, error) {
		return AdapterResult{false, "签到失败，controlled adapter"}, nil
	}}}, Hooks: SiteVisitHooks{HTTP: pages, Stats: func(context.Context, string, bool, int) error { return nil }}}
	r, err := v.Visit(context.Background(), syntheticSite, "签到", false)
	if err != nil || calls != 0 || !r.LoggedIn || r.Success {
		t.Fatal("cold unloaded source modules were invented", r, err, calls)
	}
	r, err = v.Visit(context.Background(), syntheticSite, "签到", true)
	if err != nil || calls != 1 || r.Completed() || len(pages.calls) != 1 {
		t.Fatal("source loaded adapter/signin_is_login not retained", r, err, calls, pages.calls)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r, err = v.Visit(ctx, syntheticSite, "签到", true)
	if !errors.Is(err, context.Canceled) || calls != 1 {
		t.Fatal("canceled callback executed adapter", r, err, calls)
	}
}
