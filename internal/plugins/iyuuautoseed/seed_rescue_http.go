package iyuuautoseed

import (
	"bytes"
	"context"
	"errors"
	urlprep "light-media/internal/plugins/iyuuautoseed/compat/urlprep"
	"net/http"
	"net/url"
	"strings"
)

// SeedRescueRequest is called only by the trusted native host. The reference is
// already bound to exact origins and credentials; it creates no SDK operation.
// Source detail reads do not follow redirects, and explicit header maps retain
// requests defaults instead of IYUU resolver-specific header additions.
func (t *NativeSiteHTTP) SeedRescueRequest(ctx context.Context, ref, method, address string, form map[string]string, headers map[string]string, cookie, follow bool) (*TorrentResponse, error) {
	entry, e := t.entry(ref)
	if e != nil {
		return nil, e
	}
	body := url.Values{}
	for k, v := range form {
		body.Set(k, v)
	}
	for redirects := 0; ; redirects++ {
		if redirects > 30 {
			return nil, errors.New("SeedRescue redirects exceed source limit")
		}
		if e = t.guard(ctx); e != nil {
			return nil, e
		}
		prepared, e := urlprep.Prepare(address)
		if e != nil {
			return nil, e
		}
		target, e := trustedSiteURL(entry, prepared)
		if e != nil {
			return nil, e
		}
		req, e := http.NewRequestWithContext(ctx, method, target.String(), strings.NewReader(body.Encode()))
		if e != nil {
			return nil, e
		}
		req.Header.Set("User-Agent", "python-requests/2.34.2")
		req.Header.Set("Accept", "*/*")
		req.Header.Set("Accept-Encoding", "gzip, deflate, br")
		if len(headers) == 0 {
			ua, _ := entry.binding.Fields["ua"].(string)
			if ua == "" {
				ua = t.defaultUA
			}
			if ua == t.defaultUA {
				ua += " Plugin/seedrescue"
			}
			req.Header.Set("User-Agent", ua)
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=UTF-8")
		}
		for key, value := range headers {
			if strings.ContainsAny(key+value, "\r\n\x00") {
				return nil, errors.New("invalid SeedRescue header")
			}
			req.Header.Set(key, value)
		}
		if len(form) > 0 && req.Header.Get("Content-Type") == "" {
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		}
		if cookie {
			raw, _ := entry.binding.Fields["cookie"].(string)
			parsed, e := sourceCookieHeader(raw)
			if e != nil {
				return nil, e
			}
			if parsed != "" {
				req.Header.Set("Cookie", parsed)
			}
		}
		response, e := entry.client.Do(req)
		if e != nil {
			return nil, errors.New("SeedRescue site request failed")
		}
		content, readErr := readSourceContent(response.Body, response.Header.Get("Content-Encoding"))
		response.Body.Close()
		if readErr != nil {
			return nil, readErr
		}
		if e = t.guard(ctx); e != nil {
			return nil, e
		}
		if follow && (response.StatusCode == 301 || response.StatusCode == 302 || response.StatusCode == 303 || response.StatusCode == 307 || response.StatusCode == 308) && response.Header.Get("Location") != "" {
			next, e := target.Parse(response.Header.Get("Location"))
			if e != nil {
				return nil, e
			}
			if _, e = trustedSiteURL(entry, next.String()); e != nil {
				return nil, e
			}
			address = next.String()
			if response.StatusCode == 303 || (method == "POST" && (response.StatusCode == 301 || response.StatusCode == 302)) {
				method = "GET"
				form = nil
				body = url.Values{}
				delete(headers, "Content-Type")
			}
			continue
		}
		text, e := responseText(content, response.Header.Get("Content-Type"))
		if e != nil {
			return nil, e
		}
		result := &TorrentResponse{Status: response.StatusCode, Content: bytes.Clone(content), Text: text, Headers: map[string]string{}}
		for k := range response.Header {
			result.Headers[k] = response.Header.Get(k)
		}
		return result, nil
	}
}
