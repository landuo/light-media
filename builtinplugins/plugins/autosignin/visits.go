package autosignin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// These values are host-side snapshots. The eventual SDK must receive a site
// handle, never a worker supplied Cookie, origin, URL, proxy or browser target.
type HeaderMode string

const (
	HeaderDefault    HeaderMode = ""
	HeaderBaseCookie HeaderMode = "base_cookie"
)

type PageRequest struct {
	Method, URL, Referer string
	AcceptType           string
	HeadersMode          HeaderMode
	Headers              Object
	NoCookies            bool
	CookieOverride       *string
	RawCookieHeader      *string
	JSON                 any
	DecodeAsUTF8         bool
	Binary               bool
	Form                 map[string]string
	RawForm              map[string]json.RawMessage
	FormValues           map[string][]string `json:"form_values,omitempty"`
	FormOrder            []string
	Timeout              any
}
type CookieValue struct{ Name, Value string }
type PageResponse struct {
	Status    int
	Text      string
	Content   []byte
	TextError error
	Cookies   []CookieValue
	getText   func() (string, error)
	jsonText  string
}
type PageTransport interface {
	Do(context.Context, PageRequest) (*PageResponse, error)
}
type BrowserTransport interface {
	Page(context.Context, PageRequest) (string, error)
}
type SiteVisitHooks struct {
	// Concrete source consumer value path; ordinary Browser remains string-only.
	SourcePageValue func(context.Context, PageRequest) (any, error)

	Log     func(string, string) error
	HTTP    PageTransport
	Browser BrowserTransport
	Stats   func(context.Context, string, bool, int) error
	Decode  func([]byte, func() string) (string, error)
	Now     func() time.Time
	Choose  func(int, int) (int, error)
	OCR     func(context.Context, string) (any, error)
	Sleep   func(context.Context, time.Duration) error
	Tjupt   *TjuptDependencies
	MTeam   SiteTransport
}
type AdapterResult struct {
	State   any
	Message string
}
type SiteAdapter struct {
	Match         func(string) (bool, error)
	MatchRaw      func(any) (bool, error)
	Signin        func(context.Context, Object, SiteVisitHooks) (AdapterResult, error)
	Login         func(context.Context, Object, SiteVisitHooks) (AdapterResult, error)
	SigninIsLogin bool
}
type SiteVisitor struct {
	Adapters []SiteAdapter
	Hooks    SiteVisitHooks
}

func (v SiteVisitor) adapter(site Object, loaded bool) *SiteAdapter {
	if !loaded {
		return nil
	}
	for i := range v.Adapters {
		a := &v.Adapters[i]
		if a.Match != nil || a.MatchRaw != nil {
			var yes bool
			var err error
			if a.MatchRaw != nil {
				yes, err = a.MatchRaw(site["url"])
			} else {
				yes, err = a.Match(textValue(site["url"]))
			}
			if err != nil {
				siteLog(v.Hooks, "ERROR", "站点模块加载失败："+err.Error())
			}
			if err == nil && yes {
				return a
			}
		}
	}
	return nil
}
func textValue(v any) string {
	if v == nil {
		return ""
	}
	return pythonScalar(v)
}
func pageTimeout(site Object) any {
	if v := site["timeout"]; Truth(v) {
		return v
	}
	return 60
}
func joinedURL(base, path string) (string, error) {
	u, err := url.Parse(base)
	if err != nil {
		return "", err
	}
	p, err := url.Parse(path)
	if err != nil {
		return "", err
	}
	return u.ResolveReference(p).String(), nil
}
func (r *PageResponse) text() string {
	if r == nil {
		return ""
	}
	if r.getText != nil {
		text, err := r.getText()
		if err != nil {
			panic(&sourceException{err})
		}
		return text
	}
	if r.TextError != nil {
		panic(&sourceException{r.TextError})
	}
	return r.Text
}
func checkChallenge(page string, h SiteVisitHooks) (bool, error) {
	value, err := UnderChallenge(page)
	if err != nil || page == "" {
		return value, err
	}
	doc, err := parseHTML(page)
	if err != nil {
		return false, err
	}
	titles := []string{}
	for _, node := range allNodes(doc) {
		if node.Data == "title" {
			titles = append(titles, normalizeSpace(elementText(node)))
		}
	}
	siteLog(h, "DEBUG", "under_challenge page_title="+strings.Join(titles, " "))
	return value, nil
}
func requestSitePage(ctx context.Context, site Object, request PageRequest, h SiteVisitHooks) (string, *PageResponse, error) {
	if Truth(site["render"]) {
		if h.Browser == nil {
			return "", nil, ErrAdapterNotReady
		}
		text, err := h.Browser.Page(ctx, request)
		return text, nil, err
	}
	if h.HTTP == nil {
		return "", nil, ErrAdapterNotReady
	}
	res, err := h.HTTP.Do(ctx, request)
	if err != nil {
		return "", nil, err
	}
	if res == nil {
		return "", nil, nil
	}
	return "", res, nil
}
func GenericSignin(ctx context.Context, site Object, h SiteVisitHooks) (result Result) {
	defer func() {
		if caught := recover(); caught != nil {
			if source, ok := caught.(*sourceException); ok {
				siteLog(h, "WARNING", fmt.Sprintf("%s 签到失败：%s", pythonScalar(site["name"]), source.Error()))
				result = Result{SiteName: site["name"], Message: "签到失败：" + source.Error() + "！"}
				return
			}
			panic(caught)
		}
	}()
	name := site["name"]
	failed := func(message string) Result { return Result{SiteName: name, Message: message} }
	if !Truth(site) {
		return Result{SiteName: "", Message: "签到失败，缺少站点信息"}
	}
	if !Truth(site["url"]) || !Truth(site["cookie"]) {
		siteLog(h, "WARNING", fmt.Sprintf("未配置 %s 的站点地址或Cookie，无法签到", pythonScalar(name)))
		return failed("签到失败，未配置站点地址或Cookie")
	}
	address := textValue(site["url"])
	var err error
	if !strings.Contains(address, "attendance.php") {
		address, err = joinedURL(address, "attendance.php")
		if err != nil {
			return failed("签到失败：" + err.Error() + "！")
		}
	}
	siteLog(h, "INFO", fmt.Sprintf("开始站点签到：%s，地址：%s...", pythonScalar(name), address))
	page, res, err := sourceRequestSitePageValue(ctx, site, PageRequest{Method: "GET", URL: address, Timeout: pageTimeout(site)}, h)
	if err != nil {
		siteLog(h, "WARNING", fmt.Sprintf("%s 签到失败：%s", pythonScalar(name), err.Error()))
		return failed("签到失败：" + err.Error() + "！")
	}
	if !Truth(site["render"]) {
		if res == nil {
			return failed("签到失败，无法打开网站！")
		}
		challenge, err := checkChallenge(res.text(), h)
		if err != nil {
			return failed("签到失败：" + err.Error() + "！")
		}
		if challenge {
			return failed("签到失败，站点被Cloudflare防护，请打开站点浏览器仿真")
		}
		if res.Status != 200 {
			return failed(fmt.Sprintf("签到失败，状态码：%d！", res.Status))
		}
		page = res.text()
	}
	if !Truth(page) {
		return failed("签到失败，站点返回空页面")
	}
	challenge, err := sourcePageChallenge(page, h)
	if err != nil {
		return failed("签到失败：" + err.Error() + "！")
	}
	if challenge {
		return failed("签到失败，无法通过Cloudflare！")
	}
	logged, err := sourcePageLoginEvidence(page)
	if err != nil {
		return failed("签到失败：" + err.Error() + "！")
	}
	if !logged {
		return failed("签到失败，未识别到登录状态")
	}
	signed, err := sourcePageSigninEvidence(page)
	if err != nil {
		return failed("签到失败：" + err.Error() + "！")
	}
	if signed {
		siteLog(h, "INFO", fmt.Sprintf("%s 签到成功，已确认站点返回的签到状态", pythonScalar(name)))
		return Result{SiteName: name, Message: "签到成功", Success: true, LoggedIn: true}
	}
	siteLog(h, "INFO", fmt.Sprintf("%s 登录成功，签到未确认", pythonScalar(name)))
	return Result{SiteName: name, Message: "登录成功，签到未确认", LoggedIn: true}
}
func GenericLogin(ctx context.Context, site Object, h SiteVisitHooks) (result AdapterResult, err error) {
	defer func() {
		if caught := recover(); caught != nil {
			if source, ok := caught.(*sourceException); ok {
				siteLog(h, "WARNING", fmt.Sprintf("%s 模拟登录失败：%s", pythonScalar(site["name"]), source.Error()))
				result = AdapterResult{false, "模拟登录失败：" + source.Error() + "！"}
				err = nil
				return
			}
			panic(caught)
		}
	}()
	fail := func(message string) (AdapterResult, error) { return AdapterResult{false, message}, nil }
	if !Truth(site) {
		return fail("模拟登录失败，缺少站点信息")
	}
	if !Truth(site["url"]) || !Truth(site["cookie"]) {
		siteLog(h, "WARNING", fmt.Sprintf("未配置 %s 的站点地址或Cookie，无法登录", pythonScalar(site["name"])))
		return fail("模拟登录失败，未配置站点地址或Cookie")
	}
	address := textValue(site["url"])
	if strings.Contains(address, "attendance.php") {
		address, err = joinedURL(address, "./")
		if err != nil {
			return fail("模拟登录失败：" + err.Error() + "！")
		}
	}
	siteLog(h, "INFO", fmt.Sprintf("开始站点模拟登录：%s，地址：%s...", pythonScalar(site["name"]), address))
	page, res, err := sourceRequestSitePageValue(ctx, site, PageRequest{Method: "GET", URL: address, Timeout: pageTimeout(site)}, h)
	if err != nil {
		siteLog(h, "WARNING", fmt.Sprintf("%s 模拟登录失败：%s", pythonScalar(site["name"]), err.Error()))
		return fail("模拟登录失败：" + err.Error() + "！")
	}
	if !Truth(site["render"]) {
		if res == nil {
			return fail("模拟登录失败，无法打开网站！")
		}
		challenge, err := checkChallenge(res.text(), h)
		if err != nil {
			return fail("模拟登录失败：" + err.Error() + "！")
		}
		if challenge {
			return fail("模拟登录失败，站点被Cloudflare防护，请打开站点浏览器仿真")
		}
		if res.Status != 200 {
			return fail(fmt.Sprintf("模拟登录失败，状态码：%d！", res.Status))
		}
		page = res.text()
	}
	if !Truth(page) {
		return fail("模拟登录失败，站点返回空页面")
	}
	challenge, err := sourcePageChallenge(page, h)
	if err != nil {
		return fail("模拟登录失败：" + err.Error() + "！")
	}
	if challenge {
		return fail("模拟登录失败，无法通过Cloudflare！")
	}
	logged, err := sourcePageLoginEvidence(page)
	if err != nil {
		return fail("模拟登录失败：" + err.Error() + "！")
	}
	if !logged {
		form, err := sourcePageLoginForm(page)
		if err != nil {
			return fail("模拟登录失败：" + err.Error() + "！")
		}
		if form {
			return fail("模拟登录失败，Cookie已失效")
		}
		return fail("模拟登录失败，未识别到登录状态")
	}
	siteLog(h, "INFO", fmt.Sprintf("%s 模拟登录成功", pythonScalar(site["name"])))
	return AdapterResult{true, "模拟登录成功"}, nil
}
func PTZoneMatch(address string) (bool, error) {
	u, err := url.Parse(address)
	if err != nil {
		return false, err
	}
	host := strings.ToLower(u.Hostname())
	return host == "ptzone.xyz" || host == "www.ptzone.xyz", nil
}
func PTZoneSignin(ctx context.Context, site Object, h SiteVisitHooks) (AdapterResult, error) {
	fail := func(message string) (AdapterResult, error) { return AdapterResult{false, message}, nil }
	if !Truth(site["url"]) || !Truth(site["cookie"]) {
		return fail("签到失败，未配置站点地址或Cookie")
	}
	address, err := joinedURL(textValue(site["url"]), "attendance.php")
	if err != nil {
		return AdapterResult{}, err
	}
	if h.HTTP == nil {
		return AdapterResult{}, ErrAdapterNotReady
	}
	res, err := h.HTTP.Do(ctx, PageRequest{Method: "POST", URL: address, Referer: address, Form: map[string]string{}, Timeout: pageTimeout(site)})
	if err != nil {
		return AdapterResult{}, err
	}
	if res == nil {
		return fail("签到失败，无法打开网站")
	}
	challenge, err := checkChallenge(res.text(), h)
	if err != nil {
		return AdapterResult{}, err
	}
	if challenge {
		return fail("签到失败，站点被Cloudflare防护")
	}
	if res.Status != 200 {
		return fail(fmt.Sprintf("签到失败，状态码：%d", res.Status))
	}
	if res.text() == "" {
		return fail("签到失败，站点返回空页面")
	}
	form, err := HasLoginForm(res.text())
	if err != nil {
		return AdapterResult{}, err
	}
	if form {
		return fail("签到失败，Cookie已失效")
	}
	logged, err := HasLoginEvidence(res.text())
	if err != nil {
		return AdapterResult{}, err
	}
	if !logged {
		return fail("签到失败，未识别到登录状态")
	}
	signed, err := HasSigninEvidence(res.text())
	if err != nil {
		return AdapterResult{}, err
	}
	if signed {
		return AdapterResult{true, "签到成功"}, nil
	}
	return fail("签到失败，未确认签到结果")
}
func (v SiteVisitor) loginResult(ctx context.Context, site Object, adapter *SiteAdapter) Result {
	var raw AdapterResult
	var err error
	if adapter != nil && adapter.Login != nil {
		raw, err = invokeAdapter(ctx, site, v.Hooks, adapter.Login)
	} else {
		raw, err = GenericLogin(ctx, site, v.Hooks)
	}
	if err != nil {
		raw = AdapterResult{false, "模拟登录失败：" + err.Error()}
	}
	return SiteResult(site["name"], raw.State, raw.Message, "模拟登录")
}
func invokeAdapter(ctx context.Context, site Object, hooks SiteVisitHooks, method func(context.Context, Object, SiteVisitHooks) (AdapterResult, error)) (result AdapterResult, err error) {
	defer func() {
		if caught := recover(); caught != nil {
			if source, ok := caught.(*sourceException); ok {
				err = source.cause
				return
			}
			panic(caught)
		}
	}()
	return method(ctx, site, hooks)
}
func (v SiteVisitor) Visit(ctx context.Context, site Object, operation string, loaded bool) (out Result, visitErr error) {
	ctx = defaultCacheDumpEntry(ctx, sourceCacheVisitor)
	defer func() {
		if caught := recover(); caught != nil {
			if boundary, ok := caught.(*boundaryFailure); ok {
				visitErr = boundary.cause
				return
			}
			panic(caught)
		}
	}()
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	now := v.Hooks.Now
	if now == nil {
		now = time.Now
	}
	adapter := v.adapter(site, loaded)
	start := now()
	var result Result
	switch operation {
	case "签到":
		if adapter != nil && adapter.Signin != nil {
			raw, err := invokeAdapter(ctx, site, v.Hooks, adapter.Signin)
			if err != nil {
				raw = AdapterResult{false, "签到失败：" + err.Error()}
			}
			result = SiteResult(site["name"], raw.State, raw.Message, "签到")
		} else {
			result = GenericSignin(ctx, site, v.Hooks)
		}
		if !result.Completed() && (adapter == nil || !adapter.SigninIsLogin) {
			login := v.loginResult(ctx, site, adapter)
			if login.Success {
				siteLog(v.Hooks, "INFO", fmt.Sprintf("%s 已确认登录，原签到结果：%s", pythonScalar(result.SiteName), result.Message))
				result = Result{SiteName: result.SiteName, Message: "登录成功，签到未确认", LoggedIn: true}
			} else if result.Message != login.Message {
				result.Message += "；" + login.Message
			}
		}
	case "登录":
		result = v.loginResult(ctx, site, adapter)
	default:
		return Result{}, errors.New("unknown site visit operation")
	}
	elapsed := now().Sub(start)
	seconds := int(elapsed / time.Second)
	if elapsed < 0 && elapsed%time.Second != 0 {
		seconds--
	}
	seconds %= 86400
	if seconds < 0 {
		seconds += 86400
	}
	success := result.Completed()
	if operation == "登录" {
		success = result.Success
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	if v.Hooks.Stats == nil {
		return result, errors.New("site statistics sink not ready")
	}
	if err := v.Hooks.Stats(ctx, sourceDomain(textValue(site["url"])), success, seconds); err != nil {
		return result, err
	}
	return result, nil
}
