package apprise

import (
	"net/url"
	"regexp"
	"slices"
	"strings"
)

var zulipBot = regexp.MustCompile(`(?i)^[a-z0-9_-]{1,32}`)
var zulipOrg = regexp.MustCompile(`(?i)^([a-z0-9_-]{1,32})(?:\.([^\s]+))?`)

type zulipService struct {
	providerHTTPOptions
	endpoint string
	auth     []any
	targets  []string
}

func parseZulipService(raw string) (*zulipService, bool) {
	u, valid := parseSourceURLWithHostVerification(raw, false)
	if !valid || u.user == nil {
		return nil, false
	}
	bot := zulipBot.FindString(strings.TrimFunc(*u.user, sourceSpace))
	org := zulipOrg.FindStringSubmatch(strings.TrimFunc(u.host, sourceSpace))
	if bot == "" || org == nil {
		return nil, false
	}
	host := org[2]
	if host == "" {
		host = "zulipchat.com"
	}
	host = org[1] + "." + host

	targets := strings.FieldsFunc(u.path, func(r rune) bool {
		return strings.ContainsRune(" \t\r\n,\\/", r)
	})
	for i, target := range targets {
		targets[i] = sourceUnquote(target)
	}
	token := sourceUnquote(u.query["token"])
	if token == "" && len(targets) != 0 {
		token, targets = targets[0], targets[1:]
	}
	if !alphanumericToken32.MatchString(strings.TrimSuffix(token, "\n")) {
		return nil, false
	}
	targets = append(targets, strings.FieldsFunc(sourceUnquote(u.query["to"]), func(r rune) bool {
		return strings.ContainsRune(" \t\r\n,#\\/", r)
	})...)
	targets = strings.FieldsFunc(strings.Join(targets, ","), sourceListDelimiter)
	slices.Sort(targets)
	targets = slices.Compact(targets)
	if len(targets) == 0 {
		targets = []string{"general"}
	}
	s := &zulipService{
		endpoint: "https://" + host + "/api/v1/messages",
		auth:     []any{strings.TrimSuffix(bot, "-bot") + "-bot@" + host, strings.TrimFunc(token, sourceSpace)},
		targets:  targets,
	}
	return s, s.configure(u)
}

func (s *zulipService) requests(title, body string) ([]Request, error) {
	chunks, err := notificationChunksWithLimits(title, body, s.emojis, s.overflow, s.format, 60, 10000)
	if err != nil {
		return nil, err
	}
	calls := make([]Request, 0, len(chunks)*len(s.targets))
	for _, chunk := range chunks {
		for _, target := range s.targets {
			kind := "stream"
			if match := pushBulletEmail.FindStringSubmatch(target); match != nil {
				kind, target = "private", match[1]
			}
			data := "subject=" + url.QueryEscape(chunk.title) + "&content=" + url.QueryEscape(chunk.body) +
				"&type=" + kind + "&to=" + url.QueryEscape(target)
			call := s.post(s.endpoint, "application/x-www-form-urlencoded; charset=utf-8", []byte(data))
			call.Auth = s.auth
			calls = append(calls, call)
		}
	}
	return calls, nil
}
