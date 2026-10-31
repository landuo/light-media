package app

import (
	"context"
	"encoding/json"
	"errors"
	"light-media/internal/networkproxy"
	"light-media/internal/ocr"
	"light-media/internal/plugins"
	"light-media/internal/plugins/autosignin"
	"light-media/internal/plugins/iyuuautoseed/compat/urlprep"
	"net"
	"net/url"
	"os"
	"strings"
	"unicode"
	"unicode/utf8"
)

// The source class captures settings.OCR_HOST once. Config string conversion
// strips Python whitespace, but explicit empty and a trailing slash survive.
func OCRHostEnvironment(lookup func(string) (string, bool)) string {
	value, present := lookup("OCR_HOST")
	if !present {
		return ocr.DefaultHost
	}
	return strings.TrimFunc(value, func(r rune) bool { return unicode.IsSpace(r) || r >= '\x1c' && r <= '\x1f' })
}

var originalStartupOCRHost = OCRHostEnvironment(os.LookupEnv)

func originalSharedOCRCapability(method string) bool {
	return method == plugins.SharedOCRGetCaptchaText
}
func originalOCROrigin(address string) (string, error) {
	u, err := url.Parse(address)
	if err != nil || u.Host == "" || u.User != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return "", autosignin.ErrSiteTargetDenied
	}
	port := u.Port()
	if port == "" {
		if u.Scheme == "https" {
			port = "443"
		} else {
			port = "80"
		}
	}
	return u.Scheme + "://" + net.JoinHostPort(strings.ToLower(u.Hostname()), port), nil
}
func (a *App) originalSharedOCRResource(ctx context.Context, scope originalBaseCallScope, method string, raw json.RawMessage) (json.RawMessage, error) {
	if !originalSharedOCRCapability(method) || scope.validate == nil {
		return nil, bad("PLUGIN_CAPABILITY_DENIED", "OCR宿主方法未获授权")
	}
	if err := scope.validate(ctx); err != nil {
		return nil, err
	}
	if !utf8.Valid(raw) || !sharedHistoryScalarEscapes(raw) {
		return nil, bad("INVALID_PLUGIN_VALUE", "OCR参数必须是有效Unicode字符串")
	}
	var request plugins.OCRRequest
	if err := originalResourceDecode(raw, &request); err != nil {
		return nil, err
	}
	client := ocr.Client{Endpoint: a.originalOCRHost + "/captcha/base64", Transport: a.originalOCRTransport(scope)}
	value, err := client.GetCaptchaText(ctx, ocr.Input{ImageURL: request.ImageURL, ImageBase64: request.ImageBase64, Cookie: request.Cookie, UserAgent: request.UserAgent})
	if guardErr := scope.validate(ctx); guardErr != nil {
		return nil, guardErr
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	reply := plugins.OCRReply{}
	if err != nil {
		var source *ocr.SourceError
		if !errors.As(err, &source) {
			return nil, err
		}
		reply.SourceError = source.Class
	} else {
		reply.Value = &value
	}
	out, err := json.Marshal(reply)
	if err != nil {
		return nil, err
	}
	if len(out) > plugins.MaxPayloadBytes {
		return nil, bad("PLUGIN_VALUE_TOO_LARGE", "OCR结果超过共享传输上限")
	}
	return out, nil
}

// RequestUtils validates string header syntax before urllib3/http.client
// Latin-1 encoding. InvalidHeader is caught as None; UnicodeEncodeError is not.
func originalOCRUserAgent(value string) (string, bool, error) {
	if value != "" {
		first, _ := utf8.DecodeRuneInString(value)
		if unicode.IsSpace(first) || (first >= '\x1c' && first <= '\x1f') || strings.ContainsAny(value, "\r\n") {
			return "", false, nil
		}
	}
	raw := make([]byte, 0, len(value))
	for _, point := range value {
		if point > 255 {
			return "", false, &ocr.SourceError{Class: "UnicodeEncodeError"}
		}
		raw = append(raw, byte(point))
	}
	return string(raw), true, nil
}
func originalOCRCookieEncoding(raw string) error {
	strip := func(value string) string {
		return strings.TrimFunc(value, func(point rune) bool { return unicode.IsSpace(point) || point >= '\x1c' && point <= '\x1f' })
	}
	// cookie_parse ignores fields without '=' and overwrites duplicate keys.
	// Its conditional URL decoding can only change an ASCII encoded value into
	// ASCII; non-Latin original data must be checked after the same selection.
	parsed := map[string]string{}
	for _, part := range strings.Split(raw, ";") {
		pair := strings.SplitN(part, "=", 2)
		if len(pair) == 2 {
			parsed[strip(pair[0])] = strip(pair[1])
		}
	}
	for name, value := range parsed {
		for _, point := range name + value {
			if point > 255 {
				return &ocr.SourceError{Class: "UnicodeEncodeError"}
			}
		}
	}
	return nil
}
func (a *App) originalOCRTransport(scope originalBaseCallScope) ocr.Transport {
	return func(ctx context.Context, request ocr.Request) (*ocr.Response, error) {
		guard := func(ctx context.Context) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			return scope.validate(ctx)
		}
		if err := guard(ctx); err != nil {
			return nil, err
		}
		prepared, err := urlprep.Prepare(request.URL)
		if err != nil {
			switch urlprep.ErrorClass(err) {
			case "UnicodeEncodeError", "UnicodeDecodeError", "ValueError":
				return nil, &ocr.SourceError{Class: urlprep.ErrorClass(err)}
			}
			return nil, nil // RequestUtils catches request URL preparation errors.
		}
		u, err := url.Parse(prepared)
		if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
			return nil, nil
		}
		if u.User != nil {
			return nil, bad("OCR_TARGET_DENIED", "OCR地址不能携带登录凭据")
		}
		origin, err := originalOCROrigin(prepared)
		if err != nil {
			return nil, err
		}
		cookie := ""
		var userAgent any
		if request.Method == "GET" {
			if request.Cookie != nil {
				cookie = *request.Cookie
			}
			if request.UserAgent != nil {
				agent := *request.UserAgent
				if agent == originalPluginServerUserAgent() {
					agent += " Plugin/" + plugins.OriginalPluginLower(scope.installation.PluginID)
				}
				userAgent = agent
			}
		}
		if cookie != "" {
			var snapshot *SiteConfig
			a.cfgMu.RLock()
			for _, site := range a.config.Sites {
				current, e := originalOCROrigin(site.BaseURL)
				if e == nil && current == origin {
					if snapshot != nil {
						a.cfgMu.RUnlock()
						return nil, bad("OCR_TARGET_DENIED", "OCR站点授权不唯一")
					}
					copySite := site
					snapshot = &copySite
				}
			}
			a.cfgMu.RUnlock()
			if snapshot == nil {
				return nil, bad("OCR_TARGET_DENIED", "携带Cookie的OCR图片请求需要已配置站点")
			}
			baseGuard := guard
			guard = func(ctx context.Context) error {
				if err := baseGuard(ctx); err != nil {
					return err
				}
				a.cfgMu.RLock()
				defer a.cfgMu.RUnlock()
				for _, site := range a.config.Sites {
					if site.ID == snapshot.ID && site.Config == snapshot.Config && site.Enabled == snapshot.Enabled {
						return nil
					}
				}
				return bad("OCR_SITE_CHANGED", "OCR站点授权已变化")
			}
		}
		if err := guard(ctx); err != nil {
			return nil, err
		}
		if request.Method == "GET" {
			if agent, ok := userAgent.(string); ok {
				encoded, valid, encodingErr := originalOCRUserAgent(agent)
				if encodingErr != nil {
					return nil, encodingErr
				}
				if !valid {
					return nil, nil
				}
				userAgent = encoded
			}
			if encodingErr := originalOCRCookieEncoding(cookie); encodingErr != nil {
				return nil, encodingErr
			}
		}
		binding := autosignin.SiteHTTPBinding{URL: prepared, Cookie: cookie, UserAgent: userAgent, RequestsProxy: &autosignin.RequestsProxyPolicy{Environment: os.Environ()}, BeforeRequest: guard}
		transport, err := autosignin.BindSiteHTTP(binding)
		if err != nil {
			return nil, err
		}
		response, err := transport.DoOrdinaryOCR(ctx, autosignin.OrdinaryOCRRequest{Method: request.Method, URL: prepared, Base64Image: request.Base64Image})
		if guardErr := guard(ctx); guardErr != nil {
			return nil, guardErr
		}
		if errors.Is(err, networkproxy.ErrRequestsProxyAuthEncoding) {
			return nil, &ocr.SourceError{Class: "UnicodeEncodeError"}
		}
		if err != nil || response == nil {
			return nil, err
		}
		return &ocr.Response{Status: response.Status, Content: response.Content, ContentType: response.ContentType}, nil
	}
}
