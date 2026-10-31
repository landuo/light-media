package plugins

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"unicode/utf8"
)

const (
	SiteAccessSuccessCapability = "site.access.success"
	SiteAccessFailCapability    = "site.access.fail"
)

// SharedSiteAccess exposes only the source SiteOper.success/fail operations.
// It records access statistics, not sign-in status, site configuration or HTTP.
// Authority and persistence belong to the current invocation's host.
type SharedSiteAccess struct{ Host HostCaller }

// Success retains the domain verbatim, including an empty string or surrounding
// whitespace. A nil timing means source None; zero and negative values are not
// omitted or clamped. The ordinary wire surface is the finite SQLite int64
// domain, not arbitrary Python numeric objects.
func (s SharedSiteAccess) Success(ctx context.Context, domain string, seconds *int64) error {
	return s.call(ctx, SiteAccessSuccessCapability, domain, struct {
		Domain  string `json:"domain"`
		Seconds *int64 `json:"seconds"`
	}{domain, seconds})
}

// Fail sends no timing field. A nil error means that the host returned the
// source None value, not a boolean result or an HTTP status.
func (s SharedSiteAccess) Fail(ctx context.Context, domain string) error {
	return s.call(ctx, SiteAccessFailCapability, domain, struct {
		Domain string `json:"domain"`
	}{domain})
}

// No retry is made: an error, including cancellation observed after HostCaller
// returns, does not prove that the host failed to commit its counter mutation.
func (s SharedSiteAccess) call(ctx context.Context, capability, domain string, input any) error {
	if ctx == nil || s.Host == nil {
		return errors.New("site access requires an active context and host caller")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	// encoding/json otherwise replaces invalid UTF-8 silently. No URL parsing,
	// trimming, normalization, nonempty requirement or site lookup occurs here.
	if !utf8.ValidString(domain) {
		return errors.New("site access requires a valid Unicode domain")
	}
	raw, err := json.Marshal(input)
	if err != nil {
		return err
	}
	// Preserve the existing ordinary resource envelope as an explicit boundary,
	// not an assertion that the Python source has a domain-length restriction.
	if len(raw) > 128<<10 {
		return errors.New("site access input exceeds host limit")
	}
	raw, err = s.Host(ctx, capability, raw)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	// Exactly one JSON null is the complete typed reply. This rejects objects
	// (including unknown/duplicate keys), booleans, trailing values and non-JSON
	// whitespace rather than accepting a generic truthy acknowledgement.
	if len(raw) > MaxPayloadBytes || !utf8.Valid(raw) || !json.Valid(raw) || !bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return errors.New("site access expected a JSON null reply")
	}
	return nil
}
