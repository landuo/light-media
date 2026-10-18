package iyuuautoseed

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"strings"

	urlprep "light-media/internal/plugins/iyuuautoseed/compat/urlprep"
)

// BrushRSSRead keeps XML bytes available before requests.text is evaluated.
// RssHelper first honors XML declarations; an HTTP charset must not prevent
// that decoding. The trusted site/origin/proxy/credential guard is unchanged.
func (t *NativeSiteHTTP) BrushRSSRead(ctx context.Context, ref, address, ua string) (*TorrentResponse, error) {
	entry, e := t.entry(ref)
	if e != nil {
		return nil, e
	}
	for redirects := 0; redirects <= 30; redirects++ {
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
		req, e := http.NewRequestWithContext(ctx, "GET", target.String(), nil)
		if e != nil {
			return nil, e
		}
		req.Header.Set("User-Agent", ua)
		req.Header.Set("Accept", "*/*")
		req.Header.Set("Accept-Encoding", "gzip, deflate, br")
		response, e := entry.client.Do(req)
		if e != nil {
			return nil, errors.New("BrushFlow RSS request failed")
		}
		raw, e := readSourceContent(response.Body, response.Header.Get("Content-Encoding"))
		response.Body.Close()
		if e != nil {
			return nil, e
		}
		if e = t.guard(ctx); e != nil {
			return nil, e
		}
		if (response.StatusCode == 301 || response.StatusCode == 302 || response.StatusCode == 303 || response.StatusCode == 307 || response.StatusCode == 308) && response.Header.Get("Location") != "" {
			next, e := target.Parse(response.Header.Get("Location"))
			if e != nil {
				return nil, e
			}
			if _, e = trustedSiteURL(entry, next.String()); e != nil {
				return nil, e
			}
			address = next.String()
			continue
		}
		out := &TorrentResponse{Status: response.StatusCode, Content: bytes.Clone(raw), Headers: map[string]string{}}
		for k := range response.Header {
			out.Headers[k] = response.Header.Get(k)
		}
		return out, nil
	}
	return nil, errors.New(strings.Join([]string{"BrushFlow", "RSS redirects exceed source limit"}, " "))
}
