package app

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"strings"
	"time"
	"unicode"

	"light-media/internal/networkproxy"
	"light-media/internal/plugins"
	"light-media/internal/releasemeta/legacyanime"
)

// Ordinary CAPTCHA OCR only. This helper has no model, Agent or workflow API.
func originalHostOCRStartupHost(lookup func(string) (string, bool)) string {
	if value, present := lookup("OCR_HOST"); present {
		return value
	}
	return "https://movie-pilot.org"
}
func originalHostOCRCapability(method string) bool { return method == plugins.CaptchaOCRTextCapability }

const originalOCRImageLimit = plugins.MaxPayloadBytes / 4 * 3

// Bound installed requests/urllib3 ordinary headers; this is a wire value,
// not a Python runtime dependency. See wire-headers-oracle.json.
const originalOCRDefaultUserAgent = "python-urllib3/2.7.0"

func originalOCRDataURL(value string) string {
	value = legacyanime.Strip(value)
	if !strings.HasPrefix(strings.ToLower(value), "data:image/") {
		return ""
	}
	metadata, data, ok := strings.Cut(value, ",")
	if !ok || !strings.Contains(strings.ToLower(metadata), ";base64") {
		return ""
	}
	return legacyanime.Strip(data)
}
func originalOCRNormalize(value string) string {
	if value == "" {
		return ""
	}
	if extracted := originalOCRDataURL(value); extracted != "" {
		value = extracted
	}
	value = strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) || r >= 0x1c && r <= 0x1f {
			return -1
		}
		return r
	}, value)
	if value != "" && len([]rune(value))%4 != 0 {
		value += strings.Repeat("=", 4-len([]rune(value))%4)
	}
	return value
}

// Unsupported URL schemes fail inside source RequestUtils and leave a supplied
// base64 fallback untouched. No network or site credential lookup is needed.
// RequestUtils.cookie_parse uses an insertion-ordered dictionary. It decodes
// ASCII percent escapes, keeps '+' literal, and preserves the entire original
// value if Latin-1 unquote -> UTF-8 quote -> Latin-1 unquote is not reversible.
func originalOCRCookieHeader(raw string) string {
	names := []string{}
	values := map[string]string{}
	for _, part := range strings.Split(raw, ";") {
		name, value, ok := strings.Cut(part, "=")
		if !ok {
			continue
		}
		name, value = legacyanime.Strip(name), legacyanime.Strip(value)
		candidate := []byte{}
		ascii := true
		for pos := 0; pos < len(value); pos++ {
			b := value[pos]
			if b == '%' && pos+2 < len(value) {
				digit := func(c byte) (byte, bool) {
					switch {
					case c >= '0' && c <= '9':
						return c - '0', true
					case c >= 'a' && c <= 'f':
						return c - 'a' + 10, true
					case c >= 'A' && c <= 'F':
						return c - 'A' + 10, true
					}
					return 0, false
				}
				hi, ok1 := digit(value[pos+1])
				lo, ok2 := digit(value[pos+2])
				if ok1 && ok2 {
					b = hi*16 + lo
					pos += 2
				}
			}
			if b > 127 {
				ascii = false
			}
			candidate = append(candidate, b)
		}
		if ascii {
			value = string(candidate)
		}
		if _, present := values[name]; !present {
			names = append(names, name)
		}
		values[name] = value
	}
	parts := make([]string, 0, len(names))
	for _, name := range names {
		parts = append(parts, name+"="+values[name])
	}
	return strings.Join(parts, "; ")
}
func originalOCRHeaderLatin1(value string) (string, error) {
	if strings.ContainsAny(value, "\r\n") {
		return "", bad("OCR_HEADER_INVALID", "验证码请求头包含无效换行")
	}
	raw := make([]byte, 0, len(value))
	for _, r := range value {
		if r > 255 {
			return "", bad("OCR_HEADER_INVALID", "验证码请求头无法编码为Latin-1")
		}
		raw = append(raw, byte(r))
	}
	return string(raw), nil
}

func originalOCRNeedsImageGET(value, siteID string) bool {
	u, err := url.Parse(value)
	if err != nil {
		return false
	}
	if u.Scheme == "http" || u.Scheme == "https" {
		return u.Host != ""
	}
	return u.Scheme == "" && siteID != ""
}

func originalOCROrigin(u *url.URL) string {
	port := u.Port()
	if port == "" {
		if u.Scheme == "https" {
			port = "443"
		} else {
			port = "80"
		}
	}
	return strings.ToLower(u.Scheme) + "://" + strings.ToLower(u.Hostname()) + ":" + port
}

type originalOCRGuardTransport struct {
	next  http.RoundTripper
	guard func(context.Context) error
}

func (t originalOCRGuardTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if err := t.guard(r.Context()); err != nil {
		return nil, originalSiteRequestGuardFailure(err)
	}
	return t.next.RoundTrip(r)
}

type originalOCRResponse struct {
	status      int
	body        []byte
	contentType string
}

// RequestUtils in OcrHelper supplies no explicit proxy map. Its ordinary
// requests trust-env routing is retained, separately for image GET and OCR POST.
func originalOCRRequest(ctx context.Context, method string, target *url.URL, headers http.Header, body []byte, guard func(context.Context) error, limit int, sourceMode ...bool) (*originalOCRResponse, error) {
	if err := guard(ctx); err != nil {
		return nil, err
	}
	transport, err := networkproxy.NewRequestsProxyTransport(networkproxy.NewRequestsTimeoutTransport(20*time.Second), os.Environ(), nil, target)
	if err != nil {
		return nil, nil
	}
	defer transport.CloseIdleConnections()
	// requests.request creates a temporary Session for this request, retaining
	// redirect cookies without sharing image cookies with the separate OCR POST.
	jar, _ := cookiejar.New(nil)
	var next http.RoundTripper = originalOCRGuardTransport{transport, guard}
	if len(sourceMode) == 1 && sourceMode[0] {
		next = originalSourceSiteHTTPTransport{next: next}
	}
	client := &http.Client{Jar: jar, Transport: next, CheckRedirect: func(r *http.Request, via []*http.Request) error {
		if err := guard(r.Context()); err != nil {
			return originalSiteRequestGuardFailure(err)
		}
		if len(via) > 30 {
			return errors.New("ocr_redirect_limit")
		}
		if originalOCROrigin(r.URL) != originalOCROrigin(target) {
			return bad("OCR_TARGET_CHANGED", "验证码请求不能跨来源跳转")
		}
		// RequestUtils passes configured cookies as an unscoped cookie jar.
		// A same-name Set-Cookie adds a scoped cookie rather than replacing
		// that original entry. Restore the original header before Go adds the
		// response cookies, preserving the paired requests wire behavior.
		if configured := headers.Get("Cookie"); configured != "" {
			r.Header.Set("Cookie", configured)
		}
		return nil
	}}
	request, err := http.NewRequestWithContext(ctx, method, target.String(), bytes.NewReader(body))
	if err != nil {
		return nil, nil
	}
	request.Header = headers.Clone()
	request.Header.Set("Accept-Encoding", "gzip, deflate, br")
	request.Header.Set("Connection", "keep-alive")
	response, requestErr := client.Do(request)
	if response != nil {
		defer response.Body.Close()
	}
	var denied *originalSiteRequestGuardError
	if errors.As(requestErr, &denied) {
		return nil, denied.cause
	}
	if err := guard(ctx); err != nil {
		return nil, err
	}
	if requestErr != nil {
		return nil, nil
	}
	value, err := io.ReadAll(io.LimitReader(response.Body, int64(limit)+1))
	if err != nil {
		if err := guard(ctx); err != nil {
			return nil, err
		}
		return nil, nil
	}
	if len(value) > limit {
		return nil, bad("PLUGIN_VALUE_TOO_LARGE", "验证码请求超过宿主数据上限")
	}
	decoded, decodeErr := originalHostNetworkDecode(value, response.Header.Get("Content-Encoding"))
	if decodeErr != nil {
		var bound *APIError
		if errors.As(decodeErr, &bound) && bound.Code == "PLUGIN_VALUE_TOO_LARGE" {
			return nil, decodeErr
		}
		return nil, nil
	}
	if len(decoded) > limit {
		return nil, bad("PLUGIN_VALUE_TOO_LARGE", "验证码解码超过宿主数据上限")
	}
	if err := guard(ctx); err != nil {
		return nil, err
	}
	return &originalOCRResponse{status: response.StatusCode, body: decoded, contentType: strings.Join(response.Header.Values("Content-Type"), ", ")}, nil
}

func (a *App) originalHostOCRResource(ctx context.Context, scope originalBaseCallScope, method string, raw json.RawMessage) (json.RawMessage, error) {
	if method != plugins.CaptchaOCRTextCapability {
		return nil, bad("PLUGIN_CAPABILITY_DENIED", "验证码识别方法不可用")
	}
	var in plugins.CaptchaOCRRequestV1
	if err := originalResourceDecodeBounded(raw, &in, plugins.MaxPayloadBytes); err != nil {
		return nil, err
	}
	if len(in.ImageBase64) > plugins.MaxPayloadBytes || len(in.ImageURL) > plugins.MaxPayloadBytes {
		return nil, bad("PLUGIN_VALUE_TOO_LARGE", "验证码输入超过SDK数据上限")
	}
	guard := func(ctx context.Context) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if scope.validate == nil {
			return bad("PLUGIN_CALL_CLOSED", "验证码调用已关闭")
		}
		return scope.validate(ctx)
	}
	if err := guard(ctx); err != nil {
		return nil, err
	}
	var source *originalResolvedSourceSite
	if in.SourceReference != nil {
		if in.SiteID != "" {
			return nil, bad("INVALID_PLUGIN_VALUE", "验证码源引用不能混用站点ID")
		}
		resolved, err := a.resolveOriginalSourceSite(ctx, scope, *in.SourceReference, false)
		if err != nil {
			return nil, err
		}
		source = &resolved
		previousGuard := guard
		guard = func(ctx context.Context) error {
			if err := previousGuard(ctx); err != nil {
				return err
			}
			return source.guard(ctx)
		}
	}
	result := plugins.CaptchaOCRResultV1{SchemaVersion: 1, Result: json.RawMessage(`""`)}
	encoded := originalOCRNormalize(in.ImageBase64)
	if in.ImageURL != "" {
		if data := originalOCRDataURL(in.ImageURL); data != "" {
			encoded = originalOCRNormalize(data)
		} else if originalOCRNeedsImageGET(in.ImageURL, in.SiteID) {
			var target *url.URL
			var configuredUA, configuredCookie string
			if source != nil {
				var err error
				target, configuredUA, configuredCookie, err = originalSourceOCRImage(*source, in.ImageURL)
				if err != nil {
					return nil, err
				}
			} else {
				if in.SiteID == "" {
					return nil, bad("SITE_NOT_CONFIGURED", "验证码图片URL需要已配置站点ID")
				}
				site, err := a.siteForRequest(allowDisabledSiteRequests(ctx), in.SiteID)
				if err != nil {
					return nil, err
				}
				origin, err := nativeConfiguredSiteOrigin(site)
				if err != nil {
					return nil, err
				}
				identity, err := a.Store.nativeSiteByAppID(ctx, site.ID)
				if err != nil || identity.State != "active" || identity.CanonicalOrigin != origin {
					return nil, siteIdentityError()
				}
				base, _ := url.Parse(origin)
				target, err = url.Parse(in.ImageURL)
				if err != nil {
					return nil, bad("INVALID_PLUGIN_VALUE", "验证码图片URL无效")
				}
				target = base.ResolveReference(target)
				if target.User != nil || target.Fragment != "" || target.Scheme != "http" && target.Scheme != "https" || originalOCROrigin(target) != originalOCROrigin(base) {
					return nil, bad("PLUGIN_CAPABILITY_DENIED", "验证码图片不属于当前站点")
				}
				fingerprint := a.managedSiteKey(site)
				previousGuard := guard
				guard = func(ctx context.Context) error {
					if err := previousGuard(ctx); err != nil {
						return err
					}
					current, err := a.siteForRequest(allowDisabledSiteRequests(ctx), site.ID)
					if err != nil {
						return err
					}
					if a.managedSiteKey(current) != fingerprint {
						return bad("SITE_CHANGED", "验证码站点配置已变化")
					}
					return nil
				}
				configuredUA, configuredCookie = site.UserAgent, site.Cookie
				if configuredUA == "" {
					configuredUA = originalOCRDefaultUserAgent
				}
			}
			userAgent, err := originalOCRHeaderLatin1(configuredUA)
			if err != nil {
				return nil, err
			}
			headers := http.Header{"Content-Type": []string{"application/x-www-form-urlencoded; charset=UTF-8"}, "User-Agent": []string{userAgent}}
			if configuredCookie != "" {
				cookie, err := originalOCRHeaderLatin1(originalOCRCookieHeader(configuredCookie))
				if err != nil {
					return nil, err
				}
				if cookie != "" {
					headers.Set("Cookie", cookie)
				}
			}
			response, err := originalOCRRequest(ctx, http.MethodGet, target, headers, nil, guard, originalOCRImageLimit, source != nil)
			if err != nil {
				return nil, err
			}
			if response != nil {
				// Source uses response is not None, including non-200 image bytes.
				if len(response.body) == 0 {
					return json.Marshal(result)
				}
				encoded = base64.StdEncoding.EncodeToString(response.body)
			}
		}
	}
	if encoded == "" {
		return json.Marshal(result)
	}
	if len(encoded) > plugins.MaxPayloadBytes {
		return nil, bad("PLUGIN_VALUE_TOO_LARGE", "验证码编码超过SDK数据上限")
	}
	target, err := url.Parse(a.originalHostOCRHost + "/captcha/base64")
	if err != nil || target.Host == "" || target.Scheme != "http" && target.Scheme != "https" {
		return json.Marshal(result)
	}
	// OCR service credentials, if explicitly configured in its URL, stay on the
	// host and can never be copied from site Cookie/UA or returned through SDK.
	headers := http.Header{"Content-Type": []string{"application/json"}, "User-Agent": []string{originalOCRDefaultUserAgent}}
	payload, _ := json.Marshal(map[string]string{"base64_img": encoded})
	if source != nil {
		payload, err = originalSiteHTTPJSON(payload)
		if err != nil {
			return nil, err
		}
	}
	response, err := originalOCRRequest(ctx, http.MethodPost, target, headers, payload, guard, plugins.MaxPayloadBytes, source != nil)
	if err != nil {
		return nil, err
	}
	if response == nil || response.status >= 400 {
		return json.Marshal(result)
	}
	if source != nil {
		result.SourceResponse = &plugins.CaptchaOCRSourceResponseV1{Content: response.body, ContentType: response.contentType}
		return json.Marshal(result)
	}
	decoded, err := decodeSourceJSON(response.body)
	if err != nil {
		return nil, bad("OCR_RESPONSE_INVALID", "验证码服务响应不是有效JSON")
	}
	fields, ok := decoded.(map[string]any)
	if !ok {
		return nil, bad("OCR_RESPONSE_INVALID", "验证码服务响应不是对象")
	}
	if sourceSubscriptionTruth(fields["result"]) {
		result.Result = mustCleanSeedJSON(fields["result"])
	}
	return json.Marshal(result)
}
