package apprise

import (
	"net/http"
	"strings"
)

const pagerDutyInfoIcon = "https://github.com/caronc/apprise/raw/master/apprise/assets/themes/default/apprise-info-128x128.png"

type pagerDutyService struct {
	providerHTTPOptions
	endpoint, apiKey, routingKey string
	source, component, severity  string
	group, classID, click        string
	includeImage                 bool
	details                      object
}

func parsePagerDutyService(raw string) (*pagerDutyService, bool) {
	u, valid := parseSourceURLWithHostVerification(raw, false)
	if !valid {
		return nil, false
	}
	s := &pagerDutyService{apiKey: u.host, includeImage: true}
	if !s.configure(u) {
		return nil, false
	}
	if u.user != nil {
		s.routingKey = *u.user
	}
	if key := u.query["apikey"]; key != "" {
		s.apiKey = sourceUnquote(key)
	}
	if key := u.query["integrationkey"]; key != "" {
		s.routingKey = sourceUnquote(key)
	}
	s.apiKey = pagerDutyToken(s.apiKey)
	s.routingKey = pagerDutyToken(s.routingKey)
	if s.apiKey == "" || s.routingKey == "" {
		return nil, false
	}

	path := strings.FieldsFunc(u.path, func(r rune) bool {
		return strings.ContainsRune(" \t\r\n,\\/", r)
	})
	for _, part := range []struct {
		name, fallback string
		destination    *string
	}{{"source", "Apprise", &s.source}, {"component", "Notification", &s.component}} {
		value := u.query[part.name]
		if value == "" && len(path) > 0 {
			value, path = path[0], path[1:]
		}
		value = sourceUnquote(value)
		if value == "" {
			value = part.fallback
		}
		*part.destination = pagerDutyToken(value)
		if *part.destination == "" {
			return nil, false
		}
	}

	switch strings.ToLower(sourceUnquote(u.query["region"])) {
	case "", "us":
		s.endpoint = "https://events.pagerduty.com/v2/enqueue"
	case "eu":
		s.endpoint = "https://events.eu.pagerduty.com/v2/enqueue"
	default:
		return nil, false
	}
	severity := sourceUnquote(u.query["severity"])
	for _, candidate := range []string{"info", "warning", "critical", "error"} {
		if strings.HasPrefix(candidate, severity) {
			s.severity = candidate
			break
		}
	}
	if s.severity == "" {
		return nil, false
	}

	s.group = sourceUnquote(u.query["group"])
	s.classID = sourceUnquote(u.query["class"])
	s.click = sourceUnquote(u.query["click"])
	if image, present := u.query["image"]; present {
		s.includeImage = parseBool(image, false)
	}
	// Match the source's two ordered maps: duplicate raw detail keys update
	// their original slot before the provider unquotes the keys again.
	for _, pair := range u.queryPairs {
		if key := pair[0]; strings.HasPrefix(key, "+") || strings.HasPrefix(key, " ") {
			name, _, _ := strings.Cut(key[1:], "\n")
			s.details = s.details.set(sourceUnquote(name), sourceUnquote(pair[1]))
		}
	}
	return s, true
}

func pagerDutyToken(value string) string {
	if strings.TrimLeftFunc(value, sourceSpace) != value {
		return ""
	}
	return strings.TrimRightFunc(value, sourceSpace)
}

func (s *pagerDutyService) requests(title, body string) ([]Request, error) {
	chunks, err := titlelessNotificationChunks(title, body, &s.providerHTTPOptions, 32768)
	if err != nil {
		return nil, err
	}
	calls := make([]Request, 0, len(chunks))
	for _, chunk := range chunks {
		payload := object{
			{"summary", chunk.body}, {"severity", s.severity}, {"source", s.source}, {"component", s.component},
		}
		if s.group != "" {
			payload = append(payload, field{"group", s.group})
		}
		if s.classID != "" {
			payload = append(payload, field{"class", s.classID})
		}
		if len(s.details) > 0 {
			payload = append(payload, field{"custom_details", s.details})
		}
		event := object{{"routing_key", s.routingKey}, {"payload", payload}, {"client", "Apprise"}, {"event_action", "trigger"}}
		if s.click != "" {
			event = append(event, field{"links", []any{object{{"href", s.click}}}})
		}
		if s.includeImage {
			event = append(event, field{"images", []any{object{{"src", pagerDutyInfoIcon}, {"alt", "info"}}}})
		}
		data, err := pythonJSON(event)
		if err != nil {
			return nil, err
		}
		call := s.post(s.endpoint, "application/json", data)
		call.Headers["Authorization"] = "Token token=" + s.apiKey
		call.HeaderOrder = append(call.HeaderOrder, [2]string{"Authorization", call.Headers["Authorization"]})
		calls = append(calls, call)
	}
	return calls, nil
}

func (s *pagerDutyService) successful(status int) bool {
	return status == http.StatusOK || status == http.StatusCreated || status == http.StatusAccepted
}
