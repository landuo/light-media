package download

import (
	"context"
	"math/big"
	"net/http"
	"regexp"
	"strings"

	"light-media/internal/releasemeta/legacyanime"
)

// The installed qbittorrent-api uses packaging.version.Version. This is its
// complete accepted VERSION_PATTERN closure and comparison to a final release;
// no application version or arbitrary PEP440 comparison API is exposed.
var iyuuVersionPattern = regexp.MustCompile(`^v?(?:(?P<epoch>[0-9]+)!)?(?P<release>[0-9]+(?:\.[0-9]+)*)(?P<pre>[-_.]?(?:alpha|a|beta|b|preview|pre|c|rc)[-_.]?[0-9]*)?(?P<post>(?:-[0-9]+)|(?:[-_.]?(?:post|rev|r)[-_.]?[0-9]*))?(?P<dev>[-_.]?dev[-_.]?[0-9]*)?(?:\+[a-z0-9]+(?:[-_.][a-z0-9]+)*)?$`)

func iyuuVersionAtLeast(value string, major, minor int) (bool, error) {
	s := legacyanime.Strip(value)
	s = strings.Map(func(r rune) rune {
		if r >= 'A' && r <= 'Z' {
			return r + 32
		}
		return r
	}, s)
	match := iyuuVersionPattern.FindStringSubmatch(s)
	if match == nil {
		return false, failure("iyuu_version", "invalid_webapi_version")
	}
	field := func(name string) string { return match[iyuuVersionPattern.SubexpIndex(name)] }
	if epoch := field("epoch"); epoch != "" {
		n, _ := new(big.Int).SetString(epoch, 10)
		if n.Sign() > 0 {
			return true, nil
		}
	}
	release := strings.Split(field("release"), ".")
	target := []int{major, minor, 0}
	length := len(release)
	if length < len(target) {
		length = len(target)
	}
	for i := 0; i < length; i++ {
		a := new(big.Int)
		if i < len(release) {
			a.SetString(release[i], 10)
		}
		b := new(big.Int)
		if i < len(target) {
			b.SetInt64(int64(target[i]))
		}
		if cmp := a.Cmp(b); cmp != 0 {
			return cmp > 0, nil
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
func (c *IYUUClient) webVersionLocked(ctx context.Context) (string, error) {
	if c.webVersion == "" {
		raw, e := c.client.qb(mutationReadOnly(ctx), http.MethodGet, "app/webapiVersion", "", "")
		if e != nil {
			return "", e
		}
		value := string(raw)
		if _, e := iyuuVersionAtLeast(value, 0, 0); e != nil {
			return "", e
		}
		c.webVersion = value
	}
	return c.webVersion, nil
}
