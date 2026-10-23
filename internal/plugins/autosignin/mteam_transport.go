package autosignin

import (
	"context"
	"crypto/x509"
	"net/url"
	"reflect"
	"strconv"
	"time"
)

// BoundMTeam has a compiled frontend/API pair and host-owned credentials.
// A source matcher or worker supplied hostname never authorizes this bridge.
type BoundMTeam struct {
	credentials Credentials
	api         PageTransport
	target      string
}

func BindMTeam(credentials Credentials, proxy *url.URL) (*BoundMTeam, error) {
	return bindMTeam(credentials, proxy, nil, nil, nil)
}

func bindMTeam(credentials Credentials, proxy *url.URL, policy *RequestsProxyPolicy, beforeRequest func(context.Context) error, rootCAs *x509.CertPool) (*BoundMTeam, error) {
	if policy != nil && proxy != nil {
		return nil, ErrSiteTargetDenied
	}
	policy = cloneRequestsProxyPolicy(policy)
	if !Truth(credentials.Proxy) {
		proxy = nil
		if policy != nil {
			policy.Explicit = nil
		}
	}
	u, err := url.Parse(credentials.URL)
	if err != nil || u == nil || u.User != nil || u.Scheme != "https" || u.Port() != "" {
		return nil, ErrSiteTargetDenied
	}
	host := u.Host
	family := ""
	for _, domain := range []string{"m-team.cc", "m-team.io"} {
		for _, prefix := range []string{"kp.", "tp.", ""} {
			if host == prefix+domain {
				family = domain
			}
		}
	}
	if family == "" {
		return nil, ErrSiteTargetDenied
	}
	target := "https://api." + family + mteamProtocol.Path
	plan, ok := MTeamPlan(credentials, 0)
	if ok && plan.URL != target {
		return nil, ErrSiteTargetDenied
	}
	api, err := BindSiteHTTP(SiteHTTPBinding{URL: target, ProxyURL: proxy, RequestsProxy: policy, UserAgent: credentials.UserAgent, BeforeRequest: beforeRequest, RootCAs: rootCAs})
	if err != nil {
		return nil, err
	}
	origin := originOf(u)
	api.credentialHeaderOrigin = origin
	return &BoundMTeam{credentials, api, target}, nil
}
func (b *BoundMTeam) Post(ctx context.Context, plan RequestPlan) (*AdapterResponse, error) {
	if b == nil {
		return nil, ErrAdapterNotReady
	}
	stamp, err := strconv.ParseInt(plan.Data["_timestamp"], 10, 64)
	if err != nil {
		return nil, ErrSiteTargetDenied
	}
	expected, ok := MTeamPlan(b.credentials, stamp)
	if !ok || plan.URL != b.target || !reflect.DeepEqual(plan, expected) {
		return nil, ErrSiteTargetDenied
	}
	headers := Object{}
	for k, v := range plan.Headers {
		headers[k] = v
	}
	response, err := b.api.Do(ctx, PageRequest{Method: "POST", URL: b.target, Headers: headers, NoCookies: true, Form: plan.Data, FormOrder: []string{"_timestamp", "_sgin"}, Timeout: plan.Timeout})
	if err != nil {
		return nil, err
	}
	if response == nil {
		return nil, nil
	}
	out := &AdapterResponse{Status: response.Status}
	if response.Status == 200 {
		out.Payload, out.JSONError = parseSiteJSON(response.Text)
	}
	return out, nil
}
func MTorrentAdapter() SiteAdapter {
	visit := func(ctx context.Context, site Object, h SiteVisitHooks) (AdapterResult, error) {
		token := ""
		if Truth(site["token"]) {
			token = pythonScalar(site["token"])
		}
		address := ""
		if Truth(site["url"]) {
			address = pythonScalar(site["url"])
		}
		credentials := Credentials{URL: address, Token: token, UserAgent: textValue(site["ua"]), Proxy: site["proxy"], Timeout: site["timeout"]}
		now := time.Now
		if h.Now != nil {
			now = h.Now
		}
		state, message, err := MTeamVisit(ctx, credentials, now().UnixMilli(), h.MTeam)
		return AdapterResult{state, message}, err
	}
	return SiteAdapter{Match: func(address string) (bool, error) { return MTeamMatch(address), nil }, Signin: visit, Login: visit, SigninIsLogin: true}
}

// Installed loader order, not map iteration. It remains disabled when the
// original schema-loaded state is false, as enforced by SiteVisitor.Visit.
func InstalledAdapterRegistry() []SiteAdapter {
	out := []SiteAdapter{}
	for _, kind := range []FixedAdapter{AdapterPt52, AdapterBTSchool, AdapterCHDBits, AdapterHaiDan, AdapterHares, AdapterHDArea, AdapterHDChina, AdapterHDCity, AdapterHDSky, AdapterHDUpt} {
		a, _ := FixedSiteAdapter(kind)
		out = append(out, a)
	}
	out = append(out, MTorrentAdapter())
	for _, kind := range []FixedAdapter{AdapterNexusHD, AdapterOpencd, AdapterPTerClub, AdapterPTTime} {
		a, _ := FixedSiteAdapter(kind)
		out = append(out, a)
	}
	out = append(out, SiteAdapter{Match: PTZoneMatch, Signin: PTZoneSignin})
	for _, kind := range []FixedAdapter{AdapterRousiPro, AdapterTjupt, AdapterTTG, AdapterU2, AdapterYemaPT, AdapterZhuQue} {
		a, _ := FixedSiteAdapter(kind)
		out = append(out, a)
	}
	return out
}
