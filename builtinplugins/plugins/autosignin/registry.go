package autosignin

import (
	"context"
	"fmt"
	"time"
)

func MTorrentAdapter() SiteAdapter {
	visit := func(ctx context.Context, site Object, h SiteVisitHooks) (AdapterResult, error) {
		token := ""
		if Truth(site["token"]) {
			token = pythonScalar(site["token"])
		}
		address := ""
		if Truth(site["url"]) {
			address = pythonScalar(site["url"])
		}
		credentials := Credentials{URL: address, Token: token, UserAgent: textValue(site["ua"]), Proxy: site["proxy"], Timeout: site["timeout"]}
		now := time.Now
		if h.Now != nil {
			now = h.Now
		}
		state, message, err := MTeamVisit(ctx, credentials, now().UnixMilli(), h.MTeam)
		return AdapterResult{state, message}, err
	}
	return SiteAdapter{Match: func(address string) (bool, error) { return MTeamMatch(address), nil }, MatchRaw: func(raw any) (bool, error) {
		text, ok := raw.(string)
		if !ok {
			return false, fmt.Errorf("'%s' object has no attribute 'split'", typeName(raw))
		}
		return MTeamMatch(text), nil
	}, Signin: visit, Login: visit, SigninIsLogin: true}
}

func InstalledAdapterRegistry() []SiteAdapter {
	out := []SiteAdapter{}
	for _, kind := range []FixedAdapter{AdapterPt52, AdapterBTSchool, AdapterCHDBits, AdapterHaiDan, AdapterHares, AdapterHDArea, AdapterHDChina, AdapterHDCity, AdapterHDSky, AdapterHDUpt} {
		a, _ := FixedSiteAdapter(kind)
		out = append(out, a)
	}
	out = append(out, MTorrentAdapter())
	for _, kind := range []FixedAdapter{AdapterNexusHD, AdapterOpencd, AdapterPTerClub, AdapterPTTime} {
		a, _ := FixedSiteAdapter(kind)
		out = append(out, a)
	}
	out = append(out, SiteAdapter{Match: PTZoneMatch, MatchRaw: func(raw any) (bool, error) {
		if !Truth(raw) {
			return false, nil
		}
		text, ok := raw.(string)
		if !ok {
			return false, fmt.Errorf("'%s' object has no attribute 'decode'", typeName(raw))
		}
		return PTZoneMatch(text)
	}, Signin: PTZoneSignin})
	for _, kind := range []FixedAdapter{AdapterRousiPro, AdapterTjupt, AdapterTTG, AdapterU2, AdapterYemaPT, AdapterZhuQue} {
		a, _ := FixedSiteAdapter(kind)
		out = append(out, a)
	}
	return out
}
