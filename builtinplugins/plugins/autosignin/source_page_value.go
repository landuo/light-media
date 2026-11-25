package autosignin

import (
	"context"
	"fmt"

	legacyanime "light-media/builtinplugins/plugins/autosignin/pyvalue"
	"golang.org/x/net/html"
)

// These private source helpers preserve the actual Page return value until its
// original consumer operation. Existing ordinary typed Browser APIs remain.
func sourceBasePageValue(ctx context.Context, site Object, address string, h SiteVisitHooks) (any, error) {
	if Truth(site["render"]) && h.SourcePageValue != nil {
		return h.SourcePageValue(ctx, PageRequest{Method: "GET", URL: address, Timeout: pageTimeout(site)})
	}
	return BasePageSource(ctx, site, address, h)
}
func sourceRequestSitePageValue(ctx context.Context, site Object, request PageRequest, h SiteVisitHooks) (any, *PageResponse, error) {
	if Truth(site["render"]) && h.SourcePageValue != nil {
		value, err := h.SourcePageValue(ctx, request)
		return value, nil, err
	}
	return requestSitePage(ctx, site, request, h)
}

// Operation-specific coercion is deliberately delayed: Python membership can
// accept a list/dict that its later lxml/json operation rejects differently.
func sourcePageContains(value any, needle string) (bool, error) { return contains(value, needle) }
func sourcePageHTML(value any) (string, error) {
	if text, ok := value.(string); ok {
		return text, nil
	}
	return "", fmt.Errorf("memoryview: a bytes-like object is required, not '%s'", typeName(value))
}
func sourcePageChallenge(value any, h SiteVisitHooks) (bool, error) {
	if !Truth(value) {
		return false, nil
	}
	if text, ok := value.(string); ok {
		// At this source-only PyQuery stage lxml drops isolated UTF-16 code units.
		// If that leaves no document content it raises before evidence checks;
		// usable HTML and valid non-BMP text retain the existing parse path.
		surrogate, content := false, false
		for _, cp := range sourceCodepoints(text) {
			if cp >= 0xd800 && cp <= 0xdfff {
				surrogate = true
			} else if !pySpace(rune(cp)) {
				content = true
			}
		}
		if surrogate && !content {
			panic(&sourceException{fmt.Errorf("Document is empty")})
		}
		return checkChallenge(text, h)
	}
	if values, ok := value.([]any); ok {
		panic(&sourceException{fmt.Errorf("'%s' object has no attribute 'xpath'", typeName(values[0]))})
	}
	panic(&sourceException{fmt.Errorf("%s", pythonScalar(value))})
}

func sourcePageVisibleInput(value any) (string, error) {
	if !Truth(value) {
		return "", nil
	}
	return sourcePageHTML(value)
}
func sourcePageLoginEvidence(value any) (bool, error) {
	text, e := sourcePageVisibleInput(value)
	if e != nil {
		return false, e
	}
	return HasLoginEvidence(text)
}
func sourcePageSigninEvidence(value any) (bool, error) {
	text, e := sourcePageVisibleInput(value)
	if e != nil {
		return false, e
	}
	return HasSigninEvidence(text)
}
func sourcePageLoginForm(value any) (bool, error) {
	text, e := sourcePageVisibleInput(value)
	if e != nil {
		return false, e
	}
	return HasLoginForm(text)
}

func sourcePageJSON(value any) (any, error) {
	if text, ok := value.(string); ok {
		return parseSiteJSON(text)
	}
	return nil, fmt.Errorf("the JSON object must be str, bytes or bytearray, not %s", typeName(value))
}
func sourcePTerSignin(ctx context.Context, site Object, h SiteVisitHooks) (AdapterResult, error) {
	fail := func(message string) (AdapterResult, error) {
		siteLog(h, "ERROR", pythonScalar(site["name"])+" "+message)
		return AdapterResult{false, message}, nil
	}
	page, err := sourceBasePageValue(ctx, site, fixedPolicies[AdapterPTerClub].Routes[0].URL, h)
	if err != nil {
		return AdapterResult{}, err
	}
	if !Truth(page) {
		return fail("签到失败，请检查站点连通性")
	}
	login, err := sourcePageContains(page, "login.php")
	if err != nil {
		return AdapterResult{}, err
	}
	if login {
		return fail("签到失败，Cookie已失效")
	}
	value, err := sourcePageJSON(page)
	if err != nil {
		siteLog(h, "ERROR", pythonScalar(site["name"])+" 签到失败，签到接口返回数据异常，错误信息："+err.Error())
		return AdapterResult{false, "签到失败，签到接口返回数据异常"}, nil
	}
	object, err := jsonObject(value)
	if err != nil {
		return AdapterResult{}, err
	}
	if object["status"] == "1" {
		siteLog(h, "INFO", pythonScalar(site["name"])+" 签到成功")
		return AdapterResult{true, "签到成功"}, nil
	}
	if object["status"] == "0" {
		yes, err := anySigninEvidence(object["message"])
		if err != nil {
			return AdapterResult{}, err
		}
		if yes {
			siteLog(h, "INFO", pythonScalar(site["name"])+" 今日已签到")
			return AdapterResult{true, "今日已签到"}, nil
		}
	}
	return AdapterResult{false, "签到失败，签到接口未返回明确的成功或重复签到结果"}, nil
}

// These calls occur only at the corresponding original lxml/visible_html/strip
// operation, after any preceding truth or membership test has run.
func sourcePageParseHTML(value any) (*html.Node, error) {
	text, err := sourcePageHTML(value)
	if err != nil {
		return nil, err
	}
	return parseHTML(text)
}
func sourcePageLoggedIn(value any) (bool, error) {
	text, err := sourcePageHTML(value)
	if err != nil {
		return false, err
	}
	return LegacySiteLoggedIn(text)
}
func sourcePageSignInResult(value any, patterns []string) (bool, error) {
	text, err := sourcePageVisibleInput(value)
	if err != nil {
		return false, err
	}
	return SignInResult(text, patterns)
}
func sourcePageStrip(value any) (string, error) {
	text, ok := value.(string)
	if !ok {
		return "", fmt.Errorf("'%s' object has no attribute 'strip'", typeName(value))
	}
	return legacyanime.Strip(text), nil
}
func sourcePageRegexInput(value any) (string, error) {
	text, ok := value.(string)
	if !ok {
		return "", fmt.Errorf("expected string or bytes-like object, got '%s'", typeName(value))
	}
	return text, nil
}
