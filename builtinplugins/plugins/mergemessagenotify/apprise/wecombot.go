package apprise

import (
	"regexp"
	"strings"
)

const weComBotEndpoint = "https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key="

// Python's case-insensitive ASCII ranges include these additional letters.
var weComBotKey = regexp.MustCompile(`^[A-Za-z0-9_İıſK-]+$`)
var weComBotNative = regexp.MustCompile(`(?i)^https?://qyap[iİı]\.we[iİı]x[iİı]n\.qq\.com/cg[iİı]-b[iİı]n/webhook/send/?\?key=([a-z0-9_İı-]+)/?&?(.+)?$`)

type weComBotService struct {
	providerHTTPOptions
	key string
}

func weComBotNativeURL(raw string) (string, bool) {
	match := weComBotNative.FindStringSubmatch(strings.TrimSuffix(raw, "\n"))
	if match == nil {
		return "", false
	}
	native := "wecombot://" + match[1]
	if match[2] != "" {
		native += "?" + match[2]
	}
	return native, true
}

func parseWeComBotService(raw string) (*weComBotService, bool) {
	u, valid := parseSourceURLWithHostVerification(raw, false)
	if !valid {
		return nil, false
	}
	key := u.host
	if value := u.query["key"]; value != "" {
		key = sourceUnquote(value)
	}
	if !weComBotKey.MatchString(strings.TrimSuffix(key, "\n")) {
		return nil, false
	}
	s := &weComBotService{key: strings.TrimFunc(key, sourceSpace)}
	return s, s.configure(u)
}

func (s *weComBotService) requests(title, body string) ([]Request, error) {
	chunks, err := titlelessNotificationChunks(title, body, &s.providerHTTPOptions, 32768)
	if err != nil {
		return nil, err
	}
	endpoint := weComBotEndpoint + sourceQuote(s.key, "")
	calls := make([]Request, 0, len(chunks))
	for _, chunk := range chunks {
		data, err := pythonJSON(object{{"msgtype", "text"}, {"text", object{{"content", chunk.body}}}})
		if err != nil {
			return nil, err
		}
		calls = append(calls, s.post(endpoint, "application/json; charset=utf-8", data))
	}
	return calls, nil
}
