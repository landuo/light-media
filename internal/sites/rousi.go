package sites

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

func (c *Client) rousiAPI(ctx context.Context, path string, query url.Values) (map[string]any, error) {
	if c.config.APIKey == "" {
		return nil, errors.New("site_api_key_required")
	}
	u := c.endpoint(path)
	u.RawQuery = query.Encode()
	b, _, e := c.request(ctx, http.MethodGet, u, nil, map[string]string{"Authorization": "Bearer " + c.config.APIKey, "Accept": "application/json"}, htmlLimit)
	if e != nil {
		return nil, e
	}
	var root map[string]any
	if !decodeJSON(b, &root) || root == nil || !sourceZero(root["code"]) {
		return nil, errors.New("site_protocol_changed")
	}
	data, ok := root["data"].(map[string]any)
	if !ok {
		return nil, errors.New("site_protocol_changed")
	}
	return data, nil
}

func (c *Client) searchRousi(ctx context.Context, query string, page int) (SearchResult, error) {
	params := url.Values{"page": {strconv.Itoa(page)}, "page_size": {"100"}}
	if query != "" {
		params.Set("keyword", query)
	}
	data, e := c.rousiAPI(ctx, "api/v1/torrents", params)
	if e != nil {
		return SearchResult{}, e
	}
	values, ok := data["torrents"].([]any)
	if !ok {
		return SearchResult{}, errors.New("site_protocol_changed")
	}
	if len(values) > 100 {
		return SearchResult{}, errors.New("site_body_limit")
	}
	seen := map[string]bool{}
	result := SearchResult{Resources: []Resource{}, Page: page, HasMore: len(values) >= 100}
	for _, value := range values {
		row, ok := value.(map[string]any)
		if !ok {
			return SearchResult{}, errors.New("site_protocol_changed")
		}
		id := stringValue(row["id"])
		title, ok := row["title"].(string)
		if !numericID.MatchString(id) || !ok || strings.TrimSpace(title) == "" || len(title) > 4096 || seen[id] {
			return SearchResult{}, errors.New("site_protocol_changed")
		}
		seen[id] = true
		size, e1 := sourceUserInteger(row["size"])
		seeders, e2 := sourceUserInteger(row["seeders"])
		leechers, e3 := sourceUserInteger(row["leechers"])
		if e1 != nil || e2 != nil || e3 != nil || size < 0 || seeders < 0 || leechers < 0 || int64(int(seeders)) != seeders || int64(int(leechers)) != leechers {
			return SearchResult{}, errors.New("site_protocol_changed")
		}
		item := Resource{ID: id, SiteID: c.config.ID, Title: title, Subtitle: stringValue(row["subtitle"]), Size: size, Seeders: int(seeders), Leechers: int(leechers), DownloadFactor: 1, UploadFactor: 1, Labels: []string{}}
		if created, ok := row["created_at"].(string); ok {
			item.PublishedAt = dateValue(created)
		}
		if uuid, ok := row["uuid"].(string); ok && configID.MatchString(uuid) {
			item.DetailsURL = c.endpoint("torrent/" + url.PathEscape(uuid)).String()
		}
		if promo, ok := row["promotion"].(map[string]any); ok && sourceTruth(promo["is_active"]) {
			item.DownloadFactor, item.UploadFactor = 1, 1
			if v, exists := promo["down_multiplier"]; exists {
				item.DownloadFactor, e = sourceFloat(v)
				if e != nil || item.DownloadFactor < 0 {
					return SearchResult{}, errors.New("site_protocol_changed")
				}
			}
			if v, exists := promo["up_multiplier"]; exists {
				item.UploadFactor, e = sourceFloat(v)
				if e != nil || item.UploadFactor < 0 {
					return SearchResult{}, errors.New("site_protocol_changed")
				}
			}
			if until, ok := promo["until"].(string); ok {
				item.PromotionEnd = dateValue(until)
			}
		}
		item.Free = item.DownloadFactor == 0
		classify(&item)
		result.Resources = append(result.Resources, item)
	}
	return result, nil
}

func (c *Client) rousiTorrentURL(ctx context.Context, id string) (*url.URL, error) {
	data, e := c.rousiAPI(ctx, "api/v1/torrents/"+id, nil)
	if e != nil {
		return nil, e
	}
	raw, ok := data["download_url"].(string)
	if !ok || len(raw) > 8192 {
		return nil, errors.New("site_invalid_material_url")
	}
	u, e := url.Parse(raw)
	if e != nil || raw == "" {
		return nil, errors.New("site_invalid_material_url")
	}
	u = c.base.ResolveReference(u)
	// The returned signed material URL never receives either profile or mailbox
	// credentials. Cross-origin URLs need separately reviewed CDN provenance.
	if !sameOrigin(c.base, u) || u.User != nil || u.Fragment != "" {
		return nil, errors.New("site_material_origin_blocked")
	}
	return u, nil
}
