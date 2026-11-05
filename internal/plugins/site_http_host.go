package plugins

import (
	"context"
	"encoding/json"
	"errors"
)

const SitesRequestCapability = "sites.request"
const SitesCredentialsCapability = "sites.credentials"

type SiteHTTPPairV1 struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// Credential selects a protected host field, never a supplied secret value.
// Prefix is applied literally; callers decide whether their source needs it.
type SiteHTTPCredentialV1 struct {
	Credential string `json:"credential"`
	Header     string `json:"header"`
	Prefix     string `json:"prefix,omitempty"`
}

type SiteHTTPRequestV1 struct {
	ID               string                 `json:"id"`
	Path             string                 `json:"path"`
	Method           string                 `json:"method,omitempty"`
	Query            []SiteHTTPPairV1       `json:"query,omitempty"`
	Form             []SiteHTTPPairV1       `json:"form,omitempty"`
	JSON             json.RawMessage        `json:"json,omitempty"`
	Body             []byte                 `json:"body,omitempty"`
	Headers          map[string]*string     `json:"headers,omitempty"`
	Referer          string                 `json:"referer,omitempty"`
	UseSiteUserAgent *bool                  `json:"use_site_user_agent,omitempty"`
	UseHostUserAgent bool                   `json:"use_host_user_agent,omitempty"`
	Credentials      []SiteHTTPCredentialV1 `json:"credentials,omitempty"`
	CookieNames      []string               `json:"cookie_names"`
	CookieOverride   *[]SiteHTTPPairV1      `json:"cookie_override,omitempty"`
	NoCookies        bool                   `json:"no_cookies,omitempty"`
	TimeoutSeconds   *float64               `json:"timeout_seconds,omitempty"`
	AllowRedirects   *bool                  `json:"allow_redirects,omitempty"`
	RaiseException   bool                   `json:"raise_exception,omitempty"`
}

// Content is private site response data. Do not put it in ordinary logs.
// Cookies are private response material, like host credentials and torrent bytes.
// Consume them in trusted worker memory; do not expose them through public APIs.
type SiteHTTPResponseV1 struct {
	StatusCode  int              `json:"status_code"`
	Content     []byte           `json:"content"`
	ContentType string           `json:"content_type"`
	Cookies     []SiteHTTPPairV1 `json:"cookies"`
}

type SiteHTTPResultV1 struct {
	SchemaVersion int                 `json:"schema_version"`
	Response      *SiteHTTPResponseV1 `json:"response"`
	RequestState  string              `json:"request_state"`
	ScopeChanged  bool                `json:"scope_changed,omitempty"`
}

func (s Sites) Request(ctx context.Context, input SiteHTTPRequestV1) (SiteHTTPResultV1, error) {
	var out SiteHTTPResultV1
	err := hostBusinessCall(ctx, s.Host, SitesRequestCapability, input, &out)
	if err == nil && (out.SchemaVersion != 1 || out.RequestState != "completed" && out.RequestState != "unknown" || out.Response != nil && (out.Response.StatusCode < 100 || out.Response.StatusCode > 599 || out.Response.Content == nil)) {
		err = errors.New("invalid host site HTTP response")
	}
	return out, err
}

// SiteCredentialsResultV1 is private, current-call memory for trusted Go code.
// Never serialize it to configuration, logs, public HTTP responses or reports.
// Native workers are trusted code; this API does not claim an OS sandbox.
type SiteCredentialsRequestV1 struct {
	ID string `json:"id"`
}
type SiteCredentialsResultV1 struct {
	SchemaVersion int    `json:"schema_version"`
	ID            string `json:"id"`
	Cookie        string `json:"cookie"`
	UserAgent     string `json:"ua"`
	APIKey        string `json:"api_key"`
	Token         string `json:"token"`
}

func (s Sites) Credentials(ctx context.Context, id string) (SiteCredentialsResultV1, error) {
	var out SiteCredentialsResultV1
	err := hostBusinessCall(ctx, s.Host, SitesCredentialsCapability, SiteCredentialsRequestV1{ID: id}, &out)
	if err == nil && (out.SchemaVersion != 1 || out.ID != id || id == "") {
		err = errors.New("invalid private site credential response")
	}
	return out, err
}
