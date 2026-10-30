package artifacts

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestPythonDisplayGenericNativeEnvelopeRemainsStrict(t *testing.T) {
	for _, sample := range []struct {
		name, body string
		wantError  bool
	}{
		{"valid_empty_native", `{"schema_version":1,"plugins":[]}`, false},
		{"malformed_native_with_shadow", `{"schema_version":1,"plugins":[],"OwnedShadow":{"name":"shadow","version":"1"}}`, true},
	} {
		t.Run(sample.name, func(t *testing.T) {
			req, _, _, _ := fixture(t, []byte("owned inert guard fixture"), "executable")
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(sample.body)) }))
			defer peer.Close()
			rows, err := DiscoverRepository(context.Background(), peer.URL, req.Platform, req.Validate, peer.Client())
			if (err != nil) != sample.wantError || len(rows) != 0 {
				t.Fatalf("native parse failure changed to Python cards: rows=%v error=%v", rows, err)
			}
		})
	}
}

func TestPythonDisplayExplicitPackageStillAllowsReservedRootNames(t *testing.T) {
	body := []byte(`{"schema_version":{"name":"schema card","version":"1","v2":true},"plugins":{"name":"plugins card","version":"2","v2":true}}`)
	for _, requireV2 := range []bool{false, true} {
		rows, err := discoverPythonMetadataForVersion(body, "https://github.com/Owned/Package", requireV2)
		if err != nil || len(rows) != 2 || rows[0].PluginID != "schema_version" || rows[1].PluginID != "plugins" {
			t.Fatalf("source package dictionary keys incorrectly reserved: rows=%v error=%v", rows, err)
		}
	}
	if _, err := discoverPythonMetadata(body, "http://127.0.0.1/owned-index"); err == nil {
		t.Fatal("generic native fallback accepted reserved keys")
	}
}
