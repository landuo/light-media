package apprise

import (
	"context"
	"strings"
)

type pushjetService struct {
	providerHTTPOptions
	endpoint, secret string
	auth             []any
}

func parsePushjetService(raw string) (*pushjetService, bool) {
	u, valid := parseSourceURL(raw)
	if !valid {
		return nil, false
	}
	secret := sourceUnquote(u.query["secret"])
	if secret == "" {
		path := strings.FieldsFunc(u.path, func(r rune) bool {
			return strings.ContainsRune(" \t\r\n,\\/", r)
		})
		if len(path) != 0 {
			secret = sourceUnquote(path[0])
		}
	}
	if secret == "" || strings.TrimLeftFunc(secret, sourceSpace) != secret {
		return nil, false
	}
	scheme := "http"
	if u.scheme == "pjets" {
		scheme = "https"
	}
	port := u.port
	if port == ":0" {
		port = ""
	}
	s := &pushjetService{endpoint: scheme + "://" + u.host + port + "/message/", secret: strings.TrimRightFunc(secret, sourceSpace)}
	if !s.configure(u) {
		return nil, false
	}
	if u.user != nil && *u.user != "" {
		var password any
		if u.password != nil {
			password = *u.password
		}
		s.auth = []any{*u.user, password}
	}
	return s, true
}

// NotifyPushjet disables NotifyBase's interval for local servers.
func (s *pushjetService) waitToSend(ctx context.Context) error { return ctx.Err() }

func (s *pushjetService) requests(title, body string) ([]Request, error) {
	return notificationRequests(title, body, s.emojis, s.overflow, s.format, func(title, body string) (Request, error) {
		data, err := pythonJSON(object{{"message", body}, {"title", title}, {"link", nil}, {"level", nil}})
		if err != nil {
			return Request{}, err
		}
		call := s.post(s.endpoint, "application/x-www-form-urlencoded; charset=utf-8", data)
		call.Auth = s.auth
		call.Params = map[string]string{"secret": s.secret}
		call.ParamOrder = [][2]string{{"secret", s.secret}}
		return call, nil
	})
}
