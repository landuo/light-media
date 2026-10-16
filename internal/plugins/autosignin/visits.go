package autosignin

import (
	"context"
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
}
type PageTransport interface {
	Do(context.Context, PageRequest) (*PageResponse, error)
}
type BrowserTransport interface {
	Page(context.Context, PageRequest) (string, error)
}
type SiteVisitHooks struct {
	HTTP    PageTransport
	Browser BrowserTransport
	Stats   func(context.Context, string, bool, int) error
	Decode  func([]byte, string) (string, error)
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
		if a.Match != nil {
			yes, err := a.Match(textValue(site["url"]))
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
	return res.Text, res, nil
}
func GenericSignin(ctx context.Context, site Object, h SiteVisitHooks) Result {
	name := textValue(site["name"])
	failed := func(message string) Result { return Result{SiteName: name, Message: message} }
	if !Truth(site) {
		return failed("签到失败，缺少站点信息")
	}
	if !Truth(site["url"]) || !Truth(site["cookie"]) {
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
	page, res, err := requestSitePage(ctx, site, PageRequest{Method: "GET", URL: address, Timeout: pageTimeout(site)}, h)
	if err != nil {
		return failed("签到失败：" + err.Error() + "！")
	}
	if !Truth(site["render"]) {
		if res == nil {
			return failed("签到失败，无法打开网站！")
		}
		challenge, err := UnderChallenge(res.Text)
		if err != nil {
			return failed("签到失败：" + err.Error() + "！")
		}
		if challenge {
			return failed("签到失败，站点被Cloudflare防护，请打开站点浏览器仿真")
		}
		if res.Status != 200 {
			return failed(fmt.Sprintf("签到失败，状态码：%d！", res.Status))
		}
	}
	if page == "" {
		return failed("签到失败，站点返回空页面")
	}
	challenge, err := UnderChallenge(page)
	if err != nil {
		return failed("签到失败：" + err.Error() + "！")
	}
	if challenge {
		return failed("签到失败，无法通过Cloudflare！")
	}
	logged, err := HasLoginEvidence(page)
	if err != nil {
		return failed("签到失败：" + err.Error() + "！")
	}
	if !logged {
		return failed("签到失败，未识别到登录状态")
	}
	signed, err := HasSigninEvidence(page)
	if err != nil {
		return failed("签到失败：" + err.Error() + "！")
	}
	if signed {
		return Result{SiteName: name, Message: "签到成功", Success: true, LoggedIn: true}
	}
	return Result{SiteName: name, Message: "登录成功，签到未确认", LoggedIn: true}
}
func GenericLogin(ctx context.Context, site Object, h SiteVisitHooks) (AdapterResult, error) {
	fail := func(message string) (AdapterResult, error) { return AdapterResult{false, message}, nil }
	if !Truth(site) {
		return fail("模拟登录失败，缺少站点信息")
	}
	if !Truth(site["url"]) || !Truth(site["cookie"]) {
		return fail("模拟登录失败，未配置站点地址或Cookie")
	}
	address := textValue(site["url"])
	var err error
	if strings.Contains(address, "attendance.php") {
		address, err = joinedURL(address, "./")
		if err != nil {
			return fail("模拟登录失败：" + err.Error() + "！")
		}
	}
	page, res, err := requestSitePage(ctx, site, PageRequest{Method: "GET", URL: address, Timeout: pageTimeout(site)}, h)
	if err != nil {
		return fail("模拟登录失败：" + err.Error() + "！")
	}
	if !Truth(site["render"]) {
		if res == nil {
			return fail("模拟登录失败，无法打开网站！")
		}
		challenge, err := UnderChallenge(res.Text)
		if err != nil {
			return fail("模拟登录失败：" + err.Error() + "！")
		}
		if challenge {
			return fail("模拟登录失败，站点被Cloudflare防护，请打开站点浏览器仿真")
		}
		if res.Status != 200 {
			return fail(fmt.Sprintf("模拟登录失败，状态码：%d！", res.Status))
		}
	}
	if page == "" {
		return fail("模拟登录失败，站点返回空页面")
	}
	challenge, err := UnderChallenge(page)
	if err != nil {
		return fail("模拟登录失败：" + err.Error() + "！")
	}
	if challenge {
		return fail("模拟登录失败，无法通过Cloudflare！")
	}
	logged, err := HasLoginEvidence(page)
	if err != nil {
		return fail("模拟登录失败：" + err.Error() + "！")
	}
	if !logged {
		form, err := HasLoginForm(page)
		if err != nil {
			return fail("模拟登录失败：" + err.Error() + "！")
		}
		if form {
			return fail("模拟登录失败，Cookie已失效")
		}
		return fail("模拟登录失败，未识别到登录状态")
	}
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
	challenge, err := UnderChallenge(res.Text)
	if err != nil {
		return AdapterResult{}, err
	}
	if challenge {
		return fail("签到失败，站点被Cloudflare防护")
	}
	if res.Status != 200 {
		return fail(fmt.Sprintf("签到失败，状态码：%d", res.Status))
	}
	if res.Text == "" {
		return fail("签到失败，站点返回空页面")
	}
	form, err := HasLoginForm(res.Text)
	if err != nil {
		return AdapterResult{}, err
	}
	if form {
		return fail("签到失败，Cookie已失效")
	}
	logged, err := HasLoginEvidence(res.Text)
	if err != nil {
		return AdapterResult{}, err
	}
	if !logged {
		return fail("签到失败，未识别到登录状态")
	}
	signed, err := HasSigninEvidence(res.Text)
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
		raw, err = adapter.Login(ctx, site, v.Hooks)
	} else {
		raw, err = GenericLogin(ctx, site, v.Hooks)
	}
	if err != nil {
		raw = AdapterResult{false, "模拟登录失败：" + err.Error()}
	}
	return SiteResult(textValue(site["name"]), raw.State, raw.Message, "模拟登录")
}
func (v SiteVisitor) Visit(ctx context.Context, site Object, operation string, loaded bool) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	now := v.Hooks.Now
	if now == nil {
		now = time.Now
	}
	start := now()
	adapter := v.adapter(site, loaded)
	var result Result
	switch operation {
	case "签到":
		if adapter != nil && adapter.Signin != nil {
			raw, err := adapter.Signin(ctx, site, v.Hooks)
			if err != nil {
				raw = AdapterResult{false, "签到失败：" + err.Error()}
			}
			result = SiteResult(textValue(site["name"]), raw.State, raw.Message, "签到")
		} else {
			result = GenericSignin(ctx, site, v.Hooks)
		}
		if !result.Completed() && (adapter == nil || !adapter.SigninIsLogin) {
			login := v.loginResult(ctx, site, adapter)
			if login.Success {
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
