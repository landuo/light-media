package app

import (
	"context"
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
	"unicode/utf8"

	"light-media/internal/networkproxy"
	"light-media/internal/plugins"
)

func originalHostNetworkURL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || len(raw) > 8192 || u == nil || u.Hostname() == "" || u.Opaque != "" || u.User != nil || u.Fragment != "" || u.Scheme != "http" && u.Scheme != "https" {
		return nil, bad("INVALID_PLUGIN_VALUE", "远程文本地址须为不含账号或片段的HTTP(S)地址")
	}
	return u, nil
}

// The two credential-free source profiles start at Requests.prepare_url, not
// redirect resolution. urllib3 normalizes path dots and component escapes before
// Requests requotes unreserved bytes. Query separators stay literal, and the
// initial fragment never enters the wire. Ordinary Network.Get keeps its guard.
func originalSourcePublicInitialURL(raw string) (*url.URL, error) {
	if len(raw) > 8192 || !utf8.ValidString(raw) {
		return nil, bad("INVALID_PLUGIN_VALUE", "远程文本地址无效")
	}
	raw = strings.TrimLeftFunc(raw, func(r rune) bool { return unicode.IsSpace(r) || r >= 0x1c && r <= 0x1f })
	if at := strings.IndexByte(raw, '#'); at >= 0 {
		raw = raw[:at]
	}
	end := strings.Index(raw, "://")
	if end < 0 || !strings.EqualFold(raw[:end], "http") && !strings.EqualFold(raw[:end], "https") {
		return nil, bad("INVALID_PLUGIN_VALUE", "远程文本地址无效")
	}
	scheme := strings.ToLower(raw[:end])
	rest := raw[end+3:]
	authorityEnd := strings.IndexAny(rest, "/?")
	authority, tail := rest, ""
	if authorityEnd >= 0 {
		authority, tail = rest[:authorityEnd], rest[authorityEnd:]
	}
	// Credentials, opaque URLs and invalid authorities retain the shared boundary.
	address, err := originalHostNetworkURL(scheme + "://" + authority + "/")
	if err != nil {
		return nil, err
	}
	path, query := tail, ""
	if at := strings.IndexByte(path, '?'); at >= 0 {
		path, query = path[:at], path[at+1:]
	}
	if path == "" {
		path = "/"
	}
	// Resolve only literal dots before requoting: %2e must not turn into a dot
	// segment until Requests' later unquote_unreserved stage has finished.
	path = address.ResolveReference(&url.URL{Path: path}).Path
	prepared := scheme + "://" + strings.ToLower(address.Host) + originalSourceURIComponent(path, sourceURIPathSafe)
	if query != "" {
		prepared += "?" + originalSourceURIComponent(query, sourceURIPathSafe+"?")
	}
	// The original length limit applies to the raw input above. Escaping
	// UTF8 may expand a legal input beyond8192 bytes; do not add a second cap.
	return url.Parse(originalSourceRequoteURI(prepared))
}

// The installed ordinary caller uses RequestUtils(proxies=settings.PROXY,
// headers=settings.GITHUB_HEADERS if selected, timeout=15).get_res(url).
// It does not use PluginHelper's mirror/fallback strategy or a shared Session.
func (a *App) originalHostNetworkResource(ctx context.Context, scope originalBaseCallScope, raw json.RawMessage) (json.RawMessage, error) {
	var request plugins.NetworkGetRequestV1
	if err := originalResourceDecode(raw, &request); err != nil {
		return nil, err
	}
	switch request.Profile {
	case "":
	case plugins.NetworkGetSourcePublicProfile, plugins.NetworkGetSourceDoubanImageProfile:
		if request.GitHubHeaders {
			return nil, bad("INVALID_PLUGIN_VALUE", "公共请求profile不能选择GitHub认证")
		}
	case plugins.NetworkGetSourceRemoteIdentifiersProfile:
		if request.GitHubHeaders || !originalRemoteIdentifiersTaskScope(ctx, scope) {
			return nil, bad("PLUGIN_CAPABILITY_DENIED", "远程识别词请求须绑定实际刷新任务")
		}
	default:
		return nil, bad("INVALID_PLUGIN_VALUE", "公共请求profile无效")
	}
	remoteIdentifiers := request.Profile == plugins.NetworkGetSourceRemoteIdentifiersProfile
	sourcePublic := request.Profile != "" && !remoteIdentifiers
	var address *url.URL
	var authorization string
	var private bool
	var err error
	switch {
	case remoteIdentifiers:
		address, authorization, private, err = originalRemoteIdentifiersURL(request.URL)
	case sourcePublic:
		address, err = originalSourcePublicInitialURL(request.URL)
	default:
		address, err = originalHostNetworkURL(request.URL)
	}
	if err != nil {
		return nil, err
	}
	var settings Settings
	var explicit map[string]string
	timeout := 20 * time.Second
	if !sourcePublic {
		settings = a.Settings()
		proxy, err := a.proxyConfig()
		if err != nil {
			return nil, err
		}
		explicit = map[string]string{}
		if proxy.HTTP != "" {
			explicit["http"] = proxy.HTTP
		}
		if proxy.HTTPS != "" {
			explicit["https"] = proxy.HTTPS
		}
		timeout = 15 * time.Second
	}
	base := networkproxy.NewRequestsTimeoutTransport(timeout)
	if remoteIdentifiers {
		base.TLSClientConfig.InsecureSkipVerify = false
		base.DialContext = originalRemoteIdentifiersDial(base.DialContext, timeout)
	}
	transport, err := networkproxy.NewRequestsProxyTransport(base, os.Environ(), explicit, address)
	if err != nil {
		return nil, bad("NETWORK_CONFIGURATION_INVALID", "宿主代理配置无效")
	}
	defer transport.CloseIdleConnections()
	guard := func() error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := scope.validate(ctx); err != nil {
			return err
		}
		if !sourcePublic {
			current := a.Settings()
			if current.ProxyHost != settings.ProxyHost || current.GitHubToken != settings.GitHubToken {
				return bad("NETWORK_CONFIGURATION_CHANGED", "宿主网络配置已变化")
			}
		}
		return nil
	}
	jar, _ := cookiejar.New(nil)
	crossedOrigin := false
	// Reuse the typed transient-guard marker from the frozen finite HTTP/OCR
	// boundary. It must survive http.Client's wrapping even after guard recovery.
	var requestTransport http.RoundTripper = transport
	if sourcePublic || remoteIdentifiers {
		requestTransport = originalSourcePublicGetTransport{transport}
	}
	client := &http.Client{Jar: jar, Transport: originalOCRGuardTransport{requestTransport, func(context.Context) error { return guard() }}, CheckRedirect: func(next *http.Request, via []*http.Request) error {
		if len(via) > 30 {
			return bad("NETWORK_REDIRECT_LIMIT", "远程文本重定向次数过多")
		}
		if remoteIdentifiers {
			if err := originalRemoteIdentifiersRedirect(next, via[len(via)-1].URL, &private); err != nil {
				return originalSiteRequestGuardFailure(err)
			}
		} else if _, err := originalHostNetworkURL(next.URL.String()); err != nil {
			return err
		}
		// A caller must never cause a saved host token to cross origins or
		// be reintroduced after the source requests library would remove it.
		if next.URL.Scheme != address.Scheme || !strings.EqualFold(next.URL.Host, address.Host) {
			crossedOrigin = true
		}
		if crossedOrigin {
			next.Header.Del("Authorization")
		}
		switch request.Profile {
		case plugins.NetworkGetSourcePublicProfile, plugins.NetworkGetSourceRemoteIdentifiersProfile:
			next.Header.Del("Referer")
		case plugins.NetworkGetSourceDoubanImageProfile:
			next.Header.Set("Referer", "https://movie.douban.com")
		}
		return originalSiteRequestGuardFailure(guard())
	}}
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodGet, address.String(), nil)
	if err != nil {
		return nil, bad("INVALID_PLUGIN_VALUE", "远程文本地址无效")
	}
	// RequestUtils' default User-Agent=None removes requests' session UA;
	// the pinned vendor then supplies its own urllib3 wire default.
	httpRequest.Header.Set("User-Agent", "python-urllib3/2.7.0")
	httpRequest.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=UTF-8")
	if request.Profile == plugins.NetworkGetSourceDoubanImageProfile {
		httpRequest.Header.Set("Referer", "https://movie.douban.com")
	}
	github := request.GitHubHeaders || remoteIdentifiers && strings.Contains(request.URL, "github")
	if _, _, official := originalGitHubRequestRepository(address); github && official && settings.GitHubToken != "" {
		httpRequest.Header = originalGitHubHeaders(Settings{GitHubToken: settings.GitHubToken}, "")
		httpRequest.Header.Set("Accept", "*/*")
	}
	if authorization != "" {
		httpRequest.Header.Set("Authorization", authorization)
	}
	httpRequest.Header.Set("Accept-Encoding", "gzip, deflate, br")
	httpRequest.Header.Set("Connection", "keep-alive")
	if err := guard(); err != nil {
		return nil, err
	}
	response, requestErr := client.Do(httpRequest)
	if response != nil {
		defer response.Body.Close()
	}
	var denied *originalSiteRequestGuardError
	if errors.As(requestErr, &denied) {
		return nil, denied.cause
	}
	var sourceRedirect *originalSourcePublicRedirectError
	if errors.As(requestErr, &sourceRedirect) {
		if remoteIdentifiers {
			return nil, bad("NETWORK_SOURCE_REDIRECT_INVALID", "远程识别词重定向地址无效")
		}
		return nil, bad("NETWORK_SOURCE_REDIRECT_INVALID", sourceRedirect.Error())
	}
	out := plugins.NetworkGetResultV1{SchemaVersion: 1}
	if requestErr == nil {
		// Reserve JSON/base64 overhead before allocation. The full serialized
		// result is checked as well; only the source profiles expose Content-Type
		// for source Response.text decoding. Other headers and cookies stay private.
		limit := originalHostNetworkContentLimit
		if remoteIdentifiers {
			limit = originalRemoteIdentifiersContentLimit
		}
		body, readErr := io.ReadAll(io.LimitReader(response.Body, int64(limit)+1))
		if len(body) > limit {
			return nil, bad("PLUGIN_VALUE_TOO_LARGE", "远程文本超出共享传输上限")
		}
		if readErr == nil {
			body, readErr = originalHostNetworkDecodeBounded(body, response.Header.Get("Content-Encoding"), limit)
			if bounded, ok := readErr.(*APIError); ok {
				return nil, bounded
			}
		}
		if readErr == nil {
			if body == nil {
				body = []byte{}
			}
			out.Response = &plugins.NetworkResponseV1{StatusCode: response.StatusCode, Content: body}
			if sourcePublic {
				out.Response.ContentType = strings.Join(response.Header.Values("Content-Type"), ", ")
			}
		}
	}
	if err := guard(); err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(out)
	if err == nil && len(encoded) > plugins.MaxPayloadBytes {
		return nil, bad("PLUGIN_VALUE_TOO_LARGE", "远程文本超出共享传输上限")
	}
	return encoded, err
}

// The two source-only profiles retain RequestUtils(session=None)'s zero retry
// policy. The empty opaque body is transport-local: net/http's transfer writer
// probes EOF and emits no entity body/length/chunk header, while its retry gate
// cannot classify this GET as replayable. Original client requests remain nil-
// bodied, retaining GET/307/308 redirect semantics and connection reuse.
// Mechanism reused from the separately source-reviewed MPServerStatus leaf;
// source provenance and byte-level controls are recorded with this candidate.
type originalSourcePublicGetTransport struct{ next http.RoundTripper }
type originalSourcePublicGetBody struct{}

func (originalSourcePublicGetBody) Read([]byte) (int, error) { return 0, io.EOF }
func (originalSourcePublicGetBody) Close() error             { return nil }

type originalSourcePublicRedirectError struct{ cause error }

func (e *originalSourcePublicRedirectError) Error() string { return e.cause.Error() }
func (e *originalSourcePublicRedirectError) Unwrap() error { return e.cause }

func (t originalSourcePublicGetTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	copy := request.Clone(request.Context())
	copy.Body = originalSourcePublicGetBody{}
	copy.GetBody = nil
	copy.ContentLength = 0
	response, err := t.next.RoundTrip(copy)
	if err != nil || response == nil {
		return response, err
	}
	switch response.StatusCode {
	case 301, 302, 303, 307, 308:
	default:
		return response, nil
	}
	raw := response.Header.Get("Location")
	if raw == "" {
		return response, nil
	}
	prepared, err := originalSourcePublicRedirectLocation(raw, request.URL)
	if err != nil {
		response.Body.Close()
		return nil, &originalSourcePublicRedirectError{err}
	}
	// Do this before http.Client parses Location: malformed raw '%' path bytes
	// otherwise fail before CheckRedirect can apply source preparation/guards.
	response.Header = response.Header.Clone()
	response.Header.Set("Location", prepared)
	return response, nil
}

const sourceURIUnreserved = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-._~"
const sourceURIRequoteSafe = "!#$%&'()*+,/:;=?@[]~"
const sourceURIPathSafe = "!$&'()*+,;=:@/"

func sourceURIHex(b byte) (byte, bool) {
	switch {
	case b >= '0' && b <= '9':
		return b - '0', true
	case b >= 'a' && b <= 'f':
		return b - 'a' + 10, true
	case b >= 'A' && b <= 'F':
		return b - 'A' + 10, true
	}
	return 0, false
}
func sourceURIDecimal(r rune) (int, bool) {
	for _, x := range unicode.Digit.R16 {
		if r >= rune(x.Lo) && r <= rune(x.Hi) && (r-rune(x.Lo))%rune(x.Stride) == 0 {
			return int((r-rune(x.Lo))/rune(x.Stride)) % 10, true
		}
	}
	for _, x := range unicode.Digit.R32 {
		if r >= rune(x.Lo) && r <= rune(x.Hi) && (r-rune(x.Lo))%rune(x.Stride) == 0 {
			return int((r-rune(x.Lo))/rune(x.Stride)) % 10, true
		}
	}
	return 0, false
}
func sourceURIHexRune(r rune) (int, bool) {
	if r < 128 {
		v, ok := sourceURIHex(byte(r))
		return int(v), ok
	}
	return sourceURIDecimal(r)
}
func sourceURIQuote(raw, safe string) string {
	const hex = "0123456789ABCDEF"
	var out strings.Builder
	for i := 0; i < len(raw); i++ {
		b := raw[i]
		if b < 128 && (strings.ContainsRune(sourceURIUnreserved, rune(b)) || strings.ContainsRune(safe, rune(b))) {
			out.WriteByte(b)
		} else {
			out.WriteByte('%')
			out.WriteByte(hex[b>>4])
			out.WriteByte(hex[b&15])
		}
	}
	return out.String()
}

// Direct port of Requests2.34.2 unquote_unreserved/requote_uri. Invalid alnum
// escape pairs trigger its whole-URI fallback; this is not QueryEscape or a
// parse/rebuild of query keys/values. unicode.Version is frozen by source tests.
func originalSourceRequoteURI(raw string) string {
	parts := strings.Split(raw, "%")
	invalid := false
	for i := 1; i < len(parts); i++ {
		r := []rune(parts[i])
		if len(r) >= 2 && (unicode.IsLetter(r[0]) || unicode.IsNumber(r[0])) && (unicode.IsLetter(r[1]) || unicode.IsNumber(r[1])) {
			a, oa := sourceURIHexRune(r[0])
			b, ob := sourceURIHexRune(r[1])
			if !oa || !ob {
				invalid = true
				break
			}
			v := rune(a*16 + b)
			if strings.ContainsRune(sourceURIUnreserved, v) {
				parts[i] = string(v) + string(r[2:])
				continue
			}
		}
		parts[i] = "%" + parts[i]
	}
	if invalid {
		return sourceURIQuote(raw, strings.ReplaceAll(sourceURIRequoteSafe, "%", ""))
	}
	return sourceURIQuote(strings.Join(parts, ""), sourceURIRequoteSafe)
}

// urllib3 2.7.0 _encode_invalid_chars: uppercase valid escapes first, and
// preserve percent signs only if every percent in this component is valid.
func originalSourceURIComponent(raw, safe string) string {
	var normalized strings.Builder
	valid := 0
	for i := 0; i < len(raw); i++ {
		if raw[i] == '%' && i+2 < len(raw) {
			_, a := sourceURIHex(raw[i+1])
			_, b := sourceURIHex(raw[i+2])
			if a && b {
				normalized.WriteByte('%')
				normalized.WriteString(strings.ToUpper(raw[i+1 : i+3]))
				valid++
				i += 2
				continue
			}
		}
		normalized.WriteByte(raw[i])
	}
	text := normalized.String()
	if valid == strings.Count(text, "%") {
		safe += "%"
	}
	return sourceURIQuote(text, safe)
}

// Requests decodes HTTP Location's original Latin1 octets as UTF8, then
// requotes it. urllib3 prepares path/query wire components separately. The
// existing client's relative resolution and canonical/credential guard still
// run afterwards; no target, credentials, or generic headers are introduced.
func originalSourcePublicLocation(raw string) (string, error) {
	if !utf8.ValidString(raw) {
		return "", errors.New("source public redirect Location is not valid UTF-8")
	}
	quoted := originalSourceRequoteURI(raw)
	fragment := ""
	if at := strings.IndexByte(quoted, '#'); at >= 0 {
		fragment = quoted[at:]
		quoted = quoted[:at]
	}
	query := ""
	hasQuery := false
	if at := strings.IndexByte(quoted, '?'); at >= 0 {
		query = quoted[at+1:]
		quoted = quoted[:at]
		hasQuery = true
	}
	// Separate scheme/authority without parsing malformed path escapes first.
	pathAt := 0
	authority := 0
	if strings.HasPrefix(quoted, "//") {
		authority = 2
	} else if len(quoted) > 0 && (quoted[0] >= 'a' && quoted[0] <= 'z' || quoted[0] >= 'A' && quoted[0] <= 'Z') {
		// urllib3's URI scheme grammar is anchored at the beginning. A
		// path segment containing "://" does not introduce an authority.
		for at := 1; at < len(quoted); at++ {
			b := quoted[at]
			if b == ':' {
				if strings.HasPrefix(quoted[at+1:], "//") {
					authority = at + 3
				}
				break
			}
			if !(b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9' || b == '+' || b == '-' || b == '.') {
				break
			}
		}
	}
	if authority != 0 {
		pathAt = len(quoted)
		if at := strings.IndexByte(quoted[authority:], '/'); at >= 0 {
			pathAt = authority + at
		}
	} else if !strings.HasPrefix(quoted, "/") {
		// urllib.parse.urljoin discards empty segments inside a relative
		// path (but preserves them in rooted paths). Go's ResolveReference
		// keeps those segments, so apply this source step before it resolves.
		segments := strings.Split(quoted, "/")
		if len(segments) > 2 {
			kept := segments[:1]
			for _, segment := range segments[1 : len(segments)-1] {
				if segment != "" {
					kept = append(kept, segment)
				}
			}
			kept = append(kept, segments[len(segments)-1])
			quoted = strings.Join(kept, "/")
		}
	}
	result := quoted[:pathAt] + originalSourceURIComponent(quoted[pathAt:], sourceURIPathSafe)
	if hasQuery {
		result += "?" + originalSourceURIComponent(query, sourceURIPathSafe+"?")
	}
	return result + fragment, nil
}

// Requests.resolve_redirects resolves against each response URL with Python's
// urljoin before the next request is parsed. This finite HTTP reference join
// retains raw escaped path/query text; it never rebuilds query key/value pairs.
func originalSourcePublicRedirectLocation(raw string, base *url.URL) (string, error) {
	// Requests.resolve_redirects handles a network reference before urlparse,
	// geturl and urljoin. An explicit current scheme makes Python/Go consume
	// the same two authority slashes, including an empty authority.
	if base != nil && strings.HasPrefix(raw, "//") {
		raw = base.Scheme + ":" + raw
	}
	text, err := originalSourcePublicLocation(raw)
	if err != nil {
		return "", err
	}
	if base == nil {
		return "", errors.New("source public redirect has no base URL")
	}
	// Python recognizes only an anchored ASCII scheme. For an equal scheme
	// without authority it still joins the path against the current HTTP base.
	// Go instead treats e.g. http:child as opaque, so do not delegate that step.
	schemeEnd := -1
	if len(text) > 0 && (text[0] >= 'A' && text[0] <= 'Z' || text[0] >= 'a' && text[0] <= 'z') {
		for i := 1; i < len(text); i++ {
			c := text[i]
			if c == ':' {
				schemeEnd = i
				break
			}
			if !(c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '+' || c == '-' || c == '.') {
				break
			}
		}
	}
	equalSchemePath := schemeEnd >= 0 && strings.EqualFold(text[:schemeEnd], base.Scheme) && !strings.HasPrefix(text[schemeEnd+1:], "//")
	if equalSchemePath {
		text = text[schemeEnd+1:]
	}
	// In Python a non-scheme first path component may contain ':'. Prefixing a
	// removable dot lets Go tokenize that path without interpreting a new scheme.
	if equalSchemePath || schemeEnd < 0 {
		first := text
		if i := strings.IndexAny(first, "/?#"); i >= 0 {
			first = first[:i]
		}
		if strings.ContainsRune(first, ':') {
			text = "./" + text
		}
	}
	ref, err := url.Parse(text)
	if err != nil {
		return "", err
	}
	if ref.Scheme != "" {
		ref.Scheme = strings.ToLower(ref.Scheme)
	}
	if ref.Scheme != "" && !strings.EqualFold(ref.Scheme, base.Scheme) || ref.Host != "" || ref.User != nil || ref.Opaque != "" {
		if ref.Scheme == "" {
			ref.Scheme = base.Scheme
		}
		ref.ForceQuery = false // Python urlunparse has no explicit empty-query marker.
		return ref.String(), nil
	}
	splitParams := func(path string) (string, string) {
		at := strings.LastIndexByte(path, '/')
		tail := path[at+1:]
		if semi := strings.IndexByte(tail, ';'); semi >= 0 {
			return path[:at+1+semi], tail[semi+1:]
		}
		return path, ""
	}
	path, params := splitParams(ref.EscapedPath())
	basePath, baseParams := splitParams(base.EscapedPath())
	query := ref.RawQuery
	if path == "" && params == "" {
		path, params = basePath, baseParams
		if query == "" {
			query = base.RawQuery
		}
	} else {
		baseParts := strings.Split(basePath, "/")
		if baseParts[len(baseParts)-1] != "" {
			baseParts = baseParts[:len(baseParts)-1]
		}
		var segments []string
		if strings.HasPrefix(path, "/") {
			segments = strings.Split(path, "/")
		} else {
			segments = append(baseParts, strings.Split(path, "/")...)
			if len(segments) > 2 {
				kept := segments[:1]
				for _, segment := range segments[1 : len(segments)-1] {
					if segment != "" {
						kept = append(kept, segment)
					}
				}
				kept = append(kept, segments[len(segments)-1])
				segments = kept
			}
		}
		resolved := []string{}
		for _, segment := range segments {
			switch segment {
			case "..":
				if len(resolved) > 0 {
					resolved = resolved[:len(resolved)-1]
				}
			case ".":
			default:
				resolved = append(resolved, segment)
			}
		}
		last := segments[len(segments)-1]
		if last == "." || last == ".." {
			resolved = append(resolved, "")
		}
		path = strings.Join(resolved, "/")
		if path == "" {
			path = "/"
		}
	}
	if params != "" {
		path += ";" + params
	}
	decoded, err := url.PathUnescape(path)
	if err != nil {
		return "", err
	}
	ref.Scheme, ref.Host, ref.User = base.Scheme, base.Host, base.User
	ref.Path, ref.RawPath = decoded, path
	ref.RawQuery, ref.ForceQuery = query, false
	return ref.String(), nil
}
