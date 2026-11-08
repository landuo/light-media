package download

// Cookie parsing follows CPython 3.12.13 http.cookies, used by the fixed
// MoviePilot Dockerfile. PSF license/provenance are preserved in
// testdata/plugin-host-downloader-20261031/cpython-LICENSE.txt.

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"math/big"
	"mime/multipart"
	"net/http"
	"net/netip"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	pep440 "github.com/aquasecurity/go-pep440-version"
	"golang.org/x/text/unicode/norm"
	"light-media/internal/releasemeta/legacyanime"
)

var pluginCookieReserved = map[string]bool{"expires": true, "path": true, "comment": true, "domain": true, "max-age": true, "secure": true, "httponly": true, "version": true, "samesite": true}
var pluginCookieLegalKey = regexp.MustCompile("^[A-Za-z0-9!#$%&'*+\\-.^_`|~:]+$")
var pluginCookiePattern = regexp.MustCompile("^[\\t\\n\\r\\f\\v ]*([A-Za-z0-9_!#%&'~`><@,:/$*+.\\^|)(?}{=\\-]+?)(?:[\\t\\n\\r\\f\\v ]*=[\\t\\n\\r\\f\\v ]*(\"(?:[^\\\\\"]|\\\\.)*\"|(?:[A-Za-z0-9_]{3,6}day|[A-Za-z0-9_]{3}),[\\t\\n\\r\\f\\v ][A-Za-z0-9_\\t\\n\\r\\f\\v \\-]{9,11}[\\t\\n\\r\\f\\v ][0-9:]{8}[\\t\\n\\r\\f\\v ]GMT|[A-Za-z0-9_!#%&'~`><@,:/$*+.\\^|)(?}{=\\[\\]\\-]*))?[\\t\\n\\r\\f\\v ]*(?:[\\t\\n\\r\\f\\v ]+|;|$)")

// _try_read_torrent calls Python urlparse before delegating a string. Retain
// its actual bracketed-host and NFKC authority failures without using Go's
// stricter URL escape/port rules, which would reject strings the source sends.
func pluginHostTransmissionStringValid(value string) bool {
	value = strings.NewReplacer("\t", "", "\r", "", "\n", "").Replace(strings.TrimLeftFunc(value, func(c rune) bool { return c <= 32 }))
	if colon := strings.IndexByte(value, ':'); colon > 0 {
		scheme := value[:colon]
		valid := true
		for i, c := range scheme {
			valid = valid && (c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || i > 0 && (c >= '0' && c <= '9' || c == '+' || c == '-' || c == '.'))
		}
		if valid {
			if strings.EqualFold(scheme, "file") {
				return false
			}
			value = value[colon+1:]
		}
	}
	if !strings.HasPrefix(value, "//") {
		return true
	}
	netloc := value[2:]
	if end := strings.IndexAny(netloc, "/?#"); end >= 0 {
		netloc = netloc[:end]
	}
	left, right := strings.IndexByte(netloc, '['), strings.IndexByte(netloc, ']')
	if left >= 0 != (right >= 0) || left >= 0 && right < left {
		return false
	}
	if left >= 0 {
		host := netloc[left+1 : right]
		if strings.HasPrefix(host, "v") {
			if !regexp.MustCompile(`^v[0-9A-Fa-f]+\..+$`).MatchString(host) {
				return false
			}
		} else if ip, err := netip.ParseAddr(host); err != nil || !ip.Is6() {
			return false
		}
	}
	without := strings.NewReplacer("@", "", ":", "", "#", "", "?", "").Replace(netloc)
	return norm.NFKC.String(without) == without || !strings.ContainsAny(norm.NFKC.String(without), "/?#@:")
}

func pluginCookieHostname(address string) (string, bool) {
	value := strings.TrimLeftFunc(address, func(c rune) bool { return c <= 32 })
	value = strings.NewReplacer("\t", "", "\r", "", "\n", "").Replace(value)
	if colon := strings.IndexByte(value, ':'); colon >= 0 {
		scheme := value[:colon]
		valid := scheme != ""
		for i, c := range scheme {
			valid = valid && (c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || i > 0 && (c >= '0' && c <= '9' || c == '+' || c == '-' || c == '.'))
		}
		if valid {
			value = value[colon+1:]
		}
	}
	if !strings.HasPrefix(value, "//") {
		return "", false
	}
	value = value[2:]
	if end := strings.IndexAny(value, "/?#"); end >= 0 {
		value = value[:end]
	}
	without := strings.NewReplacer("@", "", ":", "", "#", "", "?", "").Replace(value)
	if normalized := norm.NFKC.String(without); normalized != without && strings.ContainsAny(normalized, "/?#@:") {
		return "", false
	}
	if at := strings.LastIndexByte(value, '@'); at >= 0 {
		value = value[at+1:]
	}
	if strings.HasPrefix(value, "[") {
		end := strings.IndexByte(value, ']')
		if end < 0 {
			return "", false
		}
		value = value[1:end]
		if strings.HasPrefix(value, "v") {
			if !regexp.MustCompile(`^v[0-9A-Fa-f]+\..+$`).MatchString(value) {
				return "", false
			}
		} else {
			ip, err := netip.ParseAddr(value)
			if err != nil || !ip.Is6() {
				return "", false
			}
		}
	} else {
		if strings.ContainsAny(value, "[]") {
			return "", false
		}
		if colon := strings.IndexByte(value, ':'); colon >= 0 {
			value = value[:colon]
		}
	}
	if value == "" {
		return "", false
	}
	if zone := strings.IndexByte(value, '%'); zone >= 0 {
		return legacyanime.LowerCase(value[:zone]) + value[zone:], true
	}
	return legacyanime.LowerCase(value), true
}

func pluginCookieControls(value string) bool {
	for _, c := range value {
		if c < 32 || c == 127 {
			return true
		}
	}
	return false
}
func pluginCookieUnquote(value string) string {
	if len(value) < 2 || value[0] != '"' || value[len(value)-1] != '"' {
		return value
	}
	value = value[1 : len(value)-1]
	var out strings.Builder
	for i := 0; i < len(value); {
		if value[i] == '\\' && i+1 < len(value) {
			if i+3 < len(value) && value[i+1] >= '0' && value[i+1] <= '3' && value[i+2] >= '0' && value[i+2] <= '7' && value[i+3] >= '0' && value[i+3] <= '7' {
				n := int(value[i+1]-'0')*64 + int(value[i+2]-'0')*8 + int(value[i+3]-'0')
				out.WriteRune(rune(n))
				i += 4
				continue
			}
			_, n := utf8.DecodeRuneInString(value[i+1:])
			out.WriteString(value[i+1 : i+1+n])
			i += 1 + n
			continue
		}
		_, n := utf8.DecodeRuneInString(value[i:])
		out.WriteString(value[i : i+n])
		i += n
	}
	return out.String()
}

func pluginHostCookiePairs(header string) ([][2]string, error) {
	type item struct {
		attribute         bool
		key, value, coded string
	}
	items := []item{}
	seen := false
	for offset := 0; offset < len(header); {
		matches := pluginCookiePattern.FindStringSubmatchIndex(header[offset:])
		if matches == nil {
			break
		}
		key := header[offset+matches[2] : offset+matches[3]]
		hasValue := matches[4] >= 0
		value := ""
		if hasValue {
			value = header[offset+matches[4] : offset+matches[5]]
		}
		offset += matches[1]
		if strings.HasPrefix(key, "$") {
			if !seen {
				continue
			}
			items = append(items, item{attribute: true, key: key[1:], value: value, coded: value})
			continue
		}
		if pluginCookieReserved[strings.ToLower(key)] {
			if !seen {
				return nil, nil
			}
			if !hasValue && strings.ToLower(key) != "secure" && strings.ToLower(key) != "httponly" {
				return nil, nil
			}
			items = append(items, item{attribute: true, key: key, value: pluginCookieUnquote(value), coded: value})
			continue
		}
		if !hasValue {
			return nil, nil
		}
		items = append(items, item{key: key, value: pluginCookieUnquote(value), coded: value})
		seen = true
	}
	keys := []string{}
	values := map[string]string{}
	for _, entry := range items {
		if entry.attribute {
			if !pluginCookieReserved[strings.ToLower(entry.key)] || pluginCookieControls(entry.key) || pluginCookieControls(entry.value) {
				return nil, failure("plugin_cookie", "invalid_cookie")
			}
			continue
		}
		if !pluginCookieLegalKey.MatchString(entry.key) || pluginCookieControls(entry.key) || pluginCookieControls(entry.value) || pluginCookieControls(entry.coded) {
			return nil, failure("plugin_cookie", "invalid_cookie")
		}
		if _, exists := values[entry.key]; !exists {
			keys = append(keys, entry.key)
		}
		values[entry.key] = entry.value
	}
	out := [][2]string{}
	for _, key := range keys {
		out = append(out, [2]string{key, values[key]})
	}
	return out, nil
}

func pluginCookieScalar(value any) (string, error) {
	switch value := value.(type) {
	case string:
		return "s:" + value, nil
	case json.Number:
		n, ok := new(big.Rat).SetString(string(value))
		if !ok {
			return "", failure("plugin_cookie", "invalid_cookie_key")
		}
		return "n:" + n.RatString(), nil
	case bool:
		if value {
			return "n:1", nil
		}
		return "n:0", nil
	default:
		return "", failure("plugin_cookie", "invalid_cookie_key")
	}
}
func pluginCookieKey(row map[string]any) (string, bool, error) {
	if !iyuuTruth(row["domain"]) || !iyuuTruth(row["name"]) {
		return "", false, nil
	}
	path := row["path"]
	if !iyuuTruth(path) {
		path = "/"
	}
	parts := []string{}
	for _, value := range []any{row["domain"], path, row["name"]} {
		part, err := pluginCookieScalar(value)
		if err != nil {
			return "", false, err
		}
		parts = append(parts, part)
	}
	raw, _ := json.Marshal(parts)
	return string(raw), true, nil
}

// Lock is held by the add operation. Source failures deliberately fall back
// to the original cookie parameter; request guard failures still stop every
// following dispatch. Global cookie and add writes share one mutation seal.
func (c *IYUUClient) pluginHostSyncCookiesLocked(ctx context.Context, address, header string) (bool, error) {
	version := c.webVersion
	if version == "" {
		body, err := c.client.qb(mutationReadOnly(ctx), http.MethodGet, "app/webapiVersion", "", "")
		if err != nil {
			return false, pluginHostCookieTerminalError(err)
		}
		version = string(body)
	}
	current, err := pep440.Parse(strings.TrimSpace(version))
	if err != nil {
		return false, nil
	}
	minimum := pep440.MustParse("2.11.3")
	if !current.GreaterThanOrEqual(minimum) {
		return false, nil
	}
	hostname, validHost := pluginCookieHostname(address)
	if !validHost {
		return false, nil
	}
	pairs, err := pluginHostCookiePairs(header)
	if err != nil || len(pairs) == 0 {
		return false, nil
	}
	body, err := c.client.qb(mutationReadOnly(ctx), http.MethodGet, "app/cookies", "", "")
	if err != nil {
		return false, pluginHostCookieTerminalError(err)
	}
	d := json.NewDecoder(bytes.NewReader(body))
	d.UseNumber()
	var previous []any
	if d.Decode(&previous) != nil || d.Decode(new(any)) != io.EOF {
		return false, nil
	}
	order := []string{}
	merged := map[string]map[string]any{}
	put := func(row map[string]any) bool {
		key, valid, err := pluginCookieKey(row)
		if err != nil {
			return false
		}
		if !valid {
			return true
		}
		if _, exists := merged[key]; !exists {
			order = append(order, key)
		}
		merged[key] = row
		return true
	}
	for _, value := range previous {
		row, ok := value.(map[string]any)
		if !ok {
			continue
		}
		normalized := map[string]any{}
		for _, key := range []string{"domain", "path", "name", "value", "expirationDate"} {
			v := row[key]
			if v != nil && v != "" {
				normalized[key] = v
			}
		}
		if !put(normalized) {
			return false, nil
		}
	}
	for _, pair := range pairs {
		if !put(map[string]any{"domain": hostname, "path": "/", "name": pair[0], "value": pair[1]}) {
			return false, nil
		}
	}
	list := []map[string]any{}
	for _, key := range order {
		list = append(list, merged[key])
	}
	encoded, err := json.Marshal(list)
	if err != nil {
		return false, nil
	}
	_, err = c.client.qbSelected(ctx, http.MethodPost, "app/setCookies", url.Values{"cookies": {string(encoded)}}.Encode(), "application/x-www-form-urlencoded")
	return err == nil, pluginHostCookieTerminalError(err)
}

func (c *IYUUClient) PluginHostAddMaterial(ctx context.Context, spec IYUUAddSpec, paused bool, uploadBytes, downloadBytes *int64, address *string, cookie *string) (out IYUUAddResult, err error) {
	content := spec.Magnet
	if address != nil {
		content = *address
	}
	if len(spec.Torrent) == 0 && content == "" && !(c.client.config.Kind == "transmission" && address != nil) {
		return IYUUAddResult{AddedIDs: []string{}, Outcome: "rejected"}, nil
	}
	// The actual fixed dependency transmission-rpc 7.0.11 delegates even an
	// empty string as filename, while empty bytes and file: URIs fail locally.
	// Preserve the distinction instead of applying qB's `if not content` rule.
	if c.client.config.Kind == "transmission" && address != nil {
		if !pluginHostTransmissionStringValid(content) {
			return IYUUAddResult{AddedIDs: []string{}, Outcome: "rejected"}, nil
		}
	}
	if address == nil && cookie == nil {
		return c.PluginHostAdd(ctx, spec, paused, uploadBytes, downloadBytes)
	}
	ctx, phase := beginMutation(ctx)
	defer func() { err = phase.finish(err, &out.Outcome) }()
	out = IYUUAddResult{SourceTruthy: c.client.config.Kind == "qbittorrent", AddedIDs: []string{}, Outcome: "rejected"}
	if len(spec.Torrent) > maxResponse || len(content) > maxResponse || len(spec.Torrent) > 0 && content != "" {
		return out, failure("plugin_add", "invalid_material")
	}
	if err = c.client.nativeLock(ctx); err != nil {
		return out, err
	}
	defer c.client.mu.Unlock()
	if c.client.config.Kind == "qbittorrent" {
		cookieToUse := cookie
		if content != "" && cookie != nil && *cookie != "" && !strings.HasPrefix(content, "magnet:") {
			synced, syncErr := c.pluginHostSyncCookiesLocked(ctx, content, *cookie)
			if syncErr != nil {
				return out, syncErr
			}
			if synced {
				cookieToUse = nil
			}
		}
		var body bytes.Buffer
		writer := multipart.NewWriter(&body)
		category, auto := c.addCategoryMode(spec)
		fields := [][2]string{{"paused", iyuuBool(paused)}, {"stopped", iyuuBool(paused)}, {"sequentialDownload", iyuuBool(c.options.Sequential)}, {"firstLastPiecePrio", iyuuBool(c.options.FirstLast)}, {"autoTMM", iyuuBool(auto)}}
		if content != "" {
			fields = append(fields, [2]string{"urls", content})
		}
		if cookieToUse != nil {
			fields = append(fields, [2]string{"cookie", *cookieToUse})
		}
		if category != nil {
			fields = append(fields, [2]string{"category", *category})
		}
		if spec.SavePath != nil && *spec.SavePath != "" {
			fields = append(fields, [2]string{"savepath", *spec.SavePath})
		}
		if len(spec.Tags) > 0 {
			fields = append(fields, [2]string{"tags", strings.Join(spec.Tags, ",")})
		}
		if value, present := iyuuSkipCheckingField(spec); present {
			fields = append(fields, [2]string{"skip_checking", value})
		}
		if uploadBytes != nil {
			fields = append(fields, [2]string{"upLimit", strconv.FormatInt(*uploadBytes, 10)})
		}
		if downloadBytes != nil {
			fields = append(fields, [2]string{"dlLimit", strconv.FormatInt(*downloadBytes, 10)})
		}
		for _, label := range spec.QBLabels {
			fields = append(fields, [2]string{"labels", label})
		}
		for _, field := range fields {
			if err = writer.WriteField(field[0], field[1]); err != nil {
				return out, err
			}
		}
		if len(spec.Torrent) > 0 {
			part, e := writer.CreateFormFile("torrent__0", "torrent__0")
			if e != nil {
				return out, e
			}
			if _, err = part.Write(spec.Torrent); err != nil {
				return out, err
			}
		}
		if err = writer.Close(); err != nil {
			return out, err
		}
		if body.Len() > maxResponse {
			return out, failure("plugin_add", "payload_limit")
		}
		raw, err := c.client.qbSelected(ctx, http.MethodPost, "torrents/add", body.String(), writer.FormDataContentType())
		if err != nil {
			out.Outcome = "unknown"
			return out, err
		}
		accepted, ids, err := iyuuAddResponse(raw)
		if err != nil {
			out.Outcome = "unknown"
			return out, err
		}
		out.Accepted = accepted
		out.AddedIDs = ids
		if accepted {
			out.Outcome = "confirmed"
		}
		return out, nil
	}
	if err = c.client.trConnect(ctx); err != nil {
		return out, err
	}
	args := map[string]any{"paused": paused, "labels": append([]string{}, spec.Tags...)}
	if spec.SavePath != nil {
		args["download_dir"] = *spec.SavePath
	}
	if cookie != nil {
		args["cookies"] = *cookie
	}
	if address != nil || content != "" {
		args["filename"] = content
	} else {
		args["metainfo"] = base64.StdEncoding.EncodeToString(spec.Torrent)
	}
	values, err := c.client.trSelectedRPC(ctx, "torrent_add", args, c.client.trDialect)
	if err != nil {
		out.Outcome = "unknown"
		return out, err
	}
	raw, ok := values["torrent_added"]
	if !ok {
		raw, ok = values["torrent_duplicate"]
	}
	if !ok {
		out.Outcome = "unknown"
		return out, failure("plugin_add", "missing_added_object")
	}
	var row map[string]json.RawMessage
	if json.Unmarshal(raw, &row) != nil {
		return out, failure("plugin_add", "invalid_added_object")
	}
	var hash string
	_ = json.Unmarshal(row["hash_string"], &hash)
	if hash == "" {
		_ = json.Unmarshal(row["hashString"], &hash)
	}
	if _, err = c.client.validHash(hash); err != nil {
		out.Outcome = "unknown"
		return out, err
	}
	out.Hash = hash
	out.Accepted = true
	out.SourceTruthy = true
	out.Outcome = "confirmed"
	return out, nil
}

func pluginHostCookieTerminalError(err error) error {
	var denied *iyuuGuardError
	if errors.As(err, &denied) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	return nil
}
