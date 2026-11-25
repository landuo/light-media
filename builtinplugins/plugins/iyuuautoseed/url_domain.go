package iyuuautoseed

import "strings"

// SourceDomain implements the pinned host's StringUtils.get_url_domain. Only a
// case-sensitive "http" prefix triggers URL parsing. Ports remain in the
// netloc, and domains with more than three labels are retained.
func SourceDomain(raw string) (string, error) {
	for _, special := range []string{"u2.dmhy.org", "pt.ecust.pp.ua", "pt.gtkpw.xyz", "pt.gtk.pw"} {
		if strings.Contains(raw, special) {
			return special, nil
		}
	}
	host := raw
	if strings.HasPrefix(raw, "http") {
		parsed, err := sourceParseURL(raw, "")
		if err != nil {
			return "", err
		}
		host = parsed.netloc
	}
	labels := strings.Split(host, ".")
	if len(labels) == 3 {
		return strings.Join(labels[1:], "."), nil
	}
	return host, nil
}
