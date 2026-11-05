package browserrender

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"strings"
	"time"
	"unicode"

	"golang.org/x/net/idna"
	"light-media/internal/networkproxy"
)

const maxHTMLBytes = 16 << 20

func (c Config) flareSolverr(ctx context.Context, request Request) (map[string]any, error) {
	if c.FlareSolverrURL == "" {
		return nil, nil
	}
	u, err := url.Parse(c.FlareSolverrURL)
	if err != nil || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Scheme != "http" && u.Scheme != "https" {
		return nil, &Error{Code: "browser_flaresolverr_url_invalid"}
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/v1"
	endpoint := u.String()
	timeout := request.Timeout
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	proxy, err := request.proxySettings()
	if err != nil {
		return nil, err
	}
	proxySettings := func(auth bool) map[string]any {
		out := map[string]any{"url": proxy.Server}
		if auth {
			if proxy.Username != "" {
				out["username"] = proxy.Username
			}
			if proxy.Password != "" {
				out["password"] = proxy.Password
			}
		}
		return out
	}
	payload := map[string]any{"cmd": "request.get", "url": request.URL, "maxTimeout": int64(timeout/time.Second) * 1000}
	if proxy != nil && (proxy.Username != "" || proxy.Password != "") {
		var id [16]byte
		if _, err = rand.Read(id[:]); err != nil {
			return nil, &Error{Code: "browser_session_id_failed"}
		}
		id[6] = id[6]&15 | 64
		id[8] = id[8]&63 | 128
		session := fmt.Sprintf("%s-%s-%s-%s-%s", hex.EncodeToString(id[:4]), hex.EncodeToString(id[4:6]), hex.EncodeToString(id[6:8]), hex.EncodeToString(id[8:10]), hex.EncodeToString(id[10:]))
		defer func() {
			// Destruction is cleanup, including a failed create or a revoked scope.
			clean := context.WithoutCancel(ctx)
			_, _ = c.fsPost(clean, endpoint, map[string]any{"cmd": "sessions.destroy", "session": session}, 10*time.Second, nil)
		}()
		created, err := c.fsPost(ctx, endpoint, map[string]any{"cmd": "sessions.create", "session": session, "proxy": proxySettings(true)}, timeout, &request)
		if err != nil {
			return nil, err
		}
		if created["status"] != "ok" {
			return nil, nil
		}
		payload["session"] = session
	} else if proxy != nil {
		payload["proxy"] = proxySettings(false)
	}
	if request.Cookie != "" {
		payload["cookies"] = parseCookies(request.Cookie)
	}
	response, err := c.fsPost(ctx, endpoint, payload, timeout, &request)
	if err != nil {
		return nil, err
	}
	if response["status"] != "ok" {
		return nil, nil
	}
	solution, _ := response["solution"].(map[string]any)
	return solution, nil
}

func (c Config) fsPost(ctx context.Context, address string, payload map[string]any, timeout time.Duration, scope *Request) (map[string]any, error) {
	raw, _ := json.Marshal(payload)
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, address, bytes.NewReader(raw))
	if err != nil {
		return nil, &Error{Code: "browser_flaresolverr_request_failed"}
	}
	request.Header.Set("Content-Type", "application/json")
	// RequestUtils supplies UA=None, which the NAS-paired urllib3 2.7.0
	// transport ultimately materializes as this wire header.
	request.Header.Set("User-Agent", "python-urllib3/2.7.0")
	request.Header.Set("Connection", "keep-alive")
	request.Header.Set("Accept-Encoding", "gzip, deflate, br")
	client := http.Client{}
	if c.HTTPClient != nil {
		client = *c.HTTPClient
	} else {
		transport, err := networkproxy.NewRequestsProxyTransport(networkproxy.NewRequestsTimeoutTransport(timeout), os.Environ(), nil, request.URL)
		if err != nil {
			return nil, &Error{Code: "browser_flaresolverr_proxy_invalid"}
		}
		defer transport.CloseIdleConnections()
		client.Transport = transport
	}
	// RequestUtils' requests.request owns a fresh temporary Session for each
	// call, including cookies carried only within that call's redirect chain.
	client.Jar, _ = cookiejar.New(nil)
	client.CheckRedirect = func(next *http.Request, via []*http.Request) error {
		if len(via) > 30 || !sameOrigin(next.URL, via[0].URL) {
			return &Error{Code: "browser_flaresolverr_redirect_denied"}
		}
		if scope != nil {
			return guard(ctx, *scope)
		}
		return nil
	}
	if scope != nil {
		if err := guard(ctx, *scope); err != nil {
			return nil, err
		}
	}
	response, err := client.Do(request)
	if err != nil {
		if scope != nil {
			if stale := guard(ctx, *scope); stale != nil {
				return nil, stale
			}
		}
		return nil, &Error{Code: "browser_flaresolverr_request_failed"}
	}
	defer response.Body.Close()
	if response.StatusCode >= 400 {
		return nil, nil
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxHTMLBytes+1))
	if err != nil || len(data) > maxHTMLBytes {
		return nil, &Error{Code: "browser_flaresolverr_response_failed"}
	}
	data, err = networkproxy.DecodeRequestsContent(data, response.Header.Get("Content-Encoding"), maxHTMLBytes)
	if err != nil {
		return nil, &Error{Code: "browser_flaresolverr_response_failed"}
	}
	var result map[string]any
	if json.Unmarshal(data, &result) != nil {
		return nil, &Error{Code: "browser_flaresolverr_response_failed"}
	}
	return result, nil
}

func sameOrigin(a, b *url.URL) bool {
	if a == nil || b == nil {
		return false
	}
	host := func(u *url.URL) string {
		name := strings.ToLower(u.Hostname())
		if ascii, err := idna.Lookup.ToASCII(name); err == nil {
			name = ascii
		}
		return name
	}
	port := func(u *url.URL) string {
		if value := u.Port(); value != "" {
			return value
		}
		if strings.EqualFold(u.Scheme, "https") {
			return "443"
		}
		if strings.EqualFold(u.Scheme, "http") {
			return "80"
		}
		return ""
	}
	return strings.EqualFold(a.Scheme, b.Scheme) && host(a) == host(b) && port(a) == port(b)
}

// Fixed RequestUtils.cookie_parse keeps insertion order and overwrites duplicate
// names. Its unquote uses latin-1 and does not translate '+' into a space.
func parseCookies(raw string) any {
	if raw == "" {
		return map[string]string{}
	}
	result := []map[string]string{}
	positions := map[string]int{}
	for _, item := range strings.Split(raw, ";") {
		name, value, ok := strings.Cut(item, "=")
		if !ok {
			continue
		}
		trim := func(r rune) bool { return unicode.IsSpace(r) || r >= 0x1c && r <= 0x1f }
		name, value = strings.TrimFunc(name, trim), strings.TrimFunc(value, trim)
		{
			var latin strings.Builder
			// Only percent-decoded bytes are latin-1; untouched UTF-8 is retained.
			for i := 0; i < len(value); {
				if value[i] == '%' && i+2 < len(value) {
					if one, e := hex.DecodeString(value[i+1 : i+3]); e == nil {
						latin.WriteRune(rune(one[0]))
						i += 3
						continue
					}
				}
				latin.WriteByte(value[i])
				i++
			}
			decoded := latin.String()
			validLatin := true
			for _, r := range decoded {
				if r > 127 {
					validLatin = false
					break
				}
			}
			if validLatin {
				value = decoded
			}
		}
		entry := map[string]string{"name": name, "value": value}
		if pos, exists := positions[name]; exists {
			result[pos] = entry
		} else {
			positions[name] = len(result)
			result = append(result, entry)
		}
	}
	return result
}
