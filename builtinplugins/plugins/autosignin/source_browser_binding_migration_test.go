package autosignin

import (
	"context"
	"encoding/json"
	sdk "light-media/builtinplugins/internal/sdk"
	"os"
	"strings"
	"testing"
)

func TestSourcePageReferenceAndRawTimeoutDTO(t *testing.T) {
	data, err := os.ReadFile("testdata/source-page-render-oracle.json")
	if err != nil {
		t.Fatal(err)
	}
	var oracle struct {
		Rows []struct {
			Kind, Name, Mode, Caller string
			Timeout                  json.RawMessage
		}
	}
	if json.Unmarshal(data, &oracle) != nil {
		t.Fatal("source oracle")
	}
	count := 0
	for _, r := range oracle.Rows {
		if r.Kind != "browser" || r.Mode != "cloakbrowser" {
			continue
		}
		count++
		t.Run(r.Name, func(t *testing.T) {
			var timeout any
			if e := Decode(r.Timeout, &timeout); e != nil {
				t.Fatal(e)
			}
			ordinal := 3
			ref := sdk.SiteSourceReferenceV1{CustomOrdinal: &ordinal, Snapshot: strings.Repeat("a", 64)}
			site := Object{"id": 1234, "url": "https://www.hdsky.me/", "cookie": "owned-cookie-must-not-serialize", "ua": "owned-UA-must-not-serialize", "render": true, "timeout": timeout, "_host_source_reference": ref}
			called := 0
			b := sdkBackend{host: func(ctx context.Context, method string, raw json.RawMessage) (json.RawMessage, error) {
				called++
				if method != sdk.SiteRenderCapability {
					t.Fatal(method)
				}
				if strings.Contains(string(raw), "owned-cookie") || strings.Contains(string(raw), "owned-UA") {
					t.Fatal("private source field serialized", string(raw))
				}
				var in sdk.SiteRenderRequest
				if e := json.Unmarshal(raw, &in); e != nil {
					t.Fatal(e)
				}
				if in.ID != "" || in.SourceReference == nil || in.SourceReference.CustomOrdinal == nil || *in.SourceReference.CustomOrdinal != ordinal || in.SourceReference.Snapshot != ref.Snapshot || in.Path != "/attendance.php" || in.OriginProfile != "hdsky" || in.TimeoutSeconds != nil {
					t.Fatal("unbound source page request", string(raw))
				}
				expected := timeout
				if r.Caller == "site-base" && !Truth(timeout) {
					expected = 60
				}
				want, e := sourceHTTPJSONBytes(expected)
				if e != nil {
					t.Fatal(e)
				}
				if string(in.SourceTimeout) != string(want) {
					t.Fatal("original raw timeout lost", string(in.SourceTimeout), string(want))
				}
				return json.RawMessage(`{"html":"owned"}`), nil
			}}
			h := b.visitor(site)
			var got string
			if r.Caller == "site-base" {
				got, err = BasePageSource(context.Background(), site, "https://hdsky.me/attendance.php", h.Hooks)
			} else {
				got, err = (sdkSiteTransport{backend: b, site: site}).Page(context.Background(), PageRequest{URL: "https://hdsky.me/attendance.php", Timeout: timeout})
			}
			if err != nil || got != "owned" || called != 1 {
				t.Fatal(got, err, called)
			}
		})
	}
	if count != 22 {
		t.Fatal(count)
	}
}
