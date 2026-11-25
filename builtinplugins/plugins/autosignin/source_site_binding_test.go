package autosignin

import (
	"context"
	"encoding/json"
	"fmt"
	sdk "light-media/builtinplugins/internal/sdk"
	"strings"
	"testing"
)

type bindingHost struct {
	t       *testing.T
	calls   []string
	request sdk.SiteSourceHTTPRequestV1
	config  json.RawMessage
}

func (h *bindingHost) Call(ctx context.Context, method string, in json.RawMessage) (json.RawMessage, error) {
	h.calls = append(h.calls, method)
	reply := any(nil)
	switch method {
	case sdk.SitesSourceCustomConfigCapability:
		reply = sdk.SiteSourceCustomConfigV1{SchemaVersion: 1, Config: h.config, Snapshot: strings.Repeat("a", 64)}
	case sdk.SitesSourceCredentialsCapability:
		b, _ := json.Marshal(in)
		var q sdk.SiteSourceReferenceRequestV1
		json.Unmarshal(b, &q)
		q.Reference.Snapshot = strings.Repeat("b", 64)
		reply = sdk.SiteSourceCredentialsV1{SchemaVersion: 1, Reference: q.Reference, Fields: json.RawMessage(`{"id":7,"name":"source","url":"http://127.0.0.1","cookie":"synthetic-only","ua":null,"timeout":null}`)}
	case sdk.SitesSourceRequestCapability:
		b, _ := json.Marshal(in)
		json.Unmarshal(b, &h.request)
		reply = sdk.SiteHTTPResultV1{SchemaVersion: 1, RequestState: "completed", Response: &sdk.SiteHTTPResponseV1{StatusCode: 200, Content: []byte("ok")}}
	default:
		return nil, fmt.Errorf("unexpected host method %s", method)
	}
	return json.Marshal(reply)
}
func TestSourceSiteBindingCustomOrdinalAndCodepoints(t *testing.T) {
	h := &bindingHost{t: t, config: json.RawMessage(`{"enabled":"false","sites":[{"id":7,"name":"first","url":"http://127.0.0.1"},{"id":7,"name":"second","url":"http://127.0.0.1"},{"id":"7","name":"third","url":"http://127.0.0.1"},{"id":false,"name":"\ud800","url":"http://127.0.0.1"}]}`)}
	b := sdkBackend{ctx: context.Background(), host: h.Call}
	enabled, rows := b.customInventory()
	if enabled != true || len(rows) != 4 {
		t.Fatal(enabled, len(rows))
	}
	for i, row := range rows {
		r := row["_host_source_reference"].(sdk.SiteSourceReferenceV1)
		if r.SiteID != "" || *r.CustomOrdinal != i || r.Snapshot != strings.Repeat("a", 64) {
			t.Fatal(r)
		}
	}
	if !equal(rows[0]["id"], json.Number("7")) || !equal(rows[1]["id"], json.Number("7")) || rows[2]["id"] != "7" || rows[3]["id"] != false || !sourceHasPrivateString(rows[3]["name"]) {
		t.Fatal("original list order/raw identities/codepoints lost")
	}
}
func TestSourceSiteBindingPrivateNativeMetadata(t *testing.T) {
	h := &bindingHost{t: t}
	b := sdkBackend{ctx: context.Background(), host: h.Call}
	id := "native-row"
	row := b.project(sdk.SiteSourceIndexerV1{SiteID: &id, Fields: json.RawMessage(`{"id":7,"domain":"127.0.0.1"}`)}, nil, true)
	if _, ok := row["timeout"]; !ok || row["timeout"] != nil || row["ua"] != nil {
		t.Fatal("raw native fields lost")
	}
	if row["_host_source_reference"].(sdk.SiteSourceReferenceV1).SiteID != id {
		t.Fatal("native identity lost")
	}
}
func TestSourceSiteBindingRequestModes(t *testing.T) {
	for _, tc := range []struct {
		name          string
		headers       Object
		mode          HeaderMode
		token         any
		agent, cookie string
		explicit      bool
	}{{"default", nil, "", nil, "", "", false}, {"raw", nil, HeaderBaseCookie, nil, "", "raw", true}, {"token", nil, HeaderBaseCookie, "synthetic-token", "", "none", true}, {"explicit-default-ua", Object{"X-Requested-With": "XMLHttpRequest"}, "", nil, "session", "", true}, {"explicit-site-ua", Object{"User-Agent": nil}, "", nil, "", "", true}} {
		t.Run(tc.name, func(t *testing.T) {
			h := &bindingHost{t: t}
			ref := sdk.SiteSourceReferenceV1{CustomOrdinal: new(int), Snapshot: strings.Repeat("a", 64)}
			site := Object{"url": "http://127.0.0.1", "ua": nil, "token": tc.token, "_host_source_reference": ref}
			s := sdkSiteTransport{backend: sdkBackend{ctx: context.Background(), host: h.Call}, site: site}
			got, e := s.Do(context.Background(), PageRequest{URL: "http://127.0.0.1/attendance.php", Method: "GET", Headers: tc.headers, HeadersMode: tc.mode})
			if e != nil || got == nil || got.Status != 200 {
				t.Fatal(got, e)
			}
			r := h.request
			if r.Reference.SiteID != "" || r.Reference.CustomOrdinal == nil || r.CookieMode != tc.cookie || r.UserAgent != tc.agent || r.ExplicitHeaders != tc.explicit || r.Request.Path != "/attendance.php" {
				t.Fatalf("source modes %+v", r)
			}
			if len(h.calls) != 1 || h.calls[0] != sdk.SitesSourceRequestCapability {
				t.Fatal(h.calls)
			}
		})
	}
}
