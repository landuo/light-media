package app

import (
	"context"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base32"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"light-media/internal/browserrender"
	"light-media/internal/plugins"

	"github.com/chromedp/cdproto/storage"
	"github.com/chromedp/chromedp"
)

// MoviePilot app/helper/cookie.py selects the first XPath that matches, even
// when a different variant is visible earlier in document order.
var originalSiteCookieForm = map[string][]string{
	"username": {`//input[@name="username"]`, `//input[@id="form_item_username"]`, `//input[@id="username"]`, `//input[contains(@placeholder,"用户名")]`},
	"password": {`//input[@name="password"]`, `//input[@id="form_item_password"]`, `//input[@id="password"]`, `//input[@type="password"]`},
	"captcha":  {`//input[@name="imagestring"]`, `//input[@name="captcha"]`, `//input[@id="form_item_captcha"]`, `//input[@placeholder="驗證碼"]`},
	"image":    {`//img[@alt="captcha"]/@src`, `//img[@alt="CAPTCHA"]/@src`, `//img[@alt="SECURITY CODE"]/@src`, `//img[@id="LAY-user-get-vercode"]/@src`, `//img[contains(@src,"/api/getCaptcha")]/@src`},
	"submit":   {`//input[@type="submit"]`, `//button[@type="submit"]`, `//button[@lay-filter="login"]`, `//button[@lay-filter="formLogin"]`, `//input[@type="button"][@value="登录"]`, `//input[@id="submit-btn"]`},
	"remember": {`//input[@type="checkbox"][contains(@name,"remember") or contains(@id,"remember")]`, `//*[@role="checkbox"][contains(.,"保持登录") or contains(.,"记住我") or contains(.,"自动登录")]`},
	"otp":      {`//input[@name="two_step_code"]`, `//input[@name="2fa_secret"]`, `//input[@name="otp"]`},
}

const originalSiteCookieLoggedIn = `(() => {
	const has = path => document.evaluate(path, document, null, XPathResult.FIRST_ORDERED_NODE_TYPE).singleNodeValue !== null;
	return !has('//input[@type="password"]') && has('//a[contains(@href,"logout") or contains(@data-url,"logout") or contains(@href,"mybonus") or contains(@onclick,"logout") or contains(@href,"usercp") or contains(@lay-on,"logout")] | //form[contains(@action,"logout")] | //div[@class="user-info-side"] | //a[@id="myitem"]');
})()`

const originalSiteCookieErrorPath = `//table[@class='main']//td[@class='text']/text()`

type originalSiteCookieFailure string

func (failure originalSiteCookieFailure) Error() string { return string(failure) }

func (a *App) originalSiteCookieLogin(ctx context.Context, site SiteConfig, username, password, twoFA string) (cookie, ua string, err error) {
	base, err := url.Parse(site.BaseURL)
	if err != nil || base.Hostname() == "" || base.User != nil || base.Opaque != "" || base.RawQuery != "" || base.ForceQuery || base.Fragment != "" ||
		(base.Scheme != "https" && base.Scheme != "http") || username == "" || password == "" {
		return "", "", bad("SITE_COOKIE_LOGIN_FAILED", "参数错误")
	}
	proxy := a.Settings().ProxyHost
	key := a.managedSiteKey(site)
	guard := func(ctx context.Context) error {
		current, err := a.siteForRequest(allowDisabledSiteRequests(ctx), site.ID)
		if err != nil || a.managedSiteKey(current) != key || current.Proxy != site.Proxy || current.Timeout != site.Timeout ||
			(site.Proxy && a.Settings().ProxyHost != proxy) {
			return bad("SITE_CONFIGURATION_CHANGED", "站点配置已变化")
		}
		return ctx.Err()
	}
	if err := guard(ctx); err != nil {
		return "", "", err
	}
	timeout := 60 * time.Second
	if site.Timeout != 0 {
		if site.Timeout < 0 || int64(site.Timeout) > int64((1<<63-1)/int64(time.Second)) {
			return "", "", bad("SITE_COOKIE_LOGIN_FAILED", "站点等待秒数无效")
		}
		timeout = time.Duration(site.Timeout) * time.Second
	}
	request := browserrender.Request{URL: base.String(), Timeout: timeout, Guard: guard}
	if site.Proxy {
		request.SourceProxy, err = originalSourceBrowserProxy(proxy, base)
		if err != nil {
			return "", "", bad("SITE_COOKIE_LOGIN_FAILED", "站点代理配置无效")
		}
	}
	config := browserrender.ConfigFromEnvironment(os.Getenv)
	config.Emulation, config.FlareSolverrURL = a.originalHostBrowserEmulation, a.originalHostFlareSolverrURL
	config.Humanize, config.HumanPreset = a.originalHostBrowserHumanize, a.originalHostBrowserHumanPreset
	config.TempDir = filepath.Join(a.Dir, "work", "browser")
	if err := os.MkdirAll(config.TempDir, 0700); err != nil {
		return "", "", bad("SITE_COOKIE_LOGIN_FAILED", "无法创建登录浏览器目录")
	}
	ocr := func(page context.Context, image string) string {
		currentCookie, agent, err := originalSiteCookieState(page)
		if err != nil {
			return ""
		}
		return a.originalSiteCookieCaptcha(page, base, image, currentCookie, agent, guard)
	}
	err = config.CredentialAction(ctx, request, func(page context.Context, idle func(context.Context, time.Duration) error) error {
		if err := originalSiteCookieSubmit(page, idle, username, password, twoFA, ocr); err != nil {
			return err
		}
		for attempt := 0; attempt < 3; attempt++ {
			if attempt != 0 && !originalSiteCookieWait(page, 2*time.Second) {
				return page.Err()
			}
			var loggedIn bool
			if err := chromedp.Run(page, chromedp.Evaluate(originalSiteCookieLoggedIn, &loggedIn)); err == nil && loggedIn {
				cookie, ua, err = originalSiteCookieState(page)
				if err == nil && cookie == "" {
					return originalSiteCookieFailure("登录页面未返回 Cookie")
				}
				return err
			}
			if originalSiteCookieXPath(page, []string{originalSiteCookieErrorPath}) != "" {
				break
			}
		}
		// Untrusted tracker error bodies can echo login inputs. Never expose
		// the text or raw Chromium errors through chat or application logs.
		return originalSiteCookieFailure("登录失败")
	})
	if err != nil {
		var stale *browserrender.GuardError
		if errors.As(err, &stale) {
			return "", "", stale.Cause
		}
		if revoked := guard(ctx); revoked != nil {
			return "", "", revoked
		}
		var failure originalSiteCookieFailure
		if errors.As(err, &failure) {
			return "", "", bad("SITE_COOKIE_LOGIN_FAILED", failure.Error())
		}
		var browserError *browserrender.Error
		if errors.As(err, &browserError) {
			switch browserError.Code {
			case "browser_networkidle_failed":
				return "", "", bad("SITE_COOKIE_LOGIN_FAILED", "登录页面未在时限内完成加载")
			case "browser_navigation_failed":
				return "", "", bad("SITE_COOKIE_LOGIN_FAILED", "无法打开站点登录页面")
			case "browser_display_unavailable", "browser_display_failed", "browser_launch_failed":
				return "", "", bad("SITE_COOKIE_LOGIN_FAILED", "登录浏览器启动失败")
			case "browser_executable_unavailable":
				return "", "", bad("SITE_COOKIE_LOGIN_FAILED", "登录浏览器不可用")
			case "browser_navigation_origin_denied":
				return "", "", bad("SITE_COOKIE_LOGIN_FAILED", "登录请求不能跨站点来源")
			}
		}
		return "", "", bad("SITE_COOKIE_LOGIN_FAILED", "仿真登录失败")
	}
	return cookie, ua, nil
}

func originalSiteCookieSubmit(page context.Context, idle func(context.Context, time.Duration) error, username, password, twoFA string, ocr func(context.Context, string) string) error {
	userPath := originalSiteCookieXPath(page, originalSiteCookieForm["username"])
	if userPath == "" {
		wait, cancel := context.WithTimeout(page, 5*time.Second)
		_ = chromedp.Run(wait, chromedp.WaitReady(strings.Join(originalSiteCookieForm["username"], " | "), chromedp.BySearch))
		cancel()
		userPath = originalSiteCookieXPath(page, originalSiteCookieForm["username"])
	}
	if userPath == "" {
		return originalSiteCookieFailure("未找到用户名输入框")
	}
	passPath := originalSiteCookieXPath(page, originalSiteCookieForm["password"])
	if passPath == "" {
		return originalSiteCookieFailure("未找到密码输入框")
	}
	code := originalSiteCookieOTP(twoFA, time.Now())
	captchaPath := originalSiteCookieXPath(page, originalSiteCookieForm["captcha"])
	var image string
	if captchaPath != "" {
		imagePath := originalSiteCookieXPath(page, originalSiteCookieForm["image"])
		if imagePath == "" || chromedp.Run(page, chromedp.Evaluate(`(`+originalSiteCookieNode(imagePath)+`).value`, &image)) != nil || image == "" {
			return originalSiteCookieFailure("未找到验证码图片")
		}
	}
	submit := originalSiteCookieXPath(page, originalSiteCookieForm["submit"])
	if submit == "" {
		return originalSiteCookieFailure("未找到登录按钮")
	}
	form, cancel := context.WithTimeout(page, 30*time.Second)
	defer cancel()
	if err := chromedp.Run(form, chromedp.WaitVisible(submit, chromedp.BySearch), originalSiteCookieFill(userPath, username), originalSiteCookieFill(passPath, password)); err != nil {
		return err
	}
	for _, remember := range originalSiteCookieForm["remember"] {
		var unchecked *bool
		if err := chromedp.Run(form, chromedp.Evaluate(`(() => {const e=`+originalSiteCookieNode(remember)+`;return e ? (e.getAttribute('aria-checked') ?? String(e.checked)) !== 'true' : null;})()`, &unchecked)); err != nil || unchecked == nil {
			continue
		}
		if !*unchecked {
			break
		}
		click, done := context.WithTimeout(form, 3*time.Second)
		err := chromedp.Run(click, chromedp.Click(remember, chromedp.BySearch))
		done()
		if err == nil {
			break
		}
	}
	if code != "" {
		if otp := originalSiteCookieXPath(form, originalSiteCookieForm["otp"]); otp != "" {
			if err := chromedp.Run(form, originalSiteCookieFill(otp, code)); err != nil {
				return err
			}
		}
	}
	var visible bool
	if captchaPath != "" && chromedp.Run(form, chromedp.Evaluate(`(() => {const e=`+originalSiteCookieNode(captchaPath)+`;return e && e.getClientRects().length > 0 && getComputedStyle(e).visibility !== 'hidden';})()`, &visible)) == nil && visible {
		captcha := ocr(page, image)
		if captcha == "" {
			return originalSiteCookieFailure("验证码识别失败")
		}
		if err := chromedp.Run(form, originalSiteCookieFill(captchaPath, captcha)); err != nil {
			return err
		}
	}
	if err := chromedp.Run(form, chromedp.Click(submit, chromedp.BySearch)); err != nil {
		return err
	}
	if err := idle(page, 30*time.Second); err != nil {
		return err
	}
	var location string
	if err := chromedp.Run(page, chromedp.Location(&location)); err != nil {
		return err
	}
	if !strings.Contains(location, "verify") {
		return nil
	}
	if code == "" {
		return originalSiteCookieFailure("需要二次验证码")
	}
	if otp := originalSiteCookieXPath(page, originalSiteCookieForm["otp"]); otp != "" {
		verify, done := context.WithTimeout(page, 30*time.Second)
		defer done()
		if err := chromedp.Run(verify, originalSiteCookieFill(otp, originalSiteCookieOTP(twoFA, time.Now())), chromedp.Click(submit, chromedp.BySearch)); err != nil {
			return originalSiteCookieFailure("二次验证码输入失败")
		}
		return idle(page, 30*time.Second)
	}
	return nil
}

func originalSiteCookieNode(path string) string {
	encoded, _ := json.Marshal(path)
	return `document.evaluate(` + string(encoded) + `, document, null, XPathResult.FIRST_ORDERED_NODE_TYPE).singleNodeValue`
}

func originalSiteCookieXPath(page context.Context, paths []string) string {
	for _, path := range paths {
		var found bool
		if err := chromedp.Run(page, chromedp.Evaluate(originalSiteCookieNode(path)+` !== null`, &found)); err == nil && found {
			return path
		}
	}
	return ""
}

func originalSiteCookieFill(path, value string) chromedp.Action {
	encoded, _ := json.Marshal(value)
	return chromedp.Tasks{
		chromedp.WaitVisible(path, chromedp.BySearch),
		chromedp.Evaluate(`(() => {const e=`+originalSiteCookieNode(path)+`;if (!e || e.disabled || e.readOnly) throw new Error('input unavailable');e.focus();Object.getOwnPropertyDescriptor(HTMLInputElement.prototype,'value').set.call(e,`+string(encoded)+`);e.dispatchEvent(new Event('input',{bubbles:true,composed:true}));e.dispatchEvent(new Event('change',{bubbles:true}));})()`, nil),
	}
}

func originalSiteCookieState(page context.Context) (cookie, agent string, err error) {
	err = chromedp.Run(page, chromedp.ActionFunc(func(ctx context.Context) error {
		items, err := storage.GetCookies().Do(ctx)
		if err != nil {
			return err
		}
		var header strings.Builder
		for _, item := range items {
			if (&http.Cookie{Name: item.Name, Value: item.Value}).Valid() == nil {
				header.WriteString(item.Name + "=" + item.Value + "; ")
			}
		}
		cookie = header.String()
		return nil
	}), chromedp.Evaluate(`navigator.userAgent`, &agent))
	return cookie, agent, err
}

func originalSiteCookieWait(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-ctx.Done():
		return false
	}
}

func originalSiteCookieOTP(value string, now time.Time) string {
	if utf8.RuneCountInString(value) < 16 {
		return value
	}
	key, err := base32.StdEncoding.DecodeString(value)
	if err != nil || strings.ContainsAny(value, "\r\n") {
		return ""
	}
	var counter [8]byte
	binary.BigEndian.PutUint64(counter[:], uint64(now.Unix()/30))
	mac := hmac.New(sha1.New, key)
	_, _ = mac.Write(counter[:])
	code := mac.Sum(nil)
	index := code[19] & 15
	result := strconv.FormatUint(uint64(binary.BigEndian.Uint32(code[index:index+4])&0x7fffffff)%1000000, 10)
	if len(result) == 5 {
		return "0" + result
	}
	return result
}

func (a *App) originalSiteCookieCaptcha(ctx context.Context, base *url.URL, image, cookie, agent string, guard func(context.Context) error) string {
	imageURL, err := url.Parse(image)
	if err != nil || imageURL.User != nil || imageURL.Opaque != "" || imageURL.Fragment != "" {
		return ""
	}
	root := &url.URL{Scheme: base.Scheme, Host: base.Host, Path: "/"}
	imageURL = root.ResolveReference(imageURL)
	if originalOCROrigin(imageURL) != originalOCROrigin(base) {
		return ""
	}
	cookie, err = originalOCRHeaderLatin1(originalOCRCookieHeader(cookie))
	if err != nil {
		return ""
	}
	agent, err = originalOCRHeaderLatin1(agent)
	if err != nil {
		return ""
	}
	response, err := originalOCRRequest(ctx, http.MethodGet, imageURL, http.Header{"Cookie": {cookie}, "User-Agent": {agent}}, nil, guard, originalOCRImageLimit, true)
	if err != nil || response == nil || response.status >= 400 || len(response.body) == 0 {
		return ""
	}
	target, err := url.Parse(a.originalHostOCRHost + "/captcha/base64")
	if err != nil || target.Host == "" || (target.Scheme != "http" && target.Scheme != "https") {
		return ""
	}
	payload, _ := json.Marshal(map[string]string{"base64_img": base64.StdEncoding.EncodeToString(response.body)})
	response, err = originalOCRRequest(ctx, http.MethodPost, target, http.Header{"Content-Type": {"application/json"}, "User-Agent": {originalOCRDefaultUserAgent}}, payload, guard, plugins.MaxPayloadBytes, true)
	if err != nil || response == nil || response.status >= 400 {
		return ""
	}
	var value struct {
		Result string `json:"result"`
	}
	if json.Unmarshal(response.body, &value) != nil {
		return ""
	}
	return value.Result
}
