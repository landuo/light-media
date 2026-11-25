package autosignin

import (
	"context"
	"regexp"
)

const (
	AdapterBTSchool FixedAdapter = "BTSchool"
	AdapterHDUpt    FixedAdapter = "HDUpt"
	AdapterPTTime   FixedAdapter = "PTTime"
	AdapterHaiDan   FixedAdapter = "HaiDan"
)

func init() {
	fixedPolicies[AdapterBTSchool] = fixedPolicy{SiteURL: "pt.btschool.club", RegisteredHosts: []string{"pt.btschool.club", "www.pt.btschool.club"}, Routes: []fixedRoute{{"GET", "https://pt.btschool.club"}, {"GET", "https://pt.btschool.club/index.php?action=addbonus"}}}
	fixedPolicies[AdapterHDUpt] = fixedPolicy{SiteURL: "pt.hdupt.com", RegisteredHosts: []string{"pt.hdupt.com", "www.pt.hdupt.com"}, Routes: []fixedRoute{{"GET", "https://pt.hdupt.com"}, {"GET", "https://pt.hdupt.com/added.php?action=qiandao"}}}
	fixedPolicies[AdapterPTTime] = fixedPolicy{SiteURL: "pttime.org", RegisteredHosts: []string{"pttime.org", "www.pttime.org"}, Routes: []fixedRoute{{"GET", "https://www.pttime.org/attendance.php"}}}
	fixedPolicies[AdapterHaiDan] = fixedPolicy{SiteURL: "haidan.cc", RegisteredHosts: []string{"haidan.cc", "www.haidan.cc"}}
}
func LegacySiteLoggedIn(source string) (bool, error) {
	doc, err := parseHTML(source)
	if err != nil {
		return false, err
	}
	return legacyLoggedIn(doc), nil
}
func simpleRoutes(kind FixedAdapter, site Object) ([]fixedRoute, error) {
	if kind != AdapterHaiDan && kind != AdapterYemaPT {
		return fixedPolicies[kind].Routes, nil
	}
	routes := []fixedRoute{}
	paths := []string{"signin.php", "index.php"}
	if kind == AdapterYemaPT {
		paths = []string{"api/consumer/checkIn", "api/user/profile"}
	}
	for _, path := range paths {
		address, err := joinedURL(textValue(site["url"]), path)
		if err != nil {
			return nil, err
		}
		routes = append(routes, fixedRoute{"GET", address})
	}
	return routes, nil
}

var hdUptNumeric = regexp.MustCompile(`^(?:` + sourceDecimalClass + `+(?:\.` + sourceDecimalClass + `+)?|\.` + sourceDecimalClass + `+)$`)

func SimpleSignin(kind FixedAdapter, ctx context.Context, site Object, h SiteVisitHooks) (AdapterResult, error) {
	fail := func(message string) (AdapterResult, error) {
		if message != "签到失败" && message != "签到失败，未确认签到结果" {
			siteLog(h, "ERROR", pythonScalar(site["name"])+" "+message)
		}
		return AdapterResult{false, message}, nil
	}
	if kind == AdapterBTSchool {
		siteLog(h, "INFO", pythonScalar(site["name"])+" 开始签到")
	}
	routes, err := simpleRoutes(kind, site)
	if err != nil {
		return AdapterResult{}, err
	}
	get := func(i int) (any, error) { return sourceBasePageValue(ctx, site, routes[i].URL, h) }
	if kind == AdapterHaiDan {
		if _, err = get(0); err != nil {
			return AdapterResult{}, err
		}
	}
	first := 0
	if kind == AdapterHaiDan {
		first = 1
	}
	page, err := get(first)
	if err != nil {
		return AdapterResult{}, err
	}
	if !Truth(page) {
		return fail("签到失败，请检查站点连通性")
	}
	if kind == AdapterBTSchool {
		logged, err := sourcePageLoggedIn(page)
		if err != nil {
			return AdapterResult{}, err
		}
		if !logged {
			return fail("签到失败，Cookie已失效")
		}
		signed, err := sourcePageSigninEvidence(page)
		if err != nil {
			return AdapterResult{}, err
		}
		if signed {
			siteLog(h, "INFO", pythonScalar(site["name"])+" 今日已签到")
			return AdapterResult{true, "今日已签到"}, nil
		}
		page, err = get(1)
		if err != nil {
			return AdapterResult{}, err
		}
		if !Truth(page) {
			return fail("签到失败，签到接口请求失败")
		}
		logged, err = sourcePageLoggedIn(page)
		if err != nil {
			return AdapterResult{}, err
		}
		if logged {
			signed, err = sourcePageSigninEvidence(page)
			if err != nil {
				return AdapterResult{}, err
			}
			if signed {
				siteLog(h, "INFO", pythonScalar(site["name"])+" 签到成功")
				return AdapterResult{true, "签到成功"}, nil
			}
		}
		return fail("签到失败，未确认签到结果")
	}
	expired, err := sourcePageContains(page, "login.php")
	if err != nil {
		return AdapterResult{}, err
	}
	if expired {
		return fail("签到失败，Cookie已失效")
	}
	key := "_succeed_regex"
	if kind == AdapterHDUpt {
		key = "_sign_regex"
	}
	signed, err := sourcePageSignInResult(page, htmlData.Regexes[string(kind)+"."+key])
	if err != nil {
		return AdapterResult{}, err
	}
	if signed {
		message := "签到成功"
		if kind == AdapterHDUpt {
			message = "今日已签到"
		}
		siteLog(h, "INFO", pythonScalar(site["name"])+" "+message)
		return AdapterResult{true, message}, nil
	}
	if kind != AdapterHDUpt {
		siteLog(h, "ERROR", pythonScalar(site["name"])+" 签到失败，签到接口返回 "+pythonScalar(page))
		return fail("签到失败")
	}
	page, err = get(1)
	if err != nil {
		return AdapterResult{}, err
	}
	if !Truth(page) {
		return fail("签到失败，请检查站点连通性")
	}
	siteLog(h, "DEBUG", pythonScalar(site["name"])+" 签到接口返回 "+pythonScalar(page))
	stripped, err := sourcePageStrip(page)
	if err != nil {
		return AdapterResult{}, err
	}
	if hdUptNumeric.MatchString(stripped) {
		checked, err := get(0)
		if err != nil {
			return AdapterResult{}, err
		}
		yes, err := sourcePageSignInResult(checked, htmlData.Regexes[string(kind)+"._sign_regex"])
		if err != nil {
			return AdapterResult{}, err
		}
		if yes {
			siteLog(h, "INFO", pythonScalar(site["name"])+" 签到成功")
			return AdapterResult{true, "签到成功"}, nil
		}
	}
	siteLog(h, "ERROR", pythonScalar(site["name"])+" 签到失败，签到接口返回 "+pythonScalar(page))
	return fail("签到失败")
}
