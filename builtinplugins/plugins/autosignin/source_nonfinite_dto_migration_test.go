package autosignin

import (
	"context"
	"encoding/json"
	sdk "light-media/builtinplugins/internal/sdk"
	"math"
	"strings"
	"testing"
)

// Actual source nonfinite6 determines the rendering semantics; this pure test
// verifies only that the original float reaches the existing host capability.
func TestSourcePageNonfiniteTimeoutDTO(t *testing.T) {
	for _, r := range []struct {
		name  string
		value any
		kind  string
	}{{"nan", math.NaN(), "nan"}, {"inf", math.Inf(1), "infinity"}, {"negative-inf", math.Inf(-1), "-infinity"}, {"json-overflow", json.Number("1e309"), "infinity"}, {"json-negative-overflow", json.Number("-1e309"), "-infinity"}} {
		t.Run(r.name, func(t *testing.T) {
			ordinal := 0
			ref := sdk.SiteSourceReferenceV1{CustomOrdinal: &ordinal, Snapshot: strings.Repeat("a", 64)}
			site := Object{"id": 7, "url": "https://hdsky.me/", "_host_source_reference": ref}
			calls := 0
			b := sdkBackend{host: func(ctx context.Context, method string, raw json.RawMessage) (json.RawMessage, error) {
				calls++
				var in sdk.SiteRenderRequest
				if method != sdk.SiteRenderCapability || json.Unmarshal(raw, &in) != nil {
					t.Fatal(method)
				}
				if in.SourceTimeoutFloat != r.kind || len(in.SourceTimeout) != 0 || in.SourceReference == nil || in.ID != "" || in.TimeoutSeconds != nil {
					t.Fatal("special source timeout lost/mixed", string(raw))
				}
				return json.RawMessage(`{"html":null}`), nil
			}}
			_, e := (sdkSiteTransport{backend: b, site: site}).Page(context.Background(), PageRequest{URL: "https://hdsky.me/attendance.php", Timeout: r.value})
			if e != nil || calls != 1 {
				t.Fatal(e, calls)
			}
		})
	}
}
