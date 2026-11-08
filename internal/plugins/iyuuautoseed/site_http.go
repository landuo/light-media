package iyuuautoseed

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"compress/zlib"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/andybalholm/brotli"
	"light-media/internal/networkproxy"
	encodingdetector "light-media/internal/plugins/iyuuautoseed/compat/encodingdetector"
	urlprep "light-media/internal/plugins/iyuuautoseed/compat/urlprep"
)

// SiteTransportBinding is minted by the application from one authorized site
// snapshot. AllowedOrigins includes only that site's explicitly trusted origin
// aliases/API origin. Redirect responses cannot expand this list.
type SiteTransportBinding struct {
	Ref            string
	Fields         map[string]any
	AllowedOrigins []string
	ProxyURL       string
	// Global PROXY mapping supplied by the host, selected by the original
	// site's proxy flag. This is never worker JSON or a freely supplied URL.
	ProxyConfig networkproxy.Config
}
type SiteTransportOptions struct {
	Guard     func(context.Context) error
	RootCAs   *x509.CertPool
	Timeout   time.Duration
	DefaultUA string
}
type siteTransportEntry struct {
	binding SiteTransportBinding
	origins map[string]bool
	client  *http.Client
}
type NativeSiteHTTP struct {
	entries   map[string]*siteTransportEntry
	guard     func(context.Context) error
	defaultUA string
}

func NewNativeSiteHTTP(bindings []SiteTransportBinding, opt SiteTransportOptions) (*NativeSiteHTTP, error) {
	if opt.Guard == nil {
		return nil, errors.New("site HTTP owner guard required")
	}
	if opt.Timeout == 0 {
		opt.Timeout = 20 * time.Second
	}
	if opt.Timeout < 0 {
		return nil, errors.New("invalid site timeout")
	}
	t := &NativeSiteHTTP{entries: map[string]*siteTransportEntry{}, guard: opt.Guard, defaultUA: opt.DefaultUA}
	for _, b := range bindings {
		if b.Ref == "" || t.entries[b.Ref] != nil {
			return nil, errors.New("duplicate or empty site ref")
		}
		fields, e := cloneMap(b.Fields)
		if e != nil {
			return nil, e
		}
		b.Fields = fields
		origins := map[string]bool{}
		for _, origin := range b.AllowedOrigins {
			prepared, e := urlprep.Prepare(origin)
			if e != nil {
				return nil, errors.New("invalid trusted site origin")
			}
			u, e := url.Parse(prepared)
			if e != nil || u.User != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
				return nil, errors.New("invalid trusted site origin")
			}
			origins[u.Scheme+"://"+u.Host] = true
		}
		siteURL, ok := b.Fields["url"].(string)
		if !ok {
			return nil, errors.New("site URL required")
		}
		prepared, e := urlprep.Prepare(siteURL)
		if e != nil {
			return nil, errors.New("invalid site URL")
		}
		u, e := url.Parse(prepared)
		if e != nil || !origins[u.Scheme+"://"+u.Host] {
			return nil, errors.New("site origin not granted")
		}
		transport := &http.Transport{Proxy: nil, TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: opt.RootCAs}, DisableCompression: true}
		var route http.RoundTripper = transport
		config := networkproxy.Config{}
		if truth(b.Fields["proxy"]) {
			config = b.ProxyConfig
		}
		if b.ProxyURL != "" {
			if config.HTTP != "" || config.HTTPS != "" {
				return nil, errors.New("ambiguous owner proxy")
			}
			config, e = networkproxy.Resolve(b.ProxyURL, nil)
			if e != nil {
				return nil, errors.New("invalid owner proxy")
			}
		}
		if config.HTTP != "" || config.HTTPS != "" {
			route, e = config.Transport(transport)
			if e != nil {
				return nil, errors.New("invalid owner proxy")
			}
		}
		t.entries[b.Ref] = &siteTransportEntry{binding: b, origins: origins, client: &http.Client{Transport: route, Timeout: opt.Timeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
	}
	return t, nil
}
func (t *NativeSiteHTTP) Close() {
	for _, e := range t.entries {
		e.client.CloseIdleConnections()
	}
}
func (t *NativeSiteHTTP) entry(ref string) (*siteTransportEntry, error) {
	e := t.entries[ref]
	if e == nil {
		return nil, errors.New("site ref not granted")
	}
	return e, nil
}
func trustedSiteURL(e *siteTransportEntry, raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || u.User != nil || u.Host == "" || u.Opaque != "" || !e.origins[u.Scheme+"://"+u.Host] {
		return nil, errors.New("site URL lineage outside grant")
	}
	return u, nil
}
func (t *NativeSiteHTTP) MTeamToken(ctx context.Context, ref string, id any) (*APIResponse, error) {
	e, err := t.entry(ref)
	if err != nil {
		return nil, err
	}
	base := mteamAPI.ReplaceAllString(e.binding.Fields["url"].(string), "//api.m-team")
	query := url.Values{}
	// requests' _encode_params drops None and expands lists. Scalar source IDs
	// retain Python's string representation rather than JSON serialization.
	if id != nil {
		switch x := id.(type) {
		case []any:
			for _, v := range x {
				if v != nil {
					query.Add("id", pyString(v))
				}
			}
		default:
			query.Set("id", pyString(id))
		}
	}
	address := base + "api/torrent/genDlToken"
	if len(query) > 0 {
		address += "?" + query.Encode()
	}
	res, err := t.request(ctx, e, http.MethodPost, address, nil, true, false)
	if err != nil || res == nil {
		return nil, err
	}
	text, err := responseJSONText(res.Content, res.Headers["Content-Type"])
	if err != nil {
		return nil, err
	}
	return &APIResponse{Status: res.Status, Body: []byte(text)}, nil
}
func (t *NativeSiteHTTP) Page(ctx context.Context, ref string, kind PageKind, id any) (*PageResponse, error) {
	e, err := t.entry(ref)
	if err != nil {
		return nil, err
	}
	route := "details.php?id="
	if kind == GPWPage {
		route = "torrents.php?torrentid="
	} else if kind != DetailsPage {
		return nil, errors.New("unsupported site page kind")
	}
	address := e.binding.Fields["url"].(string) + route + pyString(id) + "&hit=1"
	res, err := t.request(ctx, e, http.MethodGet, address, nil, false, true)
	if err != nil || res == nil {
		return nil, err
	}
	text := res.Text
	if strings.Contains(text, "charset=utf-8") || strings.Contains(text, "charset=UTF-8") {
		text, err = encodingdetector.Decode(res.Content, "utf-8", true)
	} else {
		enc, _, de := encodingdetector.Detect(res.Content)
		if de != nil {
			return nil, de
		}
		text, err = decodeResponseBytes(res.Content, enc)
	}
	if err != nil {
		return nil, err
	}
	return &PageResponse{Status: res.Status, Text: text}, nil
}

type siteTorrentHTTP struct {
	transport *NativeSiteHTTP
	entry     *siteTransportEntry
}

func (t *NativeSiteHTTP) TorrentHTTP(ref string) (TorrentHTTP, error) {
	e, err := t.entry(ref)
	if err != nil {
		return nil, err
	}
	return &siteTorrentHTTP{t, e}, nil
}
func (t *siteTorrentHTTP) Get(ctx context.Context, address string) (*TorrentResponse, error) {
	return t.transport.request(ctx, t.entry, http.MethodGet, address, nil, false, false)
}
func (t *siteTorrentHTTP) Post(ctx context.Context, address string, data map[string]string) (*TorrentResponse, error) {
	return t.transport.request(ctx, t.entry, http.MethodPost, address, data, false, true)
}
func (t *NativeSiteHTTP) request(ctx context.Context, e *siteTransportEntry, method, address string, data map[string]string, mteam, follow bool) (*TorrentResponse, error) {
	responseCookies, _ := cookiejar.New(nil)
	purgedEntityHeaders := false
	var body []byte
	if data != nil {
		keys := make([]string, 0, len(data))
		for k := range data {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		parts := []string{}
		for _, k := range keys {
			parts = append(parts, url.QueryEscape(k)+"="+url.QueryEscape(data[k]))
		}
		body = []byte(strings.Join(parts, "&"))
	}
	for redirects := 0; ; redirects++ {
		if redirects > 30 {
			return nil, nil
		}
		if err := t.guard(ctx); err != nil {
			return nil, err
		}
		prepared, err := urlprep.Prepare(address)
		if err != nil {
			return nil, nil
		}
		u, err := trustedSiteURL(e, prepared)
		if err != nil {
			return nil, err
		}
		req, err := http.NewRequestWithContext(ctx, method, u.String(), bytes.NewReader(body))
		if err != nil {
			return nil, nil
		}
		req.Header.Set("Connection", "keep-alive")
		req.Header.Set("Accept-Encoding", "gzip, deflate, br")
		ua := or(e.binding.Fields["ua"], t.defaultUA)
		s, ok := ua.(string)
		if !ok {
			return nil, nil
		}
		if !mteam && s == t.defaultUA {
			s += " Plugin/iyuuautoseed"
		}
		req.Header.Set("User-Agent", s)
		if !purgedEntityHeaders {
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=UTF-8")
		}
		if mteam {
			if !purgedEntityHeaders {
				req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			}
			req.Header.Set("Accept", "application/json, text/plain, */*")
			api, _ := e.binding.Fields["apikey"].(string)
			req.Header.Set("x-api-key", api)
		} else if cookie, ok := e.binding.Fields["cookie"].(string); ok {
			c, err := sourceCookieHeader(cookie)
			if err != nil {
				return nil, nil
			}
			if c != "" {
				req.Header.Set("Cookie", c)
			}
		}
		for _, cookie := range responseCookies.Cookies(u) {
			value := cookie.Name + "=" + cookie.Value
			if prior := req.Header.Get("Cookie"); prior != "" {
				value = prior + "; " + value
			}
			req.Header.Set("Cookie", value)
		}
		// requests encodes headers as Latin-1. Go's UTF8 wire bytes must not silently
		// replace that contract. Invalid source header values cause no request.
		for k, vals := range req.Header {
			for i, v := range vals {
				b := []byte{}
				for _, r := range v {
					if r > 255 || r == '\r' || r == '\n' {
						return nil, nil
					}
					b = append(b, byte(r))
				}
				vals[i] = string(b)
			}
			req.Header[k] = vals
		}
		res, err := e.client.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return nil, nil
		}
		raw, err := readSourceContent(res.Body, res.Header.Get("Content-Encoding"))
		res.Body.Close()
		if err != nil {
			return nil, nil
		}
		if err = t.guard(ctx); err != nil {
			return nil, err
		}
		responseCookies.SetCookies(u, res.Cookies())
		if follow && (res.StatusCode == 301 || res.StatusCode == 302 || res.StatusCode == 303 || res.StatusCode == 307 || res.StatusCode == 308) {
			loc := res.Header.Get("Location")
			if loc == "" {
				break
			}
			next, err := u.Parse(loc)
			if err != nil {
				return nil, nil
			}
			if _, err = trustedSiteURL(e, next.String()); err != nil {
				return nil, err
			}
			address = next.String()
			if res.StatusCode == 303 || (res.StatusCode == 302) || (res.StatusCode == 301 && method == http.MethodPost) {
				method = http.MethodGet
				body = nil
			}
			if res.StatusCode != 307 && res.StatusCode != 308 {
				purgedEntityHeaders = true
			}
			continue
		}
		text, err := responseText(raw, res.Header.Get("Content-Type"))
		if err != nil {
			return nil, err
		}
		headers := map[string]string{}
		for k, v := range res.Header {
			if len(v) > 0 {
				headers[k] = v[0]
			}
		}
		return &TorrentResponse{Status: res.StatusCode, Content: raw, Text: text, Headers: headers}, nil
	}
	return nil, nil
}
func sourceCookieHeader(raw string) (string, error) {
	keys := []string{}
	values := map[string]string{}
	for _, part := range strings.Split(raw, ";") {
		pair := strings.SplitN(strings.TrimSpace(part), "=", 2)
		if len(pair) != 2 {
			continue
		}
		key, value := strings.TrimSpace(pair[0]), strings.TrimSpace(pair[1])
		if _, ok := values[key]; !ok {
			keys = append(keys, key)
		}
		// Original unquote(latin1) -> quote(UTF8) -> unquote(latin1)
		// roundtrip accepts only ASCII decoded values. High bytes or existing
		// non-ASCII text retain the original percent/string representation.
		var decoded strings.Builder
		ascii := true
		for i := 0; i < len(value); i++ {
			b := value[i]
			if b == '%' && i+2 < len(value) {
				hi, lo := hexNibble(value[i+1]), hexNibble(value[i+2])
				if hi >= 0 && lo >= 0 {
					b = byte(hi*16 + lo)
					i += 2
				}
			}
			if b >= 128 {
				ascii = false
			}
			decoded.WriteByte(b)
		}
		if ascii {
			value = decoded.String()
		}
		values[key] = value
	}
	out := []string{}
	for _, k := range keys {
		if k != "" {
			out = append(out, k+"="+values[k])
		}
	}
	return strings.Join(out, "; "), nil
}

func hexNibble(b byte) int {
	switch {
	case b >= '0' && b <= '9':
		return int(b - '0')
	case b >= 'a' && b <= 'f':
		return int(b-'a') + 10
	case b >= 'A' && b <= 'F':
		return int(b-'A') + 10
	}
	return -1
}
func readSourceContent(body io.Reader, encoding string) ([]byte, error) {
	raw, err := io.ReadAll(io.LimitReader(body, maxAPIResponse+1))
	if err != nil || len(raw) > maxAPIResponse {
		return nil, errors.New("site response limit or read failure")
	}
	encs := strings.Split(encoding, ",")
	for i := len(encs) - 1; i >= 0; i-- {
		var reader io.Reader
		var closer io.Closer
		switch strings.ToLower(strings.TrimSpace(encs[i])) {
		case "", "identity":
			continue
		case "gzip", "x-gzip":
			r, e := gzip.NewReader(bytes.NewReader(raw))
			if e != nil {
				return nil, e
			}
			reader = r
			closer = r
		case "deflate":
			r, e := zlib.NewReader(bytes.NewReader(raw))
			if e != nil {
				r = flate.NewReader(bytes.NewReader(raw))
			}
			reader = r
			closer = r
		case "br":
			reader = brotli.NewReader(bytes.NewReader(raw))
		default:
			return nil, errors.New("unsupported source content encoding")
		}
		decoded, e := io.ReadAll(io.LimitReader(reader, maxAPIResponse+1))
		if closer != nil {
			closer.Close()
		}
		if e != nil || len(decoded) > maxAPIResponse {
			return nil, errors.New("decoded site response exceeds limit or malformed")
		}
		raw = decoded
	}
	return raw, nil
}
func headerEncoding(contentType string) string {
	parts := strings.Split(contentType, ";")
	for _, p := range parts[1:] {
		kv := strings.SplitN(p, "=", 2)
		if len(kv) == 2 && strings.ToLower(strings.Trim(kv[0], " \t\r\n'\"")) == "charset" {
			return strings.Trim(strings.TrimSpace(kv[1]), "'\"")
		}
	}
	ct := strings.TrimSpace(parts[0])
	if strings.Contains(ct, "text") {
		return "ISO-8859-1"
	}
	if strings.Contains(ct, "application/json") {
		return "utf-8"
	}
	return ""
}
func decodeResponseBytes(raw []byte, enc string) (string, error) {
	if enc == "" {
		var err error
		enc, _, err = encodingdetector.Detect(raw)
		if err != nil {
			return "", err
		}
	}
	text, err := encodingdetector.Decode(raw, enc, true)
	if err != nil {
		if class := encodingdetector.ErrorClass(err); class == "LookupError" || class == "TypeError" {
			return encodingdetector.Decode(raw, "utf-8", true)
		}
		return "", err
	}
	return text, nil
}
func responseText(raw []byte, contentType string) (string, error) {
	return decodeResponseBytes(raw, headerEncoding(contentType))
}

// DecodeRequestsSiteResponse reuses the pinned requests text/JSON charset
// rules for trusted host material adapters.
func DecodeRequestsSiteResponse(raw []byte, contentType string, asJSON bool) (string, error) {
	if asJSON {
		return responseJSONText(raw, contentType)
	}
	return responseText(raw, contentType)
}
func responseJSONText(raw []byte, contentType string) (string, error) {
	enc := headerEncoding(contentType)
	if enc == "" && len(raw) > 3 {
		guess := ""
		switch {
		case bytes.HasPrefix(raw, []byte{0, 0, 254, 255}) || bytes.HasPrefix(raw, []byte{255, 254, 0, 0}):
			guess = "utf-32"
		case bytes.HasPrefix(raw, []byte{239, 187, 191}):
			guess = "utf-8-sig"
		case bytes.HasPrefix(raw, []byte{254, 255}) || bytes.HasPrefix(raw, []byte{255, 254}):
			guess = "utf-16"
		default:
			n := bytes.Count(raw[:4], []byte{0})
			if n == 0 {
				guess = "utf-8"
			} else if n == 2 {
				if raw[0] == 0 && raw[2] == 0 {
					guess = "utf-16-be"
				} else if raw[1] == 0 && raw[3] == 0 {
					guess = "utf-16-le"
				}
			} else if n == 3 {
				if raw[0] == 0 && raw[1] == 0 && raw[2] == 0 {
					guess = "utf-32-be"
				} else if raw[1] == 0 && raw[2] == 0 && raw[3] == 0 {
					guess = "utf-32-le"
				}
			}
		}
		if guess != "" {
			if text, err := encodingdetector.Decode(raw, guess, false); err == nil {
				return text, nil
			} else if encodingdetector.ErrorClass(err) != "UnicodeDecodeError" {
				return "", err
			}
		}
	}
	return responseText(raw, contentType)
}
