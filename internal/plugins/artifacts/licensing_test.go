package artifacts

import (
	"context"
	"testing"
)

func TestMarketLevelAndKeyDoNotRewriteExecutableManifest(t *testing.T) {
	req, client, index, hits := fixture(t, []byte("inert-owned-bytes"), "executable")
	class, market := 3, 1
	index.Releases[0].Manifest.AuthLevel = &class
	index.Releases[0].Manifest.PluginPublicKey = "class-public-key"
	index.Level, index.PublicKey = &market, "market-public-key"
	discovery, err := Discover(context.Background(), req.RepositoryURL, req.Platform, req.Validate, client)
	if err != nil {
		t.Fatal(err)
	}
	if discovery.Current.Manifest.AuthLevel == nil || *discovery.Current.Manifest.AuthLevel != 1 || discovery.Current.Manifest.PluginPublicKey != "market-public-key" {
		t.Fatal("market source.level/key did not override its visible metadata")
	}
	if len(discovery.Releases) == 0 || discovery.Releases[0].Manifest.AuthLevel == nil || *discovery.Releases[0].Manifest.AuthLevel != 3 || index.Releases[0].Manifest.PluginPublicKey != "class-public-key" {
		t.Fatal("market metadata rewrote the executable declaration")
	}
	if *hits != 0 {
		t.Fatal("license metadata discovery downloaded executable bytes")
	}
	acquired, err := Acquire(context.Background(), req, client)
	if err != nil || acquired.Manifest.AuthLevel == nil || *acquired.Manifest.AuthLevel != 3 || acquired.Manifest.PluginPublicKey != "class-public-key" {
		t.Fatal("acquisition trusted market metadata as class identity", err)
	}
}

func TestPythonMarketLicenseMetadataIsDisplayOnly(t *testing.T) {
	items, err := discoverPythonMetadata([]byte(`{"Owned":{"name":"Owned","version":"1","level":99,"key":"source-public"}}`), "https://example.invalid/index")
	if err != nil || len(items) != 1 || items[0].Current.Manifest.AuthLevel == nil || *items[0].Current.Manifest.AuthLevel != 99 || items[0].Current.Manifest.PluginPublicKey != "source-public" || items[0].UnavailableReason == "" || len(items[0].Releases) != 0 {
		t.Fatal("Python display metadata lost level/key or gained executable authority", err)
	}
}
