package apprise

import (
	"context"
	"encoding/json"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

var mattermostNative = regexp.MustCompile(`(?i)^http(s?)://(mattermost\.[a-z0-9_.-]+)(?::[1-9][0-9]{0,5})?/hooks/([a-z0-9_-]+)/?(\?.+)?$`)
var mattermostChannel = regexp.MustCompile(`^(?:#|%23)([A-Za-z0-9_-]+)$`)
var mattermostChannelID = regexp.MustCompile(`^(?:\+|%2B)?([A-Za-z0-9_-]+)$`)

type mattermostTarget struct {
	value  string
	lookup bool
}

type mattermostCacheEntry struct {
	id      string
	expires time.Time
}

type mattermostService struct {
	providerHTTPOptions
	base, token, user, icon string
	bot                     bool
	targets                 []mattermostTarget
	cacheMu                 sync.Mutex
	channels                map[string]mattermostCacheEntry
}

func mattermostNativeURL(raw string) (string, bool) {
	match := mattermostNative.FindStringSubmatch(raw)
	if match == nil {
		return "", false
	}
	// The source uses the explicit native port as its comparison default,
	// so a port in a native URL is omitted in the rewritten scheme.
	return "mmost" + strings.ToLower(match[1]) + "://" + match[2] + "/" + match[3] + "/" + match[4], true
}

func parseMattermostService(raw string) (*mattermostService, bool) {
	u, valid := parseSourceURL(raw)
	if !valid {
		return nil, false
	}
	path := strings.FieldsFunc(u.path, func(r rune) bool {
		return strings.ContainsRune(" \t\r\n,\\/", r)
	})
	if len(path) == 0 {
		return nil, false
	}
	for i, part := range path {
		path[i] = sourceUnquote(part)
	}
	token := path[len(path)-1]
	if token == "" || strings.TrimLeftFunc(token, sourceSpace) != token {
		return nil, false
	}
	scheme := "http"
	if u.scheme == "mmosts" {
		scheme = "https"
	}
	base := scheme + "://" + u.host + u.port
	if len(path) > 1 {
		base += "/" + strings.Join(path[:len(path)-1], "/")
	}
	s := &mattermostService{
		base: strings.TrimRight(strings.TrimFunc(base, sourceSpace), "/"), token: strings.TrimRightFunc(token, sourceSpace),
		channels: make(map[string]mattermostCacheEntry),
	}
	if !s.configure(u) {
		return nil, false
	}
	if u.user != nil {
		s.user = *u.user
	}
	mode := sourceUnquote(u.query["mode"])
	if team := u.query["team"]; team != "" {
		s.user = sourceUnquote(team)
		if mode == "" {
			mode = "bot"
		}
	} else if botname := u.query["botname"]; botname != "" {
		s.user = sourceUnquote(botname)
	}
	mode = strings.ToLower(strings.TrimFunc(mode, sourceSpace))
	switch {
	case strings.HasPrefix("webhook", mode):
	case strings.HasPrefix("bot", mode):
		s.bot = true
	default:
		return nil, false
	}
	s.icon = sourceUnquote(u.query["icon_url"])
	if s.icon == "" && parseBool(u.query["image"], true) {
		s.icon = appriseInfoIcon72
	}
	var targets []string
	for _, name := range []string{"to", "channel", "channels"} {
		for _, raw := range strings.FieldsFunc(u.query[name], sourceListDelimiter) {
			targets = append(targets, strings.FieldsFunc(sourceUnquote(raw), sourceListDelimiter)...)
		}
	}
	slices.Sort(targets)
	for _, target := range slices.Compact(targets) {
		if named := mattermostChannel.FindStringSubmatch(target); named != nil {
			if !s.bot || s.user != "" {
				s.targets = append(s.targets, mattermostTarget{value: named[1], lookup: s.bot})
			}
		} else if id := mattermostChannelID.FindStringSubmatch(target); id != nil {
			s.targets = append(s.targets, mattermostTarget{value: id[1]})
		}
	}
	if !s.bot && len(s.targets) == 0 {
		s.targets = []mattermostTarget{{}}
	}
	return s, true
}

func (s *mattermostService) waitToSend(ctx context.Context) error {
	return s.throttle.wait(ctx, 20*time.Millisecond)
}

func (s *mattermostService) successful(status int) bool {
	return status == http.StatusOK || s.bot && status == http.StatusCreated
}

func (s *mattermostService) channelID(channel string, deliver func(Request) (Response, bool)) (id string, continueSending bool) {
	s.cacheMu.Lock()
	cached := s.channels[channel]
	s.cacheMu.Unlock()
	if cached.id != "" && time.Now().Before(cached.expires) {
		return cached.id, true
	}
	endpoint := s.base + "/api/v4/teams/name/" + sourceQuote(s.user, "") + "/channels/name/" + sourceQuote(channel, "")
	reply, sent := deliver(Request{
		Method: http.MethodGet, URL: endpoint,
		HeaderOrder: [][2]string{{"User-Agent", "Apprise"}, {"Accept", "application/json"}, {"Authorization", "Bearer " + s.token}},
		Headers:     map[string]string{"User-Agent": "Apprise", "Accept": "application/json", "Authorization": "Bearer " + s.token},
		Verify:      s.verify, Timeout: s.timeout,
	})
	if !sent || reply.Status != http.StatusOK || !utf8.Valid(reply.Content) {
		return "", true
	}
	var payload any
	if json.Unmarshal(reply.Content, &payload) != nil {
		return "", true
	}
	entry, ok := payload.(map[string]any)
	if !ok {
		// The source calls .get("id") after JSON decoding. A scalar or
		// list reply aborts this notification instead of skipping a channel.
		return "", false
	}
	id, _ = entry["id"].(string)
	if strings.TrimFunc(id, sourceSpace) == "" {
		return "", true
	}
	s.cacheMu.Lock()
	s.channels[channel] = mattermostCacheEntry{id, time.Now().Add(20 * 24 * time.Hour)}
	s.cacheMu.Unlock()
	return id, true
}

func (s *mattermostService) sendText(title, body string, deliver func(Request) (Response, bool)) (bool, error) {
	chunks, err := titlelessNotificationChunks(title, body, &s.providerHTTPOptions, 4000)
	if err != nil {
		return false, err
	}
	success := len(s.targets) != 0
	for _, chunk := range chunks {
		for _, target := range s.targets {
			value := target.value
			if target.lookup {
				var continueSending bool
				value, continueSending = s.channelID(value, deliver)
				if !continueSending {
					return false, nil
				}
				if value == "" {
					success = false
					continue
				}
			}
			call, err := s.message(chunk.body, value)
			if err != nil {
				return false, err
			}
			_, sent := deliver(call)
			success = sent && success
		}
	}
	return success, nil
}

func (s *mattermostService) message(body, target string) (Request, error) {
	if s.bot {
		data, err := pythonJSON(object{{"channel_id", target}, {"message", body}})
		call := s.post(s.base+"/api/v4/posts", "application/json", data)
		call.Headers["Authorization"] = "Bearer " + s.token
		call.HeaderOrder = append(call.HeaderOrder, [2]string{"Authorization", "Bearer " + s.token})
		return call, err
	}
	payload := object{{"text", body}}
	if s.icon != "" {
		payload = append(payload, field{"icon_url", s.icon})
	}
	username := s.user
	if username == "" {
		username = "Apprise"
	}
	payload = append(payload, field{"username", username})
	if target != "" {
		payload = append(payload, field{"channel", target})
	}
	data, err := pythonJSON(payload)
	return s.post(s.base+"/hooks/"+s.token, "application/json", data), err
}
