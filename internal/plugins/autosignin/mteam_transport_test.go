package autosignin

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
	"time"
)

type oracleMTeamTransport struct{ pages *fixedPages }

func (o oracleMTeamTransport) Post(ctx context.Context, plan RequestPlan) (*AdapterResponse, error) {
	headers := Object{}
	for k, v := range plan.Headers {
		headers[k] = v
	}
	response, err := o.pages.Do(ctx, PageRequest{Method: "POST", URL: plan.URL, Headers: headers, NoCookies: true, Form: plan.Data, Timeout: plan.Timeout})
	if err != nil || response == nil {
		return nil, err
	}
	out := &AdapterResponse{Status: response.Status}
	out.Payload, out.JSONError = parseSiteJSON(response.Text)
	return out, nil
}
func TestActualInstalledMTorrentWholeFlow(t *testing.T) {
	checkInstalledAdapterReference(t, "mteam-flow-reference.json", 51)
}
func TestMTeamDedicatedGrantRealHTTPAndTamperDenial(t *testing.T) {
	credentials := Credentials{URL: "https://kp.m-team.cc/", Token: " synthetic-login-token ", UserAgent: "synthetic-agent", Timeout: 17}
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Method != "POST" || r.URL.Path != mteamProtocol.Path || r.Header.Get("Authorization") != "synthetic-login-token" || r.Header.Get("Cookie") != "" || r.Header.Get("Origin") != "https://kp.m-team.cc" || r.Header.Get("Referer") != "https://kp.m-team.cc/index" {
			t.Error("MTeam dedicated source wire differs")
		}
		r.ParseForm()
		stamp, _ := time.Parse(time.RFC3339, "2026-01-02T12:00:00Z")
		if r.PostForm.Get("_timestamp") != "1767355200000" || r.PostForm.Get("_sgin") != MTeamSign(stamp.UnixMilli()) {
			t.Error("source form/signature differs")
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"code":"1","message":"FAIL"}`)
	}))
	defer server.Close()
	grant, err := BindMTeam(credentials, nil)
	if err != nil {
		t.Fatal(err)
	}
	bound, _ := BindSiteHTTP(SiteHTTPBinding{URL: server.URL})
	bound.credentialHeaderOrigin = "https://kp.m-team.cc:443"
	// The private fixture forwards only an already validated plan to its owned
	// server, preserving source frontend Origin/Referer (not Generic grants).
	grant.api = rewriteMTeamHTTP{server.URL, bound}
	plan, _ := MTeamPlan(credentials, 1767355200000)
	// Validation target remains compiled. Rewrite at a private roundtrip seam is
	// tested separately below rather than accepting a caller-provided plan URL.
	grant.target = plan.URL
	state, msg, err := MTeamVisit(context.Background(), credentials, 1767355200000, grant)
	if err != nil || !state || msg != "模拟登录成功" || requests.Load() != 1 {
		t.Fatal(state, msg, err, requests.Load())
	}
	altered := plan
	altered.URL = "https://evil.invalid" + mteamProtocol.Path
	if _, err := grant.Post(context.Background(), altered); !errors.Is(err, ErrSiteTargetDenied) {
		t.Fatal("forged API allowed", err)
	}
	altered = plan
	altered.Headers = map[string]string{}
	for k, v := range plan.Headers {
		altered.Headers[k] = v
	}
	altered.Headers["Authorization"] = "synthetic-other"
	if _, err := grant.Post(context.Background(), altered); !errors.Is(err, ErrSiteTargetDenied) {
		t.Fatal("forged token allowed", err)
	}
	for _, address := range []string{"https://kp.m-team.cc.evil.invalid/", "https://evil.m-team.cc/", "http://kp.m-team.cc/", "https://user@kp.m-team.cc/", "https://kp.m-team.cc:443/"} {
		credentials.URL = address
		if _, err := BindMTeam(credentials, nil); !errors.Is(err, ErrSiteTargetDenied) {
			t.Fatal("unapproved frontend", err)
		}
	}
	if requests.Load() != 1 {
		t.Fatal("denied plan sent network")
	}
}
func TestInstalledRegistryPTZoneAndMTeamMatchSemantics(t *testing.T) {
	all := InstalledAdapterRegistry()
	if len(all) != 22 {
		t.Fatal(len(all))
	}
	for _, address := range []string{"https://ptzone.xyz:443/sub/", "https://www.ptzone.xyz/attendance.php"} {
		yes, err := all[15].Match(address)
		if err != nil || !yes {
			t.Fatal("source PTZone hostname semantics", address, err)
		}
	}
	for _, address := range []string{"ptzone.xyz", "https://evil.ptzone.xyz/"} {
		yes, _ := all[15].Match(address)
		if yes {
			t.Fatal("broadened PTZone", address)
		}
	}
	if !all[10].SigninIsLogin {
		t.Fatal("MTeam lost no-double-fallback marker")
	}
}

type rewriteMTeamHTTP struct {
	to     string
	target PageTransport
}

func (r rewriteMTeamHTTP) Do(ctx context.Context, p PageRequest) (*PageResponse, error) {
	p.URL = r.to + mteamProtocol.Path
	return r.target.Do(ctx, p)
}

func TestMTeamTrustedProxyStillHonorsSourceSiteFlag(t *testing.T) {
	proxy, _ := url.Parse("http://127.0.0.1:1")
	for _, flag := range []any{false, nil, true} {
		grant, err := BindMTeam(Credentials{URL: "https://kp.m-team.cc/", Token: "synthetic-token", Proxy: flag}, proxy)
		if err != nil {
			t.Fatal(err)
		}
		bound := grant.api.(*BoundSiteHTTP)
		if (bound.binding.ProxyURL != nil) != Truth(flag) {
			t.Fatal("trusted global proxy bypassed source site flag", flag)
		}
	}
}

func TestMTeamPublicBindingHasNoHostGuard(t *testing.T) {
	grant, err := BindMTeam(Credentials{URL: "https://kp.m-team.cc/", Token: "synthetic-token"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if grant.api.(*BoundSiteHTTP).binding.BeforeRequest != nil {
		t.Fatal("public binding unexpectedly installed a host guard")
	}
}
