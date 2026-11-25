package apprise

import (
	"net/http"
	"regexp"
	"strings"
)

// Apprise appends another slash to its base URL before the token.
const webexTeamsEndpoint = "https://api.ciscospark.com/v1/webhooks/incoming//"

var (
	webexTeamsToken  = regexp.MustCompile(`^[A-Za-z0-9İıſK]{80,160}$`)
	webexTeamsNative = regexp.MustCompile(`(?i)^https?://(?:ap[iİı]\.c[iİı]scospark\.com|webexap[iİı]s\.com)/v[1-9][0-9]*/webhooks/[iİı]ncom[iİı]ng/([a-z0-9_İı-]+)/?(\?.+)?$`)
)

type webexTeamsService struct {
	providerHTTPOptions
	token string
}

func webexTeamsNativeURL(raw string) (string, bool) {
	match := webexTeamsNative.FindStringSubmatch(strings.TrimSuffix(raw, "\n"))
	if match == nil {
		return "", false
	}
	return "wxteams://" + match[1] + "/" + match[2], true
}

func parseWebexTeamsService(raw string) (*webexTeamsService, bool) {
	u, valid := parseSourceURLWithHostVerification(raw, false)
	token := strings.TrimSuffix(queryToken(u), "\n")
	if !valid || !webexTeamsToken.MatchString(token) {
		return nil, false
	}
	s := &webexTeamsService{token: token}
	if !s.configure(u) {
		return nil, false
	}
	if format := strings.ToLower(u.query["format"]); format != "text" && format != "html" {
		s.format = "markdown"
	}
	return s, true
}

func (s *webexTeamsService) requests(title, body string) ([]Request, error) {
	chunks, err := titlelessNotificationChunks(title, body, &s.providerHTTPOptions, 1000)
	if err != nil {
		return nil, err
	}
	fieldName := "text"
	if s.format == "markdown" {
		fieldName = "markdown"
	}
	endpoint := webexTeamsEndpoint + sourceQuote(s.token, "")
	calls := make([]Request, 0, len(chunks))
	for _, chunk := range chunks {
		data, err := pythonJSON(object{{fieldName, chunk.body}})
		if err != nil {
			return nil, err
		}
		calls = append(calls, s.post(endpoint, "application/json", data))
	}
	return calls, nil
}

func (s *webexTeamsService) successful(status int) bool {
	return status == http.StatusOK || status == http.StatusNoContent
}
