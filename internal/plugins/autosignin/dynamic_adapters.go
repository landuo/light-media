package autosignin

import (
	"context"
	"errors"
	"regexp"
	"strings"

	"golang.org/x/net/html"
)

const (
	AdapterTTG     FixedAdapter = "TTG"
	AdapterHDChina FixedAdapter = "HDChina"
)

func init() {
	fixedPolicies[AdapterTTG] = fixedPolicy{SiteURL: "totheglory.im", RegisteredHosts: []string{"totheglory.im", "www.totheglory.im"}, Routes: []fixedRoute{{"GET", "https://totheglory.im"}, {"POST", "https://totheglory.im/signed.php"}}}
	fixedPolicies[AdapterHDChina] = fixedPolicy{SiteURL: "hdchina.org", RegisteredHosts: []string{"hdchina.org", "www.hdchina.org"}, Routes: []fixedRoute{{"GET", "https://hdchina.org/index.php"}, {"POST", "https://hdchina.org/plugin_sign-in.php?cmd=signin"}}}
}
func responseUTF8(r *PageResponse) string {
	if len(r.Content) > 0 {
		return siteUTF8(r.Content)
	}
	return r.Text
}
func firstAttribute(doc *html.Node, tag, key, value, target string) (string, bool) {
	for _, n := range allNodes(doc) {
		if n.Type == html.ElementNode && n.Data == tag && attr(n, key) == value && hasAttr(n, target) {
			return attr(n, target), true
		}
	}
	return "", false
}

var ttgTimestamp = regexp.MustCompile(`signed_timestamp: "(` + sourceDecimalClass + `{10})`)
var ttgToken = regexp.MustCompile(`signed_token: "([^\n]*)"`)

func DynamicSignin(kind FixedAdapter, ctx context.Context, site Object, h SiteVisitHooks) (AdapterResult, error) {
	fail := func(message string) (AdapterResult, error) { return AdapterResult{false, message}, nil }
	routes := fixedPolicies[kind].Routes
	var page string
	cookie := ""
	if kind == AdapterHDChina {
		raw, ok := site["cookie"].(string)
		if !ok {
			return AdapterResult{}, errors.New("'" + typeName(site["cookie"]) + "' object has no attribute 'split'")
		}
		for _, part := range strings.Split(raw, ";") {
			if strings.Contains(part, "hdchina=") {
				cookie += part + ";"
			}
		}
		if !strings.Contains(cookie, "hdchina=") {
			return fail("签到失败，Cookie已失效")
		}
		response, err := getHTTP(ctx, h, PageRequest{Method: "GET", URL: routes[0].URL, CookieOverride: &cookie, Timeout: site["timeout"]})
		if err != nil {
			return AdapterResult{}, err
		}
		if response == nil || response.Status != 200 {
			return fail("签到失败，请检查站点连通性")
		}
		if strings.Contains(response.Text, "login.php") || strings.Contains(response.Text, "阻断页面") {
			return fail("签到失败，Cookie失效")
		}
		cookie = ""
		for i, c := range response.Cookies {
			if i > 0 {
				cookie += ";"
			}
			cookie += c.Name + "=" + c.Value
		}
		page = responseUTF8(response)
	} else {
		var err error
		page, err = BasePageSource(ctx, site, routes[0].URL, h)
		if err != nil {
			return AdapterResult{}, err
		}
		if page == "" {
			return fail("签到失败，请检查站点连通性")
		}
		if strings.Contains(page, "login.php") {
			return fail("签到失败，Cookie已失效")
		}
	}
	yes, err := SignInResult(page, htmlData.Regexes[string(kind)+"._sign_regex"])
	if err != nil {
		return AdapterResult{}, err
	}
	if yes {
		return AdapterResult{true, "今日已签到"}, nil
	}
	if kind == AdapterHDChina {
		doc, err := parseHTML(page)
		if err != nil {
			return AdapterResult{}, err
		}
		if doc == nil {
			return fail("签到失败")
		}
		csrf, exists := firstAttribute(doc, "meta", "name", "x-csrf", "content")
		if !exists {
			return AdapterResult{}, errors.New("list index out of range")
		}
		if csrf == "" {
			return fail("签到失败")
		}
		response, err := getHTTP(ctx, h, PageRequest{Method: "POST", URL: routes[1].URL, CookieOverride: &cookie, Form: map[string]string{"csrf": csrf}, Timeout: site["timeout"]})
		if err != nil {
			return AdapterResult{}, err
		}
		if response == nil || response.Status != 200 {
			return fail("签到失败，签到接口请求失败")
		}
		o, err := responseObject(response)
		if err != nil {
			return AdapterResult{}, err
		}
		if o["state"] == "success" {
			return AdapterResult{true, "签到成功"}, nil
		}
		return fail("签到失败")
	}
	timestamp := ttgTimestamp.FindStringSubmatch(page)
	token := ttgToken.FindStringSubmatch(page)
	if timestamp == nil || token == nil {
		return AdapterResult{}, errors.New("'NoneType' object has no attribute 'group'")
	}
	response, err := getHTTP(ctx, h, PageRequest{Method: "POST", URL: routes[1].URL, Form: map[string]string{"signed_timestamp": timestamp[1], "signed_token": token[1]}, DecodeAsUTF8: true})
	if err != nil {
		return AdapterResult{}, err
	}
	if response == nil || response.Status != 200 {
		return fail("签到失败，签到接口请求失败")
	}
	for _, step := range []struct{ key, message string }{{"_success_text", "签到成功"}, {"_sign_text", "今日已签到"}} {
		yes, err := SignInResult(responseUTF8(response), htmlData.Regexes[string(kind)+"."+step.key])
		if err != nil {
			return AdapterResult{}, err
		}
		if yes {
			return AdapterResult{true, step.message}, nil
		}
	}
	return fail("签到失败，未知原因")
}
