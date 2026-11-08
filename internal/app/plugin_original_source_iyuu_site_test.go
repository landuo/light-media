package app

import (
	"encoding/json"
	"net/url"
	"testing"

	"light-media/internal/plugins"
)

// Token data and the observed 302 CDN shape are independent inputs. Only the
// exact next URL for this site snapshot can leave the current reverse scope.
func TestIYUUMTeamMaterialGrant(t *testing.T) {
	parse := func(value string) *url.URL {
		t.Helper()
		u, err := url.Parse(value)
		if err != nil {
			t.Fatal(err)
		}
		return u
	}
	packet := func(response *plugins.SiteHTTPResponseV1, changed bool) json.RawMessage {
		t.Helper()
		raw, err := json.Marshal(plugins.SiteHTTPResultV1{SchemaVersion: 1, RequestState: "completed", ScopeChanged: changed, Response: response})
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	site := originalResolvedSourceSite{base: parse("https://kp.m-team.cc/"), reference: plugins.SiteSourceReferenceV1{Snapshot: "owned-site"}}
	var grant originalIYUUMTeamMaterialGrant
	token := &plugins.SiteHTTPResponseV1{
		StatusCode: 200,
		Content:    []byte(`{"data":"https://api.m-team.cc/owned/token?ticket=owned","Data":"https://api.m-team.cc/wrong"}`),
	}
	first := parse("https://api.m-team.cc/owned/token?ticket=owned&https=1")
	recordOriginalIYUUMTeamMaterial(&grant, site, packet(token, true), true)
	if grant.consume("owned-site", first) {
		t.Fatal("revoked token response granted a request")
	}
	recordOriginalIYUUMTeamMaterial(&grant, site, packet(token, false), true)
	if grant.consume("another-site", first) || grant.consume("owned-site", parse("https://api.m-team.cc/wrong?https=1")) ||
		!grant.consume("owned-site", first) || grant.consume("owned-site", first) {
		t.Fatal("material grant did not bind snapshot, exact lowercase data URL, and single use")
	}
	location := "https://fr1.halomt.com/owned?app_id=1&playload=owned&sign=owned&t=1&v=1"
	redirect := &plugins.SiteHTTPResponseV1{StatusCode: 302, Content: []byte{}, Location: &location}
	recordOriginalIYUUMTeamMaterial(&grant, site, packet(redirect, false), false)
	second := parse(location)
	if grant.consume("owned-site", parse("https://fr2.halomt.com"+second.RequestURI())) || !grant.consume("owned-site", second) {
		t.Fatal("material redirect did not bind the observed CDN")
	}
	noRedirect := false
	request := plugins.SiteSourceHTTPRequestV1{
		OriginProfile: "iyuu-mteam-material-cdn", IncludeLocation: true, ExplicitHeaders: true, CookieMode: "none", UserAgent: "source",
		Request: plugins.SiteHTTPRequestV1{Path: second.RequestURI(), AllowRedirects: &noRedirect},
	}
	if err := validateOriginalIYUUMTeamMaterial(request, "GET"); err != nil {
		t.Fatal(err)
	}
	request.Request.Credentials = []plugins.SiteHTTPCredentialV1{{Credential: "api_key", Header: "x-api-key"}}
	if err := validateOriginalIYUUMTeamMaterial(request, "GET"); err == nil {
		t.Fatal("CDN request accepted a protected credential")
	}
}
