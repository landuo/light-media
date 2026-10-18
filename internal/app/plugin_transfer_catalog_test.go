package app

import (
	"light-media/internal/plugins/builtin"
	"reflect"
	"testing"
)

// c9c6ec2's complete pre-existing market order must survive the focused merge.
// In particular, Lookup alone did not catch a fuzzy hunk dropping SeedRescue
// from Catalog while preserving its worker and API implementation.
func TestTransferCatalogPreservesCurrentSixteenPlugins(t *testing.T) { assertNativeCatalogBaseline(t) }

func assertNativeCatalogBaseline(t *testing.T) {
	t.Helper()
	want := []string{"TorrentTransfer", "TorrentTransfermusic", "SeedRescue", "CloudflareSpeedTest", "IYUUAutoSeed", "AutoBackup", "CategoryEditor", "CustomHosts", "SystemProcess", "MediaServerRefresh", "RemoteIdentifiers", "MergeSiteSwitch", "SiteStatistic", "SubscribeGroup", "PluginUnInstall", "MPServerStatus", "RemoveLink", "CleanInvalidSeed"}
	baseline := map[string]bool{}
	for _, id := range want {
		baseline[id] = true
	}
	seen := map[string]bool{}
	got := []string{}
	for _, m := range builtin.Catalog() {
		if seen[m.PluginID] {
			t.Fatal("duplicate market entry", m.PluginID)
		}
		seen[m.PluginID] = true
		// Preserve the complete baseline's relative order while allowing the
		// remaining requested plugins to be implemented in later changes.
		if baseline[m.PluginID] {
			got = append(got, m.PluginID)
		}
		lookup, _, ok := builtin.Lookup(m.PluginID)
		if !ok || !reflect.DeepEqual(m, lookup) {
			t.Fatal("market and worker identity disagree", m.PluginID)
		}
	}
	if !reflect.DeepEqual(want, got) {
		t.Fatal("focused merge changed prior market entries", got)
	}
}
