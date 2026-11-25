package autosignin

import (
	"context"
	"errors"
	sdk "light-media/builtinplugins/internal/sdk"
	"light-media/builtinplugins/plugins/autosignin/pyvalue"
	"net/url"
	"sort"
	"strings"
)

// References stay private to the selected original row. A custom ordinal is
// never converted into a managed site ID; the host checks the snapshot again.
func (s sdkSiteTransport) reference(ctx context.Context) sdk.SiteSourceReferenceV1 {
	if ref, ok := s.site["_host_source_reference"].(sdk.SiteSourceReferenceV1); ok {
		return ref
	}
	private, err := (sdk.Sites{Host: s.backend.host}).SourceCredentials(ctx, sdk.SiteSourceReferenceV1{SiteID: s.identity()})
	s.backend.fatal(err)
	if s.backend.logSecrets != nil {
		s.backend.logSecrets.registerFields(private.Fields)
	}
	return private.Reference
}
func (s sdkSiteTransport) sourcePath(address string) (path, profile string, err error) {
	u, err := url.Parse(address)
	if err != nil {
		return "", "", err
	}
	if !u.IsAbs() {
		return u.String(), "", nil
	}
	base, err := url.Parse(textValue(s.site["url"]))
	if err != nil {
		return "", "", err
	}
	if u.User != nil || u.Fragment != "" {
		return "", "", errors.New("invalid source adapter target")
	}
	if MTeamMatch(base.String()) && u.Scheme == "https" && (strings.ToLower(u.Host) == "api.m-team.cc" || strings.ToLower(u.Host) == "api.m-team.io" || strings.ToLower(u.Host) == "api.m-team.cc:443" || strings.ToLower(u.Host) == "api.m-team.io:443") {
		return u.RequestURI(), "mteam", nil
	}
	if u.Scheme == base.Scheme && strings.EqualFold(u.Host, base.Host) {
		return u.RequestURI(), "", nil
	}
	// These four original adapters intentionally select an alternate origin.
	// Other fixed aliases remain explicit missing capabilities until host review.
	for _, a := range []struct{ source, target, profile string }{{"tjupt.org", "www.tjupt.org", "tjupt"}, {"open.cd", "www.open.cd", "opencd"}, {"hdsky.me", "hdsky.me", "hdsky"}} {
		if SourceURLEqual(base.String(), a.source) && u.Scheme == "https" && u.Host == a.target {
			return u.RequestURI(), a.profile, nil
		}
	}
	for name, target := range map[string]string{"52pt": "52pt.site", "hares": "club.hares.top", "btschool": "pt.btschool.club", "hdcity": "hdcity.city", "hdupt": "pt.hdupt.com", "pttime": "www.pttime.org", "pter": "pterclub.com", "chdbits": "ptchdbits.co", "hdarea": "hdarea.club", "nexushd": "v6.nexushd.org", "rousi": "rousi.pro", "ttg": "totheglory.im", "hdchina": "hdchina.org", "zhuque": "zhuque.in", "u2": "u2.dmhy.org"} {
		if SourceURLEqual(base.String(), strings.ReplaceAll(target, "www.", "")) && u.Scheme == "https" && u.Host == target {
			return u.RequestURI(), name, nil
		}
	}

	return "", "", &MissingCapability{"source-bound adapter origin", "fixed source adapter origin is not available in the current host profile"}
}
func (s sdkSiteTransport) sourceDo(ctx context.Context, request PageRequest) (*PageResponse, error) {
	path, profile, err := s.sourcePath(request.URL)
	if err != nil {
		return nil, err
	}
	input := sdk.SiteHTTPRequestV1{Path: path, Method: request.Method, Headers: map[string]*string{}, NoCookies: request.NoCookies}
	bound := sdk.SiteSourceHTTPRequestV1{Reference: s.reference(ctx), Request: input, OriginProfile: profile, ExplicitHeaders: len(request.Headers) > 0 || request.HeadersMode == HeaderBaseCookie}
	if request.HeadersMode == HeaderBaseCookie {
		bound.CookieMode = "raw"
		if Truth(s.site["token"]) {
			bound.CookieMode = "none"
			input.Credentials = append(input.Credentials, sdk.SiteHTTPCredentialV1{Credential: "token", Header: "Authorization"})
		}
	}
	rousi := request.HeadersMode != HeaderBaseCookie && request.NoCookies && SourceURLEqual(textValue(s.site["url"]), "rousi.pro") && (request.Method == "GET" && (path == "/api/v1/profile" || path == "/api/points/attendance/stats") || request.Method == "POST" && path == "/api/points/attendance")
	if rousi {
		bound.CredentialMode, bound.OriginProfile, bound.CookieMode = "rousi", "rousi", "none"
	}
	hasAgent := false
	for key, value := range request.Headers {
		switch {
		case strings.EqualFold(key, "User-Agent"):
			hasAgent = true
			// The original adapters only copy their bound source UA. Never accept a
			// worker-supplied arbitrary UA or leak it to another target.
			if !equal(value, s.site["ua"]) {
				return nil, errors.New("source User-Agent does not match bound site")
			}
		case strings.EqualFold(key, "Authorization"), strings.EqualFold(key, "api-token"):
			if rousi {
				kind, sourceKey := "token", "token"
				if path == "/api/v1/profile" || strings.EqualFold(key, "api-token") {
					kind, sourceKey = "api_key", "apikey"
				}
				secret := authString(s.site[sourceKey])
				expected := secret
				if strings.EqualFold(key, "Authorization") {
					expected = bearer(secret)
				}
				if secret == "" || textValue(value) != expected {
					return nil, errors.New("source RouSi credential selector does not match bound site")
				}
				input.Credentials = append(input.Credentials, sdk.SiteHTTPCredentialV1{Credential: kind, Header: key})
				continue
			}
			if profile == "mteam" {
				continue
			} // Host derives str(token or '').strip().
			kind, sourceKey := "token", "token"
			if strings.EqualFold(key, "api-token") {
				kind, sourceKey = "api_key", "apikey"
			}
			secret := textValue(s.site[sourceKey])
			selected := textValue(value)
			prefix := ""
			if selected != secret {
				if secret == "" || !strings.HasSuffix(selected, secret) {
					return nil, errors.New("source credential selector does not match bound site")
				}
				prefix = strings.TrimSuffix(selected, secret)
			}
			input.Credentials = append(input.Credentials, sdk.SiteHTTPCredentialV1{Credential: kind, Header: key, Prefix: prefix})
		case strings.EqualFold(key, "Cookie"):
			return nil, errors.New("source literal Cookie must use the bound cookie mode")
		default:
			if value == nil {
				input.Headers[key] = nil
			} else {
				v := textValue(value)
				input.Headers[key] = &v
			}
		}
	}
	if bound.ExplicitHeaders && !hasAgent && request.HeadersMode != HeaderBaseCookie {
		bound.UserAgent = "session"
	}
	if profile == "mteam" {
		bound.CookieMode = "none"
		input.NoCookies = true
	}
	if request.Referer != "" {
		v := request.Referer
		input.Headers["Referer"] = &v
	}
	if request.AcceptType != "" {
		v := request.AcceptType
		input.Headers["Accept"] = &v
	}
	if request.JSON != nil {
		input.JSON, err = sourceHTTPJSONBytes(request.JSON)
		if err != nil {
			return nil, err
		}
	}
	keys := append([]string(nil), request.FormOrder...)
	if len(keys) == 0 {
		for key := range request.Form {
			keys = append(keys, key)
		}
		sort.Strings(keys)
	}
	for _, key := range keys {
		if values, ok := request.FormValues[key]; ok {
			for _, value := range values {
				input.Form = append(input.Form, sdk.SiteHTTPPairV1{Name: key, Value: value})
			}
			continue
		}
		if raw, ok := request.RawForm[key]; ok {
			values, e := pyvalue.RequestFormValues(raw)
			if e != nil {
				return nil, e
			}
			for _, v := range values {
				input.Form = append(input.Form, sdk.SiteHTTPPairV1{Name: key, Value: v})
			}
		} else {
			input.Form = append(input.Form, sdk.SiteHTTPPairV1{Name: key, Value: request.Form[key]})
		}
	}
	if request.CookieOverride != nil {
		pairs := []sdk.SiteHTTPPairV1{}
		for _, part := range strings.Split(*request.CookieOverride, ";") {
			key, v, ok := strings.Cut(part, "=")
			if ok {
				pairs = append(pairs, sdk.SiteHTTPPairV1{Name: strings.TrimSpace(key), Value: strings.TrimSpace(v)})
			}
		}
		input.CookieOverride = &pairs
	}
	input.TimeoutSeconds, err = timeoutNumber(request.Timeout)
	if err != nil {
		return nil, err
	}
	bound.Request = input
	result, err := (sdk.Sites{Host: s.backend.host}).SourceRequest(ctx, bound)
	s.backend.fatal(err)
	if result.ScopeChanged {
		s.backend.fatal(errors.New("source site scope changed after request"))
	}
	if result.Response == nil {
		return nil, nil
	}
	response := result.Response
	content := append([]byte(nil), response.Content...)
	out := &PageResponse{Status: response.StatusCode, Content: content}
	for _, cookie := range response.Cookies {
		out.Cookies = append(out.Cookies, CookieValue{cookie.Name, cookie.Value})
	}
	out.getText = func() (string, error) {
		if request.DecodeAsUTF8 {
			return siteUTF8(content), nil
		}
		return responseText(content, response.ContentType)
	}
	return out, nil
}
