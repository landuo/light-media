package autosignin

import (
	"context"
	"encoding/json"
	"fmt"
	sdk "light-media/builtinplugins/internal/sdk"
	"os"
	"strings"
	"testing"
)

func TestIndependentRousiCredentialBindingSource(t *testing.T) {
	raw, e := os.ReadFile("testdata/source-oracles/independent-source-site-consumer-review-20261106/rousi-source.json")
	if e != nil {
		t.Fatal(e)
	}
	var source struct {
		Rows []struct {
			Name   string
			Site   json.RawMessage
			Login  bool
			Result []any
			Calls  []struct {
				Method, URL string
				Constructor struct{ Headers map[string]any }
			}
		}
	}
	if e = json.Unmarshal(raw, &source); e != nil || len(source.Rows) != 9 {
		t.Fatal("originalsource cases", e)
	}
	for _, c := range source.Rows {
		t.Run(c.Name, func(t *testing.T) {
			v, e := sourceRestoreSavedJSON(c.Site)
			if e != nil {
				t.Fatal(e)
			}
			site := v.(Object)
			site["_host_source_reference"] = sdk.SiteSourceReferenceV1{CustomOrdinal: new(int), Snapshot: strings.Repeat("a", 64)}
			calls := 0
			var request sdk.SiteSourceHTTPRequestV1
			host := func(_ context.Context, method string, in json.RawMessage) (json.RawMessage, error) {
				if method != sdk.SitesSourceRequestCapability {
					return nil, fmt.Errorf("unexpected %s", method)
				}
				calls++
				if e := json.Unmarshal(in, &request); e != nil {
					t.Fatal(e)
				}
				return json.Marshal(sdk.SiteHTTPResultV1{SchemaVersion: 1, RequestState: "completed", Response: &sdk.SiteHTTPResponseV1{StatusCode: 200, Content: []byte(`{"code":0}`), ContentType: "application/json"}})
			}
			tr := sdkSiteTransport{backend: sdkBackend{ctx: context.Background(), host: host}, site: site}
			result, e := RousiVisit(context.Background(), site, SiteVisitHooks{HTTP: tr}, c.Login)
			want := c.Result[0].(bool)
			if e != nil || result.State != want || result.Message != c.Result[1] || calls != len(c.Calls) {
				t.Fatalf("originalresult=%v/%v calls=%d target=%+v/%v callbacks=%d", c.Result[0], c.Result[1], len(c.Calls), result, e, calls)
			}
			if calls == 0 {
				return
			}
			s := c.Calls[0]
			wantKind := "token"
			if strings.HasSuffix(s.URL, "/api/v1/profile") || s.Constructor.Headers["api-token"] != nil {
				wantKind = "api_key"
			}
			if len(request.Request.Credentials) != 1 || request.Request.Credentials[0].Credential != wantKind {
				t.Fatalf("sourceauthprivatefield=%s targetselectors=%+v", wantKind, request.Request.Credentials)
			}
			if !request.Request.NoCookies {
				t.Fatal("sourceauth-only request acquiredcookie")
			}
			if _, ok := request.Request.Headers["Authorization"]; ok {
				t.Fatal("literal privateheader forwarded")
			}
			if request.Reference.CustomOrdinal == nil || *request.Reference.CustomOrdinal != 0 || request.Reference.SiteID != "" {
				t.Fatal("customreference synthesizednativeid", request.Reference)
			}
		})
	}
}
