package app

import (
	"net/url"
	"strings"
)

// Source OCR keeps the original private row. Only its exact origin, or the
// HDSky/OpenCD canonical image origins used by the two installed callers, may
// receive its Cookie and raw User-Agent. It never resolves a custom ID as native.
func originalSourceOCRImage(source originalResolvedSourceSite, raw string) (target *url.URL, agent, cookie string, err error) {
	target, err = originalSourcePublicInitialURL(raw)
	if err != nil {
		return nil, "", "", err
	}
	allowed := originalOCROrigin(target) == originalOCROrigin(source.base)
	for _, entry := range []struct{ host, profile string }{{"hdsky.me", "hdsky"}, {"www.open.cd", "opencd"}} {
		if target.Scheme == "https" && target.Host == entry.host {
			base, e := originalSourceSiteTarget(source, entry.profile)
			if e == nil && originalOCROrigin(base) == originalOCROrigin(target) {
				allowed = true
			}
		}
	}
	if !allowed {
		return nil, "", "", bad("PLUGIN_CAPABILITY_DENIED", "验证码图片不属于当前源站点")
	}
	agent = originalOCRDefaultUserAgent
	if value := source.fields["ua"]; len(value) > 0 && strings.TrimSpace(string(value)) != "null" {
		var ok bool
		agent, ok = sourceSiteString(value)
		if !ok {
			return nil, "", "", bad("INVALID_SOURCE_SITE", "源User-Agent不是字符串")
		}
	}
	cookie, err = originalSourceSiteFieldString(source.fields, "cookie")
	return
}
