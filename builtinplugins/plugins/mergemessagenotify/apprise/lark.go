package apprise

import (
	"context"
	"regexp"
	"strings"
)

const larkEndpoint = "https://open.larksuite.com/open-apis/bot/v2/hook/"

var (
	larkToken  = regexp.MustCompile(`^[A-Za-z0-9İıſK-]+$`)
	larkNative = regexp.MustCompile(`(?i)^https://open\.larksu[iİı]te\.com/open-ap[iİı]s/bot/v2/hook/([\pL\pN_-]+)$`)
)

type larkService struct {
	providerHTTPOptions
	token string
}

func larkNativeURL(raw string) (string, bool) {
	match := larkNative.FindStringSubmatch(strings.TrimSuffix(raw, "\n"))
	if match == nil {
		return "", false
	}
	return "lark://" + match[1], true
}

func parseLarkService(raw string) (*larkService, bool) {
	u, valid := parseSourceURLWithHostVerification(raw, false)
	token := strings.TrimSuffix(queryToken(u), "\n")
	if !valid || !larkToken.MatchString(token) {
		return nil, false
	}
	s := &larkService{token: token}
	return s, s.configure(u)
}

func (s *larkService) waitToSend(ctx context.Context) error {
	// Lark throttles on entry and again immediately before the POST.
	if err := s.providerHTTPOptions.waitToSend(ctx); err != nil {
		return err
	}
	return s.providerHTTPOptions.waitToSend(ctx)
}

func (s *larkService) requests(title, body string) ([]Request, error) {
	return notificationRequests(title, body, s.emojis, s.overflow, s.format, func(title, body string) (Request, error) {
		if title != "" {
			body = title + "\n" + body
		}
		data, err := pythonJSON(object{{"msg_type", "text"}, {"content", object{{"text", body}}}})
		return s.post(larkEndpoint+s.token, "application/json", data), err
	})
}
