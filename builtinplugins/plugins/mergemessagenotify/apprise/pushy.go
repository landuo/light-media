package apprise

import (
	"bytes"
	"encoding/json"
	"net/url"
	"regexp"
	"slices"
	"strings"
)

const pushyEndpoint = "https://api.pushy.me/push?api_key="

// Python's Unicode case-insensitive ASCII range includes these four letters.
var (
	pushyTopic   = regexp.MustCompile(`^#?([a-zA-Z0-9İıſK]+)$`)
	pushyDevice  = regexp.MustCompile(`^@([a-zA-Z0-9İıſK]+)$`)
	pushyEscaped = regexp.MustCompile(`%[0-9a-fA-F]{2}`)
)

type pushyService struct {
	providerHTTPOptions
	endpoint, sound string
	badge           json.Number
	targets         []string
}

func parsePushyService(raw string) (*pushyService, bool) {
	u, valid := parseSourceURLWithHostVerification(raw, false)
	key := u.query["key"]
	if key == "" {
		key = u.host
	}
	if !valid || key == "" || strings.TrimLeftFunc(key, sourceSpace) != key {
		return nil, false
	}
	s := &pushyService{endpoint: pushyURL(strings.TrimRightFunc(key, sourceSpace)), sound: sourceUnquote(u.query["sound"])}
	if !s.configure(u) {
		return nil, false
	}
	if badge := pushoverInteger(strings.TrimFunc(sourceUnquote(u.query["badge"]), sourceSpace), -1); badge.Sign() >= 0 {
		s.badge = json.Number(badge.String())
	}

	var targets []string
	for _, part := range strings.FieldsFunc(u.path, func(r rune) bool {
		return strings.ContainsRune(" \t\r\n,\\/", r)
	}) {
		targets = append(targets, strings.FieldsFunc(sourceUnquote(part), sourceListDelimiter)...)
	}
	for _, part := range strings.FieldsFunc(u.query["to"], sourceListDelimiter) {
		targets = append(targets, strings.FieldsFunc(sourceUnquote(part), sourceListDelimiter)...)
	}
	slices.Sort(targets)
	var devices []string
	for _, target := range slices.Compact(targets) {
		if match := pushyTopic.FindStringSubmatch(target); match != nil {
			s.targets = append(s.targets, match[1])
		} else if match := pushyDevice.FindStringSubmatch(target); match != nil {
			devices = append(devices, match[1])
		}
	}
	s.targets = append(s.targets, devices...)
	return s, true
}

func pushyURL(key string) string {
	const safe = "!$&'()*+,;=:@/?"
	encode := func(value string) string {
		// urllib3 keeps existing escapes only when every percent is escaped.
		if _, err := url.PathUnescape(value); err != nil {
			return sourceQuote(pushyEscaped.ReplaceAllStringFunc(value, strings.ToUpper), safe)
		}
		// Requests decodes escapes for unreserved bytes after urllib3 quotes.
		value = pushyEscaped.ReplaceAllStringFunc(value, func(escape string) string {
			decoded, _ := url.PathUnescape(escape)
			if sourceQuote(decoded, "") == decoded {
				return decoded
			}
			return strings.ToUpper(escape)
		})
		return sourceQuote(value, safe+"%")
	}
	query, fragment, hasFragment := strings.Cut(key, "#")
	endpoint := pushyEndpoint + encode(query)
	if hasFragment {
		endpoint += "#" + encode(fragment)
	}
	return endpoint
}

func (s *pushyService) sendText(title, body string, deliver func(Request) (Response, bool)) (bool, error) {
	if len(s.targets) == 0 {
		return false, nil
	}
	chunks, err := notificationChunksWithBodyLimit(title, body, s.emojis, s.overflow, s.format, 4096)
	if err != nil {
		return false, err
	}
	success := len(chunks) != 0
	for _, chunk := range chunks {
		notification := object{{"body", chunk.body}}
		if chunk.title != "" {
			notification = append(notification, field{"title", chunk.title})
		}
		if s.sound != "" {
			notification = append(notification, field{"sound", s.sound})
		}
		if s.badge != "" {
			notification = append(notification, field{"badge", s.badge})
		}
		for _, target := range s.targets {
			data, err := pythonJSON(object{{"to", target}, {"data", object{{"message", chunk.body}}}, {"notification", notification}})
			if err != nil {
				return false, err
			}
			call := s.post(s.endpoint, "application/json", data)
			call.Headers["Accepts"] = "application/json"
			call.HeaderOrder = append(call.HeaderOrder, [2]string{"Accepts", "application/json"})
			reply, delivered := deliver(call)
			if !delivered {
				success = false
				continue
			}
			decoded, err := pythonJSONEncoding(reply.Content)
			if err != nil || !json.Valid(decoded) {
				success = false
				continue
			}
			decoder := json.NewDecoder(bytes.NewReader(decoded))
			decoder.UseNumber()
			var content any
			if decoder.Decode(&content) != nil {
				success = false
				continue
			}
			payload, ok := content.(map[string]any)
			if !ok {
				// Python's .get() raises here, ending this notification chunk.
				success = false
				break
			}
			value := payload["success"]
			if number, ok := value.(json.Number); ok {
				// A Python overflow exponent becomes infinity, which is truthy.
				value, _ = number.Float64()
			}
			success = slackTruthy(value) && success
		}
	}
	return success, nil
}
