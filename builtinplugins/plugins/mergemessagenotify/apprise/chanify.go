package apprise

import (
	"net/url"
	"path"
	"regexp"
	"strings"
)

const chanifyEndpoint = "https://api.chanify.net"

// Python's Unicode case-insensitive ASCII range also matches these four letters.
var chanifyToken = regexp.MustCompile(`^[A-Za-z0-9._İıſK-]+$`)

type chanifyService struct {
	providerHTTPOptions
	token string
}

func parseChanifyService(raw string) (*chanifyService, bool) {
	u, valid := parseSourceURLWithHostVerification(raw, false)
	token := queryToken(u)
	if !valid || !chanifyToken.MatchString(strings.TrimSuffix(token, "\n")) {
		return nil, false
	}
	s := &chanifyService{token: strings.TrimFunc(token, sourceSpace)}
	return s, s.configure(u)
}

func (s *chanifyService) requests(title, body string) ([]Request, error) {
	chunks, err := titlelessNotificationChunks(title, body, &s.providerHTTPOptions, 32768)
	if err != nil {
		return nil, err
	}
	// Requests normalizes literal dot segments before sending the URL.
	endpoint := chanifyEndpoint + path.Clean("/v1/sender/"+s.token) + "/"
	calls := make([]Request, 0, len(chunks))
	for _, chunk := range chunks {
		data := "text=" + url.QueryEscape(chunk.body)
		calls = append(calls, s.post(endpoint, "application/x-www-form-urlencoded", []byte(data)))
	}
	return calls, nil
}
