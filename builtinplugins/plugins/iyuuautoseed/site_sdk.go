package iyuuautoseed

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strings"

	"light-media/builtinplugins/internal/encodingdetector"
	sdk "light-media/builtinplugins/internal/sdk"
	"light-media/builtinplugins/plugins/autosignin/pyvalue"
)

// sdkSiteResolver obtains both the indexer and its host-sealed site reference.
// References are private and are usable only for the current plugin invocation.
type sdkSiteResolver struct{ sites sdk.Sites }

func newSDKSiteResolver(sites sdk.Sites) *sdkSiteResolver {
	return &sdkSiteResolver{sites: sites}
}

func (r *sdkSiteResolver) Indexer(ctx context.Context, domain string) (*Site, error) {
	result, err := r.sites.SourceIndexer(ctx, domain)
	if err != nil || result.Indexer == nil {
		return nil, err
	}
	indexer := result.Indexer
	value, err := decodeJSON(indexer.Fields)
	if err != nil {
		return nil, err
	}
	fields, ok := value.(map[string]any)
	if !ok {
		return nil, errors.New("invalid source indexer fields")
	}
	site := &Site{Fields: fields}
	if indexer.SiteID == nil {
		// Catalog-only entries carry their base address in domain; a configured
		// site row supplies url. Accept either spelling so an unbound site keeps
		// the source's registry behavior instead of failing the whole candidate.
		if !truth(fields["url"]) {
			if base, ok := fields["domain"].(string); ok && strings.HasPrefix(base, "http") {
				fields["url"] = base
			}
		}
		fields["url"], err = sourceSiteDirectoryURL(fields["url"])
		if err != nil {
			return nil, err
		}
		return site, nil
	}
	credentials, err := r.sites.SourceCredentials(ctx, sdk.SiteSourceReferenceV1{SiteID: *indexer.SiteID})
	if err != nil {
		return nil, err
	}
	value, err = decodeJSON(credentials.Fields)
	if err != nil {
		return nil, err
	}
	private, ok := value.(map[string]any)
	if !ok || !scalarEqual(private["id"], fields["id"]) || !scalarEqual(private["url"], fields["url"]) {
		return nil, errors.New("source site changed during indexer lookup")
	}
	for name, v := range private {
		fields[name] = v
	}
	bound, err := json.Marshal(credentials.Reference)
	if err != nil {
		return nil, err
	}
	site.Ref = string(bound)
	// The host's raw URL and sealed reference stay unchanged. Only the worker's
	// derived resolver fields need a directory base for source-style concatenation.
	fields["url"], err = sourceSiteDirectoryURL(fields["url"])
	if err != nil {
		return nil, err
	}
	return site, nil
}

func sourceSiteDirectoryURL(value any) (string, error) {
	raw, ok := value.(string)
	if !ok {
		return "", errors.New("invalid source site base")
	}
	base, err := url.Parse(raw)
	if err != nil || (base.Scheme != "http" && base.Scheme != "https") || base.Hostname() == "" ||
		base.User != nil || base.Opaque != "" || base.ForceQuery || base.RawQuery != "" || strings.Contains(raw, "#") {
		return "", errors.New("invalid source site base")
	}
	// Check the escaped path: a final %2F is part of the configured segment,
	// not the literal delimiter needed before appending details/download routes.
	if !strings.HasSuffix(base.EscapedPath(), "/") {
		base.Path += "/"
		if base.RawPath != "" {
			base.RawPath += "/"
		}
	}
	return base.String(), nil
}

func (r *sdkSiteResolver) CheckSite(ctx context.Context, domain string) (bool, error) {
	result, err := r.sites.SourceCheck(ctx, domain)
	if err == nil && result.Limited {
		sourceLog(ctx, 1165, result.Message)
	}
	return result.Limited, err
}

func (r *sdkSiteResolver) Reference(site Site) (sdk.SiteSourceReferenceV1, error) {
	var bound sdk.SiteSourceReferenceV1
	if site.Ref == "" {
		return bound, errors.New("catalog-only source site has no configured transport")
	}
	if err := json.Unmarshal([]byte(site.Ref), &bound); err != nil {
		return sdk.SiteSourceReferenceV1{}, errors.New("invalid bound source site reference")
	}
	return bound, nil
}

func (r *sdkSiteResolver) DownloadURL(ctx context.Context, seed map[string]any, site Site, template string) (string, error) {
	base, _ := site.Fields["url"].(string)
	return (Resolver{HTTP: sdkSiteHTTP{resolver: r, base: base}}).Resolve(ctx, seed, site, template)
}

type sdkSiteHTTP struct {
	resolver *sdkSiteResolver
	base     string
}

func (h sdkSiteHTTP) MTeamToken(ctx context.Context, ref string, id any) (*APIResponse, error) {
	bound, err := h.resolver.Reference(Site{Ref: ref})
	if err != nil {
		return nil, err
	}
	wireID, err := json.Marshal(id)
	if err != nil {
		return nil, err
	}
	values, err := pyvalue.RequestFormValues(wireID)
	if err != nil {
		return nil, err
	}
	var query []sdk.SiteHTTPPairV1
	for _, value := range values {
		query = append(query, sdk.SiteHTTPPairV1{Name: "id", Value: value})
	}
	result, err := h.resolver.sites.SourceRequest(ctx, sdk.SiteSourceHTTPRequestV1{
		Reference: bound,
		Request: sdk.SiteHTTPRequestV1{
			Path: "/api/torrent/genDlToken", Method: "POST", Query: query,
		},
		OriginProfile: "iyuu-mteam", CookieMode: "none", UserAgent: "source", ExplicitHeaders: true,
	})
	if err != nil {
		return nil, err
	}
	if result.ScopeChanged {
		return nil, errors.New("source site authorization changed")
	}
	response := result.Response
	if response == nil {
		return nil, nil
	}
	if response.StatusCode >= 400 {
		return &APIResponse{Status: response.StatusCode}, nil
	}
	body, err := responseJSONText(response.Content, response.ContentType)
	if err != nil {
		return nil, err
	}
	return &APIResponse{Status: response.StatusCode, Body: []byte(body)}, nil
}

func (h sdkSiteHTTP) Page(ctx context.Context, ref string, kind PageKind, id any) (*PageResponse, error) {
	bound, err := h.resolver.Reference(Site{Ref: ref})
	if err != nil {
		return nil, err
	}
	var route string
	switch kind {
	case DetailsPage:
		route = "details.php?id="
	case GPWPage:
		route = "torrents.php?torrentid="
	default:
		return nil, errors.New("invalid source site page")
	}
	path, err := sourceSitePagePath(h.base, route+pyString(id)+"&hit=1")
	if err != nil {
		return nil, err
	}
	result, err := h.resolver.sites.SourceRequest(ctx, sdk.SiteSourceHTTPRequestV1{
		Reference: bound,
		Request:   sdk.SiteHTTPRequestV1{Path: path, Method: "GET"},
		UserAgent: "source", CookieMode: "parsed",
	})
	if err != nil {
		return nil, err
	}
	if result.ScopeChanged {
		return nil, errors.New("source site authorization changed")
	}
	response := result.Response
	if response == nil {
		return nil, nil
	}
	page := &PageResponse{Status: response.StatusCode}
	if page.Status != 200 && page.Status != 500 {
		return page, nil
	}
	text, err := responseText(response.Content, response.ContentType)
	if err != nil {
		return nil, err
	}
	if strings.Contains(text, "charset=utf-8") || strings.Contains(text, "charset=UTF-8") {
		page.Text, err = encodingdetector.Decode(response.Content, "utf-8", true)
	} else {
		var enc string
		enc, _, err = encodingdetector.Detect(response.Content)
		if err == nil {
			page.Text, err = decodeResponseBytes(response.Content, enc)
		}
	}
	return page, err
}

// The source concatenates its configured base and route before preparing the
// request. Pass the resulting path/query only when it retains that exact origin.
func sourceSitePagePath(base, route string) (string, error) {
	owner, err := url.Parse(base)
	if err != nil {
		return "", errors.New("invalid source site base")
	}
	page, err := url.Parse(base + route)
	if err != nil || page.Scheme != owner.Scheme || page.Host != owner.Host || page.User != nil || page.Host == "" {
		return "", errors.New("source page target changed origin")
	}
	return page.RequestURI(), nil
}
