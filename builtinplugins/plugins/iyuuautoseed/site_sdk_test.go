package iyuuautoseed

import (
	"encoding/json"
	"net/url"
	"os"
	"strings"
	"testing"

	sdk "light-media/builtinplugins/internal/sdk"
)

// These expectations came from executing the unchanged source resolver
// methods with the original Requests client against controlled responses.
func TestSourceSiteResolverReplay(t *testing.T) {
	raw, err := os.ReadFile("reference/resolver-source-2.22.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Cases []struct {
			Name, Template string
			Seed, Site     json.RawMessage
			Result         struct{ Value *string }
			Calls          []struct{ Op, URL string }
			Responses      []*struct {
				Status  int
				Content []byte
				Headers map[string]string
			}
		}
	}
	if err := json.Unmarshal(raw, &fixture); err != nil || len(fixture.Cases) == 0 {
		t.Fatalf("invalid resolver source fixture: %v", err)
	}
	for _, sample := range fixture.Cases {
		t.Run(sample.Name, func(t *testing.T) {
			fields, err := decodeJSON(sample.Site)
			if err != nil {
				t.Fatal(err)
			}
			private := fields.(map[string]any)
			private["id"] = json.Number("7")
			public, err := json.Marshal(map[string]any{"id": private["id"], "url": private["url"], "name": private["name"]})
			if err != nil {
				t.Fatal(err)
			}
			privateJSON, err := json.Marshal(private)
			if err != nil {
				t.Fatal(err)
			}
			domain, err := SourceDomain(private["url"].(string))
			if err != nil {
				t.Fatal(err)
			}
			id := "test-bound-site"
			ref := sdk.SiteSourceReferenceV1{SiteID: id, Snapshot: strings.Repeat("c", 64)}
			indexer := sdk.SiteSourceIndexerResultV1{SchemaVersion: 1, Indexer: &sdk.SiteSourceIndexerV1{SiteID: &id, Fields: public}}
			credentials := sdk.SiteSourceCredentialsV1{SchemaVersion: 1, Reference: ref, Fields: privateJSON}
			steps := []iyuuSDKStep{
				{sdk.SitesSourceIndexerCapability, sourceSiteReplayJSON(t, indexer), func(raw json.RawMessage) {
					var input sdk.SiteSourceIndexerRequestV1
					if json.Unmarshal(raw, &input) != nil || input.Domain != domain {
						t.Fatal("source site indexer received a different domain")
					}
				}},
				{sdk.SitesSourceCredentialsCapability, sourceSiteReplayJSON(t, credentials), func(raw json.RawMessage) {
					var input sdk.SiteSourceReferenceRequestV1
					if json.Unmarshal(raw, &input) != nil || input.Reference.SiteID != id || input.Reference.Snapshot != "" {
						t.Fatal("private source site credentials did not bind the indexer identity")
					}
				}},
			}
			for i, call := range sample.Calls {
				result := sdk.SiteHTTPResultV1{SchemaVersion: 1, RequestState: "completed"}
				if response := sample.Responses[i]; response != nil {
					result.Response = &sdk.SiteHTTPResponseV1{
						StatusCode: response.Status, Content: response.Content,
						ContentType: response.Headers["Content-Type"],
					}
				}
				steps = append(steps, iyuuSDKStep{sdk.SitesSourceRequestCapability, sourceSiteReplayJSON(t, result), func(raw json.RawMessage) {
					var input sdk.SiteSourceHTTPRequestV1
					if json.Unmarshal(raw, &input) != nil || input.Reference != ref || input.UserAgent != "source" {
						t.Fatal("resolver request did not preserve the bound site and its user agent")
					}
					expected, err := url.Parse(call.URL)
					if err != nil {
						t.Fatal(err)
					}
					if call.Op == "post" {
						query := input.Request.Query
						if input.OriginProfile != "iyuu-mteam" || input.CookieMode != "none" || !input.ExplicitHeaders ||
							input.Request.Method != "POST" || input.Request.Path != expected.Path || len(query) != 1 ||
							query[0].Name != "id" || query[0].Value != expected.Query().Get("id") {
							t.Fatal("M-Team request did not preserve the pinned token route")
						}
					} else if input.CookieMode != "parsed" || input.Request.Method != "GET" ||
						input.Request.Path != expected.RequestURI() {
						t.Fatal("resolver page request did not preserve the original relative request target")
					}
				}})
			}
			resolver := newSDKSiteResolver(sdk.Sites{Host: orderedIYUUHost(t, steps...)})
			site, err := resolver.Indexer(t.Context(), domain)
			if err != nil || site == nil {
				t.Fatalf("source indexer lookup failed: %v", err)
			}
			seed, err := decodeJSON(sample.Seed)
			if err != nil {
				t.Fatal(err)
			}
			want := ""
			if sample.Result.Value != nil {
				want = *sample.Result.Value
			}
			got, err := resolver.DownloadURL(t.Context(), seed.(map[string]any), *site, sample.Template)
			if err != nil || got != want {
				t.Fatalf("resolver URL = %q, %v; source URL = %q", got, err, want)
			}
		})
	}
}

func sourceSiteReplayJSON(t *testing.T, value any) string {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}
