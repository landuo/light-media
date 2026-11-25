package apprise

import (
	"net/http"
	"strconv"
	"strings"
)

type gotifyService struct {
	endpoint, token, overflow, format string
	priority                          int
	verify, emojis                    bool
	timeout                           [2]float64
}

// NotifyGotify uses the last decoded path segment as the token and does not
// apply URL userinfo or custom headers to its requests.
func parseGotifyService(raw string) (gotifyService, bool) {
	s := gotifyService{
		priority: 5, overflow: "upstream", format: "text",
		verify: true, timeout: [2]float64{4, 4},
	}
	u, valid := parseSourceURL(raw)
	if !valid {
		return s, false
	}
	segments := strings.FieldsFunc(u.path, func(r rune) bool {
		return strings.ContainsRune(" \t\r\n,\\/", r)
	})
	if len(segments) == 0 {
		return s, false
	}
	for i := range segments {
		segments[i] = sourceUnquote(segments[i])
	}
	token := segments[len(segments)-1]
	// Source validate_regex matches a non-space prefix and returns the whole
	// token, stripped. Embedded spaces are retained.
	if token == "" || strings.TrimLeftFunc(token, sourceSpace) != token {
		return s, false
	}
	s.token = strings.TrimRightFunc(token, sourceSpace)
	scheme := "http"
	if u.scheme == "gotifys" {
		scheme = "https"
	}
	port := u.port
	if port == ":0" {
		port = ""
	}
	path := "/"
	if len(segments) > 1 {
		path += sourceUnquote(strings.Join(segments[:len(segments)-1], "/")) + "/"
	}
	s.endpoint = scheme + "://" + u.host + port + sourceQuote(path, "/!$&'()*+,-.:;=@[]~%?#") + "message"
	s.priority = gotifyPriority(sourceUnquote(u.query["priority"]))
	for key, value := range u.query {
		switch key {
		case "verify":
			s.verify = parseBool(value, false)
		case "cto", "rto":
			if seconds, err := strconv.ParseFloat(value, 64); err == nil {
				if key == "cto" {
					s.timeout[0] = seconds
				} else {
					s.timeout[1] = seconds
				}
			}
		case "format":
			switch value = strings.ToLower(value); value {
			case "text", "html", "markdown":
				s.format = value
			}
		case "overflow":
			switch value = strings.ToLower(value); value {
			case "upstream", "truncate", "split":
				s.overflow = value
			}
		case "emojis":
			s.emojis = parseBool(value, false)
		case "tz":
			if !validSourceTimezone(value) {
				return s, false
			}
		}
	}
	return s, true
}

func gotifyPriority(value string) int {
	value = strings.ToLower(value)
	for _, priority := range []struct {
		prefix string
		value  int
	}{
		{"l", 0}, {"m", 3}, {"n", 5}, {"h", 8}, {"e", 10},
		{"10", 10}, {"0", 0}, {"1", 0}, {"2", 0}, {"3", 3},
		{"4", 3}, {"5", 5}, {"6", 5}, {"7", 5}, {"8", 8}, {"9", 8},
	} {
		if strings.HasPrefix(value, priority.prefix) {
			return priority.value
		}
	}
	return 5
}

func (s gotifyService) successful(status int) bool { return status == http.StatusOK }

func (s gotifyService) requests(title, body string) ([]Request, error) {
	return notificationRequests(title, body, s.emojis, s.overflow, s.format, s.request)
}

func (s gotifyService) request(title, body string) (Request, error) {
	payload := object{{"priority", s.priority}, {"title", title}, {"message", body}}
	if s.format == "markdown" {
		payload = append(payload, field{"extras", object{{"client::display", object{{"contentType", "text/markdown"}}}}})
	}
	data, err := pythonJSON(payload)
	if err != nil {
		return Request{}, err
	}
	headers := map[string]string{"User-Agent": "Apprise", "Content-Type": "application/json", "X-Gotify-Key": s.token}
	return Request{
		Method: http.MethodPost, URL: s.endpoint, Data: data, Headers: headers,
		HeaderOrder: [][2]string{{"User-Agent", "Apprise"}, {"Content-Type", "application/json"}, {"X-Gotify-Key", s.token}},
		Verify:      s.verify, Timeout: s.timeout,
	}, nil
}
