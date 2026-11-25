package apprise

import (
	"context"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/idna"
)

const pushDeerRequestInterval = 5500 * time.Millisecond

// Python's case-insensitive ASCII range also matches these four code points.
var pushDeerKeyPattern = regexp.MustCompile(`^[A-Za-z0-9İıſK]+$`)

type pushDeerService struct {
	endpoint, overflow, format string
	emojis                     bool
	timeout                    [2]float64

	mu       sync.Mutex
	lastSend time.Time
}

func parsePushDeerService(raw string) (*pushDeerService, bool) {
	u, valid := parseSourceURLWithHostVerification(raw, false)
	if !valid {
		return nil, false
	}
	scheme, defaultPort := "http", ":80"
	if u.scheme == "pushdeers" {
		scheme, defaultPort = "https", ":443"
	}
	host, key := "api2.pushdeer.com", u.rawHost
	segments := strings.FieldsFunc(u.path, func(r rune) bool {
		return strings.ContainsRune(" \t\r\n,\\/", r)
	})
	if len(segments) > 0 {
		key = sourceUnquote(segments[len(segments)-1])
		if u.host != "" {
			host = pushDeerRequestHost(u.host)
		}
	}
	// Python's $ also matches immediately before a final newline.
	key = strings.TrimSuffix(key, "\n")
	if !pushDeerKeyPattern.MatchString(key) {
		return nil, false
	}
	port := u.port
	if port == "" || port == ":0" {
		port = defaultPort
	}
	s := &pushDeerService{
		endpoint: scheme + "://" + host + port + "/message/push?pushkey=" + url.QueryEscape(key),
		overflow: "upstream", format: "text", timeout: [2]float64{4, 4},
	}
	for name, value := range u.query {
		switch name {
		case "cto", "rto":
			if seconds, err := strconv.ParseFloat(value, 64); err == nil {
				if name == "cto" {
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
		}
	}
	return s, true
}

func pushDeerRequestHost(host string) string {
	// Requests normalizes host case and IDNA-encodes non-ASCII hostnames.
	if strings.IndexFunc(host, func(r rune) bool { return r > 127 }) >= 0 {
		if ascii, err := idna.Lookup.ToASCII(host); err == nil {
			return ascii
		}
	}
	return strings.ToLower(host)
}

func (s *pushDeerService) waitToSend(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if delay := time.Until(s.lastSend.Add(pushDeerRequestInterval)); delay > 0 {
		timer := time.NewTimer(delay)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	s.lastSend = time.Now()
	return nil
}

func (s *pushDeerService) successful(status int) bool { return status == http.StatusOK }

func (s *pushDeerService) requests(title, body string) ([]Request, error) {
	return notificationRequests(title, body, s.emojis, s.overflow, s.format, s.request)
}

func (s *pushDeerService) request(title, body string) (Request, error) {
	if title == "" {
		title, body = body, ""
	}
	const agent = "python-requests/2.34.2"
	const contentType = "application/x-www-form-urlencoded"
	form := "text=" + url.QueryEscape(title) + "&type=text&desp=" + url.QueryEscape(body)
	return Request{
		Method: http.MethodPost, URL: s.endpoint, Data: []byte(form),
		Headers:     map[string]string{"User-Agent": agent, "Content-Type": contentType},
		HeaderOrder: [][2]string{{"User-Agent", agent}, {"Content-Type", contentType}},
		// The original omits the verify argument even when the URL says verify=no.
		Verify: true, Timeout: s.timeout,
	}, nil
}
