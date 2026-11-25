package plugins

import (
	"encoding/json"
	"errors"
	"mime"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"
)

// HTTPAPI is a compiled declaration, never a grant supplied by a request or an
// installed package. The host must resolve it from its trusted native catalog.
type HTTPAPI struct {
	Key          string          `json:"key"`
	Path         string          `json:"path"`
	Methods      []string        `json:"methods"`
	Auth         string          `json:"auth"`
	Query        []HTTPParameter `json:"query,omitempty"`
	Headers      []string        `json:"headers,omitempty"`
	Body         string          `json:"body,omitempty"`
	BodyRequired bool            `json:"body_required,omitempty"`
}
type HTTPParameter struct {
	Name     string `json:"name"`
	Kind     string `json:"kind"` // string or api_key; key values never cross IPC.
	Required bool   `json:"required,omitempty"`
}
type HTTPRequest struct {
	RequestID         string              `json:"request_id,omitempty"`
	Key               string              `json:"key"`
	Method            string              `json:"method"`
	Path              string              `json:"path"`
	Params            map[string]string   `json:"params"`
	Query             map[string][]string `json:"query"`
	CredentialMatches map[string]bool     `json:"credential_matches"`
	Headers           map[string]string   `json:"headers"`
	// Base64 IPC encoding preserves exact bytes for signed webhook requests.
	Body []byte `json:"body,omitempty"`
}

// Binary API values stay below the existing 16 MiB JSON/IPC payload ceiling
// after base64 encoding. Package-file responses use a rooted file stream.
const MaxHTTPBodyBytes = 8 << 20

type HTTPResponse struct {
	Status  int               `json:"status"`
	Body    []byte            `json:"body"`
	Headers map[string]string `json:"headers,omitempty"`
}

var httpSegment = regexp.MustCompile(`^[A-Za-z0-9_.~-]+$`)
var httpParam = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]{0,63}$`)

func (api HTTPAPI) Validate() error {
	invalid := errors.New("invalid plugin HTTP declaration")
	if !methodPattern.MatchString(api.Key) || len(api.Path) > 512 || !strings.HasPrefix(api.Path, "/") || strings.HasSuffix(api.Path, "/") || !slices.Contains([]string{"apikey", "bear", "anonymous"}, api.Auth) || len(api.Methods) == 0 || len(api.Methods) > 7 || len(api.Query) > 64 || len(api.Headers) > 16 {
		return invalid
	}
	if !slices.Contains([]string{"", "json", "raw"}, api.Body) || api.BodyRequired && api.Body == "" {
		return invalid
	}
	seen := map[string]bool{}
	for _, segment := range strings.Split(api.Path[1:], "/") {
		if strings.HasPrefix(segment, "{") && strings.HasSuffix(segment, "}") {
			name := segment[1 : len(segment)-1]
			if !httpParam.MatchString(name) || seen[name] {
				return invalid
			}
			seen[name] = true
		} else if !httpSegment.MatchString(segment) || segment == "." || segment == ".." {
			return invalid
		}
	}
	seen = map[string]bool{}
	for _, method := range api.Methods {
		if !slices.Contains([]string{"GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS"}, method) || seen[method] {
			return invalid
		}
		seen[method] = true
	}
	seen = map[string]bool{}
	for _, parameter := range api.Query {
		if !httpParam.MatchString(parameter.Name) || !slices.Contains([]string{"string", "api_key"}, parameter.Kind) || seen[parameter.Name] {
			return invalid
		}
		seen[parameter.Name] = true
	}
	seen = map[string]bool{}
	for _, header := range api.Headers {
		key := strings.ToLower(header)
		if !httpParam.MatchString(strings.ReplaceAll(header, "-", "_")) || key != header || seen[key] || slices.Contains([]string{"authorization", "cookie", "x-api-key", "host", "origin", "referer", "forwarded", "connection"}, key) || strings.HasPrefix(key, "x-forwarded-") {
			return invalid
		}
		seen[key] = true
	}
	return nil
}

func (api HTTPAPI) Match(path string) (map[string]string, bool) {
	expected, actual := strings.Split(strings.TrimPrefix(api.Path, "/"), "/"), strings.Split(strings.TrimPrefix(path, "/"), "/")
	if len(expected) != len(actual) {
		return nil, false
	}
	params := map[string]string{}
	for i, segment := range expected {
		value := actual[i]
		if value == "" || value == "." || value == ".." || strings.ContainsAny(value, "\\\x00\r\n") {
			return nil, false
		}
		if strings.HasPrefix(segment, "{") {
			params[segment[1:len(segment)-1]] = value
		} else if segment != value {
			return nil, false
		}
	}
	return params, true
}
func (request HTTPRequest) LastQuery(name string) string {
	values := request.Query[name]
	if len(values) == 0 {
		return ""
	}
	return values[len(values)-1]
}
func (response HTTPResponse) Validate() error {
	if response.Status < 200 || response.Status > 599 || len(response.Body) > MaxHTTPBodyBytes || len(response.Headers) > 8 {
		return errors.New("invalid plugin HTTP response")
	}
	for key, value := range response.Headers {
		if !slices.Contains([]string{"Content-Type", "Cache-Control", "Retry-After", "Content-Disposition", "ETag", "Last-Modified", "Content-Encoding", "Accept-Ranges", "Content-Range"}, key) || strings.ContainsAny(value, "\x00\r\n") || len(value) > 512 {
			return errors.New("invalid plugin HTTP response header")
		}
		if key == "Content-Type" {
			if _, _, err := mime.ParseMediaType(value); err != nil {
				return errors.New("invalid plugin HTTP response type")
			}
		}
		if key == "Content-Disposition" {
			kind, _, err := mime.ParseMediaType(value)
			if err != nil || kind != "inline" && kind != "attachment" {
				return errors.New("invalid plugin HTTP disposition")
			}
		}
	}
	// RFC-defined bodyless statuses are valid plugin responses. The source
	// FastAPI Response accepts them and the existing browser handles 204.
	if (response.Status == 204 || response.Status == 304) && len(response.Body) == 0 {
		return nil
	}
	if response.Headers["Content-Encoding"] != "" {
		return nil
	}
	contentType, _, _ := mime.ParseMediaType(response.Headers["Content-Type"])
	if contentType == "" || contentType == "application/json" || strings.HasSuffix(contentType, "+json") {
		if !json.Valid(response.Body) {
			return errors.New("invalid plugin JSON response")
		}
	} else if strings.HasPrefix(contentType, "text/") && !utf8.Valid(response.Body) {
		return errors.New("invalid plugin text response")
	}
	return nil
}
func JSONHTTPResponse(status int, body any) (json.RawMessage, error) {
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	response := HTTPResponse{Status: status, Body: raw}
	if err := response.Validate(); err != nil {
		return nil, err
	}
	return json.Marshal(response)
}
func LastQuery(query url.Values, name string) string {
	values := query[name]
	if len(values) == 0 {
		return ""
	}
	return values[len(values)-1]
}

// Metadata helper for method errors. It neither executes a plugin nor implies
// that an API is enabled; installation and authentication remain host checks.
func (api HTTPAPI) AllowHeader() string { return strings.Join(api.Methods, ", ") }
