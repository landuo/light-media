package iyuuautoseed

import (
	"bytes"
	"encoding/json"
	"net/url"
	"strings"
	"testing"

	sdk "light-media/builtinplugins/internal/sdk"
)

func TestSDKSiteDirectoryURLReplay(t *testing.T) {
	for _, row := range []struct{ name, raw, directory string }{
		{"root-without-slash", "https://owned.invalid", "https://owned.invalid/"},
		{"root-with-slash", "https://owned.invalid/", "https://owned.invalid/"},
		{"subpath-without-slash", "https://owned.invalid/tracker", "https://owned.invalid/tracker/"},
		{"subpath-with-slash", "https://owned.invalid/tracker/", "https://owned.invalid/tracker/"},
		{"escaped-path", "https://owned.invalid/track%2fer/a%20b", "https://owned.invalid/track%2fer/a%20b/"},
		{"escaped-path-with-slash", "https://owned.invalid/track%2fer/", "https://owned.invalid/track%2fer/"},
		{"escaped-final-slash", "https://owned.invalid/tracker%2F", "https://owned.invalid/tracker%2F/"},
		{"http-port", "http://owned.invalid:8080/tracker", "http://owned.invalid:8080/tracker/"},
	} {
		t.Run(row.name, func(t *testing.T) {
			id := "opaque-site-id"
			ref := sdk.SiteSourceReferenceV1{SiteID: id, Snapshot: strings.Repeat("a", 64)}
			public := json.RawMessage(sourceSiteReplayJSON(t, map[string]any{"id": 7, "url": row.raw, "name": "owned"}))
			private := json.RawMessage(sourceSiteReplayJSON(t, map[string]any{"id": 7, "url": row.raw, "name": "owned", "cookie": "synthetic-cookie"}))
			originalPublic, originalPrivate := bytes.Clone(public), bytes.Clone(private)
			expectedPath, err := url.Parse(row.directory + "details.php?id=7&hit=1")
			if err != nil {
				t.Fatal(err)
			}
			host := orderedIYUUHost(t,
				iyuuSDKStep{sdk.SitesSourceIndexerCapability, sourceSiteReplayJSON(t, sdk.SiteSourceIndexerResultV1{
					SchemaVersion: 1, Indexer: &sdk.SiteSourceIndexerV1{SiteID: &id, Fields: public},
				}), nil},
				iyuuSDKStep{sdk.SitesSourceCredentialsCapability, sourceSiteReplayJSON(t, sdk.SiteSourceCredentialsV1{
					SchemaVersion: 1, Reference: ref, Fields: private,
				}), func(raw json.RawMessage) {
					var request sdk.SiteSourceReferenceRequestV1
					if json.Unmarshal(raw, &request) != nil || request.Reference != (sdk.SiteSourceReferenceV1{SiteID: id}) {
						t.Fatal("credentials lookup changed the opaque indexer identity")
					}
				}},
				iyuuSDKStep{sdk.SitesSourceRequestCapability, sourceSiteReplayJSON(t, sdk.SiteHTTPResultV1{
					SchemaVersion: 1, RequestState: "completed", Response: &sdk.SiteHTTPResponseV1{
						StatusCode: 200, ContentType: "text/html; charset=utf-8",
						Content: []byte(`<html><a href="download.php?id=7">download</a></html>`),
					},
				}), func(raw json.RawMessage) {
					var request sdk.SiteSourceHTTPRequestV1
					if json.Unmarshal(raw, &request) != nil || request.Reference != ref || request.Request.Method != "GET" ||
						request.Request.Path != expectedPath.RequestURI() || request.UserAgent != "source" || request.CookieMode != "parsed" {
						t.Fatal("details request changed the bound reference, escaped path or credential policy")
					}
				}},
			)
			resolver := newSDKSiteResolver(sdk.Sites{Host: host})
			site, err := resolver.Indexer(t.Context(), "owned.invalid")
			if err != nil || site == nil || site.Fields["url"] != row.directory {
				t.Fatalf("directory projection failed: %v", err)
			}
			bound, err := resolver.Reference(*site)
			if err != nil || bound != ref {
				t.Fatal("directory projection changed the sealed reference")
			}
			if !bytes.Equal(public, originalPublic) || !bytes.Equal(private, originalPrivate) {
				t.Fatal("directory projection mutated host raw fields")
			}
			seed := map[string]any{"torrent_id": json.Number("7")}
			// Exercise the real resolver's direct template concatenation, then
			// its special-template details fallback through the bound SDK request.
			for _, template := range []string{"download.php?id={id}", "download.php?hash={id}"} {
				got, err := resolver.DownloadURL(t.Context(), seed, *site, template)
				if err != nil || got != row.directory+"download.php?id=7" {
					t.Fatalf("resolver route projection failed: %v", err)
				}
				owner, _ := url.Parse(row.raw)
				target, err := url.Parse(got)
				if err != nil || target.Scheme != owner.Scheme || target.Host != owner.Host || target.User != nil {
					t.Fatal("resolver changed the configured origin")
				}
			}
		})
	}
}

func TestSDKSiteDirectoryURLRejectsInvalidBaseReplay(t *testing.T) {
	for _, row := range []struct {
		name string
		base any
	}{
		{"missing", nil}, {"non-string", 7}, {"empty", ""},
		{"relative", "/tracker"}, {"scheme-relative", "//owned.invalid"},
		{"missing-host", "https:///tracker"}, {"missing-hostname", "https://:443/tracker"},
		{"unsupported-scheme", "ftp://owned.invalid"},
		{"userinfo", "https://synthetic-user:synthetic-password@owned.invalid"},
		{"query", "https://owned.invalid/tracker?key=synthetic"},
		{"empty-query", "https://owned.invalid/tracker?"},
		{"fragment", "https://owned.invalid/tracker#part"},
		{"empty-fragment", "https://owned.invalid/tracker#"},
		{"opaque", "https:owned.invalid"}, {"invalid-escape", "https://owned.invalid/%xx"},
	} {
		t.Run(row.name, func(t *testing.T) {
			id := "opaque-site-id"
			fields := json.RawMessage(sourceSiteReplayJSON(t, map[string]any{"id": 7, "url": row.base}))
			resolver := newSDKSiteResolver(sdk.Sites{Host: orderedIYUUHost(t,
				iyuuSDKStep{sdk.SitesSourceIndexerCapability, sourceSiteReplayJSON(t, sdk.SiteSourceIndexerResultV1{
					SchemaVersion: 1, Indexer: &sdk.SiteSourceIndexerV1{SiteID: &id, Fields: fields},
				}), nil},
				iyuuSDKStep{sdk.SitesSourceCredentialsCapability, sourceSiteReplayJSON(t, sdk.SiteSourceCredentialsV1{
					SchemaVersion: 1, Reference: sdk.SiteSourceReferenceV1{SiteID: id, Snapshot: strings.Repeat("b", 64)}, Fields: fields,
				}), nil},
			)})
			site, err := resolver.Indexer(t.Context(), "owned.invalid")
			if site != nil || err == nil || err.Error() != "invalid source site base" {
				t.Fatal("invalid base accepted or error exposed base details")
			}
		})
	}
}

func TestSDKSiteDirectoryURLValidatesRawIdentityBeforeProjection(t *testing.T) {
	for _, row := range []struct{ name, public, private string }{
		{"different-raw-url", `{"id":7,"url":"https://owned.invalid"}`, `{"id":7,"url":"https://owned.invalid/"}`},
		{"different-id", `{"id":7,"url":"https://owned.invalid"}`, `{"id":8,"url":"https://owned.invalid"}`},
	} {
		t.Run(row.name, func(t *testing.T) {
			id := "opaque-site-id"
			resolver := newSDKSiteResolver(sdk.Sites{Host: orderedIYUUHost(t,
				iyuuSDKStep{sdk.SitesSourceIndexerCapability, sourceSiteReplayJSON(t, sdk.SiteSourceIndexerResultV1{
					SchemaVersion: 1, Indexer: &sdk.SiteSourceIndexerV1{SiteID: &id, Fields: json.RawMessage(row.public)},
				}), nil},
				iyuuSDKStep{sdk.SitesSourceCredentialsCapability, sourceSiteReplayJSON(t, sdk.SiteSourceCredentialsV1{
					SchemaVersion: 1, Reference: sdk.SiteSourceReferenceV1{SiteID: id, Snapshot: strings.Repeat("c", 64)}, Fields: json.RawMessage(row.private),
				}), nil},
			)})
			if site, err := resolver.Indexer(t.Context(), "owned.invalid"); site != nil || err == nil || err.Error() != "source site changed during indexer lookup" {
				t.Fatal("normalization bypassed raw public/private identity validation")
			}
		})
	}
	// Keep the existing origin guard strict; fix the projection rather than
	// allowing a malformed concatenation to enter the host transport.
	if _, err := sourceSitePagePath("https://owned.invalid", "details.php?id=7"); err == nil {
		t.Fatal("origin-change guard was relaxed")
	}
}

func TestSDKSiteDirectoryURLCatalogReplay(t *testing.T) {
	fields := json.RawMessage(`{"id":7,"url":"https://owned.invalid/tracker"}`)
	resolver := newSDKSiteResolver(sdk.Sites{Host: orderedIYUUHost(t,
		iyuuSDKStep{sdk.SitesSourceIndexerCapability, sourceSiteReplayJSON(t, sdk.SiteSourceIndexerResultV1{
			SchemaVersion: 1, Indexer: &sdk.SiteSourceIndexerV1{Fields: fields},
		}), nil},
	)})
	site, err := resolver.Indexer(t.Context(), "owned.invalid")
	if err != nil || site == nil || site.Fields["url"] != "https://owned.invalid/tracker/" || site.Ref != "" {
		t.Fatal("catalog projection changed routing or created a configured reference")
	}
	if _, err := resolver.Reference(*site); err == nil {
		t.Fatal("catalog projection gained configured transport authority")
	}
}
