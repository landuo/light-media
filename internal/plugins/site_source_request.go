package plugins

import (
	"context"
	"encoding/json"
	"errors"
	"unicode/utf8"
)

const (
	SitesSourceCustomConfigCapability = "sites.source.custom_config"
	SitesSourceCredentialsCapability  = "sites.source.credentials"
	SitesSourceRequestCapability      = "sites.source.request"
	SitesSourceCheckCapability        = "sites.source.check"
	SitesSourceResultWriteCapability  = "sites.source.result.write"
)

// A reference is private invocation data. It is never a managed SiteID for a
// custom entry, a permission without the current host scope, or a log value.
// Native IDs may omit Snapshot only when acquiring SourceCredentials.
type SiteSourceReferenceV1 struct {
	SiteID        string `json:"site_id,omitempty"`
	CustomOrdinal *int   `json:"custom_ordinal,omitempty"`
	Snapshot      string `json:"snapshot,omitempty"`
}
type SiteSourceCustomConfigV1 struct {
	SchemaVersion int             `json:"schema_version"`
	Config        json.RawMessage `json:"config"`
	Snapshot      string          `json:"snapshot"`
}
type SiteSourceCredentialsV1 struct {
	SchemaVersion int                   `json:"schema_version"`
	Reference     SiteSourceReferenceV1 `json:"reference"`
	// Raw fields are private trusted-worker memory, including credentials. Never
	// serialize them into public API/config/log responses. Values retain source
	// null/string/number distinctions; the worker applies its original leaf rules.
	Fields json.RawMessage `json:"fields"`
}
type SiteSourceReferenceRequestV1 struct {
	Reference SiteSourceReferenceV1 `json:"reference"`
}
type SiteSourceHTTPRequestV1 struct {
	Reference SiteSourceReferenceV1 `json:"reference"`
	// ID and ordinary UA selector flags must be empty; Reference and UserAgent
	// select the source owner. Other bounded HTTP material keeps the old DTO.
	Request         SiteHTTPRequestV1 `json:"request"`
	CookieMode      string            `json:"cookie_mode,omitempty"`      // parsed(default), raw, none
	UserAgent       string            `json:"user_agent,omitempty"`       // source(default), none, session, host
	IncludeLocation bool              `json:"include_location,omitempty"` // bound GET with redirects disabled
	ExplicitHeaders bool              `json:"explicit_headers,omitempty"`
	OriginProfile   string            `json:"origin_profile,omitempty"`  // site(default) or a finite source profile
	CredentialMode  string            `json:"credential_mode,omitempty"` // empty orfinite rouSi auth normalization
}
type SiteSourceCheckRequestV1 struct {
	Domain string `json:"domain"`
}
type SiteSourceCheckResultV1 struct {
	SchemaVersion int    `json:"schema_version"`
	Limited       bool   `json:"limited"`
	Message       string `json:"message"`
}
type SiteSourceAccessResultV1 struct {
	Reference SiteSourceReferenceV1 `json:"reference"`
	Success   bool                  `json:"success"`
	Seconds   *int64                `json:"seconds,omitempty"`
}

func siteSourceReferenceValid(r SiteSourceReferenceV1, unboundNative bool) bool {
	if r.SiteID != "" && r.CustomOrdinal != nil || r.SiteID == "" && r.CustomOrdinal == nil || !utf8.ValidString(r.SiteID) || len(r.SiteID) > 4096 {
		return false
	}
	if r.CustomOrdinal != nil && *r.CustomOrdinal < 0 {
		return false
	}
	return (unboundNative && r.SiteID != "" && r.Snapshot == "") || len(r.Snapshot) == 64
}
func (s Sites) SourceCustomConfig(ctx context.Context) (SiteSourceCustomConfigV1, error) {
	var out SiteSourceCustomConfigV1
	err := hostBusinessCall(ctx, s.Host, SitesSourceCustomConfigCapability, struct{}{}, &out)
	if err == nil && (out.SchemaVersion != 1 || len(out.Config) == 0 || !json.Valid(out.Config) || len(out.Snapshot) != 64) {
		err = errors.New("invalid source custom config response")
	}
	return out, err
}
func (s Sites) SourceCredentials(ctx context.Context, ref SiteSourceReferenceV1) (SiteSourceCredentialsV1, error) {
	var out SiteSourceCredentialsV1
	if !siteSourceReferenceValid(ref, true) {
		return out, errors.New("invalid source site reference")
	}
	err := hostBusinessCall(ctx, s.Host, SitesSourceCredentialsCapability, SiteSourceReferenceRequestV1{ref}, &out)
	if err == nil && (out.SchemaVersion != 1 || !siteSourceReferenceValid(out.Reference, false) || out.Reference.SiteID != ref.SiteID || (ref.Snapshot != "" && out.Reference.Snapshot != ref.Snapshot) || (ref.CustomOrdinal == nil) != (out.Reference.CustomOrdinal == nil) || ref.CustomOrdinal != nil && *ref.CustomOrdinal != *out.Reference.CustomOrdinal || !siteSourceObject(out.Fields, false)) {
		err = errors.New("invalid private source site response")
	}
	return out, err
}
func (s Sites) SourceRequest(ctx context.Context, in SiteSourceHTTPRequestV1) (SiteHTTPResultV1, error) {
	var out SiteHTTPResultV1
	if !siteSourceReferenceValid(in.Reference, in.OriginProfile == "brush-mteam") {
		return out, errors.New("invalid bound source site reference")
	}
	if in.CredentialMode != "" && in.CredentialMode != "rousi" {
		return out, errors.New("invalid source credential mode")
	}
	err := hostBusinessCall(ctx, s.Host, SitesSourceRequestCapability, in, &out)
	if err == nil && (out.SchemaVersion != 1 || out.RequestState != "completed" && out.RequestState != "unknown" || out.Response != nil && (out.Response.StatusCode < 100 || out.Response.StatusCode > 999 || out.Response.Content == nil)) {
		err = errors.New("invalid source site HTTP response")
	}
	return out, err
}

// SourceCheck is the source helper's consuming site rate check. It does not
// make a site request or provide a grant to another URL.
func (s Sites) SourceCheck(ctx context.Context, domain string) (SiteSourceCheckResultV1, error) {
	var out SiteSourceCheckResultV1
	if domain == "" || len(domain) > 512 || !utf8.ValidString(domain) {
		return out, errors.New("invalid source site check domain")
	}
	err := hostBusinessCall(ctx, s.Host, SitesSourceCheckCapability, SiteSourceCheckRequestV1{Domain: domain}, &out)
	if err == nil && (out.SchemaVersion != 1 || !utf8.ValidString(out.Message)) {
		err = errors.New("invalid source site check response")
	}
	return out, err
}

func (s Sites) SourceRecordAccess(ctx context.Context, in SiteSourceAccessResultV1) (SiteHostAccessV1, error) {
	var out SiteHostAccessV1
	if !siteSourceReferenceValid(in.Reference, false) {
		return out, errors.New("invalid bound source site reference")
	}
	err := hostBusinessCall(ctx, s.Host, SitesSourceResultWriteCapability, in, &out)
	if err == nil && out.SchemaVersion != 1 {
		err = errors.New("invalid source site access response")
	}
	return out, err
}
