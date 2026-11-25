package apprise

import (
	"net/url"
	"regexp"
	"strings"
)

const joinEndpoint = "https://joinjoaomgcd.appspot.com/_ah/api/messaging/v1/sendPush"

var joinGroup = regexp.MustCompile(`(?i)^(?:group\.)?(all|android|chrome|windows10|phone|tablet|pc)`)

type joinService struct {
	providerHTTPOptions
	key, priority string
	targets       []string
	includeImage  bool
}

func parseJoinService(raw string) (*joinService, bool) {
	u, valid := parseSourceURLWithHostVerification(raw, false)
	if !valid {
		return nil, false
	}
	key, targets := recipientCredentialAndTargets(u)
	if !alphanumericToken32.MatchString(strings.TrimSuffix(key, "\n")) {
		return nil, false
	}
	s := &joinService{key: strings.TrimFunc(key, sourceSpace), priority: providerPriority(sourceUnquote(u.query["priority"])), targets: targets, includeImage: true}
	if !s.configure(u) {
		return nil, false
	}
	if value, specified := u.query["image"]; specified {
		s.includeImage = parseBool(value, false)
	}
	if len(s.targets) == 0 {
		s.targets = []string{"group.all"}
	}
	for i, target := range s.targets {
		if group := joinGroup.FindStringSubmatch(target); group != nil {
			s.targets[i] = "group." + strings.ToLower(group[1])
		}
	}
	return s, true
}

func (s *joinService) requests(title, body string) ([]Request, error) {
	chunks, err := notificationChunksWithBodyLimit(title, body, s.emojis, s.overflow, s.format, 1000)
	if err != nil {
		return nil, err
	}
	calls := make([]Request, 0, len(chunks)*len(s.targets))
	for _, chunk := range chunks {
		for _, target := range s.targets {
			key := "deviceNames"
			if joinGroup.MatchString(target) || alphanumericToken32.MatchString(strings.TrimSuffix(target, "\n")) {
				key = "deviceId"
			}
			query := "apikey=" + url.QueryEscape(s.key) + "&priority=" + s.priority +
				"&title=" + url.QueryEscape(chunk.title) + "&text=" + url.QueryEscape(chunk.body) + "&" + key + "=" + url.QueryEscape(target)
			if s.includeImage {
				query += "&icon=" + url.QueryEscape(appriseInfoIcon72)
			}
			calls = append(calls, s.post(joinEndpoint+"?"+query, "application/x-www-form-urlencoded", nil))
		}
	}
	return calls, nil
}
