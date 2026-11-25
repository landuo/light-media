package apprise

import (
	"context"
	"net/url"
	"regexp"
	"strings"
)

const prowlEndpoint = "https://api.prowlapp.com/publicapi/add"

var prowlKey = regexp.MustCompile(`(?i)^[a-z0-9]{40}$`)

type prowlService struct {
	providerHTTPOptions
	apiKey, providerKey, priority string
}

func parseProwlService(raw string) (*prowlService, bool) {
	u, valid := parseSourceURLWithHostVerification(raw, false)
	if !valid || !prowlKey.MatchString(strings.TrimSuffix(u.host, "\n")) {
		return nil, false
	}
	s := &prowlService{apiKey: strings.TrimFunc(u.host, sourceSpace), priority: providerPriority(sourceUnquote(u.query["priority"]))}
	path := strings.FieldsFunc(u.path, func(r rune) bool {
		return strings.ContainsRune(" \t\r\n,\\/", r)
	})
	if len(path) != 0 {
		key := sourceUnquote(path[0])
		if !prowlKey.MatchString(strings.TrimSuffix(key, "\n")) {
			return nil, false
		}
		s.providerKey = strings.TrimFunc(key, sourceSpace)
	}
	return s, s.configure(u)
}

func (s *prowlService) waitToSend(ctx context.Context) error { return ctx.Err() }

func (s *prowlService) requests(title, body string) ([]Request, error) {
	chunks, err := notificationChunksWithLimits(title, body, s.emojis, s.overflow, s.format, 1024, 10000)
	if err != nil {
		return nil, err
	}
	calls := make([]Request, 0, len(chunks))
	for _, chunk := range chunks {
		data := "apikey=" + url.QueryEscape(s.apiKey) + "&application=Apprise&event=" + url.QueryEscape(chunk.title) +
			"&description=" + url.QueryEscape(chunk.body) + "&priority=" + s.priority
		if s.providerKey != "" {
			data += "&providerkey=" + url.QueryEscape(s.providerKey)
		}
		calls = append(calls, s.postForm(prowlEndpoint, data))
	}
	return calls, nil
}
