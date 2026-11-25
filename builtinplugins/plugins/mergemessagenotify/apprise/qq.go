package apprise

import (
	"net/url"
	"regexp"
	"strings"
)

const qqEndpoint = "https://qmsg.zendee.cn/send/"

// Python's case-insensitive ASCII ranges include these additional letters.
var qqToken = regexp.MustCompile(`^[A-Za-z0-9İıſK]{24,64}$`)
var qqNative = regexp.MustCompile(`(?i)^https://qmsg\.zendee\.cn/send/([a-z0-9İı]+)$`)

type qqService struct {
	providerHTTPOptions
	token string
}

func qqNativeURL(raw string) (string, bool) {
	match := qqNative.FindStringSubmatch(strings.TrimSuffix(raw, "\n"))
	if match == nil {
		return "", false
	}
	return "qq://" + match[1], true
}

func parseQQService(raw string) (*qqService, bool) {
	u, valid := parseSourceURLWithHostVerification(raw, false)
	token := strings.TrimSuffix(queryToken(u), "\n")
	if !valid || !qqToken.MatchString(token) {
		return nil, false
	}
	s := &qqService{token: token}
	return s, s.configure(u)
}

func (s *qqService) requests(title, body string) ([]Request, error) {
	endpoint := qqEndpoint + sourceQuote(s.token, "")
	return notificationRequests(title, body, s.emojis, s.overflow, s.format, func(title, body string) (Request, error) {
		if title != "" {
			body = title + "\n" + body
		}
		return s.post(endpoint, "application/x-www-form-urlencoded", []byte("msg="+url.QueryEscape(body))), nil
	})
}
