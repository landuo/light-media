package apprise

import (
	"net/http"
	"strings"
)

const pushMeEndpoint = "https://push.i-i.me/"

type pushMeService struct {
	providerHTTPOptions
	token  string
	status bool
}

func parsePushMeService(raw string) (*pushMeService, bool) {
	u, valid := parseSourceURLWithHostVerification(raw, false)
	if !valid {
		return nil, false
	}
	token := queryToken(u)
	if u.query["token"] == "" && u.query["push_key"] != "" {
		token = sourceUnquote(u.query["push_key"])
	}
	if token == "" || strings.TrimLeftFunc(token, sourceSpace) != token {
		return nil, false
	}
	status, specified := u.query["status"]
	s := &pushMeService{token: strings.TrimRightFunc(token, sourceSpace), status: !specified || parseBool(status, false)}
	return s, s.configure(u)
}

func (s *pushMeService) requests(title, body string) ([]Request, error) {
	return notificationRequests(title, body, s.emojis, s.overflow, s.format, func(title, body string) (Request, error) {
		if s.status {
			title = "[i] " + title
		}
		kind := "text"
		if s.format == "markdown" {
			kind = "markdown"
		}
		return Request{
			Method: http.MethodPost, URL: pushMeEndpoint,
			Headers:     map[string]string{"User-Agent": "Apprise"},
			HeaderOrder: [][2]string{{"User-Agent", "Apprise"}},
			Params:      map[string]string{"push_key": s.token, "title": title, "content": body, "type": kind},
			ParamOrder:  [][2]string{{"push_key", s.token}, {"title", title}, {"content", body}, {"type", kind}},
			Verify:      s.verify, Timeout: s.timeout,
		}, nil
	})
}
