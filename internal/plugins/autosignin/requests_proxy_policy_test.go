package autosignin

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"light-media/internal/networkproxy"
)

func TestRequestsProxyPolicyLoopbackBusinessRedirectAndRevocation(t *testing.T) {
	var originHits, proxyHits atomic.Int32
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		originHits.Add(1)
		if r.Header.Get("Proxy-Authorization") != "" {
			t.Error("proxy credentials reached origin")
		}
		if r.Header.Get("Cookie") != "sid=synthetic" {
			t.Error("site cookie missing")
		}
		if r.URL.Path == "/start" || r.URL.Path == "/attendance.php" {
			http.Redirect(w, r, "/finish", http.StatusFound)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		io.WriteString(w, `<a href="logout.php">退出登录</a><div>签到成功</div>`)
	}))
	defer origin.Close()
	forward := &http.Transport{Proxy: nil}
	defer forward.CloseIdleConnections()
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		proxyHits.Add(1)
		if !r.URL.IsAbs() {
			t.Error("not a proxy request")
		}
		if r.Header.Get("Proxy-Authorization") != "Basic c3ludGhldGljOnNlY3JldA==" {
			t.Error("synthetic proxy auth missing")
		}
		upstream := r.Clone(r.Context())
		upstream.RequestURI = ""
		upstream.Header.Del("Proxy-Authorization")
		response, err := forward.RoundTrip(upstream)
		if err != nil {
			t.Error("loopback forward failed")
			http.Error(w, "failed", 502)
			return
		}
		defer response.Body.Close()
		for key, values := range response.Header {
			w.Header()[key] = values
		}
		w.WriteHeader(response.StatusCode)
		io.Copy(w, response.Body)
	}))
	defer proxy.Close()
	proxyURL, _ := url.Parse(proxy.URL)
	proxyURL.User = url.UserPassword("synthetic", "secret")
	policy := &RequestsProxyPolicy{Environment: []string{"http_proxy=" + proxyURL.String()}, Explicit: map[string]string{"http": "http://unused.invalid:1"}}
	checks := 0
	revoke := false
	denied := errors.New("synthetic revoked grant")
	hooks, err := BindRegistrationHooks(Registration{Site: Object{"url": origin.URL, "cookie": "sid=synthetic", "proxy": false}, RequestsProxy: policy, BeforeRequest: func(context.Context) error {
		checks++
		if revoke && checks%2 == 0 {
			return denied
		}
		return nil
	}}, false)
	if err != nil {
		t.Fatal(err)
	}
	bound := hooks.HTTP.(*BoundSiteHTTP)
	// Registration proxy=false preserves trust_env while dropping only explicit config.
	for i := 0; i < 2; i++ {
		response, err := bound.Do(context.Background(), PageRequest{Method: "GET", URL: origin.URL + "/start"})
		if err != nil || response == nil || !strings.Contains(response.Text, "签到成功") {
			t.Fatalf("business result absent: %#v %v", response, err)
		}
	}
	if proxyHits.Load() != 4 || originHits.Load() != 4 || checks != 4 {
		t.Fatal("redirect/request route count differs")
	}
	revoke = true
	response, err := bound.Do(context.Background(), PageRequest{Method: "GET", URL: origin.URL + "/start"})
	if response != nil || !errors.Is(err, denied) || proxyHits.Load() != 5 || originHits.Load() != 5 || checks != 6 {
		t.Fatal("revoked redirect reached proxy or denial lost", err)
	}
	revoke = false
	business := GenericSignin(context.Background(), Object{"url": origin.URL, "cookie": "sid=synthetic", "name": "synthetic"}, hooks)
	if !business.Success || !business.LoggedIn || proxyHits.Load() != 7 || originHits.Load() != 7 {
		t.Fatal("proxied business evidence did not establish successful sign-in", business)
	}
}

func TestRequestsProxyPolicyNoProxyAndFreshChains(t *testing.T) {
	var direct, proxied atomic.Int32
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { direct.Add(1); io.WriteString(w, "direct") }))
	defer origin.Close()
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { proxied.Add(1); io.WriteString(w, "proxy") }))
	defer proxy.Close()
	policy := &RequestsProxyPolicy{Environment: []string{"http_proxy=" + proxy.URL, "no_proxy=127.0.0.1"}}
	b, err := BindSiteHTTP(SiteHTTPBinding{URL: origin.URL, RequestsProxy: policy})
	if err != nil {
		t.Fatal(err)
	}
	response, err := b.Do(context.Background(), PageRequest{Method: "GET", URL: origin.URL})
	if err != nil || response == nil || response.Text != "direct" || direct.Load() != 1 || proxied.Load() != 0 {
		t.Fatal("NO_PROXY did not bypass env", err)
	}
	policy.Explicit = map[string]string{"http": proxy.URL}
	b, err = BindSiteHTTP(SiteHTTPBinding{URL: origin.URL, RequestsProxy: policy})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		response, err = b.Do(context.Background(), PageRequest{Method: "GET", URL: origin.URL})
		if err != nil || response == nil || response.Text != "proxy" {
			t.Fatal("explicit route incorrectly bypassed", err)
		}
	}
	if direct.Load() != 1 || proxied.Load() != 2 {
		t.Fatal("wrong routing count")
	}
}

func TestRequestsProxyPolicyFrozenHostOnlyAndAmbiguity(t *testing.T) {
	policy := &RequestsProxyPolicy{Environment: []string{"http_proxy=http://env.invalid"}, Explicit: map[string]string{"http": "http://explicit.invalid"}}
	registered := NewRegisteredSites([]Registration{{Site: Object{"url": "https://example.invalid"}, RequestsProxy: policy}})
	bound, err := BindSiteHTTP(SiteHTTPBinding{URL: "https://example.invalid", RequestsProxy: policy})
	if err != nil {
		t.Fatal(err)
	}
	policy.Environment[0] = "changed"
	policy.Explicit["http"] = "changed"
	for _, snapshot := range []*RequestsProxyPolicy{registered.rows[0].RequestsProxy, bound.binding.RequestsProxy} {
		if snapshot.Environment[0] != "http_proxy=http://env.invalid" || snapshot.Explicit["http"] != "http://explicit.invalid" {
			t.Fatal("mutable policy escaped freeze")
		}
	}
	for _, value := range []any{Registration{}, SiteHTTPBinding{}, RequestsProxyPolicy{}} {
		typ := reflect.TypeOf(value)
		fields := []string{"RequestsProxy"}
		if typ == reflect.TypeOf(RequestsProxyPolicy{}) {
			fields = []string{"Environment", "Explicit"}
		}
		for _, name := range fields {
			field, ok := typ.FieldByName(name)
			if !ok || field.Tag.Get("json") != "-" {
				t.Fatal("host policy serialization allowed", name)
			}
		}
	}
	raw, err := json.Marshal(SiteHTTPBinding{URL: "https://example.invalid", RequestsProxy: policy})
	if err != nil || strings.Contains(string(raw), "changed") {
		t.Fatal("host policy serialized")
	}
	legacy, _ := url.Parse("http://legacy.invalid")
	if _, err = BindSiteHTTP(SiteHTTPBinding{URL: "https://example.invalid", ProxyURL: legacy, RequestsProxy: policy}); !errors.Is(err, ErrSiteTargetDenied) {
		t.Fatal("ambiguous binding accepted")
	}
	for _, enabled := range []bool{false, true} {
		if _, err = BindRegistrationHooks(Registration{Site: Object{"url": "https://example.invalid", "proxy": enabled}, ProxyURL: legacy, RequestsProxy: policy}, false); !errors.Is(err, ErrSiteTargetDenied) {
			t.Fatal("ambiguous registration accepted")
		}
	}
}

func TestRequestsProxyPolicyTypedAuthErrorAndGuardPrecedence(t *testing.T) {
	var hits atomic.Int32
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1) }))
	defer proxy.Close()
	u, _ := url.Parse(proxy.URL)
	u.User = url.UserPassword("合成", "synthetic")
	binding := SiteHTTPBinding{URL: "http://example.invalid", RequestsProxy: &RequestsProxyPolicy{Explicit: map[string]string{"http": u.String()}}}
	b, err := BindSiteHTTP(binding)
	if err != nil {
		t.Fatal(err)
	}
	response, err := b.Do(context.Background(), PageRequest{Method: "GET", URL: binding.URL})
	if response != nil || !errors.Is(err, networkproxy.ErrRequestsProxyAuthEncoding) || hits.Load() != 0 {
		t.Fatal("typed auth error normalized or network attempted", err)
	}
	denied := errors.New("synthetic denied")
	binding.BeforeRequest = func(context.Context) error { return denied }
	b, err = BindSiteHTTP(binding)
	if err != nil {
		t.Fatal(err)
	}
	response, err = b.Do(context.Background(), PageRequest{Method: "GET", URL: binding.URL})
	if response != nil || !errors.Is(err, denied) || hits.Load() != 0 {
		t.Fatal("host guard not outermost", err)
	}
}

func TestRequestsProxyPolicyFixedAndMTeamPropagation(t *testing.T) {
	policy := &RequestsProxyPolicy{Environment: []string{"https_proxy=http://env.invalid"}, Explicit: map[string]string{"https": "http://explicit.invalid"}}
	for _, enabled := range []bool{false, true} {
		for _, address := range []string{"https://pt.btschool.club/", "https://kp.m-team.cc/"} {
			hooks, err := BindRegistrationHooks(Registration{Site: Object{"url": address, "proxy": enabled, "token": "synthetic"}, RequestsProxy: policy}, true)
			if err != nil {
				t.Fatal(err)
			}
			check := func(b *BoundSiteHTTP) {
				t.Helper()
				p := b.binding.RequestsProxy
				if p == nil || !reflect.DeepEqual(p.Environment, policy.Environment) {
					t.Fatal("environment lost")
				}
				if enabled && !reflect.DeepEqual(p.Explicit, policy.Explicit) {
					t.Fatal("explicit lost")
				}
				if !enabled && len(p.Explicit) != 0 {
					t.Fatal("disabled explicit retained")
				}
				p.Environment[0] = "mutated"
				if policy.Environment[0] == "mutated" {
					t.Fatal("alias retained")
				}
			}
			if fixed, ok := hooks.HTTP.(*FixedSiteGrant); ok {
				for _, client := range fixed.clients {
					check(client.(*BoundSiteHTTP))
				}
			}
			if mteam, ok := hooks.MTeam.(*BoundMTeam); ok {
				check(mteam.api.(*BoundSiteHTTP))
				check(hooks.HTTP.(*BoundSiteHTTP))
			}
		}
	}
	legacy, err := BindMTeam(Credentials{URL: "https://kp.m-team.cc/"}, nil)
	if err != nil || legacy.api.(*BoundSiteHTTP).binding.RequestsProxy != nil {
		t.Fatal("legacy MTeam semantics changed", err)
	}
}

func TestRequestsProxyPolicyCrossOriginAndCanceledContext(t *testing.T) {
	var hits atomic.Int32
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		http.Redirect(w, r, "http://other.invalid/secret", http.StatusFound)
	}))
	defer proxy.Close()
	b, err := BindSiteHTTP(SiteHTTPBinding{URL: "http://synthetic.invalid", Cookie: "sid=synthetic", RequestsProxy: &RequestsProxyPolicy{Environment: []string{"http_proxy=" + proxy.URL}}})
	if err != nil {
		t.Fatal(err)
	}
	response, err := b.Do(context.Background(), PageRequest{Method: "GET", URL: "http://synthetic.invalid/start"})
	if response != nil || !errors.Is(err, ErrSiteTargetDenied) || hits.Load() != 1 {
		t.Fatal("cross origin redirected through proxy", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	response, err = b.Do(ctx, PageRequest{Method: "GET", URL: "http://synthetic.invalid/start"})
	if response != nil || !errors.Is(err, context.Canceled) || hits.Load() != 1 {
		t.Fatal("canceled context normalized or sent", err)
	}
}
