package apprise

import (
	"encoding/json"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

const wxPusherEndpoint = "https://wxpusher.zjiecode.com/api/send/message"

var (
	wxPusherUser  = regexp.MustCompile(`(?i)^u[iİı]d_.+$`)
	wxPusherTopic = regexp.MustCompile(`^[1-9][0-9]{0,20}$`)
)

type wxPusherService struct {
	providerHTTPOptions
	token  string
	topics []any
	users  []any
}

func parseWxPusherService(raw string) (*wxPusherService, bool) {
	u, valid := parseSourceURLWithHostVerification(raw, false)
	if !valid {
		return nil, false
	}
	token := strings.TrimSuffix(queryToken(u), "\n")
	if len(token) <= len("AT_") || !strings.EqualFold(token[:len("AT_")], "AT_") || strings.IndexFunc(token, sourceSpace) >= 0 {
		return nil, false
	}
	s := &wxPusherService{token: token, topics: []any{}, users: []any{}}
	if !s.configure(u) {
		return nil, false
	}
	var targets []string
	addTargets := func(raw string) {
		targets = append(targets, strings.FieldsFunc(sourceUnquote(raw), sourceListDelimiter)...)
	}
	paths := []string{u.path}
	if u.query["token"] != "" {
		paths = append(paths, u.rawHost)
	}
	for _, path := range paths {
		for _, part := range strings.FieldsFunc(path, func(r rune) bool {
			return strings.ContainsRune(" \t\r\n,\\/", r)
		}) {
			addTargets(part)
		}
	}
	for _, part := range strings.FieldsFunc(u.query["to"], sourceListDelimiter) {
		addTargets(part)
	}
	slices.Sort(targets)
	for _, target := range slices.Compact(targets) {
		switch {
		case wxPusherUser.MatchString(target):
			s.users = append(s.users, target)
		case wxPusherTopic.MatchString(target):
			s.topics = append(s.topics, json.Number(target))
		}
	}
	return s, true
}

func (s *wxPusherService) requests(title, body string) ([]Request, error) {
	if len(s.topics)+len(s.users) == 0 {
		return nil, nil
	}
	contentType := 1
	switch s.format {
	case "html":
		contentType = 2
	case "markdown":
		contentType = 3
	}
	return notificationRequests(title, body, s.emojis, s.overflow, s.format, func(title, body string) (Request, error) {
		data, err := pythonJSON(object{
			{"appToken", s.token}, {"content", body}, {"summary", title}, {"contentType", contentType},
			{"topicIds", s.topics}, {"uids", s.users}, {"url", nil},
		})
		call := s.post(wxPusherEndpoint, "application/json", data)
		call.Headers["Accept"] = "application/json"
		call.HeaderOrder = append(call.HeaderOrder, [2]string{"Accept", "application/json"})
		return call, err
	})
}

func (s *wxPusherService) successfulResponse(response Response) bool {
	data, err := pythonJSONEncoding(response.Content)
	var payload map[string]json.RawMessage
	if err != nil || json.Unmarshal(data, &payload) != nil {
		return false
	}
	code, err := strconv.ParseFloat(string(payload["code"]), 64)
	return err == nil && code == 1000
}
