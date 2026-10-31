// Package torrentmaterial implements the source DownloadChain.download_torrent
// wrapper. Network, persistent cache and notifications are explicit host ports;
// none of these values grants a caller network or credential authority.
package torrentmaterial

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"light-media/internal/jinja/exec"
	"light-media/internal/ocr"
	"light-media/internal/plugins"
	"light-media/internal/plugins/iyuuautoseed"
	"light-media/internal/requestsjson"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

type Value = plugins.OCRValue

// Torrent contains only attributes consumed by this source method. Nil remains
// Python None, including in failure message formatting; it is not an empty name.
type Torrent struct {
	Enclosure, Title, SiteName, SiteCookie, SiteUA *string
	SiteProxy                                      bool
}
type MessageContext struct {
	Channel, Source *string
	UserID          json.RawMessage
}
type Notification struct {
	Channel, Source   *string
	UserID            json.RawMessage
	Type, Title, Text string
}
type DescriptorRequest struct {
	Method, URL       string
	UserAgent, Cookie *string
	Proxy             bool
	Headers, Params   Value
}
type DescriptorResponse struct {
	Status int
	// Text is the host transport's requests-compatible response.text decoding.
	Text        string
	TextError   error
	Body        []byte
	ContentType string
}
type MaterialRequest struct {
	URL                 string
	Cookie              *string
	UserAgent           string
	Proxy, CacheInvalid bool
}
type MaterialResult struct {
	Content      *plugins.TorrentMaterialContent
	Folder       string
	Files        []string
	ErrorMessage string
}
type Chain struct {
	UserAgent string
	Request   func(context.Context, DescriptorRequest) (*DescriptorResponse, error)
	Material  func(context.Context, MaterialRequest) (MaterialResult, error)
	Notify    func(context.Context, Notification) error
	// Log is a host-only callback into its existing attributed/redacted logger.
	Log func(context.Context, string, string)
}

// SourceError keeps the source exception category without exposing descriptor
// cookies, headers, private URLs or response bodies through an error string.
type SourceError struct{ Class string }

func (e *SourceError) Error() string      { return "torrent material: " + e.Class }
func sourceError(class string) error      { return &SourceError{Class: class} }
func none() plugins.TorrentMaterialResult { return plugins.TorrentMaterialResult{Files: []string{}} }
func stringResult(s string) plugins.TorrentMaterialResult {
	return plugins.TorrentMaterialResult{Content: &plugins.TorrentMaterialContent{Kind: "string", String: s}, Files: []string{}}
}
func optional(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}
func sourceOptional(v *string) string {
	if v == nil {
		return "None"
	}
	return *v
}

// FromHelper projects the existing full TorrentHelper result, not the stricter
// native Material parser. A malformed nonempty bencode body therefore survives.
func FromHelper(result iyuuautoseed.TorrentResult) MaterialResult {
	out := MaterialResult{Folder: result.Folder, Files: append([]string{}, result.Files...), ErrorMessage: result.ErrorMessage}
	if result.Magnet != "" {
		out.Content = &plugins.TorrentMaterialContent{Kind: "string", String: result.Magnet}
	} else if result.Content != nil {
		out.Content = &plugins.TorrentMaterialContent{Kind: "bytes", Bytes: bytes.Clone(result.Content)}
	}
	return out
}

func (c Chain) DownloadTorrent(ctx context.Context, torrent Torrent, message MessageContext) (plugins.TorrentMaterialResult, error) {
	if ctx == nil {
		return none(), errors.New("torrent material context required")
	}
	if err := ctx.Err(); err != nil {
		return none(), err
	}
	enclosure := optional(torrent.Enclosure)
	if enclosure == "" {
		return none(), nil
	}
	if strings.HasPrefix(enclosure, "magnet:") {
		return stringResult(enclosure), nil
	}
	cookie := torrent.SiteCookie
	indirect := strings.HasPrefix(enclosure, "[")
	address := enclosure
	if indirect {
		var err error
		address, err = c.redirectURL(ctx, enclosure, torrent.SiteUA, cookie)
		if err != nil {
			return none(), err
		}
		cookie = nil
	}
	if address == "" {
		c.log(ctx, "ERROR", sourceOptional(torrent.Title)+" 无法获取下载地址！")
		return none(), nil
	}
	if err := ctx.Err(); err != nil {
		return none(), err
	}
	if c.Material == nil {
		return none(), errors.New("torrent material dependency unavailable")
	}
	ua := optional(torrent.SiteUA)
	if ua == "" {
		ua = c.UserAgent
	}
	result, err := c.Material(ctx, MaterialRequest{URL: address, Cookie: cookie, UserAgent: ua, Proxy: torrent.SiteProxy, CacheInvalid: !indirect})
	if err != nil {
		return none(), err
	}
	if err = ctx.Err(); err != nil {
		return none(), err
	}
	if result.Content != nil && result.Content.Kind == "string" {
		return stringResult(result.Content.String), nil
	}
	if result.Content != nil && result.Content.Kind != "bytes" {
		return none(), errors.New("invalid torrent material content kind")
	}
	if result.Content == nil || len(result.Content.Bytes) == 0 {
		c.log(ctx, "ERROR", "下载种子文件失败："+sourceOptional(torrent.Title))
		if c.Notify == nil {
			return none(), errors.New("torrent material notification dependency unavailable")
		}
		source := message.Source
		if optional(message.Channel) == "" {
			source = nil
		}
		err = c.Notify(ctx, Notification{Channel: message.Channel, Source: source, UserID: bytes.Clone(message.UserID), Type: "手动处理", Title: sourceOptional(torrent.Title) + " 种子下载失败！", Text: "错误信息：" + result.ErrorMessage + "\n站点：" + sourceOptional(torrent.SiteName)})
		return none(), err
	}
	return plugins.TorrentMaterialResult{Content: &plugins.TorrentMaterialContent{Kind: "bytes", Bytes: bytes.Clone(result.Content.Bytes)}, Folder: result.Folder, Files: append([]string{}, result.Files...)}, nil
}

var descriptorPattern = regexp.MustCompile(`\[(.*)](.*)`)

func nullValue() Value           { return Value{Kind: "null"} }
func stringValue(s string) Value { return Value{Kind: "string", Points: []rune(s)} }
func valueGet(object Value, key Value) (Value, error) {
	if object.Kind != "object" {
		return Value{}, sourceError("AttributeError")
	}
	if key.Kind == "list" || key.Kind == "object" {
		return Value{}, sourceError("TypeError")
	}
	if key.Kind != "string" {
		return nullValue(), nil
	}
	for _, entry := range object.Entries {
		if len(entry.Key) != len(key.Points) {
			continue
		}
		equal := true
		for i := range entry.Key {
			if entry.Key[i] != key.Points[i] {
				equal = false
				break
			}
		}
		if equal {
			return entry.Value, nil
		}
	}
	return nullValue(), nil
}
func field(object Value, name string) Value {
	// req_params is checked before the first .get; JSON dictionary keys are str.
	v, _ := valueGet(object, stringValue(name))
	return v
}

func (c Chain) redirectURL(ctx context.Context, descriptor string, ua, cookie *string) (string, error) {
	match := descriptorPattern.FindStringSubmatch(descriptor)
	if match == nil {
		return "", nil
	}
	if match[1] == "" {
		return match[2], nil
	}
	decoded, err := decodeDescriptorBase64([]byte(match[1]))
	if err != nil {
		return "", err
	}
	if !utf8.Valid(decoded) {
		return "", sourceError("UnicodeDecodeError")
	}
	params, err := ocr.ParseSourceJSON(decoded)
	if err != nil {
		return "", err
	}
	if params.Kind != "object" {
		return "", sourceError("AttributeError")
	}
	if !field(params, "cookie").Truthy() {
		cookie = nil
	}
	headers := field(params, "header")
	if !headers.Truthy() {
		headers = nullValue()
	}
	method := "POST"
	m := field(params, "method")
	if m.Kind == "string" && pointsEqual(m.Points, []rune("get")) {
		method = "GET"
	}
	if c.Request == nil {
		return "", errors.New("torrent descriptor HTTP dependency unavailable")
	}
	response, err := c.Request(ctx, DescriptorRequest{Method: method, URL: match[2], UserAgent: ua, Cookie: cookie, Proxy: field(params, "proxy").Truthy(), Headers: headers, Params: field(params, "params")})
	if err != nil {
		return "", err
	}
	if err = ctx.Err(); err != nil {
		return "", err
	}
	if response == nil || response.Status >= 400 && response.Status < 600 {
		return "", nil
	}
	result := field(params, "result")
	if !result.Truthy() {
		if response.TextError != nil {
			return "", response.TextError
		}
		return response.Text, nil
	}
	text, err := requestsjson.ResponseJSONText(response.Body, response.ContentType)
	if err != nil {
		return "", err
	}
	data, err := ocr.ParseSourceJSON([]byte(text))
	if err != nil {
		return "", err
	}
	success := field(params, "success")
	if success.Truthy() {
		ok, err := valueGet(data, success)
		if err != nil {
			return "", err
		}
		if !ok.Truthy() {
			return "", nil
		}
	}
	keys := result.Points
	if result.Kind != "string" {
		keyText, keyErr := sourceString(result, false)
		if keyErr != nil {
			return "", keyErr
		}
		keys = []rune(keyText)
	}
	for _, key := range splitKeyPoints(keys) {
		data, err = valueGet(data, Value{Kind: "string", Points: key})
		if err != nil {
			return "", err
		}
		if !data.Truthy() {
			return "", nil
		}
	}
	path, query := field(params, "result_path"), field(params, "result_query_param")
	base := field(params, "result_base_url")
	if path.Truthy() && query.Truthy() {
		baseText, err := sourceString(base, false)
		if err != nil {
			return "", err
		}
		pathText, err := sourceString(path, false)
		if err != nil {
			return "", err
		}
		joined, err := iyuuautoseed.TorrentMaterialURLJoin(strings.TrimRight(baseText, "/")+"/", strings.TrimLeft(pathText, "/"))
		if err != nil {
			return "", err
		}
		if query.Kind == "list" || query.Kind == "object" {
			return "", sourceError("TypeError")
		}
		queryText, err := sourceString(query, false)
		if err != nil {
			return "", err
		}
		dataText, err := sourceString(data, false)
		if err != nil {
			return "", err
		}
		return joined + "?" + url.QueryEscape(queryText) + "=" + url.QueryEscape(dataText), nil
	}
	// Unlike result_path/query construction, the source does NOT str() this
	// value. Non-string JSON reaches urlparse/startswith and raises.
	if data.Kind != "string" {
		// A falsey or origin-less base returns the raw value from the source
		// normalizer: INFO is emitted before TorrentHelper.startswith fails.
		// A usable base parses the non-string URL first and raises before INFO.
		hasOrigin := false
		if base.Truthy() {
			if base.Kind != "string" {
				return "", sourceError("AttributeError")
			}
			baseText, err := sourceString(base, false)
			if err != nil {
				return "", err
			}
			hasOrigin, err = iyuuautoseed.TorrentMaterialBaseHasOrigin(baseText)
			if err != nil {
				return "", err
			}
		}
		if !hasOrigin {
			c.log(ctx, "INFO", "已获取到站点临时下载地址")
		}
		return "", sourceError("AttributeError")
	}
	address, err := sourceString(data, false)
	if err != nil {
		return "", err
	}
	if !base.Truthy() {
		c.log(ctx, "INFO", "已获取到站点临时下载地址")
		return address, nil
	}
	if base.Kind != "string" {
		return "", sourceError("AttributeError")
	}
	baseText, err := sourceString(base, false)
	if err != nil {
		return "", err
	}
	normalized, err := iyuuautoseed.NormalizeTorrentMaterialURL(address, baseText)
	if err == nil {
		c.log(ctx, "INFO", "已获取到站点临时下载地址")
	}
	return normalized, err
}

func pointsEqual(a, b []rune) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// Python b64decode(validate=False) ignores non-alphabet bytes, accepts excess
// padding, and stops at a completed padded group; it does not repair missing
// padding. This is intentionally not the OCR helper's padded normalization.
func decodeDescriptorBase64(input []byte) ([]byte, error) {
	out := make([]byte, 0, len(input)*3/4)
	position, pads := 0, 0
	var carry byte
	for _, b := range input {
		if b == '=' {
			if position >= 2 {
				pads++
				if position+pads >= 4 {
					return out, nil
				}
			}
			continue
		}
		n := -1
		switch {
		case b >= 'A' && b <= 'Z':
			n = int(b - 'A')
		case b >= 'a' && b <= 'z':
			n = int(b-'a') + 26
		case b >= '0' && b <= '9':
			n = int(b-'0') + 52
		case b == '+':
			n = 62
		case b == '/':
			n = 63
		}
		if n < 0 {
			continue
		}
		pads = 0
		switch position {
		case 0:
			carry = byte(n)
		case 1:
			out = append(out, carry<<2|byte(n)>>4)
			carry = byte(n) & 15
		case 2:
			out = append(out, carry<<4|byte(n)>>2)
			carry = byte(n) & 3
		case 3:
			out = append(out, carry<<6|byte(n))
		}
		position = (position + 1) % 4
	}
	if position != 0 {
		return nil, sourceError("Error")
	}
	return out, nil
}

// Only JSON values are involved; no arbitrary Python construction is supported.
// Existing Python formatting is reused for valid strings and binary64 numbers.
func sourceString(v Value, repr bool) (string, error) {
	switch v.Kind {
	case "null":
		return "None", nil
	case "bool":
		if v.Bool {
			return "True", nil
		}
		return "False", nil
	case "int":
		return v.Number, nil
	case "float":
		switch v.Number {
		case "nan":
			return "nan", nil
		case "+inf":
			return "inf", nil
		case "-inf":
			return "-inf", nil
		}
		n, err := strconv.ParseFloat(v.Number, 64)
		if err != nil {
			return "", err
		}
		return exec.AsValue(n).String(), nil
	case "string":
		for _, r := range v.Points {
			if r >= 0xd800 && r <= 0xdfff {
				if !repr {
					return "", sourceError("UnicodeEncodeError")
				}
				return surrogateRepr(v.Points), nil
			}
		}
		s := string(v.Points)
		if repr {
			return exec.PythonRepr(exec.AsValue(s)), nil
		}
		return s, nil
	case "list":
		parts := make([]string, 0, len(v.Items))
		for _, item := range v.Items {
			p, err := sourceString(item, true)
			if err != nil {
				return "", err
			}
			parts = append(parts, p)
		}
		return "[" + strings.Join(parts, ", ") + "]", nil
	case "object":
		parts := make([]string, 0, len(v.Entries))
		for _, entry := range v.Entries {
			k, err := sourceString(Value{Kind: "string", Points: entry.Key}, true)
			if err != nil {
				return "", err
			}
			p, err := sourceString(entry.Value, true)
			if err != nil {
				return "", err
			}
			parts = append(parts, k+": "+p)
		}
		return "{" + strings.Join(parts, ", ") + "}", nil
	}
	return "", sourceError("TypeError")
}
func surrogateRepr(points []rune) string {
	// Python chooses double quotes only for strings containing a single quote
	// and no double quote; escaped surrogate code points themselves are ASCII.
	quote := '\''
	hasSingle, hasDouble := false, false
	for _, r := range points {
		hasSingle = hasSingle || r == '\''
		hasDouble = hasDouble || r == '"'
	}
	if hasSingle && !hasDouble {
		quote = '"'
	}
	var out strings.Builder
	out.WriteRune(quote)
	for _, r := range points {
		switch {
		case r == quote || r == '\\':
			out.WriteRune('\\')
			out.WriteRune(r)
		case r >= 0xd800 && r <= 0xdfff:
			out.WriteString(`\u`)
			out.WriteString(strconv.FormatInt(int64(r), 16))
		default:
			rep := exec.PythonRepr(exec.AsValue(string(r)))
			out.WriteString(rep[1 : len(rep)-1])
		}
	}
	out.WriteRune(quote)
	return out.String()
}

func splitKeyPoints(points []rune) [][]rune {
	out := [][]rune{}
	start := 0
	for i, point := range points {
		if point == '.' {
			out = append(out, points[start:i])
			start = i + 1
		}
	}
	return append(out, points[start:])
}

func (c Chain) log(ctx context.Context, level, message string) {
	if c.Log != nil {
		c.Log(ctx, level, message)
	}
}
