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
	return r.text()
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
	fail := func(message string) (AdapterResult, error) {
		if message != "签到失败" {
			siteLog(h, "ERROR", pythonScalar(site["name"])+" "+message)
		}
		return AdapterResult{false, message}, nil
	}
	routes := fixedPolicies[kind].Routes
	var page any
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
		if strings.Contains(response.text(), "login.php") || strings.Contains(response.text(), "阻断页面") {
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
		page, err = sourceBasePageValue(ctx, site, routes[0].URL, h)
		if err != nil {
			return AdapterResult{}, err
		}
		if !Truth(page) {
			return fail("签到失败，请检查站点连通性")
		}
		expired, err := sourcePageContains(page, "login.php")
		if err != nil {
			return AdapterResult{}, err
		}
		if expired {
			return fail("签到失败，Cookie已失效")
		}
	}
	yes, err := sourcePageSignInResult(page, htmlData.Regexes[string(kind)+"._sign_regex"])
	if err != nil {
		return AdapterResult{}, err
	}
	if yes {
		siteLog(h, "INFO", pythonScalar(site["name"])+" 今日已签到")
		return AdapterResult{true, "今日已签到"}, nil
	}
	if kind == AdapterHDChina {
		doc, err := sourcePageParseHTML(page)
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
			siteLog(h, "ERROR", "{site} 签到失败，获取x-csrf失败")
			return fail("签到失败")
		}
		siteLog(h, "DEBUG", "获取到x-csrf "+csrf)
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
		siteLog(h, "DEBUG", "签到返回结果 "+pythonJSONRepr(response.jsonText))
		if o["state"] == "success" {
			siteLog(h, "INFO", pythonScalar(site["name"])+" 签到成功")
			return AdapterResult{true, "签到成功"}, nil
		}
		siteLog(h, "ERROR", pythonScalar(site["name"])+" 签到失败，不正确的CSRF / Incorrect CSRF token")
		return fail("签到失败")
	}
	pageString, err := sourcePageRegexInput(page)
	if err != nil {
		return AdapterResult{}, err
	}
	timestamp := ttgTimestamp.FindStringSubmatch(pageString)
	token := ttgToken.FindStringSubmatch(pageString)
	if timestamp == nil || token == nil {
		return AdapterResult{}, errors.New("'NoneType' object has no attribute 'group'")
	}
	siteLog(h, "DEBUG", "signed_timestamp="+timestamp[1]+" signed_token="+token[1])
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
			siteLog(h, "INFO", pythonScalar(site["name"])+" "+step.message)
			return AdapterResult{true, step.message}, nil
		}
	}
	return fail("签到失败，未知原因")
}
