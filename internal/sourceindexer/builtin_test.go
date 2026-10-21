package sourceindexer

import (
	"encoding/json"
	"net/url"
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"
)

type reviewedCatalog struct {
	CatalogSHA256 string `json:"catalog_sha256"`
	Rows          []struct {
		SourceDBDomain string   `json:"source_db_domain"`
		Schema         string   `json:"schema"`
		Domain         string   `json:"domain"`
		ExtDomains     []string `json:"ext_domains"`
	} `json:"rows"`
}

func readReviewedCatalog(t *testing.T) reviewedCatalog {
	t.Helper()
	raw, err := os.ReadFile("../../testdata/plugins/site-indexer-context/original-selected-catalog.json")
	if err != nil {
		t.Fatal(err)
	}
	var catalog reviewedCatalog
	if err = json.Unmarshal(raw, &catalog); err != nil || len(catalog.Rows) != 17 {
		t.Fatal("reviewed catalog fixture invalid", err)
	}
	return catalog
}

func hostOf(t *testing.T, raw string) string {
	t.Helper()
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Hostname() == "" {
		t.Fatalf("invalid reviewed host %q: %v", raw, err)
	}
	return strings.ToLower(u.Hostname())
}

func TestBuiltinCatalogMatchesReviewedProjection(t *testing.T) {
	catalog := readReviewedCatalog(t)
	if catalog.CatalogSHA256 != "f6099dcd4f55f99646d53dbe4cec2c3f01ad4ec95929052a4e348a4ad90205c0" {
		t.Fatal("reviewed catalog digest changed; revalidate the baked projection")
	}
	entries := BuiltinCatalog()
	if len(entries) != len(catalog.Rows) {
		t.Fatalf("entry count %d != reviewed %d", len(entries), len(catalog.Rows))
	}
	// Every reviewed row's schema and host set must appear exactly once.
	seen := map[string]int{}
	for _, entry := range entries {
		for _, host := range entry.Hosts {
			seen[entry.Schema+" "+host]++
		}
	}
	for _, row := range catalog.Rows {
		hosts := []string{hostOf(t, row.Domain)}
		for _, ext := range row.ExtDomains {
			hosts = append(hosts, hostOf(t, ext))
		}
		for _, host := range hosts {
			if seen[row.Schema+" "+host] != 1 {
				t.Errorf("reviewed host %q/%q missing or duplicated in baked catalog", row.Schema, host)
			}
		}
	}
	reviewedHosts := map[string]bool{}
	for _, row := range catalog.Rows {
		reviewedHosts[hostOf(t, row.Domain)] = true
		for _, ext := range row.ExtDomains {
			reviewedHosts[hostOf(t, ext)] = true
		}
	}
	if len(seen) != len(reviewedHosts) {
		t.Errorf("baked catalog host count %d != reviewed %d", len(seen), len(reviewedHosts))
	}
}

func TestResolveReviewedInstalledDomains(t *testing.T) {
	catalog := readReviewedCatalog(t)
	for _, row := range catalog.Rows {
		t.Run(row.SourceDBDomain, func(t *testing.T) {
			schema, registered, ok := ResolveHost(row.SourceDBDomain)
			if !ok || schema != row.Schema || registered != Domain(row.SourceDBDomain) {
				t.Fatalf("installed domain resolved to %q/%q ok=%v want %q/%q", schema, registered, ok, row.Schema, Domain(row.SourceDBDomain))
			}
		})
	}
}

func TestResolveReviewedAliasHostsAndSubdomains(t *testing.T) {
	cases := map[string]struct{ schema, registered string }{
		"kp.m-team.cc":        {"MTorrent", "m-team.cc"},
		"xp.m-team.io":        {"MTorrent", "m-team.io"},
		"cspt.top":            {"NexusPhp", "cspt.top"},
		"www.cspt.top":        {"NexusPhp", "cspt.top"},
		"pt.xingyungept.org":  {"NexusPhp", "xingyungept.org"},
		"hhan.club":           {"NexusHhanclub", "hhan.club"},
		"www.hhan.club":       {"NexusHhanclub", "hhan.club"},
		"www.pttime.top":      {"NexusPhp", "pttime.top"},
		"mua.xloli.cc":        {"NexusPhp", "xloli.cc"},
		"pt.btschool.club":    {"NexusPhp", "btschool.club"},
		"www.musopia.vip":     {"NexusPhp", "musopia.vip"},
		"rousi.pro":           {"RousiPro", "rousi.pro"},
		"hdhome.org":          {"NexusPhp", "hdhome.org"},
		"unknown-tracker.xyz": {"", ""},
	}
	for host, want := range cases {
		schema, registered, ok := ResolveHost(host)
		if want.schema == "" {
			if ok {
				t.Errorf("%s unexpectedly resolved to %q", host, schema)
			}
			continue
		}
		if !ok || schema != want.schema || registered != want.registered {
			t.Errorf("%s resolved to %q/%q ok=%v want %q/%q", host, schema, registered, ok, want.schema, want.registered)
		}
	}
}

func TestResolveURLReviewedOrigins(t *testing.T) {
	for raw, want := range map[string]string{
		"https://kp.m-team.cc/":          "MTorrent",
		"https://xp.m-team.cc/":          "MTorrent",
		"https://www.pttime.top/":        "NexusPhp",
		"https://hhan.club/":             "NexusHhanclub",
		"http://cspt.top/":               "NexusPhp",
		"https://52pt.site/torrents.php": "NexusPhp",
		"https://example.org/":           "",
		"ftp://52pt.site/":               "",
		"not a url":                      "",
	} {
		schema, _, ok := ResolveURL(raw)
		if want == "" {
			if ok {
				t.Errorf("%q unexpectedly resolved to %q", raw, schema)
			}
			continue
		}
		if !ok || schema != want {
			t.Errorf("%q resolved to %q ok=%v want %q", raw, schema, ok, want)
		}
	}
}

func TestBuiltinCatalogHasUniqueRegisteredKeys(t *testing.T) {
	keys := map[string]string{}
	for _, entry := range BuiltinCatalog() {
		for _, host := range entry.Hosts {
			key := Domain(host)
			if prior, exists := keys[key]; exists && prior != entry.Schema {
				t.Errorf("registered key %q belongs to both %q and %q", key, prior, entry.Schema)
			}
			keys[key] = entry.Schema
		}
	}
	names := make([]string, 0, len(keys))
	for key := range keys {
		names = append(names, key)
	}
	sort.Strings(names)
	if !reflect.DeepEqual(names, []string{"1ptba.com", "52pt.site", "btschool.club", "carpt.net", "cspt.cc", "cspt.top", "hdfans.org", "hdhome.org", "hhan.club", "hhanclub.net", "m-team.cc", "m-team.io", "musopia.vip", "pandapt.net", "pttime.org", "pttime.top", "raingfh.top", "rousi.pro", "ubits.club", "xingyungept.cn", "xingyungept.org", "xloli.cc"}) {
		t.Errorf("registered keys = %v", names)
	}
}
