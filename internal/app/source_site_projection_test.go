package app

import (
	"encoding/json"
	"testing"
)

func TestZZCatalogOnlyIndexerExposesBaseURL(t *testing.T) {
	fields := map[string]any{"id": "ptlover", "name": "AFUN", "domain": "https://www.ptlover.cc/", "schema": "NexusPhp", "public": false}
	out, err := originalSourceSiteProjection(fields, nil)
	if err != nil || out == nil {
		t.Fatalf("projection failed: %v", err)
	}
	var got map[string]any
	if err = json.Unmarshal(out.Fields, &got); err != nil {
		t.Fatal(err)
	}
	if got["url"] != "https://www.ptlover.cc/" {
		t.Fatalf("catalog base address missing from indexer fields: %s", out.Fields)
	}
}
