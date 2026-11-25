package apprise

import (
	"regexp"
	"strconv"
	"strings"
)

var iftttNative = regexp.MustCompile(`(?i)^https?://maker\.ifttt\.com/use/([a-z0-9_-]+)((?:/[a-z0-9_-]+)+)?/?(\?.+)?$`)

type iftttService struct {
	providerHTTPOptions
	key    string
	events []string
}

func iftttNativeURL(raw string) (string, bool) {
	match := iftttNative.FindStringSubmatch(raw)
	if match == nil {
		return "", false
	}
	eventPath := match[2]
	if eventPath != "" {
		eventPath = "@" + eventPath
	}
	return "ifttt://" + match[1] + eventPath + match[3], true
}

func parseIFTTTService(raw string) (*iftttService, bool) {
	u, valid := parseSourceURLWithHostVerification(raw, false)
	if !valid {
		return nil, false
	}
	key, events := recipientCredentialAndTargets(u)
	if key == "" || strings.TrimLeftFunc(key, sourceSpace) != key || len(events) == 0 {
		return nil, false
	}
	s := &iftttService{key: strings.TrimRightFunc(key, sourceSpace), events: events}
	if !s.configure(u) {
		return nil, false
	}
	// URL parsing supplies add_token/del_token, but the constructor accepts
	// add_tokens/del_tokens. URL options do not override the three ingredients.
	return s, true
}

func (s *iftttService) requests(title, body string) ([]Request, error) {
	chunks, err := notificationChunks(title, body, s.emojis, s.overflow, s.format)
	if err != nil {
		return nil, err
	}
	calls := make([]Request, 0, len(chunks)*len(s.events))
	for _, chunk := range chunks {
		data, err := pythonJSON(object{{"value1", chunk.title}, {"value2", chunk.body}, {"value3", "info"}})
		if err != nil {
			return nil, err
		}
		for _, event := range s.events {
			path := "/trigger/" + event + "/with/key/" + s.key
			calls = append(calls, s.post(iftttEndpoint(path), "application/json", data))
		}
	}
	return calls, nil
}

// Requests requotes the interpolated URL, keeping delimiters and reserved
// percent escapes while decoding escapes for unreserved ASCII characters.
func iftttEndpoint(path string) string {
	const reserved = "!#$&'()*+,/:;=?@[]"
	suffix := ""
	if i := strings.IndexAny(path, "?#"); i >= 0 {
		path, suffix = path[:i], path[i:]
	}
	endpoint := "https://maker.ifttt.com" + iftttRemoveDotSegments(path) + suffix
	var normalized strings.Builder
	for i := 0; i < len(endpoint); i++ {
		if endpoint[i] == '%' && i+2 < len(endpoint) {
			value, err := strconv.ParseUint(endpoint[i+1:i+3], 16, 8)
			if err != nil {
				return sourceQuote(endpoint, reserved)
			}
			r := rune(value)
			if r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || strings.ContainsRune("_.-~", r) {
				normalized.WriteByte(byte(value))
				i += 2
				continue
			}
		}
		normalized.WriteByte(endpoint[i])
	}
	return sourceQuote(normalized.String(), reserved+"%")
}

// urllib3 removes literal dot segments before Requests decodes unreserved
// escapes. Empty path elements are significant to the URL preparer.
func iftttRemoveDotSegments(path string) string {
	segments := strings.Split(path, "/")
	var parts []string
	for _, segment := range segments {
		switch segment {
		case ".":
		case "..":
			if len(parts) != 0 {
				parts = parts[:len(parts)-1]
			}
		default:
			parts = append(parts, segment)
		}
	}
	if len(parts) == 0 || parts[0] != "" {
		parts = append([]string{""}, parts...)
	}
	if strings.HasSuffix(path, "/.") || strings.HasSuffix(path, "/..") {
		parts = append(parts, "")
	}
	return strings.Join(parts, "/")
}
