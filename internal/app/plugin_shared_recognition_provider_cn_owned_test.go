package app

// Test-launcher App + installed ordinary SDK worker + constructor-bound owned
// HTTP. This is not cmd/server or real TMDB acceptance. All provider detail
// bytes below come from the independently executed five-case source oracle.
import (
	"bytes"
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"reflect"
	"sync"
	"testing"

	"light-media/internal/catalog"
	"light-media/internal/networkproxy"
)

//go:embed testdata/source-provider-cn05.json
var ownedProviderCNSource05 []byte

type ownedProviderCNCase struct {
	ID    string `json:"id"`
	Input struct {
		MType    string          `json:"mtype"`
		TMDBID   int             `json:"tmdbid"`
		Language string          `json:"language"`
		Detail   json.RawMessage `json:"detail"`
	} `json:"input"`
	Outcome struct {
		Kind  string          `json:"kind"`
		Value json.RawMessage `json:"value"`
		Class string          `json:"class"`
	} `json:"outcome"`
}

func ownedProviderCNCases(t *testing.T) []ownedProviderCNCase {
	t.Helper()
	sum := sha256.Sum256(ownedProviderCNSource05)
	if hex.EncodeToString(sum[:]) != "285ad4a28c5b5bc03533baff22ff1bb43da490fa27be99222d3de1aa2f86dffa" {
		t.Fatal("source CN05 bytes changed")
	}
	var doc struct {
		Count int                   `json:"case_count"`
		Drift []json.RawMessage     `json:"input_drift"`
		Cases []ownedProviderCNCase `json:"cases"`
	}
	if json.Unmarshal(ownedProviderCNSource05, &doc) != nil || doc.Count != 5 || len(doc.Cases) != 5 || len(doc.Drift) != 0 {
		t.Fatal("source CN05 structure changed")
	}
	for _, c := range doc.Cases {
		if c.Input.TMDBID != 900071 || c.Input.Language != "zh-CN" {
			t.Fatal("unbound source ID/language")
		}
	}
	return doc.Cases
}

func ownedProviderCNJSON(t *testing.T, raw []byte) any {
	t.Helper()
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	var value any
	if err := d.Decode(&value); err != nil {
		t.Fatal(err)
	}
	return value
}

type ownedProviderCNPeer struct {
	mu              sync.Mutex
	server          *httptest.Server
	selected        ownedProviderCNCase
	logical, actual []string
	rejected        int
}

func (p *ownedProviderCNPeer) selectCase(t *testing.T, c ownedProviderCNCase) {
	t.Helper()
	switch c.ID {
	case "movie-traditional-phrase-first-unchanged-cn", "tv-default-wikitext-to-sg", "cn-scalar-title-source-typeerror":
	default:
		t.Fatal("outside fixed source cases")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.selected = c
}
func (p *ownedProviderCNPeer) assertTrace(t *testing.T, want []string) {
	t.Helper()
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.rejected != 0 || !reflect.DeepEqual(p.logical, want) || !reflect.DeepEqual(p.actual, want) {
		t.Fatalf("logical=%v actual=%v rejects=%d want=%v", p.logical, p.actual, p.rejected, want)
	}
}
func newOwnedProviderCNPeer(t *testing.T, first ownedProviderCNCase) *ownedProviderCNPeer {
	t.Helper()
	p := &ownedProviderCNPeer{}
	p.selectCase(t, first)
	p.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p.mu.Lock()
		defer p.mu.Unlock()
		kind := "movie"
		if p.selected.Input.MType == "TV" {
			kind = "tv"
		}
		if r.Method != http.MethodGet || r.URL.Path != "/owned/"+kind || r.URL.RawQuery != "" || r.ContentLength > 0 || r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" {
			p.rejected++
			http.Error(w, "outside fixed owned provider", 400)
			return
		}
		p.actual = append(p.actual, p.selected.ID+"/"+kind)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(p.selected.Input.Detail) // exact original detail input, not expected output
	}))
	t.Cleanup(func() {
		p.server.Close()
		p.mu.Lock()
		defer p.mu.Unlock()
		if p.rejected != 0 {
			t.Errorf("owned boundary rejects=%d", p.rejected)
		}
	})
	return p
}

// Same already-accepted test-only constructor seam as the S11-ID launcher.
// Only two official logical GETs are recognized. A NEW request is constructed
// to the one captured owned listener; no delegation, redirect or fallback.
type ownedProviderCNTransport struct {
	peer     *ownedProviderCNPeer
	endpoint string
	base     *http.Transport
}

func (tr *ownedProviderCNTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	deny := func() (*http.Response, error) {
		tr.peer.mu.Lock()
		tr.peer.rejected++
		tr.peer.mu.Unlock()
		return nil, errors.New("outside fixed owned CN transport")
	}
	if r == nil || r.URL == nil || r.Method != http.MethodGet || r.URL.Scheme != "https" || r.URL.Host != "api.themoviedb.org" || r.Host != "api.themoviedb.org" || r.URL.User != nil || r.URL.Fragment != "" || r.URL.Opaque != "" || r.URL.RawPath != "" || r.RequestURI != "" || r.ContentLength != 0 || (r.Body != nil && r.Body != http.NoBody) || r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" {
		return deny()
	}
	kind, fields := "", ""
	switch r.URL.Path {
	case "/3/movie/900071":
		kind, fields = "movie", "images,credits,alternative_titles,translations,release_dates,external_ids"
	case "/3/tv/900071":
		kind, fields = "tv", "images,credits,alternative_titles,translations,content_ratings,external_ids,episode_groups"
	default:
		return deny()
	}
	q, e := url.ParseQuery(r.URL.RawQuery)
	want := url.Values{"api_key": {ownedRecognitionKey}, "language": {"zh-CN"}, "append_to_response": {fields}}
	if e != nil || !reflect.DeepEqual(q, want) || r.Header.Get("Accept") != "application/json" || r.Header.Get("User-Agent") != "light-media/0.1" {
		return deny()
	}
	tr.peer.mu.Lock()
	tr.peer.logical = append(tr.peer.logical, tr.peer.selected.ID+"/"+kind)
	tr.peer.mu.Unlock()
	req, e := http.NewRequestWithContext(r.Context(), http.MethodGet, tr.endpoint+"/owned/"+kind, nil)
	if e != nil {
		return nil, errors.New("invalid fixed owned endpoint")
	}
	req.Header.Set("Accept", "application/json")
	return tr.base.RoundTrip(req)
}
func (tr *ownedProviderCNTransport) CloneForSourceRecognition() (http.RoundTripper, error) {
	return &ownedProviderCNTransport{tr.peer, tr.endpoint, tr.base.Clone()}, nil
}
func (tr *ownedProviderCNTransport) CloseIdleConnections() { tr.base.CloseIdleConnections() }
func bindOwnedProviderCNCatalog(t *testing.T, a *App, p *ownedProviderCNPeer) {
	t.Helper()
	s := a.Settings()
	if s.TMDB.APIKey != ownedRecognitionKey || s.TMDB.AccessToken != "" {
		t.Fatal("not the synthetic catalog key")
	}
	proxy, e := networkproxy.Resolve(s.ProxyHost, os.Getenv)
	if e != nil {
		t.Fatal("existing proxy policy invalid; no bypass")
	}
	base, ok := p.server.Client().Transport.(*http.Transport)
	if !ok {
		t.Fatal("owned transport type")
	}
	tr := &ownedProviderCNTransport{p, p.server.URL, base.Clone()}
	client := catalog.NewWithTransport(catalog.Credentials{APIKey: s.TMDB.APIKey}, tr)
	a.catMu.Lock()
	if a.catClient != nil {
		a.catClient.CloseIdleConnections()
	}
	a.catClient, a.catCreds, a.catProxy = client, s.TMDB, proxy
	a.catMu.Unlock()
}

func queryOwnedProviderCN(t *testing.T, a *App, cookie *http.Cookie, op string, cache json.RawMessage) ownedRecognitionWorkerSnapshot {
	t.Helper()
	operation := map[string]any{"op": op}
	if cache != nil {
		operation["cache"] = cache
	}
	body, _ := json.Marshal(map[string]any{"operations": []any{operation}})
	response := callTest(t, a.Handler(), http.MethodPost, "/api/v1/plugin/"+ownedRecognitionWorkerID+"/media-recognize", string(body), cookie, "")
	var out ownedRecognitionWorkerSnapshot
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &out) != nil || !out.OK || out.PID <= 0 || out.PID == os.Getpid() || out.InitCount != 1 || out.Requested != 1 || out.Completed != 1 || out.Succeeded != 1 || out.Failed != 0 || out.Interrupted != "" || len(out.Results) != 1 {
		t.Fatalf("installed worker call HTTP=%d body=%s", response.Code, response.Body.Bytes())
	}
	r := out.Results[0]
	if !r.OK || r.Index != 0 || r.Op != op || r.ErrorCode != "" || r.HostCalls.Total != 1 || r.HostCalls.Succeeded != 1 || r.HostCalls.Failed != 0 || r.After.Recognize-r.Before.Recognize != 1 {
		t.Fatal("actual callback/module accounting")
	}
	if len(out.ModuleTrace) == 0 || out.ModuleTrace[len(out.ModuleTrace)-1].Returned != "source_none_continue_to_native" {
		t.Fatal("module did not decline to native")
	}
	var module struct {
		Args   []json.RawMessage
		Kwargs map[string]json.RawMessage
	}
	if json.Unmarshal(out.ModuleTrace[len(out.ModuleTrace)-1].Input, &module) != nil || len(module.Args) != 0 || string(module.Kwargs["tmdbid"]) != "900071" || string(module.Kwargs["meta"]) != "null" {
		t.Fatal("exact source ID or Meta=None lost")
	}
	mt := `"电影"`
	if op == "native_tv" {
		mt = `"电视剧"`
	}
	wantCache := "true"
	if cache != nil {
		wantCache = string(cache)
	}
	if string(module.Kwargs["mtype"]) != mt || string(module.Kwargs["cache"]) != wantCache {
		t.Fatal("type/cache normalization")
	}
	_ = ownedRecognitionInstalledObject(t, a)
	return out
}
func assertOwnedProviderCNModel(t *testing.T, out ownedRecognitionWorkerSnapshot, c ownedProviderCNCase) {
	t.Helper()
	r := out.Results[0]
	if c.Outcome.Kind == "uncaught_exception" {
		if c.Outcome.Class != "TypeError" || r.ResultKind != "source_none" || string(r.Result) != "null" {
			t.Fatal("definite source TypeError did not reach original host None catch")
		}
		return
	}
	if c.Outcome.Kind != "return" || r.ResultKind != "full_raw_media_info" {
		t.Fatal("source/full model result kind")
	}
	want, ok := ownedProviderCNJSON(t, c.Outcome.Value).(map[string]any)
	if !ok {
		t.Fatal("source whole dict")
	}
	enum, ok := want["media_type"].(map[string]any)
	if !ok || len(enum) != 3 || enum["enum"] != "MediaType" || enum["name"] != c.Input.MType {
		t.Fatal("exact source enum")
	}
	want["media_type"] = enum["value"]
	fields, ok := ownedProviderCNJSON(t, r.Result).(map[string]any)
	if !ok {
		t.Fatal("complete model missing")
	}
	// The exact get_info dict is independently observed. Do not call target
	// projector to manufacture the expected full MediaInfo object.
	if !reflect.DeepEqual(fields["tmdb_info"], want) {
		got, _ := json.Marshal(fields["tmdb_info"])
		expected, _ := json.Marshal(want)
		t.Fatalf("whole provider payload differs\ngot=%s\nsource=%s", got, expected)
	}
	var defaults map[string]json.RawMessage
	if json.Unmarshal(sourceMediaInfoDefaults, &defaults) != nil {
		t.Fatal("source model field vocabulary")
	}
	for key := range defaults {
		if _, present := fields[key]; !present {
			t.Fatalf("full model missing %s", key)
		}
	}
	titleKey := "title"
	if c.Input.MType == "TV" {
		titleKey = "name"
	}
	if fields["source"] != "themoviedb" || fields["tmdb_id"] != want["id"] || fields["type"] != want["media_type"] || fields["title"] != want[titleKey] || fields["title_year"] != want[titleKey] || fields["recognize_cache_hit"] != false {
		t.Fatal("source identity/title projection or noMeta hit state")
	}
	for _, key := range []string{"en_title", "hk_title", "tw_title", "sg_title", "names", "genre_ids"} {
		if !reflect.DeepEqual(fields[key], want[key]) {
			t.Fatalf("source model field %s differs", key)
		}
	}
	if _, ok := fields["category"].(string); !ok {
		t.Fatal("real category path did not return string")
	}
	// Full new-fixture model equality is NOT claimed: the existing independently
	// source-tested model/category components carry that separate evidence.
}

func TestOriginalMediaRecognitionOwnedProviderCNSource05(t *testing.T) {
	cases := ownedProviderCNCases(t)
	movie, tv, bad := cases[0], cases[1], cases[4]
	peer := newOwnedProviderCNPeer(t, movie)
	a, e := New(Options{DataDir: t.TempDir()})
	if e != nil {
		t.Fatal(e)
	}
	closed := false
	t.Cleanup(func() {
		if !closed {
			_ = a.Close()
		}
	})
	disableResetFixtureStatistics(t, a)
	settings := a.Settings()
	settings.PluginMarkets, settings.PluginLocalRepoPaths = []string{}, []string{}
	settings.PluginAutoReload = false
	settings.Sites, settings.Downloaders, settings.Roots, settings.AllowedRoots = []SiteConfig{}, []DownloaderConfig{}, []Root{}, []string{}
	settings.TMDB = TMDBConfig{APIKey: ownedRecognitionKey}
	// The inherited statistics helper disables PluginStatisticShare and clears
	// PluginServerURL (owned center isolation). It does not change media-report
	// Share/API, ProxyHost, environment, CA or production origin. Meta is null
	// in every SDK call: the source report payload is None, independently of
	// that statistics isolation; the noMeta target route has no report call.
	if e = a.saveSettings(context.Background(), settings); e != nil {
		t.Fatal(e)
	}
	bindOwnedProviderCNCatalog(t, a, peer)
	cookie := setupTestAdmin(t, a)
	installOwnedRecognitionWorker(t, a, cookie)
	trace := []string{}
	var previous ownedRecognitionWorkerSnapshot
	call := func(c ownedProviderCNCase, cache json.RawMessage, fetch bool) ownedRecognitionWorkerSnapshot {
		peer.selectCase(t, c)
		op, kind := "native", "movie"
		if c.Input.MType == "TV" {
			op, kind = "native_tv", "tv"
		}
		out := queryOwnedProviderCN(t, a, cookie, op, cache)
		if previous.PID != 0 && (out.PID != previous.PID || out.Invocation != previous.Invocation+1) {
			t.Fatal("warm worker identity/recovery changed")
		}
		if fetch {
			trace = append(trace, c.ID+"/"+kind)
		}
		peer.assertTrace(t, trace)
		assertOwnedProviderCNModel(t, out, c)
		sum := sha256.Sum256(out.Results[0].Result)
		t.Logf("case=%s invocation=%d pid=%d callback=%d result=%s sha256=%x actual_provider_reads=%d", c.ID, out.Invocation, out.PID, out.Results[0].HostCalls.Total, out.Results[0].ResultKind, sum, len(trace))
		t.Logf("owned_full_result case=%s raw=%s", c.ID, out.Results[0].Result)
		previous = out
		return out
	}
	first := call(movie, json.RawMessage(`false`), true)
	if first.Invocation != 1 {
		t.Fatal("first business invocation")
	}
	call(tv, json.RawMessage(`false`), true)
	call(bad, json.RawMessage(`false`), true)
	recovered := call(movie, json.RawMessage(`false`), true)
	warm := call(movie, nil, false)
	if !bytes.Equal(warm.Results[0].Result, recovered.Results[0].Result) {
		t.Fatal("warm snapshot changed complete model")
	}
	installed, e := a.pluginStore.Installation(context.Background(), ownedRecognitionWorkerID)
	if e != nil || !installed.Installed {
		t.Fatal("installed artifact identity")
	}
	dir := a.Dir
	if e = a.Close(); e != nil {
		t.Fatal(e)
	}
	closed = true
	cold, e := New(Options{DataDir: dir})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = cold.Close() })
	bindOwnedProviderCNCatalog(t, cold, peer)
	if e = cold.initializeOriginalPlugins(context.Background()); e != nil {
		t.Fatal(e)
	}
	retained, e := cold.pluginStore.Installation(context.Background(), ownedRecognitionWorkerID)
	if e != nil || !retained.Installed || retained.PluginID != installed.PluginID || retained.Version != installed.Version || retained.Generation != installed.Generation {
		t.Fatal("cold artifact changed/reinstalled")
	}
	login := callTest(t, cold.Handler(), http.MethodPost, "/api/auth/login", `{"username":"admin","password":"a-strong-test-password"}`, nil, "")
	var coldCookie *http.Cookie
	for _, value := range login.Result().Cookies() {
		if value.Name == "lm_session" {
			coldCookie = value
		}
	}
	if login.Code != 200 || coldCookie == nil {
		t.Fatal("cold login")
	}
	peer.selectCase(t, movie)
	reopened := queryOwnedProviderCN(t, cold, coldCookie, "native", nil)
	assertOwnedProviderCNModel(t, reopened, movie)
	trace = append(trace, movie.ID+"/movie")
	peer.assertTrace(t, trace)
	if reopened.PID == first.PID || reopened.InitCount != 1 || reopened.Invocation != 1 {
		t.Fatal("cold child identity did not restart")
	}
	if !bytes.Equal(reopened.Results[0].Result, recovered.Results[0].Result) {
		t.Fatal("cold complete model differs for exact source input")
	}
	t.Logf("owned_full_result cold=true raw=%s", reopened.Results[0].Result)
	t.Logf("owned App launcher only: warm_pid=%d cold_pid=%d; six SDK calls, five actual owned provider GETs, two artifact GETs; noMeta source-catch recovery/warm/cold. No claim of real TMDB or with-Meta report acceptance", first.PID, reopened.PID)
}
