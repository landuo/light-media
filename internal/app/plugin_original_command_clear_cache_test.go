package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"light-media/internal/catalog"
)

const ownedClearID = "OwnedClearCache"

type ownedClearSnapshot struct {
	PID                     int            `json:"pid"`
	Calls                   map[string]int `json:"calls"`
	Events, Notices, Inputs []json.RawMessage
	Clears                  int `json:"clears"`
}

func ownedClearRead(t *testing.T, p *originalPluginInstance) ownedClearSnapshot {
	t.Helper()
	var s ownedClearSnapshot
	if e := p.readSession(context.Background(), "snapshot", &s); e != nil {
		t.Fatal(e)
	}
	return s
}
func ownedClearFixture(t *testing.T) (*App, *http.Cookie, *originalPluginInstance) {
	t.Helper()
	index, payload := originalMarketOwnedIndex(t, "OWNED_CLEAR_CACHE_BINARY")
	if index.PluginID != ownedClearID {
		t.Fatal("wrong owned worker")
	}
	repo := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/index":
			_ = json.NewEncoder(w).Encode(index)
		case "/worker":
			_, _ = w.Write(payload)
		default:
			t.Errorf("unexpected repository path %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(repo.Close)
	index.Releases[0].Artifacts[0].URL = repo.URL + "/worker"
	a := newTestApp(t)
	cookie := setupTestAdmin(t, a)
	raw, _ := json.Marshal(map[string]any{"plugin_id": ownedClearID, "repo_url": repo.URL + "/index"})
	r := callTest(t, a.Handler(), "POST", "/api/plugins/install", string(raw), cookie, "")
	assertOriginalRegistrySuccess(t, r.Code, r.Body.Bytes())
	a.wg.Wait()
	v, ok := a.pluginRunningInstances.Load(ownedClearID)
	if !ok {
		t.Fatal("actual worker not loaded")
	}
	return a, cookie, v.(*originalPluginInstance)
}
func ownedClearConfigure(t *testing.T, a *App, c *http.Cookie, fields map[string]any) {
	t.Helper()
	raw, e := json.Marshal(fields)
	if e != nil {
		t.Fatal(e)
	}
	ownedDashboardMetadataSave(t, a, c, ownedClearID, string(raw), 200)
	a.wg.Wait()
}
func ownedClearCommand(t *testing.T, a *App, c *http.Cookie) {
	t.Helper()
	r := callTest(t, a.Handler(), "POST", "/api/v1/message/web", `{"text":"/clear_cache ignored arguments","channel":"Telegram","source":"forged","user":"forged"}`, c, "")
	if r.Code != 200 || !strings.Contains(r.Body.String(), `"accepted":true`) {
		t.Fatal(r.Code, r.Body.String())
	}
	a.wg.Wait()
}

var ownedClearRemove = []string{"browse:movie:popular::1", "search:movie:owned:1", "directory-identify:movie:owned", "directory-identify:movie:owned:page:2", "media:movie:120", "season:120:1", "source-detail:movie:120", "source-search:movie:owned:2026", "source-person:120", "source-credits:120:1", "explore:v1:themoviedb:movie:1:", "explore-detail:v1:themoviedb:movie:120", "explore:v1:douban:movie:1:", "explore-detail:v1:douban:movie:120", "explore:v1:bangumi:tv:1:", "explore-detail:v1:bangumi:tv:120", "explore:v1:anilist:anime:1:", "explore-detail:v1:anilist:tv:120"}
var ownedClearKeep = []string{"recommend:v1:tmdb_movies:1:2026-10-30", "recommend:v1:douban_movies:1:2026-10-30", "foreign:owned", "Media:movie:120", "media_other:120"}

func ownedClearSeed(t *testing.T, a *App) {
	t.Helper()
	for _, k := range append(append([]string{}, ownedClearRemove...), ownedClearKeep...) {
		if e := a.Store.Put(context.Background(), "cache", k, map[string]any{"owned": true}); e != nil {
			t.Fatal(e)
		}
	}
	if e := a.Store.Put(context.Background(), "owned_business", "media:movie:120", map[string]any{"keep": true}); e != nil {
		t.Fatal(e)
	}
}
func ownedClearCount(t *testing.T, a *App, key string) int {
	t.Helper()
	var n int
	if e := a.Store.DB.QueryRow(`SELECT count(*) FROM records WHERE kind='cache' AND id=?`, key).Scan(&n); e != nil {
		t.Fatal(e)
	}
	return n
}
func ownedClearCheck(t *testing.T, a *App, cleared bool) {
	t.Helper()
	for _, k := range ownedClearRemove {
		want := 1
		if cleared {
			want = 0
		}
		if n := ownedClearCount(t, a, k); n != want {
			t.Fatalf("cache %s = %d want %d", k, n, want)
		}
	}
	for _, k := range ownedClearKeep {
		if n := ownedClearCount(t, a, k); n != 1 {
			t.Fatalf("unrelated cache %s changed", k)
		}
	}
	var v any
	if e := a.Store.Get(context.Background(), "owned_business", "media:movie:120", &v); e != nil {
		t.Fatal("business state changed", e)
	}
}

func TestOriginalClearCacheActualSDKSemantics(t *testing.T) {
	a, c, p := ownedClearFixture(t)
	ownedClearSeed(t, a)
	dir := t.TempDir()
	cachePath := filepath.Join(dir, "cache.value")
	keepPath := filepath.Join(dir, "business.value")
	if e := os.WriteFile(keepPath, []byte("keep"), 0600); e != nil {
		t.Fatal(e)
	}
	var mu sync.Mutex
	var notices []json.RawMessage
	recipient := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		notices = append(notices, b)
		mu.Unlock()
		w.WriteHeader(204)
	}))
	defer recipient.Close()
	raw, err := os.ReadFile("../../testdata/standard-commands/clear-cache-source.json")
	var oracle struct {
		SourceCommit string `json:"source_commit"`
		Cases        []struct {
			Name        string          `json:"name"`
			Input       json.RawMessage `json:"input"`
			Fallback    bool            `json:"native_fallback"`
			Title       string          `json:"title"`
			SaveHistory bool            `json:"save_history"`
		} `json:"cases"`
	}
	if err != nil || json.Unmarshal(raw, &oracle) != nil || oracle.SourceCommit != "8a83efeacd3e26b82be495e17d50146e31c96a60" || len(oracle.Cases) != 9 {
		t.Fatal("fixed source component evidence missing", err)
	}
	for _, sample := range oracle.Cases {
		t.Run(sample.Name, func(t *testing.T) {
			ownedClearSeed(t, a)
			if e := os.WriteFile(cachePath, []byte("owned cache"), 0600); e != nil {
				t.Fatal(e)
			}
			ownedClearConfigure(t, a, c, map[string]any{"enabled": true, "directory": dir, "endpoint": recipient.URL, "result": sample.Input})
			before := ownedClearRead(t, p)
			ownedClearCommand(t, a, c)
			after := ownedClearRead(t, p)
			if after.PID != before.PID || after.Calls["clear"] != before.Calls["clear"]+1 || after.Clears != before.Clears+1 {
				t.Fatal("actual retained SDK invocation/data mismatch", before, after)
			}
			if len(after.Inputs) == 0 || !originalBaseJSONEqual(after.Inputs[len(after.Inputs)-1], json.RawMessage(`{"args":[],"kwargs":{}}`)) {
				t.Fatal("command args leaked into zero-arg module", after.Inputs)
			}
			ownedClearCheck(t, a, sample.Fallback)
			if _, e := os.Stat(cachePath); !errors.Is(e, os.ErrNotExist) {
				t.Fatal("plugin-owned cache was not removed", e)
			}
			if b, e := os.ReadFile(keepPath); e != nil || string(b) != "keep" {
				t.Fatal("unrelated disk changed", e)
			}
			mu.Lock()
			last := append(json.RawMessage(nil), notices[len(notices)-1]...)
			mu.Unlock()
			var call struct {
				Kwargs struct{ Message map[string]any }
			}
			if json.Unmarshal(last, &call) != nil {
				t.Fatal(string(last))
			}
			m := call.Kwargs.Message
			if m["title"] != sample.Title || m["channel"] != "Web" || m["source"] != "admin" || m["userid"] != "admin" || m["save_history"] != sample.SaveHistory {
				t.Fatal("source notification differs", string(last))
			}
		})
	}
	var count int
	if e := a.Store.DB.QueryRow(`SELECT count(*) FROM records WHERE kind='notification'`).Scan(&count); e != nil || count != 0 {
		t.Fatal("save_history=false not retained", count, e)
	}
	// Every request still persists its incoming message, independently of output.
	if e := a.Store.DB.QueryRow(`SELECT count(*) FROM records WHERE kind='message'`).Scan(&count); e != nil || count != len(oracle.Cases) {
		t.Fatal(count, e)
	}
}

func TestOriginalClearCacheFailuresOverrideAuthAndCold(t *testing.T) {
	a, c, p := ownedClearFixture(t)
	ownedClearSeed(t, a)
	for _, sample := range []struct {
		cookie *http.Cookie
		origin string
		status int
	}{{nil, "", 401}, {c, "https://foreign.invalid", 403}} {
		r := callTest(t, a.Handler(), "POST", "/api/message/web", `{"text":"/clear_cache"}`, sample.cookie, sample.origin)
		if r.Code != sample.status {
			t.Fatal(r.Code, r.Body.String())
		}
	}
	ownedClearCheck(t, a, false)
	ownedClearConfigure(t, a, c, map[string]any{"enabled": true, "override": true})
	before := ownedClearRead(t, p)
	ownedClearCommand(t, a, c)
	after := ownedClearRead(t, p)
	if after.Calls["clear"] != before.Calls["clear"] {
		t.Fatal("preset bypassed plugin override")
	}
	ownedClearCheck(t, a, false)
	found := false
	for _, raw := range after.Events {
		var ev struct{ Type string }
		_ = json.Unmarshal(raw, &ev)
		found = found || ev.Type == "owned.clear_override"
	}
	if !found {
		t.Fatal("override event absent")
	}
	ownedClearConfigure(t, a, c, map[string]any{"enabled": true, "fail": true})
	ownedClearCommand(t, a, c)
	ownedClearCheck(t, a, true)
	after = ownedClearRead(t, p)
	found = false
	for _, raw := range after.Events {
		var ev struct {
			Type string
			Data map[string]any
		}
		_ = json.Unmarshal(raw, &ev)
		found = found || (ev.Type == "system.error" && ev.Data["plugin_method"] == "clear_cache")
	}
	if !found {
		t.Fatal("plugin failure not reported", after.Events)
	}
	ownedClearSeed(t, a)
	ownedClearConfigure(t, a, c, map[string]any{"enabled": true})
	if _, e := a.Store.DB.Exec(`CREATE TRIGGER owned_cache_failure BEFORE DELETE ON records WHEN OLD.kind='cache' AND OLD.id='media:movie:120' BEGIN SELECT RAISE(ABORT,'owned cache failure'); END`); e != nil {
		t.Fatal(e)
	}
	ownedClearCommand(t, a, c)
	if ownedClearCount(t, a, "media:movie:120") != 1 || ownedClearCount(t, a, "explore-detail:v1:douban:movie:120") != 0 {
		t.Fatal("module failure did not continue following provider")
	}
	if _, e := a.Store.DB.Exec(`DROP TRIGGER owned_cache_failure`); e != nil {
		t.Fatal(e)
	}
	ownedClearCommand(t, a, c)
	ownedClearCheck(t, a, true)
	old := ownedClearRead(t, p)
	dir := a.Dir
	if e := a.Close(); e != nil {
		t.Fatal(e)
	}
	cold, e := New(Options{DataDir: dir})
	if e != nil {
		t.Fatal(e)
	}
	defer cold.Close()
	if e = cold.initializeOriginalPlugins(context.Background()); e != nil {
		t.Fatal(e)
	}
	cold.wg.Wait()
	v, ok := cold.pluginRunningInstances.Load(ownedClearID)
	if !ok {
		t.Fatal("worker failed cold load")
	}
	next := ownedClearRead(t, v.(*originalPluginInstance))
	if next.PID == old.PID || next.Clears != old.Clears {
		t.Fatal("process/data not recovered", old, next)
	}
	ownedClearCheck(t, cold, true)
	var missing any
	if e = cold.Store.Get(context.Background(), "cache", "media:movie:120", &missing); !errors.Is(e, sql.ErrNoRows) {
		t.Fatal(e)
	}
}

func TestOriginalClearCacheNativeNoPluginAndRecommendation(t *testing.T) {
	a := newTestApp(t)
	c := setupTestAdmin(t, a)
	ownedClearSeed(t, a)
	ownedClearCommand(t, a, c)
	ownedClearCheck(t, a, true)
	// Exercise the actual native cache consumer, not only raw DELETE assertions.
	calls := 0
	var result catalog.Media
	fetch := func() (any, error) { calls++; return catalog.Media{ID: 120, Title: "owned fresh"}, nil }
	if _, e := a.cached(context.Background(), "media:movie:120", 1000000000000, &result, fetch); e != nil {
		t.Fatal(e)
	}
	if _, e := a.cached(context.Background(), "media:movie:120", 1000000000000, &result, fetch); e != nil {
		t.Fatal(e)
	}
	if calls != 1 {
		t.Fatal(calls)
	}
	ownedClearCommand(t, a, c)
	if _, e := a.cached(context.Background(), "media:movie:120", 1000000000000, &result, fetch); e != nil {
		t.Fatal(e)
	}
	if calls != 2 || !reflect.DeepEqual(result, catalog.Media{ID: 120, Title: "owned fresh"}) {
		t.Fatal("native cache not invalidated", calls, result)
	}
}

func TestOriginalClearCacheTwoActualWorkersOrder(t *testing.T) {
	a, c, p := ownedClearFixture(t)
	ownedClearConfigure(t, a, c, map[string]any{"enabled": true, "result": false})
	r := callTest(t, a.Handler(), "POST", "/api/plugins/"+ownedClearID+"/clone", `{"suffix":"B","name":"Owned second cache"}`, c, "")
	assertOriginalRegistrySuccess(t, r.Code, r.Body.Bytes())
	a.wg.Wait()
	v, ok := a.pluginRunningInstances.Load(ownedClearID + "b")
	if !ok {
		t.Fatal("second real worker absent")
	}
	q := v.(*originalPluginInstance)
	ownedDashboardMetadataSave(t, a, c, ownedClearID+"b", `{"enabled":true}`, 200)
	a.wg.Wait()
	first, second := ownedClearRead(t, p), ownedClearRead(t, q)
	if first.PID == second.PID {
		t.Fatal("not distinct processes")
	}
	ownedClearSeed(t, a)
	ownedClearCommand(t, a, c)
	next := ownedClearRead(t, q)
	if next.Calls["clear"] != second.Calls["clear"] {
		t.Fatal("false did not stop later plugin")
	}
	ownedClearCheck(t, a, false)
	ownedClearConfigure(t, a, c, map[string]any{"enabled": true, "result": []any{false}})
	ownedClearCommand(t, a, c)
	next = ownedClearRead(t, q)
	if next.Calls["clear"] != second.Calls["clear"]+1 {
		t.Fatal("list did not continue later plugin")
	}
	ownedClearCheck(t, a, true)
	ownedClearSeed(t, a)
	ownedClearConfigure(t, a, c, map[string]any{"enabled": false})
	before := ownedClearRead(t, p)
	ownedClearCommand(t, a, c)
	after := ownedClearRead(t, p)
	if after.Calls["clear"] != before.Calls["clear"] {
		t.Fatal("disabled provider executed")
	}
	ownedClearCheck(t, a, true)
}

func TestOriginalClearCacheAcceptedRequestCancellation(t *testing.T) {
	a, c, p := ownedClearFixture(t)
	ownedClearSeed(t, a)
	dir := t.TempDir()
	if e := os.WriteFile(filepath.Join(dir, "cache.value"), []byte("owned"), 0600); e != nil {
		t.Fatal(e)
	}
	ownedClearConfigure(t, a, c, map[string]any{"enabled": true, "directory": dir, "clear_delay_ms": 250})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req := httptest.NewRequest("POST", "/api/message/web", strings.NewReader(`{"text":"/clear_cache"}`)).WithContext(ctx)
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(c)
	response := httptest.NewRecorder()
	done := make(chan struct{})
	go func() { defer close(done); a.Handler().ServeHTTP(response, req) }()
	for until := time.Now().Add(5 * time.Second); ; {
		if _, e := os.Stat(filepath.Join(dir, "clear.entered")); e == nil {
			break
		}
		if time.Now().After(until) {
			t.Fatal("accepted command did not enter actual worker")
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("accepted command did not finish after caller cancellation")
	}
	a.wg.Wait()
	ownedClearCheck(t, a, true)
	if response.Code != 200 {
		t.Fatal(response.Code, response.Body.String())
	}
	s := ownedClearRead(t, p)
	if s.Clears != 1 || len(s.Notices) != 1 {
		t.Fatal("cancel interrupted source accepted command", s)
	}
}
