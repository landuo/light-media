package iyuuautoseed

import (
	"light-media/internal/plugins/cleaninvalidseed"
	"strings"
)

// SourceDomain is the exact StringUtils last-two-label policy, including its
// four special substring domains, case-sensitive http gate, raw ports and the
// more-than-three-label exception. URLNetloc reuses the already source-verified
// CPython3.12 URL parsing closure rather than net/url's stricter parser.
func SourceDomain(raw string) (string, error) {
	if raw == "" {
		return "", nil
	}
	for _, s := range []string{"u2.dmhy.org", "pt.ecust.pp.ua", "pt.gtkpw.xyz", "pt.gtk.pw"} {
		if strings.Contains(raw, s) {
			return s, nil
		}
	}
	parts, e := cleaninvalidseed.URLNetloc(raw)
	if e != nil {
		return "", e
	}
	host := parts[1]
	if host == "" {
		return "", nil
	}
	labels := strings.Split(host, ".")
	if len(labels) > 3 {
		return host, nil
	}
	if len(labels) > 2 {
		labels = labels[len(labels)-2:]
	}
	return strings.Join(labels, "."), nil
}
