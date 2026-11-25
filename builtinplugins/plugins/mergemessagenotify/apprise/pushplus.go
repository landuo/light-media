package apprise

import (
	"regexp"
	"strings"
)

const pushPlusEndpoint = "https://www.pushplus.plus/send"

var pushPlusToken = regexp.MustCompile(`(?i)^[a-z0-9_-]{32,64}$`)
var pushPlusNative = regexp.MustCompile(`(?i)^https://www\.pushplus\.plus/send\?token=([a-z0-9_-]+)$`)

type pushPlusService struct {
	providerHTTPOptions
	token string
}

func pushPlusNativeURL(raw string) (string, bool) {
	match := pushPlusNative.FindStringSubmatch(raw)
	if match == nil {
		return "", false
	}
	return "pushplus://" + match[1], true
}

func parsePushPlusService(raw string) (*pushPlusService, bool) {
	u, valid := parseSourceURLWithHostVerification(raw, false)
	token := queryToken(u)
	if !valid || !pushPlusToken.MatchString(strings.TrimSuffix(token, "\n")) {
		return nil, false
	}
	s := &pushPlusService{token: strings.TrimFunc(token, sourceSpace)}
	return s, s.configure(u)
}

func (s *pushPlusService) requests(title, body string) ([]Request, error) {
	return notificationRequests(title, body, s.emojis, s.overflow, s.format, func(title, body string) (Request, error) {
		if title == "" {
			title = body
		}
		data, err := pythonJSON(object{{"token", s.token}, {"title", title}, {"content", body}})
		return s.post(pushPlusEndpoint, "application/json", data), err
	})
}
