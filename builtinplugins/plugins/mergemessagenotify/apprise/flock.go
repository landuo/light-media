package apprise

import (
	"regexp"
	"slices"
	"strings"
)

const flockWebhookEndpoint = "https://api.flock.com/hooks/sendMessage/"
const flockAPIEndpoint = "https://api.flock.co/v1/chat.sendMessage"

var flockToken = regexp.MustCompile(`(?i)^[a-z0-9-]+$`)
var flockUser = regexp.MustCompile(`(?i)^(?:@|u:)?([a-z0-9_]+)$`)
var flockGroup = regexp.MustCompile(`(?i)^(?:#|g:)([a-z0-9_]+)$`)
var flockNative = regexp.MustCompile(`(?i)^https?://api\.flock\.com/hooks/sendMessage/([a-z0-9-]{24})/?(\?.+)?$`)
var flockEscape = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", "'", "&apos;", "\"", "&quot;")

type flockService struct {
	providerHTTPOptions
	token, user string
	image       bool
	targets     []string
}

func flockNativeURL(raw string) (string, bool) {
	match := flockNative.FindStringSubmatch(raw)
	if match == nil {
		return "", false
	}
	return "flock://" + match[1] + "/" + match[2], true
}

func parseFlockService(raw string) (*flockService, bool) {
	u, valid := parseSourceURLWithHostVerification(raw, false)
	if !valid || !flockToken.MatchString(strings.TrimSuffix(u.host, "\n")) {
		return nil, false
	}
	s := &flockService{token: strings.TrimFunc(u.host, sourceSpace), user: "Apprise", image: true}
	if image, ok := u.query["image"]; ok {
		s.image = parseBool(image, false)
	}
	if !s.configure(u) {
		return nil, false
	}
	if u.user != nil && *u.user != "" {
		s.user = *u.user
	}
	var targets []string
	for _, segment := range strings.FieldsFunc(u.path, func(r rune) bool {
		return strings.ContainsRune(" \t\r\n,\\/", r)
	}) {
		targets = append(targets, strings.FieldsFunc(sourceUnquote(segment), sourceListDelimiter)...)
	}
	for _, segment := range strings.FieldsFunc(u.query["to"], sourceListDelimiter) {
		targets = append(targets, strings.FieldsFunc(sourceUnquote(segment), sourceListDelimiter)...)
	}
	slices.Sort(targets)
	for _, target := range slices.Compact(targets) {
		if user := flockUser.FindStringSubmatch(target); user != nil {
			s.targets = append(s.targets, "u:"+user[1])
		} else if group := flockGroup.FindStringSubmatch(target); group != nil {
			s.targets = append(s.targets, "g:"+group[1])
		}
	}
	return s, len(targets) == 0 || len(s.targets) != 0
}

func (s *flockService) requests(title, body string) ([]Request, error) {
	chunks, err := notificationChunks(title, body, s.emojis, s.overflow, s.format)
	if err != nil {
		return nil, err
	}
	targets := s.targets
	if len(targets) == 0 {
		targets = []string{""}
	}
	var image any
	if s.image {
		image = appriseInfoIcon72
	}
	calls := make([]Request, 0, len(chunks)*len(targets))
	for _, chunk := range chunks {
		message := chunk.body
		if s.format != "html" {
			message = flockEscape.Replace(message)
			if chunk.title != "" {
				message = "<b>" + flockEscape.Replace(chunk.title) + "</b><br/>" + message
			}
		}
		for _, target := range targets {
			payload := object{{"token", s.token}, {"flockml", "<flockml>" + message + "</flockml>"}, {"sendAs", object{{"name", s.user}, {"profileImage", image}}}}
			endpoint := flockWebhookEndpoint + s.token
			if target != "" {
				payload = append(payload, field{"to", target})
				endpoint = flockAPIEndpoint
			}
			data, err := pythonJSON(payload)
			if err != nil {
				return nil, err
			}
			calls = append(calls, s.post(endpoint, "application/json", data))
		}
	}
	return calls, nil
}
