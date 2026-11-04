package mediaservervendors

import (
	"bytes"
	"context"
	"encoding/json"
	"math/big"
	"net/url"
	"strings"

	"light-media/internal/releasemeta/legacyanime"
)

// Only the selected real API's already-normalized origin is used here. These
// URLs are library metadata, never dispatched or used to widen request origin.
func (v *Ugreen) refreshImageURL(ctx context.Context, source string) (json.RawMessage, error) {
	api, err := v.refreshAPI()
	if err != nil {
		return nil, err
	}
	if api == nil {
		return json.RawMessage(`null`), nil
	}
	details, err := ugreenCall(ctx, v, func() (ugreenRefreshDetails, error) { return api.refreshDetails(ctx) })
	if err != nil {
		return nil, err
	}
	token := details.staticToken
	if !ejTruth(token) {
		token = details.token
	}
	if !ejTruth(token) {
		return json.RawMessage(`null`), nil
	}
	pairs := []struct {
		name  string
		value json.RawMessage
	}{{"app_name", ugreenKey("web")}, {"name", ugreenKey(source)}, {"size", json.RawMessage(`1`)}}
	name := "token"
	if details.isUGK {
		name = "ugk"
	}
	pairs = append(pairs, struct {
		name  string
		value json.RawMessage
	}{name, token})
	parts := []string{}
	for _, pair := range pairs {
		values := []json.RawMessage{pair.value}
		raw := bytes.TrimSpace(pair.value)
		if len(raw) > 0 && (raw[0] == '[' || raw[0] == '{') {
			values, err = ejIter(raw)
			if err != nil {
				return nil, err
			}
		}
		for _, value := range values {
			text, err := ejPythonText(value, false)
			if err != nil {
				return nil, err
			}
			parts = append(parts, url.QueryEscape(pair.name)+"="+url.QueryEscape(text))
		}
	}
	return ugreenKey(strings.TrimRight(details.host, "/") + "/ugreen/v2/video/getImaStream?" + strings.Join(parts, "&")), nil
}
func (v *Ugreen) refreshExpiredImage(text string) (bool, error) {
	parsed, err := url.Parse(text)
	if err != nil || parsed.User != nil || strings.ToLower(parsed.Host) != "scraper.ugnas.com" {
		return false, nil
	}
	value, present, err := ugreenRefreshFirstQueryValue(parsed.RawQuery, "auth_key")
	if err != nil {
		return false, err
	}
	if !present || value == "" {
		return false, nil
	}
	expiry, err := legacyanime.Integer(strings.SplitN(value, "-", 2)[0])
	if err != nil {
		return false, nil
	}
	return expiry.Cmp(big.NewInt(v.deps.Now().Unix())) <= 0, nil
}
func (v *Ugreen) refreshResolveImage(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	if !ejTruth(raw) {
		return json.RawMessage(`null`), nil
	}
	text, err := ugreenUTF8(raw)
	if err != nil {
		return nil, err
	}
	if !strings.HasPrefix(text, "http://") && !strings.HasPrefix(text, "https://") {
		return json.RawMessage(`null`), nil
	}
	parsed, err := url.Parse(text)
	if err != nil {
		return nil, ejSource("ValueError")
	}
	if parsed.User == nil && strings.ToLower(parsed.Host) == "scraper.ugnas.com" {
		stream, err := v.refreshImageURL(ctx, text)
		if err != nil {
			return nil, err
		}
		if ejTruth(stream) {
			return stream, nil
		}
	}
	expired, err := v.refreshExpiredImage(text)
	if err != nil {
		return nil, err
	}
	if expired {
		return json.RawMessage(`null`), nil
	}
	return ejCopy(raw), nil
}
func (v *Ugreen) refreshRootURL(ctx context.Context) (json.RawMessage, error) {
	v.mu.RLock()
	host := ejCopy(v.playHost)
	v.mu.RUnlock()
	if !ejTruth(host) {
		api, err := v.refreshAPI()
		if err != nil {
			return nil, err
		}
		host = ugreenKey("")
		if api != nil {
			details, err := ugreenCall(ctx, v, func() (ugreenRefreshDetails, error) { return api.refreshDetails(ctx) })
			if err != nil {
				return nil, err
			}
			host = ugreenKey(details.host)
		}
	}
	if !ejTruth(host) {
		return ugreenKey(""), nil
	}
	text, err := ugreenUTF8(host)
	if err != nil {
		return nil, err
	}
	return ugreenKey(strings.TrimRight(text, "/") + "/"), nil
}
