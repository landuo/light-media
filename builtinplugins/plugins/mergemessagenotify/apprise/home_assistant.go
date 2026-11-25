package apprise

import (
	"crypto/rand"
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

const homeAssistantPath = "/api/services/persistent_notification/create"

// Python re.I includes the dotted and dotless I, long s, and Kelvin sign.
var homeAssistantNID = regexp.MustCompile(`^[A-Za-z0-9_İıſK-]+$`)
var homeAssistantEscape = regexp.MustCompile(`%[A-Fa-f0-9]{2}`)

type homeAssistantService struct {
	providerHTTPOptions
	endpoint, token, nid string
	auth                 []any
}

func parseHomeAssistantService(raw string) (*homeAssistantService, bool) {
	u, valid := parseSourceURLWithHostVerification(raw, false)
	if !valid {
		return nil, false
	}

	prefix := u.path
	token := sourceUnquote(u.query["accesstoken"])
	if u.query["accesstoken"] == "" {
		parts := strings.FieldsFunc(prefix, func(r rune) bool {
			return strings.ContainsRune(" \t\r\n,\\/", r)
		})
		if len(parts) == 0 {
			return nil, false
		}
		for i, part := range parts {
			parts[i] = sourceUnquote(part)
		}
		token = parts[len(parts)-1]
		prefix = "/" + strings.Join(parts[:len(parts)-1], "/")
	}
	// validate_regex matches a non-whitespace prefix, then trims the whole token.
	if token == "" || strings.TrimLeftFunc(token, sourceSpace) != token {
		return nil, false
	}

	nid := sourceUnquote(u.query["nid"])
	if nid != "" && !homeAssistantNID.MatchString(strings.TrimSuffix(nid, "\n")) {
		return nil, false
	}

	scheme, port := "http", u.port
	if u.scheme == "hassios" {
		scheme = "https"
	} else if port == "" || port == ":0" {
		port = ":8123"
	}
	s := &homeAssistantService{
		endpoint: scheme + "://" + strings.ToLower(u.host) + port + homeAssistantRequestPath(prefix),
		token:    strings.TrimRightFunc(token, sourceSpace),
		nid:      strings.TrimFunc(nid, sourceSpace),
	}
	if u.user != nil && *u.user != "" {
		var password any
		if u.password != nil {
			password = *u.password
		}
		s.auth = []any{*u.user, password}
	}
	return s, s.configure(u)
}

func homeAssistantRequestPath(prefix string) string {
	path := strings.TrimRight(prefix, "/") + homeAssistantPath
	// Requests normalizes dot segments while keeping query and fragment delimiters.
	suffix := ""
	if i := strings.IndexAny(path, "?#"); i >= 0 {
		path, suffix = path[:i], path[i:]
	}
	target := homeAssistantEscape.ReplaceAllStringFunc(iftttRemoveDotSegments(path)+suffix, strings.ToUpper)
	const safe = "!#$&'()*+,/:;=?@[]"
	if _, err := url.PathUnescape(target); err != nil {
		return sourceQuote(target, safe)
	}
	target = homeAssistantEscape.ReplaceAllStringFunc(target, func(escape string) string {
		decoded, _ := url.PathUnescape(escape)
		if sourceQuote(decoded, "") == decoded {
			return decoded
		}
		return escape
	})
	return sourceQuote(target, safe+"%")
}

func (s *homeAssistantService) requests(title, body string) ([]Request, error) {
	return notificationRequests(title, body, s.emojis, s.overflow, s.format, func(title, body string) (Request, error) {
		nid := s.nid
		if nid == "" {
			var id [16]byte
			_, _ = rand.Read(id[:])
			id[6] = id[6]&0x0f | 0x40
			id[8] = id[8]&0x3f | 0x80
			nid = fmt.Sprintf("%x-%x-%x-%x-%x", id[:4], id[4:6], id[6:8], id[8:10], id[10:])
		}
		data, err := pythonJSON(object{{"title", title}, {"message", body}, {"notification_id", nid}})
		if err != nil {
			return Request{}, err
		}
		call := s.post(s.endpoint, "application/json", data)
		bearer := "Bearer " + s.token
		call.Headers["Authorization"] = bearer
		call.HeaderOrder = append(call.HeaderOrder, [2]string{"Authorization", bearer})
		call.Auth = s.auth
		return call, nil
	})
}
