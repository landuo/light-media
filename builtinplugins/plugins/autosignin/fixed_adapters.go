package autosignin

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"unicode/utf8"
)

type FixedAdapter string

const (
	AdapterHDArea  FixedAdapter = "HDArea"
	AdapterNexusHD FixedAdapter = "NexusHD"
	AdapterHDCity  FixedAdapter = "HDCity"
)

type fixedRoute struct{ Method, URL string }
type fixedPolicy struct {
	SiteURL         string
	RegisteredHosts []string
	Routes          []fixedRoute
}

var fixedPolicies = map[FixedAdapter]fixedPolicy{
	AdapterHDArea:  {SiteURL: "hdarea.club", RegisteredHosts: []string{"hdarea.club", "www.hdarea.club"}, Routes: []fixedRoute{{"POST", "https://hdarea.club/sign_in.php"}}},
	AdapterNexusHD: {SiteURL: "v6.nexushd.org", RegisteredHosts: []string{"v6.nexushd.org", "www.v6.nexushd.org"}, Routes: []fixedRoute{{"POST", "https://v6.nexushd.org/signin.php"}}},
	AdapterHDCity:  {SiteURL: "hdcity.city", RegisteredHosts: []string{"hdcity.city", "www.hdcity.city"}, Routes: []fixedRoute{{"GET", "https://hdcity.city/sign"}}},
}

func SourceURLEqual(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	netloc := func(s string) string {
		if !strings.HasPrefix(s, "http") {
			return s
		}
		u, err := url.Parse(s)
		if err != nil {
			return ""
		}
		host := u.Host
		if u.User != nil {
			host = u.User.String() + "@" + host
		}
		return host
	}
	return strings.ReplaceAll(netloc(a), "www.", "") == strings.ReplaceAll(netloc(b), "www.", "")
}
func FixedAdapterMatch(kind FixedAdapter, address string) (bool, error) {
	p, ok := fixedPolicies[kind]
	if !ok {
		return false, ErrAdapterNotReady
	}
	if kind == AdapterYemaPT {
		return strings.Contains(address, p.SiteURL), nil
	}
	return SourceURLEqual(address, p.SiteURL), nil
}
func sourceRequestTimeout(raw any) any {
	if Truth(raw) {
		return raw
	}
	return 20
}

// Original base.get_page_source decodes content with chardet before falling
// back to response.text. The unambiguous ASCII/UTF8/BOM subset is verified by
// original execution; ambiguous byte encodings require a trusted decoder.
func BasePageSource(ctx context.Context, site Object, address string, h SiteVisitHooks) (string, error) {
	if Truth(site["render"]) {
		if h.Browser == nil {
			return "", ErrAdapterNotReady
		}
		return h.Browser.Page(ctx, PageRequest{Method: "GET", URL: address, Timeout: pageTimeout(site)})
	}
	if h.HTTP == nil {
		return "", ErrAdapterNotReady
	}
	request := PageRequest{Method: "GET", URL: address, Timeout: sourceRequestTimeout(site["timeout"]), HeadersMode: HeaderBaseCookie}
	if cookie, ok := site["cookie"].(string); ok {
		request.RawCookieHeader = &cookie
	}
	response, err := h.HTTP.Do(ctx, request)
	if err != nil {
		return "", err
	}
	if response == nil || response.Status != 200 {
		return "", nil
	}
	if len(response.Content) == 0 {
		if response.TextError != nil {
			return "", response.TextError
		}
		return response.text(), nil
	}
	if h.Decode != nil {
		return h.Decode(response.Content, response.text)
	}
	raw := response.Content
	if len(raw) >= 3 && string(raw[:3]) == "\xef\xbb\xbf" {
		return string(raw[3:]), nil
	}
	if utf8.Valid(raw) {
		return string(raw), nil
	}
	return "", ErrSiteEncodingNotReady
}
func FixedSignin(kind FixedAdapter) func(context.Context, Object, SiteVisitHooks) (AdapterResult, error) {
	return func(ctx context.Context, site Object, h SiteVisitHooks) (AdapterResult, error) {
		if kind == AdapterTjupt {
			return TjuptSignin(ctx, site, h)
		}
		if kind == AdapterHDSky || kind == AdapterOpencd {
			return OCRSignin(kind, ctx, site, h)
		}
		if kind == AdapterU2 {
			return U2Signin(ctx, site, h)
		}
		if kind == AdapterPt52 || kind == AdapterCHDBits {
			return QuizSignin(kind, ctx, site, h)
		}
		if kind == AdapterTTG || kind == AdapterHDChina {
			return DynamicSignin(kind, ctx, site, h)
		}
		if kind == AdapterHares || kind == AdapterPTerClub || kind == AdapterRousiPro || kind == AdapterYemaPT || kind == AdapterZhuQue {
			return JSONSignin(kind, ctx, site, h)
		}
		if kind == AdapterBTSchool || kind == AdapterHDUpt || kind == AdapterPTTime || kind == AdapterHaiDan {
			return SimpleSignin(kind, ctx, site, h)
		}
		p, ok := fixedPolicies[kind]
		if !ok {
			return AdapterResult{}, ErrAdapterNotReady
		}
		var page any
		pageText := func() any { return page }
		if kind == AdapterHDCity {
			var err error
			page, err = sourceBasePageValue(ctx, site, p.Routes[0].URL, h)
			if err != nil {
				return AdapterResult{}, err
			}
		} else {
			if h.HTTP == nil {
				return AdapterResult{}, ErrAdapterNotReady
			}
			form := map[string]string{"action": "sign_in"}
			if kind == AdapterNexusHD {
				form = map[string]string{"action": "post", "content": ""}
			}
			response, err := h.HTTP.Do(ctx, PageRequest{Method: "POST", URL: p.Routes[0].URL, Form: form, Timeout: site["timeout"]})
			if err != nil {
				return AdapterResult{}, err
			}
			if response == nil || response.Status != 200 {
				siteLog(h, "ERROR", pythonScalar(site["name"])+" 签到失败，请检查站点连通性")
				return AdapterResult{false, "签到失败，请检查站点连通性"}, nil
			}
			pageText = func() any { return response.text() }
		}
		if !Truth(page) {
			if kind == AdapterHDCity {
				siteLog(h, "ERROR", pythonScalar(site["name"])+" 签到失败，请检查站点连通性")
				return AdapterResult{false, "签到失败，请检查站点连通性"}, nil
			}
		}
		expired := "login.php"
		if kind == AdapterHDCity {
			expired = "login"
		}
		isExpired, err := sourcePageContains(pageText(), expired)
		if err != nil {
			return AdapterResult{}, err
		}
		if isExpired {
			siteLog(h, "ERROR", pythonScalar(site["name"])+" 签到失败，Cookie已失效")
			return AdapterResult{false, "签到失败，Cookie已失效"}, nil
		}
		for _, step := range []struct{ key, message string }{{"_success_text", "签到成功"}, {"_repeat_text", "今日已签到"}} {
			patterns := htmlData.Regexes[string(kind)+"."+step.key]
			if len(patterns) == 0 {
				return AdapterResult{}, errors.New("missing pinned adapter patterns")
			}
			yes, err := sourcePageSignInResult(pageText(), patterns)
			if err != nil {
				return AdapterResult{}, err
			}
			if yes {
				siteLog(h, "INFO", pythonScalar(site["name"])+" "+step.message)
				return AdapterResult{true, step.message}, nil
			}
		}
		siteLog(h, "ERROR", pythonScalar(site["name"])+" 签到失败，签到接口返回 "+pythonScalar(pageText()))
		return AdapterResult{false, "签到失败"}, nil
	}
}
func FixedSiteAdapter(kind FixedAdapter) (SiteAdapter, error) {
	if _, ok := fixedPolicies[kind]; !ok {
		return SiteAdapter{}, ErrAdapterNotReady
	}
	a := SiteAdapter{Match: func(address string) (bool, error) { return FixedAdapterMatch(kind, address) }, Signin: FixedSignin(kind)}
	a.MatchRaw = func(raw any) (bool, error) {
		if kind == AdapterYemaPT {
			return contains(raw, fixedPolicies[kind].SiteURL)
		}
		if !Truth(raw) {
			return false, nil
		}
		text, ok := raw.(string)
		if !ok {
			return false, fmt.Errorf("'%s' object has no attribute 'startswith'", typeName(raw))
		}
		return FixedAdapterMatch(kind, text)
	}
	if kind == AdapterRousiPro {
		a.Login = func(ctx context.Context, s Object, h SiteVisitHooks) (AdapterResult, error) {
			return RousiVisit(ctx, s, h, true)
		}
	}
	if kind == AdapterYemaPT {
		a.Login = func(ctx context.Context, s Object, h SiteVisitHooks) (AdapterResult, error) {
			return YemaVisit(ctx, s, h, true)
		}
	}
	return a, nil
}
