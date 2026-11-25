package apprise

import (
	"slices"
	"strings"
)

const (
	lineEndpoint = "https://api.line.me/v2/bot/message/push"
	lineInfoIcon = "https://github.com/caronc/apprise/raw/master/apprise/assets/themes/default/apprise-info-128x128.png"
)

type lineService struct {
	providerHTTPOptions
	token        string
	targets      []string
	includeImage bool
}

func parseLineService(raw string) (*lineService, bool) {
	u, valid := parseSourceURLWithHostVerification(raw, false)
	if !valid {
		return nil, false
	}

	path := strings.FieldsFunc(u.path, func(r rune) bool {
		return strings.ContainsRune(" \t\r\n,\\/", r)
	})
	for i, part := range path {
		path[i] = sourceUnquote(part)
	}
	token := queryToken(u)
	if u.query["token"] == "" && !strings.HasSuffix(token, "=") {
		// An unescaped long-lived token can span path segments up to its padding.
		for i, part := range path {
			if strings.HasSuffix(part, "=") {
				token += "/" + strings.Join(path[:i+1], "/")
				path = path[i+1:]
				break
			}
		}
	}
	if token == "" || strings.TrimLeftFunc(token, sourceSpace) != token {
		return nil, false
	}

	s := &lineService{token: strings.TrimRightFunc(token, sourceSpace), includeImage: true}
	if image, exists := u.query["image"]; exists {
		s.includeImage = parseBool(image, false)
	}
	queryTargets := strings.FieldsFunc(sourceUnquote(u.query["to"]), func(r rune) bool {
		return strings.ContainsRune(" \t\r\n,#\\/", r)
	})
	for _, target := range append(path, queryTargets...) {
		s.targets = append(s.targets, strings.FieldsFunc(target, sourceListDelimiter)...)
	}
	slices.Sort(s.targets)
	s.targets = slices.Compact(s.targets)
	return s, s.configure(u)
}

func (s *lineService) requests(title, body string) ([]Request, error) {
	if len(s.targets) == 0 {
		return nil, nil
	}
	chunks, err := titlelessNotificationChunks(title, body, &s.providerHTTPOptions, 5000)
	if err != nil {
		return nil, err
	}
	sender := object{{"name", "Apprise"}}
	if s.includeImage {
		sender = append(sender, field{"iconUrl", lineInfoIcon})
	}
	var requests []Request
	for _, chunk := range chunks {
		messages := []any{object{{"type", "text"}, {"text", chunk.body}, {"sender", sender}}}
		for _, target := range s.targets {
			data, err := pythonJSON(object{{"to", target}, {"messages", messages}})
			if err != nil {
				return nil, err
			}
			call := s.post(lineEndpoint, "application/json", data)
			call.Headers["Authorization"] = "Bearer " + s.token
			call.HeaderOrder = append(call.HeaderOrder, [2]string{"Authorization", call.Headers["Authorization"]})
			requests = append(requests, call)
		}
	}
	return requests, nil
}
