package app

import (
	"context"
	"encoding/json"
	"light-media/internal/plugins"
	"light-media/internal/sites"
	"os"
	"testing"
)

// Expectations come from the executed fixed helper/catalog projection, not
// from the generated target constant. No credentialed request is performed.
func TestSourceSiteMetadataActual193OriginalCatalog(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/plugin-source-site-metadata-20261106/source/projection.json")
	if err != nil {
		t.Fatal(err)
	}
	var source struct {
		Success bool             `json:"success"`
		Rows    []map[string]any `json:"rows"`
	}
	if json.Unmarshal(raw, &source) != nil || !source.Success || len(source.Rows) != 193 {
		t.Fatal("fixed original capture required")
	}
	a := newTestApp(t)
	public := 0
	for _, row := range source.Rows {
		domain := row["source_db_domain"].(string)
		t.Run(domain, func(t *testing.T) {
			result, _, e := a.originalSourceSiteIndexers(context.Background(), &domain)
			if e != nil || result == nil || result.SiteID != nil {
				t.Fatal("catalog-only source metadata gained or lost identity", e)
			}
			var fields map[string]any
			if json.Unmarshal(result.Fields, &fields) != nil {
				t.Fatal("metadata wire")
			}
			for _, key := range []string{"id", "name", "domain", "schema", "public"} {
				actual, _ := json.Marshal(fields[key])
				want, _ := json.Marshal(row[key])
				if string(actual) != string(want) {
					t.Fatalf("source field %s: %s want %s", key, actual, want)
				}
			}
			if fields["public"] == true {
				public++
			}
			for _, key := range []string{"cookie", "apikey", "token", "ua", "category", "search"} {
				if _, ok := fields[key]; ok {
					t.Fatalf("private/unconsumed field %s escaped", key)
				}
			}
		})
	}
	if public != 7 {
		t.Fatal("source public selection changed", public)
	}
}

func TestSourceSiteMetadataActualSDKCurrentBuiltinOptions(t *testing.T) {
	a := newTestApp(t)
	cookie := setupTestAdmin(t, a)
	worker := sourceSitesFixture(t, a, cookie)
	cfg := a.Settings()
	cfg.Sites = []SiteConfig{
		{Config: sites.Config{ID: "source-public", Name: "OwnedPublic", Kind: "ubits", BaseURL: "http://127.0.0.1:43221", Cookie: "OWNED_METADATA_SECRET", UserAgent: "OwnedMetadataAgent", Timeout: 7}, SiteOptions: SiteOptions{Domain: "mikanani.me", Proxy: true, Render: true, Public: false}, Enabled: true},
		{Config: sites.Config{ID: "source-disabled", Name: "OwnedDisabled", Kind: "ubits", BaseURL: "http://127.0.0.1:43222"}, SiteOptions: SiteOptions{Domain: "1ptba.com", Public: true}, Enabled: false},
	}
	ownedDownloaderSave(t, a, cfg)
	var result plugins.SiteSourceIndexersResultV1
	if err := hostDownloaderCall(t, worker, "source_indexers", struct{}{}, &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Items) != 1 || result.Items[0].SiteID == nil || *result.Items[0].SiteID != "source-public" {
		t.Fatal("source active selection/canonical identity", result)
	}
	check := func(row plugins.SiteSourceIndexerV1, timeout int, wantPublic, proxy, render bool) {
		t.Helper()
		var f map[string]any
		if json.Unmarshal(row.Fields, &f) != nil {
			t.Fatal("metadata")
		}
		if f["public"] != wantPublic || f["proxy"] != proxy || f["render"] != render || f["timeout"] != float64(timeout) {
			t.Fatal("source template public + DB options mismatch", string(row.Fields))
		}
		for _, k := range []string{"cookie", "apikey", "token", "ua"} {
			if _, ok := f[k]; ok {
				t.Fatal("private field escaped", k)
			}
		}
	}
	check(result.Items[0], 7, true, true, true)
	cfg = a.Settings()
	cfg.Sites[0].Timeout = 9
	ownedDownloaderSave(t, a, cfg)
	var getter plugins.SiteSourceIndexerResultV1
	if err := hostDownloaderCall(t, worker, "source_indexer", plugins.SiteSourceIndexerRequestV1{Domain: "mikanani.me"}, &getter); err != nil || getter.Indexer == nil {
		t.Fatal("source getter after settings update", err)
	}
	check(*getter.Indexer, 9, true, true, true)
	if err := hostDownloaderCall(t, worker, "source_indexer", plugins.SiteSourceIndexerRequestV1{Domain: "1ptba.com"}, &getter); err != nil || getter.Indexer == nil || getter.Indexer.SiteID == nil || *getter.Indexer.SiteID != "source-disabled" {
		t.Fatal("disabled single getter", err)
	}
	check(*getter.Indexer, 0, false, false, false)
}
