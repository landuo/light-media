package app

// Owned App test launcher, NOT an ordinary cmd/server process. The coordinator
// supplies an already-built standard worker. No build, env mutation, upstream
// access, production origin setting or replacement HostCaller is used here.
import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"

	"light-media/internal/catalog"
	"light-media/internal/networkproxy"
	"light-media/internal/plugins"
)

const ownedRecognitionWorkerID = "OwnedMediaRecognition"
const ownedRecognitionKey = "owned-recognition-synthetic-key"

type ownedRecognitionProvider struct {
	mu              sync.Mutex
	server          *httptest.Server
	phase           string
	logical, actual []string
	unexpected      int
}

// All payloads are controlled protocol fixtures, not copied original oracle
// results. Unknown provider fields and a large JSON integer witness full raw
// MediaInfo preservation beyond the compact UI model.
func ownedRecognitionDetail(kind, title string) map[string]any {
	out := map[string]any{
		"id": 900045, "original_language": "en", "origin_country": []string{"US"},
		"genres": []any{}, "credits": map[string]any{"cast": []any{}, "crew": []any{}},
		"external_ids":         map[string]any{"imdb_id": "owned-not-imdb", "tvdb_id": 900046},
		"alternative_titles":   map[string]any{"titles": []any{}, "results": []any{}},
		"translations":         map[string]any{"translations": []any{}},
		"images":               map[string]any{"logos": []any{}},
		"production_countries": []any{map[string]any{"iso_3166_1": "US", "name": "Owned"}},
		"overview":             "Owned complete provider payload", "vote_average": json.Number("7.5"),
		"source_only_field": map[string]any{"beyond": "compact DTO", "precise": json.Number("900719925474099312345")},
	}
	if kind == "movie" {
		out["title"], out["original_title"], out["release_date"] = title, title, "2026-01-02"
		out["release_dates"] = map[string]any{"results": []any{}}
	} else {
		out["name"], out["original_name"], out["first_air_date"] = title, title, "2026-01-02"
		out["content_ratings"] = map[string]any{"results": []any{}}
		out["seasons"] = []any{map[string]any{"season_number": 1, "episode_count": 2, "air_date": "2026-01-02"}}
		out["episode_groups"] = map[string]any{"results": []any{}}
	}
	return out
}

func newOwnedRecognitionProvider(t *testing.T) *ownedRecognitionProvider {
	t.Helper()
	p := &ownedRecognitionProvider{phase: "movie-a"}
	p.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p.mu.Lock()
		defer p.mu.Unlock()
		kind := ""
		switch r.URL.Path {
		case "/owned/movie":
			kind = "movie"
		case "/owned/tv":
			kind = "tv"
		}
		if r.Method != http.MethodGet || kind == "" || r.URL.RawQuery != "" || r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" || r.ContentLength > 0 {
			p.unexpected++
			http.Error(w, "outside fixed owned provider", http.StatusBadRequest)
			return
		}
		p.actual = append(p.actual, kind+":"+p.phase)
		w.Header().Set("Content-Type", "application/json")
		if p.phase == "error" || (p.phase == "unknown" && kind == "movie") {
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(map[string]any{"success": false, "status_code": 34, "status_message": "Owned missing result"})
			return
		}
		_ = json.NewEncoder(w).Encode(ownedRecognitionDetail(kind, "Owned "+p.phase))
	}))
	t.Cleanup(func() {
		p.server.Close()
		p.mu.Lock()
		defer p.mu.Unlock()
		if p.unexpected != 0 {
			t.Errorf("owned provider boundary rejects=%d", p.unexpected)
		}
	})
	return p
}

func (p *ownedRecognitionProvider) setPhase(phase string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	switch phase {
	case "movie-a", "movie-b", "movie-c", "error", "recovered", "tv", "unknown", "cold":
		p.phase = phase
	default:
		panic("invalid owned provider phase")
	}
}

func (p *ownedRecognitionProvider) assertTrace(t *testing.T, want []string) {
	t.Helper()
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.unexpected != 0 || !reflect.DeepEqual(p.logical, want) || !reflect.DeepEqual(p.actual, want) {
		t.Fatalf("fixed provider trace differs: logical=%v actual=%v want=%v rejects=%d", p.logical, p.actual, want, p.unexpected)
	}
}

// This trusted test-only transport admits exactly two official logical reads,
// then constructs a NEW request to the one owned listener captured at creation.
// It never delegates an official URL, original credential header, CONNECT,
// redirect, arbitrary URL or rejected request to any transport.
type ownedRecognitionTransport struct {
	peer     *ownedRecognitionProvider
	endpoint string
	base     *http.Transport
}

func (tr *ownedRecognitionTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	denied := func() (*http.Response, error) {
		tr.peer.mu.Lock()
		tr.peer.unexpected++
		tr.peer.mu.Unlock()
		return nil, errors.New("outside fixed owned recognition transport")
	}
	if r == nil || r.URL == nil || r.Method != http.MethodGet || r.URL.Scheme != "https" || r.URL.Host != "api.themoviedb.org" || r.Host != "api.themoviedb.org" || r.ContentLength != 0 || r.URL.User != nil || r.URL.Fragment != "" || r.URL.Opaque != "" || r.URL.RawPath != "" || r.RequestURI != "" || (r.Body != nil && r.Body != http.NoBody) || r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" {
		return denied()
	}
	kind, appendFields := "", ""
	switch r.URL.Path {
	case "/3/movie/900045":
		kind, appendFields = "movie", "images,credits,alternative_titles,translations,release_dates,external_ids"
	case "/3/tv/900045":
		kind, appendFields = "tv", "images,credits,alternative_titles,translations,content_ratings,external_ids,episode_groups"
	default:
		return denied()
	}
	query, err := url.ParseQuery(r.URL.RawQuery)
	want := url.Values{"api_key": {ownedRecognitionKey}, "language": {"zh-CN"}, "append_to_response": {appendFields}}
	if err != nil || !reflect.DeepEqual(query, want) || r.Header.Get("Accept") != "application/json" || r.Header.Get("User-Agent") != "light-media/0.1" {
		return denied()
	}
	tr.peer.mu.Lock()
	tr.peer.logical = append(tr.peer.logical, kind+":"+tr.peer.phase)
	tr.peer.mu.Unlock()
	request, err := http.NewRequestWithContext(r.Context(), http.MethodGet, tr.endpoint+"/owned/"+kind, nil)
	if err != nil {
		return nil, errors.New("invalid fixed owned listener")
	}
	request.Header.Set("Accept", "application/json")
	return tr.base.RoundTrip(request)
}

func (tr *ownedRecognitionTransport) CloneForSourceRecognition() (http.RoundTripper, error) {
	return &ownedRecognitionTransport{peer: tr.peer, endpoint: tr.endpoint, base: tr.base.Clone()}, nil
}
func (tr *ownedRecognitionTransport) CloseIdleConnections() { tr.base.CloseIdleConnections() }

func bindOwnedRecognitionCatalog(t *testing.T, a *App, peer *ownedRecognitionProvider) {
	t.Helper()
	settings := a.Settings()
	if settings.TMDB.APIKey != ownedRecognitionKey || settings.TMDB.AccessToken != "" {
		t.Fatal("not the fixed synthetic catalog credential")
	}
	proxy, err := networkproxy.Resolve(settings.ProxyHost, os.Getenv)
	if err != nil {
		t.Fatal("existing proxy policy is invalid; not bypassing it")
	}
	base, ok := peer.server.Client().Transport.(*http.Transport)
	if !ok {
		t.Fatal("owned server transport type differs")
	}
	transport := &ownedRecognitionTransport{peer: peer, endpoint: peer.server.URL, base: base.Clone()}
	client := catalog.NewWithTransport(catalog.Credentials{APIKey: settings.TMDB.APIKey, AccessToken: settings.TMDB.AccessToken}, transport)
	a.catMu.Lock()
	if a.catClient != nil {
		a.catClient.CloseIdleConnections()
	}
	a.catClient, a.catCreds, a.catProxy = client, settings.TMDB, proxy
	a.catMu.Unlock()
}

func ownedRecognitionInstalledObject(t *testing.T, a *App) *originalPluginInstance {
	t.Helper()
	value, ok := a.pluginRunningInstances.Load(ownedRecognitionWorkerID)
	if !ok {
		t.Fatal("actual recognition worker not loaded")
	}
	object, ok := value.(*originalPluginInstance)
	if !ok || !object.external || object.hostContract != pluginResourcesSDKContract {
		t.Fatal("not an ordinary resource-contract external worker")
	}
	object.sessionMu.Lock()
	concurrent := object.session != nil && object.session.SupportsConcurrent()
	object.sessionMu.Unlock()
	if !concurrent {
		t.Fatal("actual worker did not negotiate concurrent self-reentry")
	}
	return object
}

func installOwnedRecognitionWorker(t *testing.T, a *App, cookie *http.Cookie) {
	t.Helper()
	index, payload := originalMarketOwnedIndex(t, "OWNED_MEDIA_RECOGNITION_WORKER")
	manifest := index.Releases[0].Manifest
	if manifest.Validate() != nil || manifest.PluginID != ownedRecognitionWorkerID || manifest.Entry != "owned-media-recognition-fixture" || !reflect.DeepEqual(manifest.Capabilities, []string{plugins.MediaRecognizeCapability}) || len(manifest.APIs) != 1 || manifest.APIs[0].Key != "media_recognize" || manifest.APIs[0].Path != "/media-recognize" || manifest.APIs[0].Auth != "bear" || manifest.APIs[0].Body != "json" || !manifest.APIs[0].BodyRequired || !reflect.DeepEqual(manifest.APIs[0].Methods, []string{"POST"}) {
		t.Fatal("actual recognition artifact manifest differs")
	}
	index.Releases[0].Artifacts[0].HostContract = pluginResourcesSDKContract
	var indexHits, workerHits, rejected atomic.Int32
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.RawQuery != "" || r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" || r.ContentLength > 0 {
			rejected.Add(1)
			http.Error(w, "fixed read-only artifact", 400)
			return
		}
		switch r.URL.Path {
		case "/index":
			indexHits.Add(1)
			_ = json.NewEncoder(w).Encode(index)
		case "/worker":
			workerHits.Add(1)
			_, _ = w.Write(payload)
		default:
			rejected.Add(1)
			http.NotFound(w, r)
		}
	}))
	index.Releases[0].Artifacts[0].URL = "http://" + server.Listener.Addr().String() + "/worker"
	server.Start()
	t.Cleanup(func() {
		server.Close()
		if indexHits.Load() != 1 || workerHits.Load() != 1 || rejected.Load() != 0 {
			t.Errorf("artifact fetches=%d/%d rejects=%d; cold must not reinstall/refetch", indexHits.Load(), workerHits.Load(), rejected.Load())
		}
	})
	body, _ := json.Marshal(map[string]string{"plugin_id": ownedRecognitionWorkerID, "repo_url": server.URL + "/index"})
	response := callTest(t, a.Handler(), http.MethodPost, "/api/plugins/install", string(body), cookie, "")
	assertOriginalRegistrySuccess(t, response.Code, response.Body.Bytes())
	// App.Start is intentionally not called; this joins the finite actual
	// installer, not unrelated long-lived schedulers.
	a.wg.Wait()
	_ = ownedRecognitionInstalledObject(t, a)
}

type ownedRecognitionWorkerSnapshot struct {
	PID, Invocation, Requested, Completed, Succeeded, Failed int
	InitCount                                                int `json:"init_count"`
	OK                                                       bool
	Interrupted                                              string
	Results                                                  []struct {
		Index         int
		Op            string
		OK            bool
		Result        json.RawMessage
		ResultKind    string                                 `json:"result_kind"`
		ErrorCode     string                                 `json:"error_code"`
		HostCalls     struct{ Total, Succeeded, Failed int } `json:"host_calls"`
		Before, After struct{ State, Declaration, Recognize int }
	}
	ModuleTrace []struct {
		Sequence int
		Input    json.RawMessage
		Returned string
	} `json:"module_trace"`
}

func queryOwnedRecognition(t *testing.T, a *App, cookie *http.Cookie, op string, cache json.RawMessage) ownedRecognitionWorkerSnapshot {
	t.Helper()
	operation := map[string]any{"op": op}
	if cache != nil {
		operation["cache"] = cache
	}
	body, _ := json.Marshal(map[string]any{"operations": []any{operation}})
	response := callTest(t, a.Handler(), http.MethodPost, "/api/v1/plugin/"+ownedRecognitionWorkerID+"/media-recognize", string(body), cookie, "")
	var out ownedRecognitionWorkerSnapshot
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &out) != nil || !out.OK || out.PID <= 0 || out.PID == os.Getpid() || out.InitCount != 1 || out.Requested != 1 || out.Completed != 1 || out.Succeeded != 1 || out.Failed != 0 || out.Interrupted != "" || len(out.Results) != 1 {
		t.Fatalf("actual recognition worker call failed: HTTP=%d body=%s", response.Code, response.Body.Bytes())
	}
	r := out.Results[0]
	if !r.OK || r.Index != 0 || r.Op != op || r.ErrorCode != "" || r.HostCalls.Total != 1 || r.HostCalls.Succeeded != 1 || r.HostCalls.Failed != 0 || r.After.Recognize-r.Before.Recognize != 1 {
		t.Fatal("actual SDK/native callback or module accounting differs")
	}
	if len(out.ModuleTrace) == 0 || out.ModuleTrace[len(out.ModuleTrace)-1].Returned != "source_none_continue_to_native" {
		t.Fatal("owned module did not actually decline to native")
	}
	var module struct {
		Args   []json.RawMessage
		Kwargs map[string]json.RawMessage
	}
	if json.Unmarshal(out.ModuleTrace[len(out.ModuleTrace)-1].Input, &module) != nil || len(module.Args) != 0 || string(module.Kwargs["tmdbid"]) != "900045" {
		t.Fatal("native fixed module trace differs")
	}
	wantType := `"电影"`
	if op == "native_tv" {
		wantType = `"电视剧"`
	}
	if op == "native_unknown" {
		wantType = "null"
	}
	wantCache := "true"
	if cache != nil {
		wantCache = string(cache)
	}
	if string(module.Kwargs["mtype"]) != wantType || string(module.Kwargs["cache"]) != wantCache {
		t.Fatal("native type/cache normalization lost")
	}
	_ = ownedRecognitionInstalledObject(t, a)
	return out
}

func assertOwnedRecognitionModel(t *testing.T, out ownedRecognitionWorkerSnapshot, kind, title string) {
	t.Helper()
	r := out.Results[0]
	var fields, defaults map[string]json.RawMessage
	if r.ResultKind != "full_raw_media_info" || json.Unmarshal(r.Result, &fields) != nil || json.Unmarshal(sourceMediaInfoDefaults, &defaults) != nil {
		t.Fatal("complete raw model missing")
	}
	for key := range defaults {
		if key != "recognize_cache_hit" {
			if _, exists := fields[key]; !exists {
				t.Fatalf("complete native model omitted %s", key)
			}
		}
	}
	text := func(key string) string {
		var value string
		if json.Unmarshal(fields[key], &value) != nil {
			t.Fatalf("native model field %s not a string", key)
		}
		return value
	}
	if string(fields["recognize_cache_hit"]) != "false" {
		t.Fatal("HTTP snapshot hit was confused with metadata identity-cache hit")
	}
	if text("source") != "themoviedb" || text("type") != kind || text("title") != title || text("imdb_id") != "owned-not-imdb" || string(fields["tmdb_id"]) != "900045" || text("title_year") != title+" (2026)" {
		t.Fatal("native source full-model identity/title differs")
	}
	var info, extra map[string]json.RawMessage
	if json.Unmarshal(fields["tmdb_info"], &info) != nil || json.Unmarshal(info["source_only_field"], &extra) != nil || string(extra["precise"]) != "900719925474099312345" {
		t.Fatal("raw native provider extension/number lost")
	}
	if kind == "电视剧" {
		var seasons map[string][]int
		if json.Unmarshal(fields["seasons"], &seasons) != nil || !reflect.DeepEqual(seasons["1"], []int{1, 2}) {
			t.Fatal("native TV season projection lost")
		}
	}
}

func TestOriginalMediaRecognitionOwnedWorkerProviderAndCold(t *testing.T) {
	peer := newOwnedRecognitionProvider(t)
	a, err := New(Options{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
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
	if err := a.saveSettings(context.Background(), settings); err != nil {
		t.Fatal("save owned recognition settings failed")
	}
	bindOwnedRecognitionCatalog(t, a, peer)
	cookie := setupTestAdmin(t, a)
	installOwnedRecognitionWorker(t, a, cookie)
	trace := []string{}
	var previous ownedRecognitionWorkerSnapshot
	call := func(phase, op string, cache json.RawMessage, additions []string, kind, title string) ownedRecognitionWorkerSnapshot {
		peer.setPhase(phase)
		out := queryOwnedRecognition(t, a, cookie, op, cache)
		if previous.PID != 0 && (out.PID != previous.PID || out.Invocation != previous.Invocation+1 || out.InitCount != previous.InitCount) {
			t.Fatal("warm worker was replaced or invocation skipped")
		}
		trace = append(trace, additions...)
		peer.assertTrace(t, trace)
		if kind == "" {
			if out.Results[0].ResultKind != "source_none" || string(out.Results[0].Result) != "null" {
				t.Fatal("valid source provider error was not None")
			}
		} else {
			assertOwnedRecognitionModel(t, out, kind, title)
		}
		sum := sha256.Sum256(out.Results[0].Result)
		t.Logf("owned step invocation=%d op=%s cache=%s pid=%d callbacks=%d result_kind=%s result_sha256=%x provider_trace=%v", out.Invocation, op, string(cache), out.PID, out.Results[0].HostCalls.Total, out.Results[0].ResultKind, sum, trace)
		previous = out
		return out
	}
	first := call("movie-a", "native", nil, []string{"movie:movie-a"}, "电影", "Owned movie-a")
	if first.Invocation != 1 {
		t.Fatal("first worker business invocation is not one")
	}
	call("movie-b", "native", nil, nil, "电影", "Owned movie-a")
	call("movie-b", "native", json.RawMessage(`false`), []string{"movie:movie-b"}, "电影", "Owned movie-b")
	call("movie-c", "native", json.RawMessage(`null`), []string{"movie:movie-c"}, "电影", "Owned movie-c")
	call("error", "native", json.RawMessage(`false`), []string{"movie:error"}, "", "")
	call("recovered", "native", nil, nil, "", "") // valid error JSON remains cached
	call("recovered", "native", json.RawMessage(`false`), []string{"movie:recovered"}, "电影", "Owned recovered")
	call("tv", "native_tv", json.RawMessage(`false`), []string{"tv:tv"}, "电视剧", "Owned tv")
	// Unknown type MUST read TV then movie even after TV succeeds. The fixed
	// movie response is a source error object; the one successful side is TV.
	call("unknown", "native_unknown", json.RawMessage(`false`), []string{"tv:unknown", "movie:unknown"}, "电视剧", "Owned unknown")
	installed, err := a.pluginStore.Installation(context.Background(), ownedRecognitionWorkerID)
	if err != nil || !installed.Installed {
		t.Fatal("actual installed identity missing")
	}
	dir := a.Dir
	if err := a.Close(); err != nil {
		t.Fatal("warm App close failed")
	}
	closed = true
	cold, err := New(Options{DataDir: dir})
	if err != nil {
		t.Fatal("same owned database reopen failed")
	}
	t.Cleanup(func() { _ = cold.Close() })
	bindOwnedRecognitionCatalog(t, cold, peer)
	if err := cold.initializeOriginalPlugins(context.Background()); err != nil {
		t.Fatal("cold actual plugin initialization failed")
	}
	retained, err := cold.pluginStore.Installation(context.Background(), ownedRecognitionWorkerID)
	if err != nil || !retained.Installed || retained.PluginID != installed.PluginID || retained.Version != installed.Version || retained.Generation != installed.Generation {
		t.Fatal("cold artifact was reinstalled or identity changed")
	}
	login := callTest(t, cold.Handler(), http.MethodPost, "/api/auth/login", `{"username":"admin","password":"a-strong-test-password"}`, nil, "")
	var coldCookie *http.Cookie
	for _, value := range login.Result().Cookies() {
		if value.Name == "lm_session" {
			coldCookie = value
		}
	}
	if login.Code != http.StatusOK || coldCookie == nil {
		t.Fatal("cold ordinary login failed")
	}
	peer.setPhase("cold")
	reopened := queryOwnedRecognition(t, cold, coldCookie, "native", nil)
	assertOwnedRecognitionModel(t, reopened, "电影", "Owned cold")
	trace = append(trace, "movie:cold")
	peer.assertTrace(t, trace)
	t.Logf("owned cold invocation=%d provider_trace=%v", reopened.Invocation, trace)
	if reopened.PID == first.PID || reopened.Invocation != 1 || reopened.InitCount != 1 {
		t.Fatal("cold worker object did not restart")
	}
	t.Logf("owned App launcher: warm_pid=%d cold_pid=%d; 10 installed SDK calls, 9 owned provider reads, 2 artifact GETs; cache/fresh/error recovery and TV-before-movie order; not ordinary cmd/server or real TMDB acceptance", first.PID, reopened.PID)
}
