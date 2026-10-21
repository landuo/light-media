package sourceindexer

import (
	"net/url"
	"strings"
)

// CatalogEntry is one reviewed indexer template projection that the installed
// MoviePilot sites actually reference. Hosts contains the template's own
// domain plus every reviewed ext_domain; the registry key stays the registered
// domain (Domain), exactly like the original create_indexer put/get calls.
type CatalogEntry struct {
	Schema string
	Hosts  []string
}

// BuiltinCatalog is the frozen 17-row projection from the installed original
// catalog (testdata/plugins/site-indexer-context/original-selected-catalog.json,
// catalog_sha256 f6099dcd4f55f99646d53dbe4cec2c3f01ad4ec95929052a4e348a4ad90205c0).
// It is installed-site evidence, not a claim that the complete upstream
// template universe is ported: unknown domains keep the generic NexusPhp path
// that the caller already had.
func BuiltinCatalog() []CatalogEntry {
	return []CatalogEntry{
		{Schema: "MTorrent", Hosts: []string{"xp.m-team.io", "xp.m-team.cc", "kp.m-team.cc"}},
		{Schema: "RousiPro", Hosts: []string{"rousi.pro"}},
		{Schema: "NexusPhp", Hosts: []string{"52pt.site"}},
		{Schema: "NexusPhp", Hosts: []string{"cspt.cc", "cspt.top"}},
		{Schema: "NexusPhp", Hosts: []string{"pandapt.net"}},
		{Schema: "NexusPhp", Hosts: []string{"raingfh.top"}},
		{Schema: "NexusPhp", Hosts: []string{"pt.btschool.club"}},
		{Schema: "NexusPhp", Hosts: []string{"1ptba.com"}},
		{Schema: "NexusPhp", Hosts: []string{"pt.xingyungept.cn", "pt.xingyungept.org"}},
		{Schema: "NexusHhanclub", Hosts: []string{"hhanclub.net", "hhan.club"}},
		{Schema: "NexusPhp", Hosts: []string{"mua.xloli.cc"}},
		{Schema: "NexusPhp", Hosts: []string{"www.pttime.org", "www.pttime.top"}},
		{Schema: "NexusPhp", Hosts: []string{"hdfans.org"}},
		{Schema: "NexusPhp", Hosts: []string{"ubits.club"}},
		{Schema: "NexusPhp", Hosts: []string{"carpt.net"}},
		{Schema: "NexusPhp", Hosts: []string{"www.musopia.vip"}},
		{Schema: "NexusPhp", Hosts: []string{"hdhome.org"}},
	}
}

type builtinHosts struct {
	exact      map[string]CatalogEntry
	registered map[string]CatalogEntry
}

func builtinIndex() builtinHosts {
	index := builtinHosts{exact: map[string]CatalogEntry{}, registered: map[string]CatalogEntry{}}
	for _, entry := range BuiltinCatalog() {
		for _, host := range entry.Hosts {
			host = normalizeHost(host)
			if host == "" {
				continue
			}
			index.exact[host] = entry
			if key := Domain(host); key != "" {
				if _, exists := index.registered[key]; !exists {
					index.registered[key] = entry
				}
			}
		}
	}
	return index
}

var builtin = builtinIndex()

func normalizeHost(host string) string {
	host = strings.ToLower(strings.TrimSpace(host))
	return strings.TrimSuffix(host, ".")
}

// ResolveHost resolves a configured site host (an exact reviewed template host
// or a subdomain of one) to its reviewed schema and registered domain. The
// registered domain is the identity the original SitesHelper used as its
// catalog key; it is also what site statistics stores as domain.
func ResolveHost(host string) (schema, registered string, ok bool) {
	host = normalizeHost(host)
	if host == "" {
		return "", "", false
	}
	key := Domain(host)
	if key == "" {
		return "", "", false
	}
	if entry, found := builtin.exact[host]; found {
		return entry.Schema, key, true
	}
	if entry, found := builtin.registered[key]; found {
		return entry.Schema, key, true
	}
	return "", "", false
}

// ResolveURL applies ResolveHost to an absolute site URL. Parsing mirrors the
// original urlparse-based projection: only http/https with a host participate;
// userinfo, ports, paths and query strings are irrelevant to template choice.
func ResolveURL(raw string) (schema, registered string, ok bool) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return "", "", false
	}
	return ResolveHost(u.Hostname())
}
