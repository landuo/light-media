package iyuuautoseed

import (
	"context"
	"encoding/json"
	"testing"

	sdk "light-media/builtinplugins/internal/sdk"
)

func TestCatalogOnlySiteUsesDomainAsBaseURL(t *testing.T) {
	host := sdk.HostCaller(func(ctx context.Context, method string, raw json.RawMessage) (json.RawMessage, error) {
		if method != sdk.SitesSourceIndexerCapability {
			return nil, errUnexpectedSourceLogCall
		}
		return json.RawMessage(`{"schema_version":1,"indexer":{"site_id":null,"fields":{"id":"ptlover","name":"AFUN","domain":"https://www.ptlover.cc/","schema":"NexusPhp","public":false}}}`), nil
	})
	resolver := &sdkSiteResolver{sites: sdk.Sites{Host: host}}
	site, err := resolver.Indexer(context.Background(), "ptlover.cc")
	if err != nil {
		t.Fatal(err)
	}
	if site == nil || site.Fields["url"] != "https://www.ptlover.cc/" {
		t.Fatalf("catalog-only site lost its base address: %#v", site)
	}
	if site.Fields["id"] != "ptlover" {
		t.Fatalf("catalog identity replaced: %#v", site.Fields)
	}
}
