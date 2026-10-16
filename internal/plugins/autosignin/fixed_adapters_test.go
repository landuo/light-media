package autosignin

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

type fixedPages struct {
	site      Object
	responses []any
	calls     []any
	steps     *[]any
}

func (f *fixedPages) Do(ctx context.Context, r PageRequest) (*PageResponse, error) {
	proxy := any(nil)
	if Truth(f.site["proxy"]) {
		proxy = Object{"https": "synthetic-proxy-reference"}
	}
	call := Object{"method": r.Method, "proxies": proxy, "timeout": r.Timeout, "url": r.URL}
	if r.HeadersMode == HeaderBaseCookie {
		call["headers"] = Object{"User-Agent": f.site["ua"], "Cookie": f.site["cookie"]}
	} else {
		if r.Headers != nil {
			call["headers"] = r.Headers
		} else {
			call["ua"] = f.site["ua"]
		}
		if !r.NoCookies {
			cookie := f.site["cookie"]
			if r.CookieOverride != nil {
				cookie = *r.CookieOverride
			}
			call["cookies"] = cookie
		}
	}
	if r.AcceptType != "" {
		call["accept_type"] = r.AcceptType
	}
	if r.Referer != "" {
		call["referer"] = r.Referer
	}

	if r.Method == "POST" {
		if r.JSON != nil {
			call["json"] = r.JSON
		} else {
			call["data"] = r.Form
		}
	}
	f.calls = append(f.calls, call)
	if f.steps != nil {
		*f.steps = append(*f.steps, Object{"op": "request", "index": len(f.calls) - 1})
	}
	if len(f.responses) == 0 {
		return nil, nil
	}
	raw := f.responses[0]
	f.responses = f.responses[1:]
	if raw == nil {
		return nil, nil
	}
	row := raw.(Object)
	if msg, ok := row["error"].(string); ok {
		return nil, errors.New(msg)
	}
	status := 200
	if n := row["status"]; n != nil {
		integer, _ := sourceInteger(n)
		status = int(integer.Int64())
	}
	text := textValue(row["text"])
	cookies := []CookieValue{}
	for _, row := range asList(row["cookies"]) {
		pair := asList(row)
		cookies = append(cookies, CookieValue{textValue(pair[0]), textValue(pair[1])})
	}
	content := []byte(text)
	if raw, ok := row["raw_hex"].(string); ok {
		content, _ = hex.DecodeString(raw)
	}
	return &PageResponse{Status: status, Text: text, Content: content, Cookies: cookies}, nil
}
func (f *fixedPages) Page(ctx context.Context, r PageRequest) (string, error) {
	proxy := any(nil)
	if Truth(f.site["proxy"]) {
		proxy = "synthetic-proxy-reference"
	}
	f.calls = append(f.calls, Object{"method": "BROWSER", "url": r.URL, "cookies": f.site["cookie"], "ua": f.site["ua"], "proxies": proxy, "timeout": r.Timeout})
	if f.steps != nil {
		*f.steps = append(*f.steps, Object{"op": "browser", "index": len(f.calls) - 1})
	}
	if len(f.responses) == 0 {
		return "", nil
	}
	raw := f.responses[0]
	f.responses = f.responses[1:]
	if raw == nil {
		return "", nil
	}
	row := raw.(Object)
	if msg, ok := row["error"].(string); ok {
		return "", errors.New(msg)
	}
	return textValue(row["text"]), nil
}
func fixedEngine(site Object) *Engine {
	e := New(Inventory{Configured: []Object{{"id": 1, "name": "Synthetic"}}, Indexers: []Object{site}}, func() time.Time { return time.Date(2026, 1, 2, 12, 0, 0, 0, time.UTC) })
	if err := e.Init(Object{"enabled": true, "sign_sites": []any{1}, "login_sites": []any{}, "queue_cnt": 1, "notify": false, "retry_keyword": "Cookie已失效", "clean": false}); err != nil {
		panic(err)
	}
	e.Trace = []Object{}
	return e
}
func TestActualInstalledFixedAdaptersWholeFlow(t *testing.T) {
	checkInstalledAdapterReference(t, "fixed-reference.json", 105)
}
func TestActualInstalledOCRAdaptersWholeFlow(t *testing.T) {
	checkInstalledAdapterReference(t, "ocr-reference.json", 147)
}
func TestActualInstalledQuizAdaptersWholeFlow(t *testing.T) {
	checkInstalledAdapterReference(t, "quiz-reference.json", 180)
}
func TestActualInstalledDynamicAdaptersWholeFlow(t *testing.T) {
	checkInstalledAdapterReference(t, "dynamic-reference.json", 96)
}
func TestActualInstalledJSONAdaptersWholeFlow(t *testing.T) {
	checkInstalledAdapterReference(t, "json-reference.json", 255)
}
func TestActualInstalledSimpleAdaptersWholeFlow(t *testing.T) {
	checkInstalledAdapterReference(t, "simple-reference.json", 153)
}
func checkInstalledAdapterReference(t *testing.T, path string, count int) {
	raw, err := readAutoSignInFixture(path)
	if err != nil {
		t.Fatal(err)
	}
	var ref struct {
		Cases []struct {
			Name, Kind                      string
			Adapter                         FixedAdapter
			Input, Expected, Error, Partial Object
			Entropy, Steps                  []any
		}
	}
	if err = Decode(raw, &ref); err != nil {
		t.Fatal(err)
	}
	if len(ref.Cases) != count {
		t.Fatal("reference count changed")
	}
	for _, c := range ref.Cases {
		t.Run(c.Name, func(t *testing.T) {
			policy := fixedPolicies[c.Adapter]
			if c.Adapter == "MTorrent" {
				policy.SiteURL = "m-team"
			}
			site := Object{"id": 1, "name": "Synthetic", "url": "https://" + policy.SiteURL + "/", "cookie": "synthetic-cookie=not-a-credential", "ua": "synthetic-agent"}
			for k, v := range asObject(c.Input["site"]) {
				site[k] = v
			}
			pages := &fixedPages{site: site, calls: []any{}, responses: asList(clone(c.Input["responses"]))}
			stats := []any{}
			steps := []any{}
			if c.Steps != nil {
				pages.steps = &steps
			}
			entropy := []any{}
			h := SiteVisitHooks{HTTP: pages, Browser: pages, Now: func() time.Time { return oracleLocalTime(c.Input) }, Stats: func(_ context.Context, domain string, ok bool, seconds int) error {
				row := Object{"domain": domain, "success": ok}
				if ok {
					row["seconds"] = seconds
				}
				stats = append(stats, row)
				return nil
			}}
			h.Choose = oracleChoose(c.Input, &entropy)
			oracleOCRHooks(&h, site, c.Input, &steps)
			if c.Adapter == AdapterTjupt {
				h.Tjupt = &TjuptDependencies{Cache: oracleAnswerCache{c.Input, &steps}, Public: oraclePublicPages{pages}}
			}
			adapter, _ := FixedSiteAdapter(c.Adapter)
			if c.Adapter == "MTorrent" {
				adapter = MTorrentAdapter()
				h.MTeam = oracleMTeamTransport{pages}
			}
			visitor := SiteVisitor{Adapters: []SiteAdapter{adapter}, Hooks: h}
			e := fixedEngine(site)
			e.Now = h.Now
			if history, ok := c.Input["history"].(Object); ok {
				e.Data = clone(history).(Object)
			}
			var result any
			var runErr error
			switch c.Kind {
			case "adapter":
				r, err := adapter.Signin(context.Background(), site, h)
				runErr = err
				result = []any{r.State, r.Message}
			case "login-adapter":
				r, err := adapter.Login(context.Background(), site, h)
				runErr = err
				result = []any{r.State, r.Message}
			case "wrapper":
				r, err := visitor.Visit(context.Background(), site, "签到", true)
				runErr = err
				result = normalizedResult(r)
			case "do":
				keys := []string{}
				for k := range e.Data {
					keys = append(keys, k)
				}
				runErr = e.Do(context.Background(), e.Now(), "签到", []any{1}, nil, keys, RunHooks{Visit: visitor.Visit, Emit: func(context.Context, string, Object) error { return nil }, Notify: func(context.Context, Object) error { return nil }})
			}
			if c.Steps != nil && !reflect.DeepEqual(canonical(steps), canonical(c.Steps)) {
				t.Errorf("steps got %v want %v", steps, c.Steps)
			}
			if c.Entropy != nil && !reflect.DeepEqual(canonical(entropy), canonical(c.Entropy)) {
				t.Errorf("RNG calls got %v want %v", entropy, c.Entropy)
			}
			got := Object{"result": result, "calls": pages.calls, "stats": stats, "data": e.Data, "config": e.Config, "trace": e.Trace}
			if c.Error != nil {
				if runErr == nil || runErr.Error() != c.Error["message"] {
					t.Fatalf("error got %v want %v", runErr, c.Error)
				}
				delete(got, "result")
				if !reflect.DeepEqual(canonical(effectiveConstructorCalls(got)), canonical(effectiveConstructorCalls(c.Partial))) {
					t.Errorf("partial got %#v\nwant %#v", canonical(got), canonical(c.Partial))
				}
				return
			}
			if runErr != nil {
				t.Fatal(runErr)
			}
			if !reflect.DeepEqual(canonical(effectiveConstructorCalls(got)), canonical(effectiveConstructorCalls(c.Expected))) {
				t.Errorf("got %#v\nwant %#v", canonical(got), canonical(c.Expected))
			}
		})
	}
}

// This test-only wrapper rewrites a previously authorized, fixed production
// target into a self-created loopback server. It is not present in production
// constructors, source grants or worker-facing parameters.
type rewriteFixedHTTP struct {
	to     string
	target PageTransport
}

func (r rewriteFixedHTTP) Do(ctx context.Context, p PageRequest) (*PageResponse, error) {
	u, _ := url.Parse(p.URL)
	path := u.RequestURI()
	p.URL = r.to + path
	if p.Referer != "" {
		ref, _ := url.Parse(p.Referer)
		p.Referer = r.to + ref.RequestURI()
	}
	if p.Headers != nil {
		headers := clone(p.Headers).(Object)
		for name, value := range headers {
			if name == "Origin" {
				headers[name] = r.to
			}
			if name == "Referer" {
				u, _ := url.Parse(textValue(value))
				headers[name] = r.to + u.RequestURI()
			}
		}
		p.Headers = headers
	}
	return r.target.Do(ctx, p)
}
func TestFixedGrantFullPipelineLoopbackAndNoCredentialEscape(t *testing.T) {
	for _, kind := range []FixedAdapter{AdapterHDArea, AdapterNexusHD, AdapterHDCity} {
		for _, scenario := range []string{"confirmed", "repeat", "expired", "unknown", "hidden", "fallback"} {
			t.Run(string(kind)+"/"+scenario, func(t *testing.T) {
				policy := fixedPolicies[kind]
				sourceURL := "https://www." + policy.SiteURL + "/"
				var mu sync.Mutex
				calls := []Object{}
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					raw, _ := io.ReadAll(r.Body)
					mu.Lock()
					calls = append(calls, Object{"method": r.Method, "path": r.URL.RequestURI(), "body": string(raw)})
					mu.Unlock()
					if r.Header.Get("Cookie") != "sid=synthetic-only" {
						t.Error("host Cookie missing")
					}
					w.Header().Set("Content-Type", "text/html; charset=UTF-8")
					success := htmlData.Regexes[string(kind)+"._success_text"][0]
					repeat := htmlData.Regexes[string(kind)+"._repeat_text"][0]
					page := ""
					if r.URL.Path == "/" {
						if scenario == "fallback" {
							page = `<a href="logout.php">Logout</a>`
						} else {
							page = `<input type="password">`
						}
					} else {
						switch scenario {
						case "confirmed":
							page = success
						case "repeat":
							page = repeat
						case "expired":
							page = "login.php"
						case "unknown", "fallback":
							page = "unknown"
						case "hidden":
							page = "<template>" + success + "</template>"
						}
					}
					io.WriteString(w, page)
				}))
				defer server.Close()
				binding := SiteHTTPBinding{URL: sourceURL, Cookie: "sid=synthetic-only", UserAgent: "synthetic-UA"}
				grant, err := BindFixedAdapter(kind, binding)
				if err != nil {
					t.Fatal(err)
				}
				loopback, _ := BindSiteHTTP(SiteHTTPBinding{URL: server.URL, Cookie: binding.Cookie, UserAgent: binding.UserAgent})
				for origin := range grant.clients {
					grant.clients[origin] = rewriteFixedHTTP{to: server.URL, target: loopback}
				}
				site := Object{"id": 1, "name": "Synthetic", "url": sourceURL, "cookie": "sid=synthetic-only", "ua": "synthetic-UA"}
				adapter, _ := FixedSiteAdapter(kind)
				visitor := SiteVisitor{Adapters: []SiteAdapter{adapter}, Hooks: SiteVisitHooks{HTTP: grant, Now: func() time.Time { return time.Date(2026, 1, 2, 12, 0, 0, 0, time.UTC) }, Stats: func(context.Context, string, bool, int) error { return nil }}}
				e := fixedEngine(site)
				if err = e.Do(context.Background(), e.Now(), "签到", []any{1}, nil, nil, RunHooks{Visit: visitor.Visit, Emit: func(context.Context, string, Object) error { return nil }}); err != nil {
					t.Fatal(err)
				}
				daily := asObject(e.Data["签到-2026-01-02"])
				result := asObject(asObject(daily["results"])["1"])
				confirmed := scenario == "confirmed" || scenario == "repeat"
				logged := confirmed || scenario == "fallback"
				if result["success"] != confirmed || result["logged_in"] != logged {
					t.Fatal("pipeline status/history disagreement", result)
				}
				retries := asList(daily["retry"])
				if len(retries) == 0 && (!logged) || len(retries) > 0 && logged {
					t.Fatal("retry disagreement", retries, result)
				}
				mu.Lock()
				got := append([]Object{}, calls...)
				mu.Unlock()
				expectedCalls := 1
				if !confirmed {
					expectedCalls++
				}
				if len(got) != expectedCalls {
					t.Fatal("fallback call count", got)
				}
				first := got[0]
				if first["method"] != policy.Routes[0].Method {
					t.Fatal("wrong source HTTP method", first)
				}
				wantBody := ""
				if kind == AdapterHDArea {
					wantBody = "action=sign_in"
				}
				if kind == AdapterNexusHD {
					wantBody = "action=post&content="
				}
				if first["body"] != wantBody {
					t.Fatal("wrong source form", first)
				}
				before := len(got)
				for _, request := range []PageRequest{{Method: "GET", URL: "https://evil.invalid/"}, {Method: "POST", URL: policy.Routes[0].URL + "?invented=1"}, {Method: "GET", URL: "http://" + policy.SiteURL + "/private"}} {
					if _, err = grant.Do(context.Background(), request); !errors.Is(err, ErrSiteTargetDenied) {
						t.Fatal("uncompiled target granted", request, err)
					}
				}
				mu.Lock()
				if len(calls) != before {
					t.Error("unapproved URL reached fixture")
				}
				mu.Unlock()
				for _, unapproved := range []string{"https://" + policy.SiteURL + ".evil.invalid/", "https://www.www." + policy.SiteURL + "/", "https://user:synthetic@" + policy.SiteURL + "/", "https://" + policy.SiteURL + ":443/"} {
					binding.URL = unapproved
					if _, err := BindFixedAdapter(kind, binding); !errors.Is(err, ErrSiteTargetDenied) {
						t.Fatal("source match was mistaken for authority", unapproved, err)
					}
				}
			})
		}
	}
}
func TestFixedGrantTLSStillStrictForApexFamily(t *testing.T) {
	calls := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "text/html; charset=UTF-8")
		io.WriteString(w, "此次签到您获得10")
	}))
	defer server.Close()
	grant, _ := BindFixedAdapter(AdapterHDArea, SiteHTTPBinding{URL: "https://www.hdarea.club/", Cookie: "sid=synthetic-only"})
	bound, _ := BindSiteHTTP(SiteHTTPBinding{URL: server.URL, Cookie: "sid=synthetic-only"})
	for origin := range grant.clients {
		grant.clients[origin] = rewriteFixedHTTP{to: server.URL, target: bound}
	}
	route := fixedPolicies[AdapterHDArea].Routes[0]
	response, err := grant.Do(context.Background(), PageRequest{Method: route.Method, URL: route.URL, Form: map[string]string{"action": "sign_in"}})
	if err != nil || response != nil || calls != 0 {
		t.Fatal("dedicated grant bypassed strict TLS", response, err, calls)
	}
	roots := x509.NewCertPool()
	roots.AddCert(server.Certificate())
	bound.testTLS = &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}
	response, err = grant.Do(context.Background(), PageRequest{Method: route.Method, URL: route.URL, Form: map[string]string{"action": "sign_in"}})
	if err != nil || response == nil || calls != 1 {
		t.Fatal("owned fixture root failed", response, err, calls)
	}
}

func TestInstalledAdapterDoReferencesThroughRealBoundLoopbackHTTP(t *testing.T) {
	executed := 0
	for _, file := range []string{"fixed-reference.json", "simple-reference.json", "json-reference.json", "dynamic-reference.json", "quiz-reference.json", "ocr-reference.json", "tjupt-reference.json"} {
		raw, err := readAutoSignInFixture(file)
		if err != nil {
			t.Fatal(err)
		}
		var refs struct {
			Cases []struct {
				Name, Kind             string
				Adapter                FixedAdapter
				Input, Expected, Error Object
				NetworkEvidence        []Object `json:"network_evidence"`
			}
		}
		if err = Decode(raw, &refs); err != nil {
			t.Fatal(err)
		}
		for _, c := range refs.Cases {
			if c.Kind != "do" || Truth(asObject(c.Input["site"])["render"]) || c.Error != nil {
				continue
			}
			responses := asList(clone(c.Input["responses"]))
			syntheticError := false
			for _, res := range responses {
				if Truth(asObject(res)["error"]) {
					syntheticError = true
				}
			}
			if syntheticError {
				continue
			}
			executed++
			t.Run(c.Name, func(t *testing.T) {
				policy := fixedPolicies[c.Adapter]
				site := Object{"id": 1, "name": "Synthetic", "url": "https://" + policy.SiteURL + "/", "cookie": "synthetic-cookie=not-a-credential", "ua": "synthetic-agent"}
				for k, v := range asObject(c.Input["site"]) {
					site[k] = v
				}
				expectedCalls := asList(c.Expected["calls"])
				var mu sync.Mutex
				next := 0
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					mu.Lock()
					index := next
					next++
					var raw any
					if index < len(responses) {
						raw = responses[index]
					}
					mu.Unlock()
					if index >= len(expectedCalls) {
						t.Errorf("extra HTTP request %s %s", r.Method, r.URL.RequestURI())
					} else {
						want := expectedCalls[index].(Object)
						expectedURL := textValue(want["url"])
						if index < len(c.NetworkEvidence) && c.NetworkEvidence[index]["url"] != nil {
							expectedURL = textValue(c.NetworkEvidence[index]["url"])
						}
						u, _ := url.Parse(expectedURL)
						if r.Method != want["method"] || r.URL.RequestURI() != u.RequestURI() {
							t.Errorf("request got %s %s want %s %s", r.Method, r.URL.RequestURI(), want["method"], u.RequestURI())
						}
						wantCookie := textValue(want["cookies"])
						if headers, ok := want["headers"].(Object); ok {
							if value, exists := headers["Cookie"]; exists {
								wantCookie = textValue(value)
							}
							for name, value := range headers {
								expectedHeader := textValue(value)
								if name == "Origin" {
									expectedHeader = "http://" + r.Host
								}
								if name == "Referer" {
									u, _ := url.Parse(expectedHeader)
									expectedHeader = "http://" + r.Host + u.RequestURI()
								}
								if name != "Cookie" && value != nil && r.Header.Get(name) != expectedHeader {
									t.Errorf("source header %s differs", name)
								}
							}
						}
						if index < len(c.NetworkEvidence) {
							wantCookie = textValue(c.NetworkEvidence[index]["cookie"])
						}
						if r.Header.Get("Cookie") != wantCookie {
							t.Error("bound credentials differ")
						}
						if index < len(c.NetworkEvidence) {
							body, _ := io.ReadAll(r.Body)
							wantBody, _ := hex.DecodeString(textValue(c.NetworkEvidence[index]["body_hex"]))
							if string(body) != string(wantBody) {
								t.Errorf("wire body got %q want %q", body, wantBody)
							}
							r.Body = io.NopCloser(strings.NewReader(string(body)))
						}
						if want["json"] != nil {
							body, _ := io.ReadAll(r.Body)
							var gotJSON any
							if err := Decode(body, &gotJSON); err != nil {
								t.Error(err)
							}
							if !reflect.DeepEqual(canonical(gotJSON), canonical(want["json"])) {
								t.Errorf("JSON body differs")
							}
						}
						if want["data"] != nil {
							r.ParseForm()
							form := Object{}
							for k, v := range r.PostForm {
								if len(v) == 1 {
									form[k] = v[0]
								}
							}
							if !reflect.DeepEqual(canonical(form), canonical(want["data"])) {
								t.Errorf("body got %v want %v", form, want["data"])
							}
						}
					}
					if raw == nil {
						conn, _, err := w.(http.Hijacker).Hijack()
						if err == nil {
							conn.Close()
						}
						return
					}
					row := raw.(Object)
					w.Header().Set("Content-Type", "text/html; charset=UTF-8")
					for _, pair := range asList(row["cookies"]) {
						fields := asList(pair)
						http.SetCookie(w, &http.Cookie{Name: textValue(fields[0]), Value: textValue(fields[1])})
					}
					status := 200
					if row["status"] != nil {
						n, _ := sourceInteger(row["status"])
						status = int(n.Int64())
					}
					w.WriteHeader(status)
					if raw, ok := row["raw_hex"].(string); ok {
						data, _ := hex.DecodeString(raw)
						w.Write(data)
					} else {
						io.WriteString(w, textValue(row["text"]))
					}
				}))
				defer server.Close()
				grant, err := BindFixedAdapter(c.Adapter, SiteHTTPBinding{URL: textValue(site["url"]), Cookie: textValue(site["cookie"]), UserAgent: site["ua"]})
				if err != nil {
					t.Fatal(err)
				}
				bound, _ := BindSiteHTTP(SiteHTTPBinding{URL: server.URL, Cookie: textValue(site["cookie"]), UserAgent: site["ua"]})
				for origin := range grant.clients {
					grant.clients[origin] = rewriteFixedHTTP{to: server.URL, target: bound}
				}
				stats := []any{}
				a, _ := FixedSiteAdapter(c.Adapter)
				visitor := SiteVisitor{Adapters: []SiteAdapter{a}, Hooks: SiteVisitHooks{HTTP: grant, Now: func() time.Time { return oracleLocalTime(c.Input) }, Stats: func(_ context.Context, domain string, ok bool, seconds int) error {
					row := Object{"domain": domain, "success": ok}
					if ok {
						row["seconds"] = seconds
					}
					stats = append(stats, row)
					return nil
				}}}
				entropy, steps := []any{}, []any{}
				visitor.Hooks.Choose = oracleChoose(c.Input, &entropy)
				oracleOCRHooks(&visitor.Hooks, site, c.Input, &steps)
				if c.Adapter == AdapterTjupt {
					public, _ := BindTjuptPublicHTTP([]string{"https://img.doubanio.com"})
					publicBound, _ := BindSiteHTTP(SiteHTTPBinding{URL: server.URL})
					for origin := range public.clients {
						public.clients[origin] = rewriteFixedHTTP{to: server.URL, target: publicBound}
					}
					visitor.Hooks.Tjupt = &TjuptDependencies{Cache: oracleAnswerCache{c.Input, &steps}, Public: public}
				}
				e := fixedEngine(site)
				e.Now = visitor.Hooks.Now
				if history, ok := c.Input["history"].(Object); ok {
					e.Data = clone(history).(Object)
				}
				keys := []string{}
				for k := range e.Data {
					keys = append(keys, k)
				}
				if err = e.Do(context.Background(), e.Now(), "签到", []any{1}, nil, keys, RunHooks{Visit: visitor.Visit, Emit: func(context.Context, string, Object) error { return nil }, Notify: func(context.Context, Object) error { return nil }}); err != nil {
					t.Fatal(err)
				}
				got := Object{"data": e.Data, "config": e.Config, "trace": e.Trace, "stats": stats}
				want := Object{"data": c.Expected["data"], "config": c.Expected["config"], "trace": c.Expected["trace"], "stats": c.Expected["stats"]}
				if !reflect.DeepEqual(canonical(got), canonical(want)) {
					t.Errorf("complete HTTP workflow got %#v\nwant %#v", canonical(got), canonical(want))
				}
				mu.Lock()
				if next != len(expectedCalls) {
					t.Errorf("HTTP call count got %d want %d", next, len(expectedCalls))
				}
				mu.Unlock()
			})
		}
	}
	if executed != 327 {
		t.Fatalf("real HTTP workflow coverage changed: %d", executed)
	}
}

// Raw original calls stay in the fixture. RequestUtils.__init__ declares
// timeout=None, so omission and explicit None have the same bound parameter.
// Compare that effective parameter rather than inventing a call difference.
func effectiveConstructorCalls(value Object) Object {
	out := clone(value).(Object)
	for _, raw := range asList(out["calls"]) {
		call := raw.(Object)
		if call["method"] != "BROWSER" {
			// The installed constructor ignores its separate ua argument when
			// nonempty headers are supplied; headers[User-Agent] is decisive.
			if Truth(call["headers"]) {
				call["ua"] = nil
			}
			// Explicit constructor defaults and omission produce identical headers.
			if call["content_type"] == "application/x-www-form-urlencoded; charset=UTF-8" {
				delete(call, "content_type")
			}
			if _, exists := call["proxies"]; !exists {
				call["proxies"] = nil
			}
			if _, exists := call["timeout"]; !exists {
				call["timeout"] = nil
			}
		}
	}
	return out
}

func oracleLocalTime(input Object) time.Time {
	if raw, ok := input["now"].(string); ok {
		v, err := time.Parse("2006-01-02T15:04:05", raw)
		if err != nil {
			panic(err)
		}
		return v
	}
	return time.Date(2026, 1, 2, 12, 0, 0, 0, time.UTC)
}
func oracleChoose(input Object, trace *[]any) func(int, int) (int, error) {
	return func(minimum, maximum int) (int, error) {
		row := Object{"minimum": minimum, "maximum": maximum}
		*trace = append(*trace, row)
		if maximum < minimum {
			return defaultChoose(minimum, maximum)
		}
		value := 0
		if raw := input["choice"]; raw != nil {
			n, err := sourceInteger(raw)
			if err != nil {
				return 0, err
			}
			value = int(n.Int64())
		}
		row["value"] = value
		return value, nil
	}
}

func oracleOCRHooks(h *SiteVisitHooks, site, input Object, steps *[]any) {
	results := asList(clone(input["ocr"]))
	h.OCR = func(_ context.Context, address string) (any, error) {
		*steps = append(*steps, Object{"op": "ocr", "image_url": address, "cookie": site["cookie"], "ua": site["ua"]})
		if len(results) == 0 {
			return "", nil
		}
		result := results[0]
		results = results[1:]
		if row, ok := result.(Object); ok && row["error"] != nil {
			return nil, errors.New(textValue(row["error"]))
		}
		return result, nil
	}
	h.Sleep = func(_ context.Context, d time.Duration) error {
		*steps = append(*steps, Object{"op": "sleep", "seconds": int(d / time.Second)})
		return nil
	}
}
