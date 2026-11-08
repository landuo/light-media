package plugins

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestRenderSiteRichValueScopeBudget(t *testing.T) {
	ordinal := 0
	ref := &SiteSourceReferenceV1{CustomOrdinal: &ordinal, Snapshot: "owned-opaque"}
	for _, r := range []struct {
		name    string
		ref     *SiteSourceReferenceV1
		value   []byte
		wantErr bool
	}{{"ordinary-id-cannot-receive-source-value", nil, []byte(`true`), true}, {"source-raw-nonfinite-leaf", ref, []byte(`NaN`), false}, {"source-null-distinct-from-absent", ref, []byte(`null`), false}, {"source-over-budget", ref, []byte(strings.Repeat("x", MaxPayloadBytes+1)), true}} {
		t.Run(r.name, func(t *testing.T) {
			host := func(ctx context.Context, method string, raw json.RawMessage) (json.RawMessage, error) {
				if method != SiteRenderCapability {
					t.Fatal(method)
				}
				return json.Marshal(SiteRenderResult{SourceValueJSON: r.value})
			}
			got, e := RenderSite(context.Background(), host, SiteRenderRequest{ID: "owned", SourceReference: r.ref})
			if (e != nil) != r.wantErr {
				t.Fatal("source-only bounded DTO invariant", e)
			}
			if !r.wantErr && string(got.SourceValueJSON) != string(r.value) {
				t.Fatal("source leaf lost original bytes")
			}
		})
	}
}
