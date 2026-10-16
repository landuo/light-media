package app

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"light-media/internal/media/words"
	"light-media/internal/plugins/builtin"
)

type identifiersFetcher struct {
	client *http.Client
	sleep  func(context.Context, time.Duration) error
}

func identifiersClient() *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, errors.New("invalid words endpoint")
		}
		addresses, err := net.DefaultResolver.LookupIPAddr(ctx, host)
		if err != nil {
			return nil, errors.New("words endpoint DNS failed")
		}
		for _, address := range addresses {
			ip := address.IP
			if ip.IsUnspecified() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() {
				return nil, errors.New("words endpoint network range denied")
			}
		}
		for _, address := range addresses {
			connection, err := (&net.Dialer{Timeout: 15 * time.Second, KeepAlive: 30 * time.Second}).DialContext(ctx, network, net.JoinHostPort(address.IP.String(), port))
			if err == nil {
				return connection, nil
			}
		}
		return nil, errors.New("words endpoint connection failed")
	}
	return &http.Client{Transport: transport, Timeout: 15 * time.Second, CheckRedirect: identifiersRedirect}
}

func identifiersURL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "http" && u.Scheme != "https" || u.Hostname() == "" || u.Opaque != "" || strings.ContainsAny(raw, "\x00\r\n") {
		return nil, bad("INVALID_IDENTIFIERS_URL", "远程词表必须是有效的HTTP或HTTPS文件地址")
	}
	// Explicit local Etherpad is allowed. Metadata/link-local destinations are
	// never meaningful word list sources, including through a remote index.
	if ip := net.ParseIP(u.Hostname()); ip != nil && (ip.IsUnspecified() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast()) {
		return nil, bad("INVALID_IDENTIFIERS_URL", "远程词表地址不允许访问此网络范围")
	}
	return u, nil
}

func identifiersRedirect(request *http.Request, via []*http.Request) error {
	if len(via) >= 10 {
		return bad("IDENTIFIERS_REDIRECT_FAILED", "远程词表重定向过多")
	}
	if _, err := identifiersURL(request.URL.String()); err != nil {
		return err
	}
	previous := via[len(via)-1].URL
	if previous.Scheme == "https" && request.URL.Scheme != "https" {
		return bad("IDENTIFIERS_REDIRECT_FAILED", "远程词表不允许HTTPS降级")
	}
	if !strings.EqualFold(previous.Host, request.URL.Host) || previous.Scheme != request.URL.Scheme {
		request.Header.Del("Authorization")
		request.Header.Del("Cookie")
		request.Header.Del("Proxy-Authorization")
		if previous.User != nil || request.URL.User != nil || previous.RawQuery != "" {
			return bad("IDENTIFIERS_REDIRECT_FAILED", "私有订阅地址不允许跨来源重定向")
		}
	}
	return nil
}

func (f identifiersFetcher) text(ctx context.Context, raw string) (string, error) {
	if strings.Contains(raw, "etherpad") && !strings.Contains(raw, "export") {
		raw += "/export/txt"
	}
	u, err := identifiersURL(raw)
	if err != nil {
		return "", err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return "", bad("INVALID_IDENTIFIERS_URL", "远程词表请求无效")
	}
	request.Header.Set("User-Agent", "LightMedia/RemoteIdentifiers")
	response, err := f.client.Do(request)
	if err != nil {
		return "", bad("IDENTIFIERS_FETCH_FAILED", "远程词表连接失败，请检查地址与网络")
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return "", bad("IDENTIFIERS_HTTP_FAILED", "远程词表返回了非成功状态")
	}
	rawBytes, err := io.ReadAll(io.LimitReader(response.Body, words.MaxBytes+1))
	if err != nil || len(rawBytes) > words.MaxBytes {
		return "", bad("IDENTIFIERS_BODY_LIMIT", "远程词表读取失败或超过8 MiB")
	}
	if !utf8.Valid(rawBytes) {
		return "", bad("IDENTIFIERS_ENCODING_FAILED", "远程词表必须使用UTF-8编码")
	}
	text := string(rawBytes)
	if strings.Index(text, "doctype html") > 0 || strings.Contains(text, "try again later") {
		return "", bad("IDENTIFIERS_NOT_WORDS", "远程地址返回网页或限流提示，未应用词表")
	}
	return text, nil
}

// Python dict preserves first insertion position and the last duplicate value.
// Decode tokens instead of a Go map so list application order stays identical.
func identifiersMapping(text string) ([]string, error) {
	decoder := json.NewDecoder(strings.NewReader(text))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return nil, bad("IDENTIFIERS_MAPPING_INVALID", "词表索引必须是名称到地址的JSON对象")
	}
	values := []string{}
	positions := map[string]int{}
	for decoder.More() {
		token, err = decoder.Token()
		if err != nil {
			return nil, bad("IDENTIFIERS_MAPPING_INVALID", "词表索引不是有效的JSON对象")
		}
		key, ok := token.(string)
		var value string
		if !ok || decoder.Decode(&value) != nil || strings.TrimSpace(value) == "" {
			return nil, bad("IDENTIFIERS_MAPPING_INVALID", "词表索引的地址必须为非空字符串")
		}
		value = strings.TrimSpace(value)
		if pos, ok := positions[key]; ok {
			values[pos] = value
		} else {
			positions[key] = len(values)
			values = append(values, value)
		}
		if len(values) > 2048 {
			return nil, bad("IDENTIFIERS_MAPPING_LIMIT", "词表索引条数过多")
		}
	}
	if _, err = decoder.Token(); err != nil || decoder.Decode(new(any)) != io.EOF {
		return nil, bad("IDENTIFIERS_MAPPING_INVALID", "词表索引含无效的JSON内容")
	}
	return values, nil
}

func (f identifiersFetcher) collect(ctx context.Context, config builtin.RemoteIdentifiersConfiguration) ([]string, error) {
	result := []string{remoteIdentifiersMarker}
	total := len(remoteIdentifiersMarker)
	appendText := func(text string) error {
		for _, line := range strings.Split(text, "\n") {
			if config.Flitter && line == "" {
				continue
			}
			total += len(line)
			if total > words.MaxBytes || len(result) >= 100000 || len(line) > 64<<10 {
				return bad("IDENTIFIERS_WORD_LIMIT", "远程识别词超过允许范围")
			}
			result = append(result, line)
		}
		return nil
	}
	for _, raw := range strings.Split(config.FileURLs, "\n") {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		if strings.HasSuffix(strings.ToLower(raw), ".json") {
			text, err := f.text(ctx, raw)
			if err != nil {
				return nil, err
			}
			urls, err := identifiersMapping(text)
			if err != nil {
				return nil, err
			}
			for _, mapped := range urls {
				text, err := f.text(ctx, mapped)
				if err != nil {
					return nil, err
				}
				if err = appendText(text); err != nil {
					return nil, err
				}
			}
		} else {
			text, err := f.text(ctx, raw)
			if err != nil {
				return nil, err
			}
			if err = appendText(text); err != nil {
				return nil, err
			}
		}
	}
	return result, nil
}

func (f identifiersFetcher) fetch(ctx context.Context, config builtin.RemoteIdentifiersConfiguration) ([]string, error) {
	var last error
	for attempt := 0; attempt < 3; attempt++ {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		result, err := f.collect(ctx, config)
		if err == nil {
			return result, nil
		}
		last = err
		if attempt == 2 {
			break
		}
		delay := time.Duration(5<<attempt) * time.Second
		if f.sleep != nil {
			err = f.sleep(ctx, delay)
		} else {
			timer := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				err = ctx.Err()
			case <-timer.C:
			}
			timer.Stop()
		}
		if err != nil {
			return nil, err
		}
	}
	if last == nil {
		last = errors.New("identifiers unavailable")
	}
	return nil, last
}
