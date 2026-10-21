package app

import (
	"testing"

	"light-media/internal/sites"
)

func TestSiteImportOriginReviewedIndexerCatalogAliases(t *testing.T) {
	cases := []struct {
		raw, kind, origin, domain string
	}{
		// Reviewed MTorrent template and ext domains.
		{"https://kp.m-team.cc/", "mteam", "https://api.m-team.cc", "m-team.cc"},
		{"https://xp.m-team.cc/", "mteam", "https://api.m-team.cc", "m-team.cc"},
		{"https://xp.m-team.io/", "mteam", "https://api.m-team.io", "m-team.io"},
		// RousiPro template.
		{"https://rousi.pro/", "rousi", "https://rousi.pro", "rousi.pro"},
		// NexusHhanclub template with the hhan.club ext domain.
		{"https://hhanclub.net/", "hhanclub", "https://hhanclub.net", "hhanclub.net"},
		{"https://hhan.club/", "hhanclub", "https://hhan.club", "hhan.club"},
		// Reviewed NexusPhp rows and ext domains keep the generic adapter but
		// resolve to the registered domain the original database stored.
		{"https://52pt.site/", "nexusphp", "https://52pt.site", "52pt.site"},
		{"https://cspt.cc/", "nexusphp", "https://cspt.cc", "cspt.cc"},
		{"https://cspt.top/", "nexusphp", "https://cspt.top", "cspt.top"},
		{"https://pandapt.net/", "nexusphp", "https://pandapt.net", "pandapt.net"},
		{"https://raingfh.top/", "nexusphp", "https://raingfh.top", "raingfh.top"},
		{"https://pt.btschool.club/", "nexusphp", "https://pt.btschool.club", "btschool.club"},
		{"https://pt.xingyungept.cn/", "nexusphp", "https://pt.xingyungept.cn", "xingyungept.cn"},
		{"https://pt.xingyungept.org/", "nexusphp", "https://pt.xingyungept.org", "xingyungept.org"},
		{"https://mua.xloli.cc/", "nexusphp", "https://mua.xloli.cc", "xloli.cc"},
		{"https://www.pttime.org/", "nexusphp", "https://www.pttime.org", "pttime.org"},
		{"https://www.pttime.top/", "nexusphp", "https://www.pttime.top", "pttime.top"},
		{"https://hdfans.org/", "nexusphp", "https://hdfans.org", "hdfans.org"},
		{"https://carpt.net/", "nexusphp", "https://carpt.net", "carpt.net"},
		{"https://www.musopia.vip/", "nexusphp", "https://www.musopia.vip", "musopia.vip"},
		// Existing native specializations stay attached to their reviewed rows.
		{"https://hdhome.org/", "jiayuan", "https://hdhome.org", "hdhome.org"},
		{"https://ubits.club/", "ubits", "https://ubits.club", "ubits.club"},
		{"https://1ptba.com/", "1ptba", "https://1ptba.com", "1ptba.com"},
		// Domains outside the reviewed 17-row projection keep the generic
		// NexusPHP fallback the application already had.
		{"https://tracker.example.org/", "nexusphp", "https://tracker.example.org", "example.org"},
	}
	for _, c := range cases {
		t.Run(c.raw, func(t *testing.T) {
			kind, origin, domain, err := siteImportOrigin(c.raw)
			if err != nil {
				t.Fatal(err)
			}
			if kind != c.kind || origin != c.origin || domain != c.domain {
				t.Fatalf("got %q %q %q want %q %q %q", kind, origin, domain, c.kind, c.origin, c.domain)
			}
		})
	}
}

func TestNormalizeManagedSiteUsesReviewedAliasSchema(t *testing.T) {
	site := SiteConfig{Config: sites.Config{ID: "alias", BaseURL: "https://www.hhan.club/", Cookie: "configuration-validation-only"}}
	if err := normalizeManagedSite(&site); err != nil {
		t.Fatal(err)
	}
	if site.Kind != "hhanclub" || site.BaseURL != "https://www.hhan.club" || site.Domain != "hhan.club" || site.Name != "憨憨" {
		t.Fatalf("alias schema not applied: %#v", site)
	}
	mteam := SiteConfig{Config: sites.Config{ID: "alias-mteam", BaseURL: "https://kp.m-team.cc/", APIKey: "configuration-validation-only"}}
	if err := normalizeManagedSite(&mteam); err != nil {
		t.Fatal(err)
	}
	if mteam.Kind != "mteam" || mteam.BaseURL != "https://api.m-team.cc" || mteam.Domain != "m-team.cc" {
		t.Fatalf("mteam alias not normalized: %#v", mteam)
	}
}
