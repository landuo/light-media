package apprise

import (
	"net/http"
	"regexp"
	"strings"
)

var googleChatNative = regexp.MustCompile(`(?i)^https://chat\.googleapis\.com/v1/spaces/([a-z0-9_-]+)/messages/*(.+)$`)

type googleChatService struct {
	providerHTTPOptions
	space, key, token, thread string
}

func googleChatNativeURL(raw string) (string, bool) {
	match := googleChatNative.FindStringSubmatch(raw)
	if match == nil {
		return "", false
	}
	return "gchat://" + match[1] + "/" + match[2], true
}

func parseGoogleChatService(raw string) (*googleChatService, bool) {
	u, valid := parseSourceURLWithHostVerification(raw, false)
	if !valid {
		return nil, false
	}
	path := strings.FieldsFunc(u.path, func(r rune) bool {
		return strings.ContainsRune(" \t\r\n,\\/", r)
	})
	s := &googleChatService{space: u.host}
	for i, target := range []*string{&s.key, &s.token, &s.thread} {
		if i < len(path) {
			*target = sourceUnquote(path[i])
		}
	}
	for _, setting := range []struct {
		key    string
		target *string
	}{{"workspace", &s.space}, {"key", &s.key}, {"token", &s.token}, {"threadkey", &s.thread}, {"thread", &s.thread}} {
		if value, ok := u.query[setting.key]; ok {
			*setting.target = sourceUnquote(value)
		}
	}
	if s.space == "" || s.key == "" || s.token == "" {
		return nil, false
	}
	for _, value := range []*string{&s.space, &s.key, &s.token, &s.thread} {
		if strings.TrimLeftFunc(*value, sourceSpace) != *value {
			return nil, false
		}
		*value = strings.TrimRightFunc(*value, sourceSpace)
	}
	if !s.configure(u) {
		return nil, false
	}
	switch strings.ToLower(u.query["format"]) {
	case "html", "text":
	default:
		s.format = "markdown"
	}
	return s, true
}

func (s *googleChatService) successful(status int) bool {
	return status == http.StatusOK || status == http.StatusNoContent
}

func (s *googleChatService) requests(title, body string) ([]Request, error) {
	chunks, err := titlelessNotificationChunks(title, body, &s.providerHTTPOptions, 4000)
	if err != nil {
		return nil, err
	}
	calls := make([]Request, 0, len(chunks))
	for _, chunk := range chunks {
		payload := object{{"text", chunk.body}}
		params := [][2]string{{"token", s.token}, {"key", s.key}}
		if s.thread != "" {
			params = append(params, [2]string{"messageReplyOption", "REPLY_MESSAGE_FALLBACK_TO_NEW_THREAD"})
			payload = append(payload, field{"thread", object{{"thread_key", s.thread}}})
		}
		data, err := pythonJSON(payload)
		if err != nil {
			return nil, err
		}
		call := s.post("https://chat.googleapis.com/v1/spaces/"+s.space+"/messages", "application/json; charset=utf-8", data)
		call.ParamOrder = params
		call.Params = make(map[string]string, len(params))
		for _, item := range params {
			call.Params[item[0]] = item[1]
		}
		calls = append(calls, call)
	}
	return calls, nil
}
