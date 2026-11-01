package downloadercommands

// Fixed 2.11.3 threshold only, selected from packaging.version26.3 and the
// already-delivered private IYUU PEP440-final comparison. Packaging is dual
// Apache-2.0/BSD; provenance and notices accompany this candidate.
import (
	"light-media/internal/releasemeta/legacyanime"
	"math/big"
	"regexp"
	"strings"
)

var qbCookieVersionPattern = regexp.MustCompile(`^v?(?:(?P<epoch>[0-9]+)!)?(?P<release>[0-9]+(?:\.[0-9]+)*)(?P<pre>[-_.]?(?:alpha|a|beta|b|preview|pre|c|rc)[-_.]?[0-9]*)?(?P<post>(?:-[0-9]+)|(?:[-_.]?(?:post|rev|r)[-_.]?[0-9]*))?(?P<dev>[-_.]?dev[-_.]?[0-9]*)?(?:\+[a-z0-9]+(?:[-_.][a-z0-9]+)*)?$`)

// QBSupportsCookieAPI compares only to the fixed source final release2.11.3.
// This pure helper is shared with the two vendor endpoint guards; it is not a
// generic version API or a newly exposed host capability.
func QBSupportsCookieAPI(value any) (bool, error) {
	s, err := scalarString(value)
	if err != nil {
		switch value.(type) {
		case []any, map[string]any:
			return false, nil
		}
		return false, err
	}
	supported, err := QBRequireCookieAPI(s)
	if _, invalid := err.(*SourceException); invalid {
		return false, nil
	}
	return supported, err
}

// QBRequireCookieAPI is the pinned vendor's strict version guard. Unlike the
// source compatibility probe, InvalidVersion must reach its surrounding catch.
func QBRequireCookieAPI(s string) (bool, error) {
	s = legacyanime.Strip(s)
	// packaging 26.3 _VERSION_PATTERN uses (?a:...) for these tokens.
	// Do not inherit the old comparator's Unicode IGNORECASE aliases.
	s = strings.Map(func(r rune) rune {
		if r >= 'A' && r <= 'Z' {
			return r + 32
		}
		return r
	}, s)
	match := qbCookieVersionPattern.FindStringSubmatch(s)
	if match == nil {
		return false, &SourceException{Class: "InvalidVersion", Message: "Invalid qB Web API version"}
	}
	field := func(n string) string { return match[qbCookieVersionPattern.SubexpIndex(n)] }
	if epoch := field("epoch"); epoch != "" {
		n, _ := new(big.Int).SetString(epoch, 10)
		if n.Sign() > 0 {
			return true, nil
		}
	}
	release := strings.Split(field("release"), ".")
	threshold := []int64{2, 11, 3}
	n := len(release)
	if n < len(threshold) {
		n = len(threshold)
	}
	for i := 0; i < n; i++ {
		a, b := new(big.Int), new(big.Int)
		if i < len(release) {
			a.SetString(release[i], 10)
		}
		if i < len(threshold) {
			b.SetInt64(threshold[i])
		}
		if c := a.Cmp(b); c != 0 {
			return c > 0, nil
		}
	}
	if field("pre") != "" {
		return false, nil
	}
	if field("post") == "" && field("dev") != "" {
		return false, nil
	}
	return true, nil
}

func qbMergeSiteCookies(existing []any, sites []map[string]any) ([]map[string]any, error) {
	type key struct{ domain, path, name string }
	keys := map[key]int{}
	out := []map[string]any{}
	put := func(item map[string]any) error {
		if !truth(item["name"]) || !truth(item["domain"]) {
			return nil
		}
		name, nok := item["name"].(string)
		domain, dok := item["domain"].(string)
		path := "/"
		if truth(item["path"]) {
			var ok bool
			path, ok = item["path"].(string)
			if !ok {
				return &BoundaryError{"non-string stored Cookie path is outside the fixed JSON cookie domain"}
			}
		}
		if !nok || !dok {
			return &BoundaryError{"non-string stored Cookie identity is outside the fixed JSON cookie domain"}
		}
		identity := key{domain, path, name}
		if i, exists := keys[identity]; exists {
			out[i] = item
		} else {
			keys[identity] = len(out)
			out = append(out, item)
		}
		return nil
	}
	for _, old := range existing {
		object, _ := old.(map[string]any)
		normalized := map[string]any{}
		// None/scalar rows have no selected attributes in the original helper.
		for _, field := range []string{"domain", "path", "name", "value", "expirationDate"} {
			v := object[field]
			if v == nil {
				continue
			}
			if text, ok := v.(string); ok && text == "" {
				continue
			}
			normalized[field] = v
		}
		if err := put(normalized); err != nil {
			return nil, err
		}
	}
	for _, site := range sites {
		copy := make(map[string]any, len(site))
		for k, v := range site {
			copy[k] = v
		}
		if err := put(copy); err != nil {
			return nil, err
		}
	}
	return out, nil
}
