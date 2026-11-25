package apprise

import (
	"bytes"
	"context"
	"encoding/json"
	"maps"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

const twistAPI = "https://api.twist.com/api/v3/"

var (
	twistChannel   = regexp.MustCompile(`^#?(([A-Za-z0-9_-]+:)?([^\t-\r\x{001c}-\x{0020}\x{0085}\p{Z}]{1,64}))$`)
	twistChannelID = regexp.MustCompile(`^(?:[0-9]+:)?([0-9]+)$`)
)

// The API can encode IDs as numbers or strings. The source compares workspace
// IDs by type and only turns a channel ID into an integer at delivery time.
type twistID struct {
	text    string
	numeric bool
}

type twistService struct {
	providerHTTPOptions
	mu               sync.Mutex
	email, password  string
	token            string
	defaultWorkspace twistID
	names            map[string]bool
	ids              map[string]bool
	workspaces       map[string]twistID
	channels         map[twistID]map[string]twistID
	retrying         bool
}

func parseTwistService(raw string) (*twistService, bool) {
	u, ok := parseSourceURL(raw)
	if !ok || u.rawUser == nil || *u.rawUser == "" {
		return nil, false
	}
	var targets []string
	for _, part := range strings.FieldsFunc(u.path, func(r rune) bool {
		return strings.ContainsRune(" \t\r\n,\\/", r)
	}) {
		targets = append(targets, sourceUnquote(part))
	}
	user := *u.user
	var password string
	if u.rawPassword != nil && *u.rawPassword != "" {
		// Twist's authority puts the password before the email address.
		user, password = *u.password, user
	} else if len(targets) != 0 {
		password, targets = targets[0], targets[1:]
	}
	email := pushBulletEmail.FindStringSubmatch(user + "@" + u.host)
	if email == nil || password == "" {
		return nil, false
	}
	s := &twistService{email: email[1], password: password, names: map[string]bool{}, ids: map[string]bool{}}
	if !s.configure(u) {
		return nil, false
	}
	if s.format == "text" && !strings.EqualFold(u.query["format"], "text") {
		s.format = "markdown"
	}
	for _, part := range strings.FieldsFunc(u.query["to"], sourceListDelimiter) {
		targets = append(targets, sourceUnquote(part))
	}
	for _, target := range strings.FieldsFunc(strings.Join(targets, ","), sourceListDelimiter) {
		if twistChannelID.MatchString(target) {
			s.ids[target] = true
		} else if match := twistChannel.FindStringSubmatch(target); match != nil {
			s.names[strings.ToLower(match[1])] = true
		}
	}
	if len(s.ids)+len(s.names) == 0 {
		s.names["general"] = true
	}
	return s, true
}

func (s *twistService) waitForRequest(ctx context.Context, _ Request) error {
	if s.retrying {
		return ctx.Err()
	}
	return s.throttle.wait(ctx, 200*time.Millisecond)
}

func (s *twistService) sendText(title, body string, deliver func(Request) (Response, bool)) (bool, error) {
	chunks, err := notificationChunksWithBodyLimit(title, body, s.emojis, s.overflow, s.format, 1000)
	if err != nil {
		return false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	success := len(chunks) != 0
	for _, chunk := range chunks {
		success = s.sendChunk(chunk, deliver) && success
	}
	return success, nil
}

func (s *twistService) sendChunk(chunk notificationChunk, deliver func(Request) (Response, bool)) bool {
	if !s.authenticated(deliver) || len(s.names) != 0 && !s.migrateChannels(deliver) {
		return false
	}
	success := len(s.ids) != 0
	for _, target := range slices.Sorted(maps.Keys(s.ids)) {
		match := twistChannelID.FindStringSubmatch(target)
		if match == nil || len(match[1]) > 4300 {
			return false
		}
		id := strings.TrimLeft(match[1], "0")
		if id == "" {
			id = "0"
		}
		payload := "channel_id=" + id + "&title=" + url.QueryEscape(chunk.title) + "&content=" + url.QueryEscape(chunk.body)
		_, sent := s.fetch("threads/add", []byte(payload), deliver)
		success = sent && success
	}
	return success
}

func (s *twistService) migrateChannels(deliver func(Request) (Response, bool)) bool {
	if !s.authenticated(deliver) {
		return true // Lookup failure does not prevent sending to explicit IDs.
	}
	if s.defaultWorkspace.present() && s.channels[s.defaultWorkspace] == nil {
		channels, valid := s.list("channels/get", []byte("workspace_id="+url.QueryEscape(s.defaultWorkspace.text)), deliver)
		if !valid {
			return false
		}
		s.channels[s.defaultWorkspace] = channels
	}
	for _, name := range slices.Sorted(maps.Keys(s.names)) {
		delete(s.names, name)
		parts := twistChannel.FindStringSubmatch(name)
		workspace, channel := strings.TrimSuffix(parts[2], ":"), parts[3]
		id := s.defaultWorkspace
		if workspace != "" {
			if len(s.workspaces) == 0 {
				workspaces, valid := s.list("workspaces/get", nil, deliver)
				if !valid {
					return false
				}
				s.workspaces = workspaces
			}
			var ok bool
			id, ok = s.workspaces[workspace]
			if !ok {
				continue
			}
		}
		// Only the default workspace's channels are fetched, even for targets
		// prefixed by the name of another workspace.
		if channelID, ok := s.channels[id][channel]; ok {
			s.ids[id.text+":"+channelID.text] = true
		}
	}
	return true
}

func (s *twistService) list(endpoint string, data []byte, deliver func(Request) (Response, bool)) (map[string]twistID, bool) {
	result := map[string]twistID{}
	if !s.authenticated(deliver) {
		return result, true
	}
	response, ok := s.fetch(endpoint, data, deliver)
	if !ok {
		return result, true
	}
	entries, ok := response.([]any)
	if !ok {
		// The channels endpoint checks the list type. A truthy malformed team
		// response causes a provider error when the source tries to iterate it.
		return result, endpoint == "channels/get" || !slackTruthy(response)
	}
	for _, value := range entries {
		entry, ok := value.(map[string]any)
		if !ok {
			return nil, false
		}
		name := ""
		if value, exists := entry["name"]; exists {
			name, ok = value.(string)
			if !ok {
				return nil, false
			}
		}
		result[strings.ToLower(name)] = parseTwistID(entry["id"])
	}
	return result, true
}

func parseTwistID(value any) twistID {
	switch value := value.(type) {
	case string:
		return twistID{text: value}
	case json.Number:
		return twistID{text: value.String(), numeric: true}
	}
	return twistID{}
}

func (id twistID) present() bool { return id.text != "" && (!id.numeric || id.text != "0") }

func (s *twistService) authenticated(deliver func(Request) (Response, bool)) bool {
	return s.token != "" || s.login(deliver)
}

func (s *twistService) login(deliver func(Request) (Response, bool)) bool {
	s.defaultWorkspace = twistID{}
	s.workspaces = nil
	s.channels = map[twistID]map[string]twistID{}
	payload := "email=" + url.QueryEscape(s.email) + "&password=" + url.QueryEscape(s.password)
	response, ok := s.fetch("users/login", []byte(payload), deliver)
	if !ok || !slackTruthy(response) {
		s.token = ""
		return false
	}
	login, ok := response.(map[string]any)
	if !ok {
		return false
	}
	s.defaultWorkspace = parseTwistID(login["default_workspace"])
	s.token, _ = login["token"].(string)
	return true
}

func (s *twistService) fetch(endpoint string, data []byte, deliver func(Request) (Response, bool)) (any, bool) {
	call := s.post(twistAPI+endpoint, "application/x-www-form-urlencoded; charset=utf-8", data)
	if s.token != "" {
		value := "Bearer " + s.token
		call.Headers["Authorization"] = value
		call.HeaderOrder = append(call.HeaderOrder, [2]string{"Authorization", value})
	}
	response, sent := deliver(call)
	content := twistResponse(response)
	if !sent && endpoint != "users/login" && twistExpired(content) && s.login(deliver) {
		// The source resends the original headers and skips its throttle here.
		s.retrying = true
		response, sent = deliver(call)
		s.retrying = false
		content = twistResponse(response)
	}
	return content, sent
}

func twistResponse(response Response) any {
	if !json.Valid(response.Content) {
		return nil
	}
	decoder := json.NewDecoder(bytes.NewReader(response.Content))
	decoder.UseNumber()
	var content any
	_ = decoder.Decode(&content)
	return content
}

func twistExpired(content any) bool {
	object, _ := content.(map[string]any)
	code, ok := object["error_code"].(json.Number)
	if !ok {
		return false
	}
	value, _ := strconv.ParseFloat(code.String(), 64)
	return value == 120 || value == 200
}

func (s *twistService) closeNotification(ctx context.Context, transport Transport) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.token == "" {
		return
	}
	s.fetch("users/logout", nil, func(call Request) (Response, bool) {
		if s.waitForRequest(ctx, call) != nil {
			return Response{}, false
		}
		response, err := transport(ctx, call)
		return response, err == nil && s.successful(response.Status)
	})
	s.token = ""
}
