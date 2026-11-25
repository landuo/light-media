package remoteidentifiers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	sdk "light-media/builtinplugins/internal/sdk"
)

// Keep the existing host word store bounds on both downloaded and merged data.
const remoteWordsMaxBytes = 8 << 20

type collection struct {
	urls    []string
	pending []mappingSource
	words   []string
	bytes   int
	filter  bool
}

func newCollection(fileURLs string, filter bool) collection {
	return collection{urls: strings.Split(fileURLs, "\n"), words: []string{Marker}, bytes: len(Marker), filter: filter}
}

// One ordered source request at a time keeps the original stop-on-error and
// whole-collection retry behavior across bounded SDK callback phases.
func (c *collection) step(ctx context.Context, caller sdk.HostCaller) (bool, error) {
	network := sdk.Network{Host: caller}
	logger := sdk.Logger{Host: caller}
	var source mappingSource
	if len(c.pending) > 0 {
		source, c.pending = c.pending[0], c.pending[1:]
	} else {
		for len(c.urls) > 0 && source.url == "" {
			source.url, c.urls = Strip(c.urls[0]), c.urls[1:]
		}
		if source.url == "" {
			return true, nil
		}
		if strings.HasSuffix(strings.ToLower(source.url), ".json") {
			body, err := sourceText(ctx, network, source.url)
			if err != nil {
				return false, err
			}
			c.pending, err = sourceMapping(body)
			if err == nil {
				_ = logger.Log(ctx, sdk.LogInfo, fmt.Sprintf("订阅文件解析成功，共 %d 个词表", len(c.pending)))
			}
			return false, err
		}
	}
	body, err := sourceText(ctx, network, source.url)
	if err != nil {
		return false, err
	}
	lines := strings.Split(body, "\n")
	for _, line := range lines {
		if c.filter && line == "" {
			continue
		}
		c.bytes += len(line)
		if c.bytes > remoteWordsMaxBytes || len(c.words) >= 100000 || len(line) > 64<<10 {
			return false, errors.New("远程识别词超过允许范围")
		}
		c.words = append(c.words, line)
	}
	if source.named {
		_ = logger.Log(ctx, sdk.LogInfo, fmt.Sprintf("词表获取成功，识别词数量：%d", len(lines)))
	}
	return false, nil
}

func sourceText(ctx context.Context, network sdk.Network, url string) (string, error) {
	if strings.Contains(url, "etherpad") && !strings.Contains(url, "export") {
		url += "/export/txt"
	}
	result, err := network.Get(ctx, sdk.NetworkGetRequestV1{
		URL:     url,
		Profile: sdk.NetworkGetSourceRemoteIdentifiersProfile,
	})
	if err != nil || result.Response == nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", errors.New("远程词表连接失败，请检查地址与网络")
	}
	response := result.Response
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("远程词表返回了非成功状态（%d）", response.StatusCode)
	}
	if len(response.Content) > remoteWordsMaxBytes {
		return "", errors.New("远程词表超过8 MiB")
	}
	if !utf8.Valid(response.Content) {
		return "", errors.New("远程词表必须使用UTF-8编码")
	}
	body := string(response.Content)
	if strings.Index(body, "doctype html") > 0 || strings.Contains(body, "try again later") {
		return "", errors.New("远程地址返回网页或限流提示，未应用词表")
	}
	return body, nil
}

type mappingSource struct {
	url   string
	named bool
}

// Python dict retains a duplicate key's first position and last value.
// Validate URL values after parsing all entries.
func sourceMapping(body string) ([]mappingSource, error) {
	if containsIsolatedSurrogate(body) {
		return nil, errors.New("词表索引含无法转换的Unicode字符")
	}
	body, err := normalizeMappingJSON(body)
	if err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(strings.NewReader(body))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return nil, errors.New("词表索引必须是名称到地址的JSON对象")
	}
	var keys []string
	values := make(map[string]json.RawMessage)
	for decoder.More() {
		token, err = decoder.Token()
		if err != nil {
			return nil, errors.New("词表索引不是有效的JSON对象")
		}
		key, ok := token.(string)
		var value json.RawMessage
		if !ok || decoder.Decode(&value) != nil {
			return nil, errors.New("词表索引的地址必须为非空字符串")
		}
		if _, exists := values[key]; !exists {
			keys = append(keys, key)
		}
		values[key] = value
		if len(keys) > 2048 {
			return nil, errors.New("词表索引条数过多")
		}
	}
	if _, err = decoder.Token(); err != nil || decoder.Decode(new(json.RawMessage)) != io.EOF {
		return nil, errors.New("词表索引含无效的JSON内容")
	}
	out := make([]mappingSource, 0, len(keys))
	for _, key := range keys {
		var url string
		if json.Unmarshal(values[key], &url) != nil {
			return nil, errors.New("词表索引的地址必须为非空字符串")
		}
		url = Strip(url)
		if url == "" {
			return nil, errors.New("词表索引的地址必须为非空字符串")
		}
		out = append(out, mappingSource{url: url, named: key != ""})
	}
	return out, nil
}

// Python json.loads accepts non-finite number tokens. They count as non-string
// values, which may be overwritten by a later duplicate key. Null has the same
// effect on mapping validation. Escapes and string tokens remain unchanged.
func normalizeMappingJSON(body string) (string, error) {
	var out strings.Builder
	quoted := false
	for i := 0; i < len(body); {
		c := body[i]
		if quoted {
			out.WriteByte(c)
			i++
			if c == '\\' && i < len(body) {
				out.WriteByte(body[i])
				i++
				continue
			}
			if c == '"' {
				quoted = false
			}
			continue
		}
		if c == '"' {
			quoted = true
			out.WriteByte(c)
			i++
			continue
		}
		replaced := false
		for _, literal := range []string{"-Infinity", "Infinity", "NaN"} {
			if strings.HasPrefix(body[i:], literal) {
				out.WriteString("null")
				i += len(literal)
				replaced = true
				break
			}
		}
		if replaced {
			continue
		}
		if c == '-' || c >= '0' && c <= '9' {
			start := i
			i++
			for i < len(body) && strings.ContainsRune("0123456789.eE+-", rune(body[i])) {
				i++
			}
			number := body[start:i]
			// The installed CPython rejects integers above 4300 digits even
			// when a duplicate would overwrite the value.
			if !strings.ContainsAny(number, ".eE") && len(strings.TrimPrefix(number, "-")) > 4300 {
				return "", errors.New("词表索引整数超过原版JSON解析范围")
			}
			out.WriteString(number)
			continue
		}
		out.WriteByte(c)
		i++
	}
	return out.String(), nil
}

// Reject isolated escapes before Go's JSON decoder replaces them with U+FFFD
// and changes a mapping key or URL.
func containsIsolatedSurrogate(body string) bool {
	quoted := false
	for i := 0; i < len(body); i++ {
		if body[i] == '"' {
			quoted = !quoted
			continue
		}
		if !quoted || body[i] != '\\' {
			continue
		}
		i++
		if i+4 >= len(body) || body[i] != 'u' {
			continue
		}
		unit, err := strconv.ParseUint(body[i+1:i+5], 16, 16)
		i += 4
		if err != nil {
			continue
		}
		if unit >= 0xdc00 && unit <= 0xdfff {
			return true
		}
		if unit < 0xd800 || unit > 0xdbff {
			continue
		}
		if i+6 >= len(body) || body[i+1:i+3] != "\\u" {
			return true
		}
		low, err := strconv.ParseUint(body[i+3:i+7], 16, 16)
		if err != nil || low < 0xdc00 || low > 0xdfff {
			return true
		}
		i += 6
	}
	return false
}
