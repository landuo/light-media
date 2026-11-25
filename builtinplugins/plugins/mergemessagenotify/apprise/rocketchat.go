package apprise

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"
)

const rocketChatIcon = "https://github.com/caronc/apprise/raw/master/apprise/assets/themes/default/apprise-info-128x128.png"
const rocketChatLogin = "/api/v1/login"
const rocketChatLogout = "/api/v1/logout"
const rocketChatPost = "/api/v1/chat.postMessage"
const rocketChatRequestsAgent = "python-requests/2.34.2"

var rocketChatWebhookURL = regexp.MustCompile(`(?i)^([^:]+://)(?:([^:]+):)?([a-z0-9]+(?:/|%2F)[a-z0-9]+)@(.+)$`)
var rocketChatChannel = regexp.MustCompile(`^#[A-Za-z0-9_-]+$`)
var rocketChatUser = regexp.MustCompile(`^@[A-Za-z0-9._-]+$`)
var rocketChatRoom = regexp.MustCompile(`^[A-Za-z0-9]+$`)

type rocketChatService struct {
	providerHTTPOptions
	base, mode, webhook    string
	user, password         string
	avatar                 bool
	users, channels, rooms []string
}

func parseRocketChatService(raw string) (*rocketChatService, bool) {
	s := &rocketChatService{}
	var webhookPassword string
	if match := rocketChatWebhookURL.FindStringSubmatch(strings.TrimLeftFunc(raw, sourceSpace)); match != nil {
		webhookPassword = sourceUnquote(match[3])
		s.webhook = webhookPassword
		user := ""
		if match[2] != "" {
			user = match[2] + "@"
		}
		raw = match[1] + user + match[4]
	}
	u, valid := parseSourceURL(raw)
	if !valid || !s.configure(u) {
		return nil, false
	}
	switch strings.ToLower(u.query["format"]) {
	case "text", "html":
	default:
		s.format = "markdown"
	}
	scheme := "http"
	if u.scheme == "rockets" {
		scheme = "https"
	}
	s.base = scheme + "://" + u.host + u.port
	if u.user != nil {
		s.user = *u.user
	}
	if u.password != nil {
		s.password = *u.password
	}
	if webhookPassword != "" {
		s.password = webhookPassword
	}
	if webhook := u.query["webhook"]; webhook != "" {
		s.webhook = sourceUnquote(webhook)
	}
	s.mode = strings.ToLower(sourceUnquote(u.query["mode"]))
	if s.mode == "" {
		switch {
		case s.webhook != "":
			s.mode = "webhook"
		case utf8.RuneCountInString(s.password) > 32:
			s.mode = "token"
		default:
			s.mode = "basic"
		}
	}
	switch s.mode {
	case "webhook":
		if s.webhook == "" {
			return nil, false
		}
	case "basic", "token":
		if s.user == "" || s.password == "" {
			return nil, false
		}
	default:
		return nil, false
	}
	s.avatar = s.mode != "basic"
	if avatar := u.query["avatar"]; avatar != "" {
		s.avatar = parseBool(avatar, false)
	}
	var targets []string
	for _, part := range strings.FieldsFunc(u.path, func(r rune) bool {
		return strings.ContainsRune(" \t\r\n,\\/", r)
	}) {
		targets = append(targets, strings.FieldsFunc(sourceUnquote(part), sourceListDelimiter)...)
	}
	for _, part := range strings.FieldsFunc(u.query["to"], sourceListDelimiter) {
		targets = append(targets, strings.FieldsFunc(sourceUnquote(part), sourceListDelimiter)...)
	}
	slices.Sort(targets)
	for _, target := range slices.Compact(targets) {
		switch {
		case rocketChatChannel.MatchString(target):
			s.channels = append(s.channels, target)
		case rocketChatUser.MatchString(target):
			s.users = append(s.users, target)
		case rocketChatRoom.MatchString(target):
			s.rooms = append(s.rooms, target)
		}
	}
	if s.mode == "basic" && len(s.channels) == 0 && len(s.rooms) == 0 {
		return nil, false
	}
	return s, true
}

func (s *rocketChatService) waitForRequest(ctx context.Context, req Request) error {
	switch req.URL {
	case s.base + rocketChatLogin, s.base + rocketChatLogout:
		return ctx.Err()
	default:
		return s.waitToSend(ctx)
	}
}

func (s *rocketChatService) sendText(title, body string, deliver func(Request) (Response, bool)) (bool, error) {
	chunks, err := titlelessNotificationChunks(title, body, &s.providerHTTPOptions, 1000)
	if err != nil {
		return false, err
	}
	targets := slices.Concat(s.users, s.channels, s.rooms)
	path := rocketChatPost
	if s.mode == "webhook" {
		path = "/hooks/" + s.webhook
		if len(targets) == 0 {
			targets = []string{""}
		}
	}
	success := true
	for _, chunk := range chunks {
		var authHeaders [][2]string
		switch s.mode {
		case "token":
			authHeaders = [][2]string{{"X-User-Id", s.user}, {"X-Auth-Token", s.password}}
		case "basic":
			var authenticated bool
			authHeaders, authenticated = s.login(deliver)
			if !authenticated {
				success = false
				continue
			}
		}
		for i, target := range targets {
			payload := object{{"text", chunk.body}}
			if s.avatar {
				payload = append(payload, field{"avatar", rocketChatIcon})
			}
			if target != "" {
				key := "channel"
				if s.mode != "webhook" && i >= len(s.users)+len(s.channels) {
					key = "roomId"
				}
				payload = append(payload, field{key, target})
			}
			data, err := pythonJSON(payload)
			if err != nil {
				return false, err
			}
			_, sent := deliver(s.request(path, "Apprise", "application/json", data, authHeaders))
			success = sent && success
		}
		if s.mode == "basic" {
			// The original logs logout failures without changing the post result.
			_, _ = deliver(s.request(rocketChatLogout, rocketChatRequestsAgent, "", nil, authHeaders))
		}
	}
	return success, nil
}

func (s *rocketChatService) login(deliver func(Request) (Response, bool)) ([][2]string, bool) {
	data := "username=" + url.QueryEscape(s.user) + "&password=" + url.QueryEscape(s.password)
	reply, sent := deliver(s.request(rocketChatLogin, rocketChatRequestsAgent, "application/x-www-form-urlencoded", []byte(data), nil))
	if !sent {
		return nil, false
	}
	var envelope map[string]json.RawMessage
	if json.Unmarshal(reply.Content, &envelope) != nil {
		return nil, false
	}
	var status string
	if json.Unmarshal(envelope["status"], &status) != nil || status != "success" {
		return nil, false
	}
	if envelope["data"] == nil {
		return nil, true
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(envelope["data"], &fields) != nil || fields == nil {
		return nil, false
	}
	var headers [][2]string
	for _, mapping := range [][2]string{{"authToken", "X-Auth-Token"}, {"userId", "X-User-Id"}} {
		if raw := fields[mapping[0]]; raw != nil {
			var value *string
			if json.Unmarshal(raw, &value) != nil {
				// Requests rejects non-string headers before sending the post.
				return nil, false
			}
			if value != nil {
				headers = append(headers, [2]string{mapping[1], *value})
			}
		}
	}
	return headers, true
}

func (s *rocketChatService) request(path, agent, contentType string, data []byte, auth [][2]string) Request {
	pairs := append(slices.Clone(auth), [2]string{"User-Agent", agent})
	if contentType != "" {
		pairs = append(pairs, [2]string{"Content-Type", contentType})
	}
	headers := make(map[string]string, len(pairs))
	for _, pair := range pairs {
		headers[pair[0]] = pair[1]
	}
	return Request{
		Method: http.MethodPost, URL: s.base + path, Data: data,
		Headers: headers, HeaderOrder: pairs, Verify: s.verify, Timeout: s.timeout,
	}
}
