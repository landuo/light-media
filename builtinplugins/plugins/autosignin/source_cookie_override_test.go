package autosignin

import (
	"context"
	"encoding/json"
	"fmt"
	sdk "light-media/builtinplugins/internal/sdk"
	"strings"
	"testing"
)

func TestSourceBoundCookieSelectionNotEmpty(t *testing.T) {
	for _, mode := range []string{"normal", "raw", "override", "none"} {
		t.Run(mode, func(t *testing.T) {
			var got sdk.SiteSourceHTTPRequestV1
			calls := 0
			host := func(_ context.Context, m string, in json.RawMessage) (json.RawMessage, error) {
				if m != sdk.SitesSourceRequestCapability {
					return nil, fmt.Errorf("unexpected%s", m)
				}
				calls++
				if e := json.Unmarshal(in, &got); e != nil {
					t.Fatal(e)
				}
				return json.Marshal(sdk.SiteHTTPResultV1{SchemaVersion: 1, RequestState: "completed", Response: &sdk.SiteHTTPResponseV1{StatusCode: 200, Content: []byte("owned")}})
			}
			ref := sdk.SiteSourceReferenceV1{CustomOrdinal: new(int), Snapshot: strings.Repeat("a", 64)}
			tr := sdkSiteTransport{backend: sdkBackend{ctx: context.Background(), host: host}, site: Object{"url": "https://hdchina.org/", "cookie": "hdchina=one", "ua": "owned-UA", "_host_source_reference": ref}}
			request := PageRequest{URL: "https://hdchina.org/index.php", Method: "GET"}
			raw := "hdchina=first%2F;hdchina=last%3D"
			if mode == "raw" {
				request.HeadersMode = HeaderBaseCookie
			}
			if mode == "override" {
				request.CookieOverride = &raw
			}
			if mode == "none" {
				request.NoCookies = true
			}
			if _, e := tr.sourceDo(context.Background(), request); e != nil || calls != 1 {
				t.Fatal(e, calls)
			}
			if got.Request.CookieNames != nil {
				t.Fatal("source full-cookie mode became empty subset")
			}
			if mode == "override" && got.Request.CookieOverride == nil {
				t.Fatal("override lost")
			}
		})
	}
}
func TestSourceHDChinaWWWFixedOriginBinding(t *testing.T) {
	tr := sdkSiteTransport{site: Object{"url": "https://www.hdchina.org/"}}
	for _, address := range []string{"https://hdchina.org/index.php", "https://hdchina.org/plugin_sign-in.php?cmd=signin"} {
		path, profile, e := tr.sourcePath(address)
		if e != nil || profile != "hdchina" || !strings.HasPrefix(path, "/") {
			t.Fatal(address, path, profile, e)
		}
	}
}

// Discovery cross-check only: source-static-origin-scan.json remains the source
// authority. Routes without a fixed URL deliberately stay caller-owned.
func TestSourceFixedPolicyRegisteredRoutesCovered(t *testing.T) {
	count := 0
	for name, policy := range fixedPolicies {
		for _, host := range policy.RegisteredHosts {
			for _, route := range policy.Routes {
				if route.URL == "" {
					continue
				}
				for _, scheme := range []string{"https", "http"} {
					configured := scheme + "://" + host + "/"
					tr := sdkSiteTransport{site: Object{"url": configured}}
					_, _, e := tr.sourcePath(route.URL)
					if e != nil {
						t.Errorf("Go discovery clue needs originalsource check: adapter%s source%s target%s: %v", name, configured, route.URL, e)
					}
					count++
				}
			}
		}
	}
	if count == 0 {
		t.Fatal("no fixed policy cases")
	}
	t.Log("registeredhost route discovery cells", count)
}
