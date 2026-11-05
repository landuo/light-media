package app

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/netip"
	"net/url"
	"regexp"
	"sort"
	"strings"

	"golang.org/x/text/unicode/norm"
	"light-media/internal/plugins"
	"light-media/internal/releasemeta/legacyanime"
)

var originalTrackerAddress = regexp.MustCompile(`(?i)\b[a-z][a-z0-9+.-]*://[^\s<>"']+`)
var originalTrackerCredential = regexp.MustCompile(`(?i)(?:password|passwd|pwd|cookie|authorization|api[_-]?key|access[_-]?token|refresh[_-]?token|token|passkey|secret|session[_-]?id)\s*["']?\s*[:=]\s*(?:"[^"\r\n]*"|'[^'\r\n]*'|[^\s,;}&]+)`)
var originalTrackerIPvFuture = regexp.MustCompile(`^v[0-9A-Fa-f]+\..+$`)

// Fixed StringUtils.get_url_netloc: its HTTP prefix test is case-sensitive;
// non-HTTP values (including UDP URIs) use the entire original string.
func originalTrackerSourceGroup(raw string) (string, bool) {
	if raw == "" || !strings.HasPrefix(raw, "http") {
		return raw, true
	}
	value := strings.NewReplacer("\t", "", "\r", "", "\n", "").Replace(raw)
	colon := strings.IndexByte(value, ':')
	if colon < 0 {
		return "", true
	}
	for i, c := range value[:colon] {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || i > 0 && (c >= '0' && c <= '9' || c == '+' || c == '-' || c == '.')) {
			return "", true
		}
	}
	rest := value[colon+1:]
	if !strings.HasPrefix(rest, "//") {
		return "", true
	}
	rest = rest[2:]
	if end := strings.IndexAny(rest, "/?#"); end >= 0 {
		rest = rest[:end]
	}
	if strings.Contains(rest, "[") != strings.Contains(rest, "]") {
		return "", false
	}
	if open := strings.IndexByte(rest, '['); open >= 0 {
		end := strings.IndexByte(rest[open+1:], ']')
		if end < 0 {
			return "", false
		}
		host := rest[open+1 : open+1+end]
		if strings.HasPrefix(host, "v") {
			if !originalTrackerIPvFuture.MatchString(host) {
				return "", false
			}
		} else {
			ip, err := netip.ParseAddr(host)
			if err != nil || !ip.Is6() {
				return "", false
			}
		}
	}
	without := strings.NewReplacer("@", "", ":", "", "#", "", "?", "").Replace(rest)
	if normalized := norm.NFKC.String(without); normalized != without && strings.ContainsAny(normalized, "/?#@:") {
		return "", false
	}
	return rest, true
}

func (a *App) originalTrackerEndpoint(raw string) plugins.DownloaderTrackerEndpointV1 {
	group, valid := originalTrackerSourceGroup(raw)
	out := plugins.DownloaderTrackerEndpointV1{SourceValid: valid, Display: "[TRACKER]"}
	if valid {
		mac := hmac.New(sha256.New, a.Store.key)
		_, _ = mac.Write([]byte("plugin-downloader-tracker-comparison/v1\x00"))
		_, _ = mac.Write([]byte(group))
		out.ComparisonKey = hex.EncodeToString(mac.Sum(nil))
	}
	if raw == "" || raw == "unknown" || raw == "** [DHT] **" || raw == "** [PeX] **" || raw == "** [LSD] **" {
		out.Display = raw
		return out
	}
	if parsed, err := url.Parse(raw); err == nil && parsed.Scheme != "" && parsed.Hostname() != "" {
		out.Display = parsed.Scheme + "://" + parsed.Host
	}
	return out
}

func originalTrackerSecrets(value any) []string {
	secrets := map[string]bool{}
	var visit func(any)
	visit = func(value any) {
		switch item := value.(type) {
		case []map[string]any:
			for _, v := range item {
				visit(v)
			}
		case map[string]any:
			for _, v := range item {
				visit(v)
			}
		case []any:
			for _, v := range item {
				visit(v)
			}
		case string:
			for _, address := range originalTrackerAddress.FindAllString(item, -1) {
				parsed, err := url.Parse(address)
				if err != nil {
					continue
				}
				if parsed.User != nil {
					secrets[parsed.User.Username()] = true
					if password, ok := parsed.User.Password(); ok {
						secrets[password] = true
					}
				}
				for _, values := range parsed.Query() {
					for _, v := range values {
						secrets[v] = true
					}
				}
				for _, part := range strings.Split(parsed.Path, "/") {
					if len(part) >= 16 {
						secrets[part] = true
					}
				}
			}
		}
	}
	visit(value)
	delete(secrets, "")
	out := []string{}
	for value := range secrets {
		out = append(out, value, url.QueryEscape(value), url.PathEscape(value))
	}
	sort.Slice(out, func(i, j int) bool { return len(out[i]) > len(out[j]) })
	return out
}

func (a *App) originalTrackerText(value string, messages, secrets []string) *plugins.DownloaderTrackerTextV1 {
	stripped := legacyanime.Strip(value)
	out := &plugins.DownloaderTrackerTextV1{NonEmpty: value != "", NonBlank: stripped != "", Matches: []bool{}, StrippedMatches: []bool{}}
	for _, message := range messages {
		out.Matches = append(out.Matches, value == message)
		out.StrippedMatches = append(out.StrippedMatches, stripped == message)
	}
	display := cleanWithSettings(value, a.Settings())
	for _, secret := range secrets {
		display = strings.ReplaceAll(display, secret, "[REDACTED]")
	}
	display = originalTrackerAddress.ReplaceAllString(display, "[URL]")
	display = originalTrackerCredential.ReplaceAllString(display, "[CREDENTIAL]")
	display = logSecretPattern.ReplaceAllString(display, "[CREDENTIAL]")
	out.Display = boundedLogString(display, 2048)
	return out
}

func originalTrackerNumber(value any) (*json.Number, error) {
	if value == nil {
		return nil, nil
	}
	number, ok := value.(json.Number)
	if !ok {
		return nil, bad("DOWNLOADER_INVALID_RESPONSE", "Tracker数字字段无效")
	}
	return &number, nil
}
func originalTrackerBoolean(value any) (*bool, error) {
	if value == nil {
		return nil, nil
	}
	flag, ok := value.(bool)
	if !ok {
		return nil, bad("DOWNLOADER_INVALID_RESPONSE", "Tracker布尔字段无效")
	}
	return &flag, nil
}

func (a *App) originalTrackerRow(row map[string]any, qb bool, messages, secrets []string) (out plugins.DownloaderTrackerV1, err error) {
	key := "announce"
	if qb {
		key = "url"
	}
	address := ""
	if row[key] != nil {
		var ok bool
		address, ok = row[key].(string)
		if !ok {
			return out, bad("DOWNLOADER_INVALID_RESPONSE", "Tracker地址字段无效")
		}
	}
	out.Endpoint = a.originalTrackerEndpoint(address)
	out.Status, err = originalTrackerNumber(row["status"])
	if err != nil {
		return out, err
	}
	out.Tier, err = originalTrackerNumber(row["tier"])
	if err != nil {
		return out, err
	}
	textKey := "lastAnnounceResult"
	if qb {
		textKey = "msg"
	}
	if row[textKey] != nil {
		text, ok := row[textKey].(string)
		if !ok {
			return out, bad("DOWNLOADER_INVALID_RESPONSE", "Tracker消息字段无效")
		}
		out.Message = a.originalTrackerText(text, messages, secrets)
	}
	out.LastAnnounceSucceeded, err = originalTrackerBoolean(row["lastAnnounceSucceeded"])
	if err != nil {
		return out, err
	}
	out.HasAnnounced, err = originalTrackerBoolean(row["hasAnnounced"])
	return out, err
}

func (a *App) originalHostDownloaderTrackers(ctx context.Context, scope originalBaseCallScope, raw json.RawMessage) (json.RawMessage, error) {
	var request plugins.DownloaderTrackersRequestV1
	if err := originalResourceDecode(raw, &request); err != nil {
		return nil, err
	}
	if !plugins.DownloaderTaskHashValid(request.Hash) || request.ErrorMessages == nil {
		return nil, bad("INVALID_PLUGIN_VALUE", "Tracker读取必须指定准确Hash及错误消息数组")
	}
	request.Hash = strings.ToLower(request.Hash)
	client, kind, guard, err := a.originalHostDownloaderClient(ctx, scope, request.DownloaderID)
	if err != nil {
		return nil, err
	}
	defer client.Close()
	body, err := client.PluginHostTrackerRows(ctx, request.Hash)
	if current := guard(ctx); current != nil {
		return nil, current
	}
	if err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	var rows []map[string]any
	if decoder.Decode(&rows) != nil || rows == nil {
		return nil, bad("DOWNLOADER_INVALID_RESPONSE", "Tracker响应无效")
	}
	secrets := originalTrackerSecrets(rows)
	out := plugins.DownloaderTrackersV1{SchemaVersion: 1, DownloaderID: request.DownloaderID, Hash: request.Hash, Kind: kind, TrackerStats: []plugins.DownloaderTrackerV1{}, Trackers: []plugins.DownloaderTrackerV1{}, TrackerList: []plugins.DownloaderTrackerEndpointV1{}}
	if kind == "qbittorrent" {
		out.TaskFound = true
		out.TrackersPresent = true
		for _, row := range rows {
			item, err := a.originalTrackerRow(row, true, request.ErrorMessages, secrets)
			if err != nil {
				return nil, err
			}
			out.Trackers = append(out.Trackers, item)
		}
		return originalHostDownloaderMarshal(out)
	}
	var task map[string]any
	for _, row := range rows {
		hash, _ := row["hashString"].(string)
		if strings.EqualFold(hash, request.Hash) {
			if task != nil {
				return nil, bad("DOWNLOADER_INVALID_RESPONSE", "Tracker任务重复")
			}
			task = row
		}
	}
	if task == nil {
		return originalHostDownloaderMarshal(out)
	}
	out.TaskFound = true
	out.TaskErrorCode, err = originalTrackerNumber(task["error"])
	if err != nil {
		return nil, err
	}
	if task["errorString"] != nil {
		text, ok := task["errorString"].(string)
		if !ok {
			return nil, bad("DOWNLOADER_INVALID_RESPONSE", "任务错误消息无效")
		}
		out.TaskErrorText = a.originalTrackerText(text, request.ErrorMessages, secrets)
	}
	for field, destination := range map[string]*[]plugins.DownloaderTrackerV1{"trackerStats": &out.TrackerStats, "trackers": &out.Trackers} {
		value, present := task[field]
		if field == "trackerStats" {
			out.TrackerStatsPresent = present
		} else {
			out.TrackersPresent = present
		}
		if value == nil {
			continue
		}
		items, ok := value.([]any)
		if !ok {
			return nil, bad("DOWNLOADER_INVALID_RESPONSE", "Tracker集合字段无效")
		}
		for _, value := range items {
			row, ok := value.(map[string]any)
			if !ok {
				if text, ok := value.(string); ok {
					row = map[string]any{"announce": text}
				} else {
					return nil, bad("DOWNLOADER_INVALID_RESPONSE", "Tracker条目无效")
				}
			}
			item, err := a.originalTrackerRow(row, false, request.ErrorMessages, secrets)
			if err != nil {
				return nil, err
			}
			*destination = append(*destination, item)
		}
	}
	value, present := task["trackerList"]
	out.TrackerListPresent = present
	if value != nil {
		switch value := value.(type) {
		case string:
			out.TrackerListWasString = true
			for _, char := range value {
				out.TrackerList = append(out.TrackerList, a.originalTrackerEndpoint(string(char)))
			}
		case []any:
			for _, item := range value {
				text, ok := item.(string)
				if !ok {
					if row, ok := item.(map[string]any); ok {
						text, _ = row["announce"].(string)
					} else {
						return nil, bad("DOWNLOADER_INVALID_RESPONSE", "Tracker列表条目无效")
					}
				}
				out.TrackerList = append(out.TrackerList, a.originalTrackerEndpoint(text))
			}
		default:
			return nil, bad("DOWNLOADER_INVALID_RESPONSE", "Tracker列表字段无效")
		}
	}
	return originalHostDownloaderMarshal(out)
}
