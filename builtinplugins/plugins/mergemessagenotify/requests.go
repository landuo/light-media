package mergemessagenotify

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"sort"
	"strings"
	"time"

	"light-media/builtinplugins/plugins/mpserverstatus/requestsproxy"
)

// RequestsHTTP realizes the Requests arguments produced by the built-in
// channels. Proxy environment is the private source-compatible host snapshot.
type RequestsHTTP struct {
	Environment []string
}

var errChannelHTTP = errors.New("通知渠道 HTTP 请求失败")

func (h RequestsHTTP) Send(ctx context.Context, input Request) (Response, error) {
	target, err := url.Parse(input.URL)
	if err != nil || !httpDestination(target) {
		return Response{}, errChannelHTTP
	}
	if params := input.Kwargs["params"]; params != nil {
		query, err := requestForm(params)
		if err != nil {
			return Response{}, err
		}
		if query != "" {
			if target.RawQuery != "" {
				target.RawQuery += "&"
			}
			target.RawQuery += query
		}
	}
	headers := http.Header{"Accept": {"*/*"}, "User-Agent": {"python-requests/2.34.2"}}
	if input.Kwargs["headers"] != nil {
		values, ok := input.Kwargs["headers"].(map[string]any)
		if !ok {
			return Response{}, errChannelHTTP
		}
		for name, value := range values {
			if value == nil {
				headers.Del(name)
				continue
			}
			text, ok := value.(string)
			if !ok {
				return Response{}, errChannelHTTP
			}
			headers.Set(name, text)
		}
	}
	body, contentType, err := requestBody(input.Kwargs)
	if err != nil {
		return Response{}, err
	}
	if contentType != "" && headers.Get("Content-Type") == "" {
		headers.Set("Content-Type", contentType)
	}
	req, err := http.NewRequestWithContext(ctx, strings.ToUpper(input.Method), target.String(), bytes.NewReader(body))
	if err != nil {
		return Response{}, errChannelHTTP
	}
	req.Header = headers
	var base *http.Transport
	if seconds, ok := input.Kwargs["timeout"].(int); ok && seconds > 0 {
		base = requestsproxy.NewRequestsTimeoutTransportFrom(http.DefaultTransport.(*http.Transport), time.Duration(seconds)*time.Second)
	} else {
		base = http.DefaultTransport.(*http.Transport).Clone()
		base.TLSHandshakeTimeout = 0
	}
	proxies, _ := input.Kwargs["proxies"].(map[string]string)
	transport, err := requestsproxy.NewRequestsProxyTransport(base, h.Environment, proxies, target)
	if err != nil {
		return Response{}, errChannelHTTP
	}
	defer transport.CloseIdleConnections()
	jar, _ := cookiejar.New(nil)
	client := http.Client{Transport: transport, Jar: jar, CheckRedirect: checkChannelRedirect}
	reply, err := client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return Response{}, ctx.Err()
		}
		return Response{}, errChannelHTTP
	}
	defer reply.Body.Close()
	content, err := io.ReadAll(reply.Body)
	if err != nil {
		return Response{}, errChannelHTTP
	}
	decoded, decodeErr := Decode(content)
	return Response{Status: reply.StatusCode, JSON: decoded, JSONError: decodeErr != nil, Text: string(content), Content: content}, nil
}

func httpDestination(target *url.URL) bool {
	return target != nil && target.Hostname() != "" && (target.Scheme == "http" || target.Scheme == "https")
}

func checkChannelRedirect(next *http.Request, via []*http.Request) error {
	if len(via) >= 30 || !httpDestination(next.URL) {
		return errChannelHTTP
	}
	last := via[len(via)-1].URL
	if !strings.EqualFold(last.Host, next.URL.Host) || last.Scheme != next.URL.Scheme {
		// Channel bodies can contain bearer tokens or recipient credentials.
		if next.Body != nil && next.Body != http.NoBody {
			return errChannelHTTP
		}
		next.Header.Del("Authorization")
		next.Header.Del("Cookie")
	}
	return nil
}

func requestBody(kwargs map[string]any) ([]byte, string, error) {
	switch data := kwargs["data"].(type) {
	case nil:
		if body := kwargs["json"]; body != nil {
			value, err := pyJSONString(body)
			return []byte(value), "application/json", err
		}
		return nil, "", nil
	case string:
		return []byte(data), "", nil
	case []byte:
		return data, "", nil
	default:
		value, err := requestForm(data)
		return []byte(value), "application/x-www-form-urlencoded", err
	}
}

func requestForm(input any) (string, error) {
	if input == nil {
		return "", nil
	}
	if raw, ok := input.(string); ok {
		return raw, nil
	}
	var fields object
	switch values := input.(type) {
	case object:
		fields = values
	case map[string]any:
		keys := make([]string, 0, len(values))
		for key := range values {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			fields.put(key, values[key])
		}
	default:
		return "", errChannelHTTP
	}
	var pairs []string
	for _, field := range fields {
		values, ok := field.v.([]any)
		if !ok {
			values = []any{field.v}
		}
		for _, value := range values {
			if value != nil {
				pairs = append(pairs, url.QueryEscape(str(field.k))+"="+url.QueryEscape(str(value)))
			}
		}
	}
	return strings.Join(pairs, "&"), nil
}
