package artifacts

import (
	"context"
	"encoding/json"
	"light-media/internal/plugins"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

func TestMetadataLabelsActualFixedPythonOracleAndRepositoryHTTP(t *testing.T) {
	raw, err := os.ReadFile("../../../testdata/standard-market/labels-oracle.json")
	if err != nil {
		t.Fatal(err)
	}
	var oracle struct {
		Cases []struct {
			Input    json.RawMessage
			Expected *string
		}
	}
	if json.Unmarshal(raw, &oracle) != nil {
		t.Fatal("invalid fixed label oracle")
	}
	for _, row := range oracle.Cases {
		expected := ""
		if row.Expected != nil {
			expected = *row.Expected
		}
		got, err := normalizeMetadataLabel(row.Input)
		if err != nil || got != expected {
			t.Fatal("fixed source label normalization differs", string(row.Input), got, expected, err)
		}
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewEncoder(w).Encode(map[string]any{"OwnedLabelFixture": map[string]any{"name": "Owned label fixture", "version": "1.0", "v2": true, "labels": row.Input}})
		}))
		values, err := DiscoverRepository(context.Background(), server.URL, "linux-amd64", func(_ plugins.Manifest, _ string) error { return nil }, server.Client())
		server.Close()
		if err != nil || len(values) != 1 || values[0].Label != expected || values[0].UnavailableReason == "" {
			t.Fatal("actual display-only HTTP index lost normalized labels or granted Python runtime", values, err)
		}
	}
}
