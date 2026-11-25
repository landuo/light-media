package iyuuautoseed

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"

	sdk "light-media/builtinplugins/internal/sdk"
)

type sourceTorrentHTTP struct {
	sites     sdk.Sites
	reference sdk.SiteSourceReferenceV1
	siteURL   string
	userAgent string
}

func downloadSourceTorrent(ctx context.Context, sites sdk.Sites, ref sdk.SiteSourceReferenceV1, site Site, cache TorrentCache, address string) (TorrentContent, error) {
	siteURL, _ := site.Fields["url"].(string)
	agent := "host"
	if truth(site.Fields["ua"]) {
		agent = "source"
	}
	transport := sourceTorrentHTTP{sites: sites, reference: ref, siteURL: siteURL, userAgent: agent}
	result, err := (TorrentHelper{HTTP: transport, Cache: cache}).Download(ctx, address, true)
	if err != nil {
		return TorrentContent{}, err
	}
	return TorrentContent{Bytes: result.Content, Magnet: result.Magnet, ErrorMessage: result.ErrorMessage}, nil
}

func (h sourceTorrentHTTP) Get(ctx context.Context, address string) (*TorrentResponse, error) {
	return h.request(ctx, http.MethodGet, address, nil)
}

func (h sourceTorrentHTTP) Post(ctx context.Context, address string, fields []TorrentFormField) (*TorrentResponse, error) {
	form := make([]sdk.SiteHTTPPairV1, 0, len(fields))
	for _, field := range fields {
		form = append(form, sdk.SiteHTTPPairV1{Name: field.Name, Value: field.Value})
	}
	return h.request(ctx, http.MethodPost, address, form)
}

func (h sourceTorrentHTTP) request(ctx context.Context, method, address string, form []sdk.SiteHTTPPairV1) (*TorrentResponse, error) {
	target, err := url.Parse(address)
	if err != nil || (target.Scheme != "http" && target.Scheme != "https") || target.Host == "" || target.Opaque != "" {
		// Requests treats an absent URL scheme in the raw Location as a normal
		// RequestException. This helper does not resolve relative redirects.
		return nil, nil
	}
	site, err := url.Parse(h.siteURL)
	if err != nil || target.User != nil {
		return nil, errors.New("torrent target outside the bound site origin")
	}
	profile := mteamTorrentProfile(site, target)
	cookieMode := ""
	if profile != "" {
		if method != http.MethodGet || target.Scheme != "https" || torrentPort(target) != "443" || target.Fragment != "" {
			return nil, errors.New("torrent request outside the bound M-Team material profile")
		}
		cookieMode = "none"
	} else if !sameTorrentOrigin(site, target) {
		return nil, errors.New("torrent target outside the bound site origin")
	}
	follow := method == http.MethodPost
	reply, err := h.sites.SourceRequest(ctx, sdk.SiteSourceHTTPRequestV1{
		Reference: h.reference, UserAgent: h.userAgent, CookieMode: cookieMode,
		OriginProfile: profile, ExplicitHeaders: profile != "", IncludeLocation: !follow,
		Request: sdk.SiteHTTPRequestV1{Method: method, Path: target.RequestURI(), Form: form, AllowRedirects: &follow},
	})
	if err != nil {
		return nil, err
	}
	if reply.ScopeChanged {
		return nil, errors.New("torrent site authorization changed")
	}
	if reply.Response == nil {
		return nil, nil
	}
	response := reply.Response
	out := &TorrentResponse{Status: response.StatusCode, Content: response.Content, Headers: make(http.Header)}
	if response.Location != nil {
		out.Headers.Set("Location", *response.Location)
	}
	if response.StatusCode == 200 && (bytes.HasPrefix(response.Content, []byte("magnet:")) ||
		bytes.Contains(response.Content, []byte("下载种子文件"))) {
		out.Text, err = responseText(response.Content, response.ContentType)
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

// The existing metainfo evidence records api.m-team.cc -> fr1.halomt.com.
// These GETs carry only the token URL and never inherit site credentials.
func mteamTorrentProfile(site, target *url.URL) string {
	siteHost := strings.ToLower(site.Hostname())
	targetHost := strings.ToLower(target.Hostname())
	for _, domain := range []string{"m-team.cc", "m-team.io"} {
		if siteHost != domain && !strings.HasSuffix(siteHost, "."+domain) {
			continue
		}
		switch targetHost {
		case "api." + domain:
			return "iyuu-mteam-material-api"
		case "fr1.halomt.com":
			return "iyuu-mteam-material-cdn"
		}
	}
	return ""
}

func sameTorrentOrigin(site, target *url.URL) bool {
	return site.Scheme == target.Scheme && strings.EqualFold(site.Hostname(), target.Hostname()) &&
		torrentPort(site) == torrentPort(target)
}

func torrentPort(address *url.URL) string {
	if port := address.Port(); port != "" {
		return port
	}
	if address.Scheme == "https" {
		return "443"
	}
	return "80"
}
