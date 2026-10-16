package autosignin

import (
	"compress/flate"
	"compress/gzip"
	"compress/zlib"
	"context"
	"crypto/tls"
	"encoding/binary"
	"errors"
	"io"
	"mime"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/andybalholm/brotli"
	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/htmlindex"
	"golang.org/x/text/encoding/unicode"
	"golang.org/x/text/transform"
	"light-media/internal/releasemeta/legacyanime"
)

var ErrSiteTargetDenied = errors.New("site transport target is not authorized")
var ErrSiteEncodingNotReady = errors.New("site character encoding cannot be determined by verified decoder")
var ErrSiteResponseLimit = errors.New("site response exceeds bounded transport limits")
var ErrSiteCodecNotReady = errors.New("site compression outside verified codecs")

// SiteHTTPBinding is constructed by a trusted host from its current SiteStore.
// This prototype never accepts it from SDK invocation input. Transport objects
// must not be shared across sites or persisted to plugin JSON/history/logs.
type SiteHTTPBinding struct {
	URL, Cookie string
	UserAgent   any
	ProxyURL    *url.URL
}
type BoundSiteHTTP struct {
	binding                SiteHTTPBinding
	origin                 string
	testTLS                *tls.Config
	testIdle               time.Duration
	credentialHeaderOrigin string
}

func BindSiteHTTP(binding SiteHTTPBinding) (*BoundSiteHTTP, error) {
	u, err := url.Parse(binding.URL)
	if err != nil || u == nil || u.Host == "" || u.User != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return nil, ErrSiteTargetDenied
	}
	if binding.ProxyURL != nil && (binding.ProxyURL.Scheme != "http" && binding.ProxyURL.Scheme != "https") {
		return nil, ErrSiteTargetDenied
	}
	if binding.ProxyURL != nil {
		copyURL := *binding.ProxyURL
		binding.ProxyURL = &copyURL
	}
	return &BoundSiteHTTP{binding: binding, origin: originOf(u)}, nil
}
func originOf(u *url.URL) string {
	host := strings.ToLower(u.Hostname())
	port := u.Port()
	if port == "" {
		if u.Scheme == "https" {
			port = "443"
		} else {
			port = "80"
		}
	}
	return strings.ToLower(u.Scheme) + "://" + net.JoinHostPort(host, port)
}
func (b *BoundSiteHTTP) authorize(address string) (*url.URL, error) {
	u, err := url.Parse(address)
	if err != nil || u == nil || u.User != nil || originOf(u) != b.origin {
		return nil, ErrSiteTargetDenied
	}
	return u, nil
}

type idleSiteConn struct {
	net.Conn
	idle time.Duration
}

func (c *idleSiteConn) Read(p []byte) (int, error) {
	if err := c.Conn.SetReadDeadline(time.Now().Add(c.idle)); err != nil {
		return 0, err
	}
	return c.Conn.Read(p)
}
func (c *idleSiteConn) Write(p []byte) (int, error) {
	if err := c.Conn.SetWriteDeadline(time.Now().Add(c.idle)); err != nil {
		return 0, err
	}
	return c.Conn.Write(p)
}
func requestIdle(value any) (time.Duration, error) {
	if value == nil || !Truth(value) {
		return 20 * time.Second, nil
	}
	n, ok := numeric(value)
	if !ok {
		return 0, errors.New("invalid site request timeout")
	}
	v, _ := n.Float64()
	if v <= 0 || v > 86400 {
		return 0, errors.New("invalid site request timeout")
	}
	d := time.Duration(v * float64(time.Second))
	if d <= 0 {
		return 0, errors.New("invalid site request timeout")
	}
	return d, nil
}
func sourceCookies(raw string) (string, error) {
	values := map[string]string{}
	order := []string{}
	for _, part := range strings.Split(raw, ";") {
		pair := strings.SplitN(part, "=", 2)
		if len(pair) != 2 {
			continue
		}
		name := legacyanime.Strip(pair[0])
		value := legacyanime.Strip(pair[1])
		decoded, err := url.PathUnescape(value)
		if err == nil {
			ascii := true
			for _, r := range decoded {
				if r > 127 {
					ascii = false
					break
				}
			}
			if ascii {
				value = decoded
			}
		}
		if name == "" || strings.ContainsAny(name, "()<>@,;:\\\"/[]?={} \t\r\n") || strings.ContainsAny(value, "\r\n\x00") {
			return "", errors.New("site cookie outside verified header syntax")
		}
		for _, r := range value {
			if r > 255 {
				return "", errors.New("site cookie outside latin-1 request header syntax")
			}
		}
		if _, exists := values[name]; !exists {
			order = append(order, name)
		}
		values[name] = value
	}
	parts := make([]string, 0, len(order))
	for _, name := range order {
		parts = append(parts, name+"="+values[name])
	}
	rawHeader := strings.Join(parts, "; ")
	latin := make([]byte, 0, len(rawHeader))
	for _, r := range rawHeader {
		if r > 255 {
			return "", errors.New("site cookie outside latin-1 request header syntax")
		}
		latin = append(latin, byte(r))
	}
	return string(latin), nil
}
func (b *BoundSiteHTTP) Do(ctx context.Context, request PageRequest) (*PageResponse, error) {
	return b.do(ctx, request, "")
}

// fixedPublicReferer is only supplied by the typed uncredentialed public-image
// client; ordinary site Do has no such exception.
func (b *BoundSiteHTTP) do(ctx context.Context, request PageRequest, fixedPublicReferer string) (*PageResponse, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if _, err := b.authorize(request.URL); err != nil {
		return nil, err
	}
	if request.Method != "GET" && request.Method != "POST" {
		return nil, ErrSiteTargetDenied
	}
	if request.Referer != "" {
		if _, err := b.authorize(request.Referer); err != nil && !(fixedPublicReferer == request.Referer && b.binding.Cookie == "" && request.NoCookies) {
			return nil, err
		}
	}
	idle, err := requestIdle(request.Timeout)
	if err != nil {
		return nil, err
	}
	if b.testIdle > 0 {
		idle = b.testIdle
	}
	if request.HeadersMode != HeaderDefault && request.HeadersMode != HeaderBaseCookie {
		return nil, ErrSiteTargetDenied
	}
	rawCookie := b.binding.Cookie
	if request.NoCookies {
		rawCookie = ""
	}
	if request.CookieOverride != nil {
		rawCookie = *request.CookieOverride
	}
	cookie, err := sourceCookies(rawCookie)
	if request.HeadersMode == HeaderBaseCookie {
		cookie = rawCookie
		if request.RawCookieHeader != nil {
			cookie = *request.RawCookieHeader
		}
		err = nil
	}
	if err != nil {
		return nil, err
	}
	dialer := &net.Dialer{Timeout: idle}
	transport := &http.Transport{DisableKeepAlives: false, DisableCompression: true, ForceAttemptHTTP2: false, TLSHandshakeTimeout: idle, MaxResponseHeaderBytes: 64 << 10, TLSNextProto: map[string]func(string, *tls.Conn) http.RoundTripper{}}
	// Normal authenticated site requests always validate both origin and HTTPS
	// proxy certificates. No caller option can weaken this policy.
	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12}
	if b.testTLS != nil {
		tlsConfig = b.testTLS.Clone()
		if tlsConfig.InsecureSkipVerify {
			return nil, errors.New("insecure test TLS is forbidden")
		}
	}
	transport.TLSClientConfig = tlsConfig
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		c, err := dialer.DialContext(ctx, network, address)
		if err != nil {
			return nil, err
		}
		return &idleSiteConn{Conn: c, idle: idle}, nil
	}
	if b.binding.ProxyURL != nil {
		transport.Proxy = http.ProxyURL(b.binding.ProxyURL)
	}
	defer transport.CloseIdleConnections()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Transport: transport, Jar: jar, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) > 30 {
			return errors.New("site redirect limit exceeded")
		}
		if _, err := b.authorize(req.URL.String()); err != nil {
			return err
		}
		// requests rebuilds Cookie on redirects. A Cookie supplied as a
		// custom base-page header is not part of its original cookie jar.
		if request.HeadersMode == HeaderBaseCookie {
			req.Header.Del("Cookie")
		}
		req.Header.Del("Referer")
		if len(via) > 0 {
			if original := via[0].Header.Get("Referer"); original != "" {
				req.Header.Set("Referer", original)
			}
		}
		if req.Method != "POST" {
			req.Header.Del("Content-Type")
			req.Header.Del("Content-Length")
			req.Header.Del("Transfer-Encoding")
		}
		return nil
	}}
	var body io.Reader
	if request.JSON != nil {
		raw, err := encodeSourceJSON(request.JSON)
		if err != nil {
			return nil, errors.New("invalid site JSON payload")
		}
		body = strings.NewReader(string(raw))
	} else if request.Form != nil {
		encoded := encodeSourceForm(request.Form, request.FormOrder)
		if encoded != "" {
			body = strings.NewReader(encoded)
		}
	}
	req, err := http.NewRequestWithContext(ctx, request.Method, request.URL, body)
	if err != nil {
		return nil, errors.New("invalid site request")
	}
	agent := "python-urllib3/2.7.0"
	if b.binding.UserAgent != nil {
		var ok bool
		agent, ok = b.binding.UserAgent.(string)
		if !ok {
			return nil, errors.New("site User-Agent must be text")
		}
		if agent == "" {
			agent = " "
		}
	}
	req.Header["User-Agent"] = []string{agent}
	if request.Headers != nil {
		req.Header.Set("User-Agent", "python-requests/2.34.2")
		req.Header.Set("Accept", "*/*")
		allowed := map[string]bool{"user-agent": true, "accept": true, "content-type": true, "authorization": true, "api-token": true, "x-csrf-token": true, "referer": true, "origin": true, "ts": true, "webversion": true, "did": true, "visitorid": true}
		for name, value := range request.Headers {
			key := strings.ToLower(name)
			if !allowed[key] {
				return nil, ErrSiteTargetDenied
			}
			if key == "origin" || key == "referer" {
				if value != nil {
					if target, err := b.authorize(textValue(value)); err != nil {
						target, _ = url.Parse(textValue(value))
						if b.credentialHeaderOrigin == "" || target == nil || target.User != nil || originOf(target) != b.credentialHeaderOrigin {
							return nil, err
						}
					}
				}
			}
			if value == nil {
				req.Header.Del(name)
				if key == "user-agent" {
					req.Header["User-Agent"] = []string{"python-urllib3/2.7.0"}
				}
			} else {
				v, ok := value.(string)
				if !ok {
					return nil, errors.New("site header must be text")
				}
				if key == "user-agent" && v == "" {
					v = " "
				}
				req.Header.Set(name, v)
			}
		}
	} else if request.HeadersMode == HeaderBaseCookie {
		req.Header.Set("Accept", "*/*")
	} else {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=UTF-8")
	}
	// requests.PreparedRequest.prepare_body adds a content type for a
	// supplied body when custom headers omitted it (notably U2).
	if req.Header.Get("Content-Type") == "" {
		if request.JSON != nil {
			req.Header.Set("Content-Type", "application/json")
		} else if request.Form != nil && body != nil {
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		}
	}
	if request.AcceptType != "" && request.Headers == nil {
		req.Header.Set("Accept", request.AcceptType)
	}
	// RequestUtils passes Accept=None; requests removes its default Accept.
	req.Header.Set("Accept-Encoding", "gzip, deflate, br")
	req.Header.Set("Connection", "keep-alive")
	if cookie != "" && !request.NoCookies {
		req.Header.Set("Cookie", cookie)
	}
	if request.HeadersMode == HeaderBaseCookie && request.RawCookieHeader != nil && !request.NoCookies {
		req.Header["Cookie"] = []string{cookie}
	}
	if request.Referer != "" && request.Headers == nil {
		req.Header.Set("Referer", request.Referer)
	}
	res, err := client.Do(req)
	if err != nil {
		if res != nil && res.Body != nil {
			res.Body.Close()
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if errors.Is(err, ErrSiteTargetDenied) {
			return nil, ErrSiteTargetDenied
		}
		return nil, nil
	}
	defer res.Body.Close()
	compressed, err := readDocument(res.Body, 1<<20)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if strings.Contains(err.Error(), "exceeds") {
			return nil, ErrSiteResponseLimit
		}
		return nil, nil
	}
	decoded, err := decodeSiteContent(compressed, res.Header.Values("Content-Encoding"))
	if err != nil {
		if errors.Is(err, ErrSiteResponseLimit) || errors.Is(err, ErrSiteCodecNotReady) {
			return nil, err
		}
		return nil, nil
	}
	text, err := decodeSiteText(decoded, res.Header.Get("Content-Type"))
	if request.Binary {
		text = ""
		err = nil
	}
	if request.DecodeAsUTF8 {
		text = siteUTF8(decoded)
		err = nil
	}
	if err != nil {
		if request.HeadersMode != HeaderBaseCookie {
			return nil, err
		}
		return &PageResponse{Status: res.StatusCode, Content: decoded, TextError: err}, nil
	}
	cookies := []CookieValue{}
	positions := map[string]int{}
	for _, cookie := range res.Cookies() {
		if i, ok := positions[cookie.Name]; ok {
			cookies[i].Value = cookie.Value
		} else {
			positions[cookie.Name] = len(cookies)
			cookies = append(cookies, CookieValue{cookie.Name, cookie.Value})
		}
	}
	return &PageResponse{Status: res.StatusCode, Text: text, Content: decoded, Cookies: cookies}, nil
}
func decodeSiteContent(data []byte, headers []string) ([]byte, error) {
	encodings := strings.Split(strings.Join(headers, ","), ",")
	// Installed urllib3 lacks zstandard: unrecognized single encodings are
	// returned untouched, not decoded by whichever Go libraries are present.
	if len(encodings) == 1 {
		name := strings.ToLower(encodings[0])
		if name != "gzip" && name != "x-gzip" && name != "deflate" && name != "br" {
			return data, nil
		}
	}

	for i := len(encodings) - 1; i >= 0; i-- {
		enc := strings.ToLower(strings.TrimSpace(encodings[i]))
		if enc == "" || enc == "identity" {
			continue
		}
		var r io.Reader
		var close func() error
		switch enc {
		case "gzip", "x-gzip":
			g, err := gzip.NewReader(strings.NewReader(string(data)))
			if err != nil {
				return nil, errors.New("invalid site gzip content")
			}
			r = g
			close = g.Close
		case "deflate":
			g, err := zlib.NewReader(strings.NewReader(string(data)))
			if err == nil {
				r = g
				close = g.Close
			} else {
				f := flate.NewReader(strings.NewReader(string(data)))
				r = f
				close = f.Close
			}
		case "br":
			r = brotli.NewReader(strings.NewReader(string(data)))
		default:
			return nil, ErrSiteCodecNotReady
		}
		raw, err := readDocument(r, 1<<20)
		if close != nil {
			close()
		}
		if err != nil {
			if strings.Contains(err.Error(), "exceeds") {
				return nil, ErrSiteResponseLimit
			}
			return nil, errors.New("invalid compressed site content")
		}
		data = raw
	}
	return data, nil
}
func siteUTF8(body []byte) string {
	var b strings.Builder
	for i := 0; i < len(body); {
		r, width := utf8.DecodeRune(body[i:])
		if r != utf8.RuneError || width > 1 {
			b.WriteRune(r)
			i += width
			continue
		}
		count := 1
		lead := body[i]
		length := 0
		switch {
		case lead >= 0xc2 && lead <= 0xdf:
			length = 2
		case lead >= 0xe0 && lead <= 0xef:
			length = 3
		case lead >= 0xf0 && lead <= 0xf4:
			length = 4
		}
		for count < length && i+count < len(body) {
			next := body[i+count]
			lo, hi := byte(0x80), byte(0xbf)
			if count == 1 {
				switch lead {
				case 0xe0:
					lo = 0xa0
				case 0xed:
					hi = 0x9f
				case 0xf0:
					lo = 0x90
				case 0xf4:
					hi = 0x8f
				}
			}
			if next < lo || next > hi {
				break
			}
			count++
		}
		b.WriteRune(utf8.RuneError)
		i += count
	}
	return b.String()
}

func decodeSiteText(data []byte, contentType string) (string, error) {
	if len(data) == 0 {
		return "", nil
	}
	_, params, err := mime.ParseMediaType(contentType)
	if err != nil {
		params = map[string]string{}
	}
	charset := params["charset"]
	var decoder encoding.Encoding
	if charset != "" {
		key := strings.ToLower(strings.NewReplacer("-", "", "_", "", " ", "").Replace(charset))
		switch key {
		case "utf8":
			return siteUTF8(data), nil
		case "ascii", "usascii", "646", "ansix3.41968", "ansix3.41986", "iso646us", "isoir6", "cp367", "ibm367", "csascii":
			var out strings.Builder
			for _, c := range data {
				if c < 128 {
					out.WriteByte(c)
				} else {
					out.WriteRune(utf8.RuneError)
				}
			}
			return out.String(), nil
		case "iso88591", "latin1", "latin", "l1", "cp819", "ibm819", "csisolatin1", "8859":
			var out strings.Builder
			for _, c := range data {
				out.WriteRune(rune(c))
			}
			return out.String(), nil
		case "gbk", "cp936", "936", "ms936":
			decoder, err = htmlindex.Get("gbk")
			if err != nil {
				return "", ErrSiteEncodingNotReady
			}
		default:
			return "", ErrSiteEncodingNotReady
		}
	}
	if decoder == nil && charset == "" {
		lower := strings.ToLower(contentType)
		if strings.HasPrefix(lower, "text/") {
			return decodeSiteText(data, "text/plain; charset=iso-8859-1")
		}
		if strings.Contains(lower, "application/json") {
			return siteUTF8(data), nil
		}
		if len(data) >= 3 && string(data[:3]) == "\xef\xbb\xbf" {
			return siteUTF8(data[3:]), nil
		}
		if len(data) >= 2 && binary.BigEndian.Uint16(data[:2]) == 0xfeff {
			decoder = unicode.UTF16(unicode.BigEndian, unicode.ExpectBOM)
		} else if len(data) >= 2 && binary.LittleEndian.Uint16(data[:2]) == 0xfeff {
			decoder = unicode.UTF16(unicode.LittleEndian, unicode.ExpectBOM)
		} else if utf8.Valid(data) {
			return string(data), nil
		} else {
			return "", ErrSiteEncodingNotReady
		}
	}
	result, _, err := transform.Bytes(decoder.NewDecoder(), data)
	if err != nil {
		return "", ErrSiteEncodingNotReady
	}
	return string(result), nil
}

// Compile-time policy documentation for Go fields deliberately unavailable to
// worker RPC: http.Client, RootCAs, proxy URL and test timeout seams.
var _ PageTransport = (*BoundSiteHTTP)(nil)
