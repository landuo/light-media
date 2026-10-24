package sites

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

func rousiOrigin(u *url.URL) bool {
	return u != nil && u.Scheme == "https" && u.User == nil && (u.Port() == "" || u.Port() == "443") && (u.Path == "" || u.Path == "/") && (strings.EqualFold(u.Hostname(), "rousi.pro") || strings.EqualFold(u.Hostname(), "www.rousi.pro"))
}

// Match MoviePilot's RousiPro API v1. Never send its cookie or the independent
// web mailbox token with the Passkey, or return upstream messages containing secrets.
func (c *Client) rousiAPI(ctx context.Context, path string, query url.Values) (map[string]any, error) {
	if c.config.APIKey == "" {
		return nil, errors.New("site_api_key_required")
	}
	u := c.endpoint(path)
	u.RawQuery = query.Encode()
	b, _, err := c.request(ctx, http.MethodGet, u, nil, map[string]string{"Authorization": "Bearer " + c.config.APIKey, "Accept": "application/json"}, htmlLimit)
	if err != nil {
		return nil, err
	}
	payload, err := envelope(b)
	if err != nil {
		return nil, err
	}
	var data map[string]any
	if !decodeJSON(payload, &data) || data == nil {
		return nil, errors.New("site_protocol_changed")
	}
	return data, nil
}

func (c *Client) rousiUserStats(ctx context.Context, s UserStats) (UserStats, error) {
	data, err := c.rousiAPI(ctx, "api/v1/profile", url.Values{"include_fields[user]": {"seeding_leeching_data"}})
	if err != nil {
		return s, err
	}
	s.UserID, s.Username = stringValue(data["id"]), statsText(stringValue(data["username"]), 128)
	if !numericID.MatchString(s.UserID) || s.Username == "" {
		return s, errors.New("site_user_identity_missing")
	}
	s.Level = statsText(stringValue(data["level_text"]), 128)
	if s.Level == "" {
		s.Level = statsText(stringValue(data["role_text"]), 128)
	}
	s.JoinedAt = statsText(stringValue(data["registered_at"]), 80)
	sl, _ := data["seeding_leeching_data"].(map[string]any)
	for _, field := range []struct {
		key, name string
		source    map[string]any
		dst       **int64
	}{
		{"uploaded", "uploaded", data, &s.Uploaded}, {"downloaded", "downloaded", data, &s.Downloaded},
		{"seeding_count", "seeding", sl, &s.Seeding}, {"seeding_size", "seeding_bytes", sl, &s.SeedingBytes}, {"leeching_count", "leeching", sl, &s.Leeching},
	} {
		if raw, exists := field.source[field.key]; exists && raw != nil {
			n, ok := integer(raw)
			if !ok {
				return s, errors.New("site_protocol_changed")
			}
			*field.dst = &n
			s.Sources[field.name] = "api/v1/profile:" + field.key
		}
	}
	for _, field := range []struct {
		key string
		dst **float64
	}{{"karma", &s.Bonus}, {"ratio", &s.Ratio}} {
		if raw, exists := data[field.key]; exists && raw != nil {
			n, ok := statsFloat(stringValue(raw))
			if !ok {
				return s, errors.New("site_protocol_changed")
			}
			if field.key == "ratio" {
				*n, _ = strconv.ParseFloat(strconv.FormatFloat(*n, 'f', 2, 64), 64)
			}
			*field.dst = n
		}
	}
	for _, key := range []string{"user_id", "username", "level", "joined_at"} {
		s.Sources[key] = "api/v1/profile"
	}
	if s.Bonus != nil {
		s.Sources["bonus"] = "api/v1/profile:karma"
	}
	if s.Ratio != nil {
		s.Sources["ratio"] = "api/v1/profile:ratio"
	}
	return s, nil
}

func (c *Client) searchRousi(ctx context.Context, query string, page int) (SearchResult, error) {
	params := url.Values{"page": {strconv.Itoa(page)}, "page_size": {"100"}}
	if query != "" {
		params.Set("keyword", query)
	}
	data, err := c.rousiAPI(ctx, "api/v1/torrents", params)
	if err != nil {
		return SearchResult{}, err
	}
	rows, ok := data["torrents"].([]any)
	if !ok {
		return SearchResult{}, errors.New("site_protocol_changed")
	}
	if len(rows) > 100 {
		return SearchResult{}, errors.New("site_body_limit")
	}
	result := SearchResult{Resources: []Resource{}, Page: page, HasMore: len(rows) == 100}
	seen := map[string]bool{}
	for _, raw := range rows {
		row, ok := raw.(map[string]any)
		if !ok {
			return SearchResult{}, errors.New("site_protocol_changed")
		}
		id, title := stringValue(row["id"]), stringValue(row["title"])
		size, ok1 := integer(row["size"])
		seeders, ok2 := integer(row["seeders"])
		leechers, ok3 := integer(row["leechers"])
		if !numericID.MatchString(id) || strings.TrimSpace(title) == "" || len(title) > 4096 || seen[id] || !ok1 || !ok2 || !ok3 || int64(int(seeders)) != seeders || int64(int(leechers)) != leechers {
			return SearchResult{}, errors.New("site_protocol_changed")
		}
		seen[id] = true
		item := Resource{ID: id, SiteID: c.config.ID, Title: title, Subtitle: stringValue(row["subtitle"]), Size: size, Seeders: int(seeders), Leechers: int(leechers), DownloadFactor: 1, UploadFactor: 1, PublishedAt: dateValue(stringValue(row["created_at"]))}
		if uuid := stringValue(row["uuid"]); configID.MatchString(uuid) {
			item.DetailsURL = c.endpoint("torrent/" + uuid).String()
		}
		if promo, ok := row["promotion"].(map[string]any); ok && promo["is_active"] == true {
			for _, f := range []struct {
				key string
				dst *float64
			}{{"down_multiplier", &item.DownloadFactor}, {"up_multiplier", &item.UploadFactor}} {
				if raw, exists := promo[f.key]; exists {
					n, ok := statsFloat(stringValue(raw))
					if !ok {
						return SearchResult{}, errors.New("site_protocol_changed")
					}
					*f.dst = *n
				}
			}
			item.PromotionEnd = dateValue(stringValue(promo["until"]))
		}
		item.Free = item.DownloadFactor == 0
		classify(&item)
		result.Resources = append(result.Resources, item)
	}
	return result, nil
}

func (c *Client) rousiTorrentURL(ctx context.Context, id string) (*url.URL, error) {
	data, err := c.rousiAPI(ctx, "api/v1/torrents/"+id, nil)
	if err != nil {
		return nil, err
	}
	raw := stringValue(data["download_url"])
	u, err := url.Parse(raw)
	if err != nil || raw == "" || len(raw) > 8192 {
		return nil, errors.New("site_invalid_material_url")
	}
	u = c.base.ResolveReference(u)
	if !sameOrigin(c.base, u) || u.User != nil || u.Fragment != "" {
		return nil, errors.New("site_material_origin_blocked")
	}
	return u, nil
}
