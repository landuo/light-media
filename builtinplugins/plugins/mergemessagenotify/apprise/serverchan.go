package apprise

import (
	"context"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var serverChanTokenPrefix = regexp.MustCompile(`^schan://([A-Za-z0-9]+)`)

type serverChanService struct {
	endpoint, overflow, format string
	verify, emojis             bool
	timeout                    [2]float64

	throttle providerThrottle
}

func parseServerChanService(raw string) (*serverChanService, bool) {
	raw = strings.ReplaceAll(strings.TrimFunc(raw, sourceSpace), "/#", "/%23")
	u, valid := parseSourceURLWithHostVerification(raw, false)
	if !valid {
		return nil, false
	}
	// The source reads an undecoded alphanumeric prefix, and only requires
	// a slash immediately after the token when the input ends with one.
	token := serverChanTokenPrefix.FindStringSubmatch(raw)
	if token == nil || strings.HasSuffix(raw, "/") && raw[len(token[0])] != '/' {
		return nil, false
	}
	s := &serverChanService{
		endpoint: "https://sctapi.ftqq.com/" + token[1] + ".send",
		overflow: "upstream", format: "text", verify: true, timeout: [2]float64{4, 4},
	}
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
				return nil, false
			}
		case "store":
			// NotifyBase attempts to disable URL identity via a setter this
			// provider's property does not expose, so Add rejects the URL.
			if !parseBool(value, false) {
				return nil, false
			}
		}
	}
	return s, true
}

func (s *serverChanService) waitToSend(ctx context.Context) error {
	return s.throttle.wait(ctx, 5500*time.Millisecond)
}

func (s *serverChanService) successful(status int) bool { return status == http.StatusOK }

func (s *serverChanService) requests(title, body string) ([]Request, error) {
	return notificationRequests(title, body, s.emojis, s.overflow, s.format, s.request)
}

func (s *serverChanService) request(title, body string) (Request, error) {
	const agent = "python-requests/2.34.2"
	const contentType = "application/x-www-form-urlencoded"
	form := "title=" + url.QueryEscape(title) + "&desp=" + url.QueryEscape(body)
	return Request{
		Method: http.MethodPost, URL: s.endpoint, Data: []byte(form),
		Headers:     map[string]string{"User-Agent": agent, "Content-Type": contentType},
		HeaderOrder: [][2]string{{"User-Agent", agent}, {"Content-Type", contentType}},
		Verify:      s.verify, Timeout: s.timeout,
	}, nil
}
