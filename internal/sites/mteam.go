package sites

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const pageSize = 100

func decodeJSON(b []byte, out any) bool {
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	return d.Decode(out) == nil && d.Decode(new(any)) == io.EOF
}

func stringValue(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case json.Number:
		return x.String()
	default:
		return ""
	}
}

func integer(v any) (int64, bool) {
	n, err := strconv.ParseInt(strings.ReplaceAll(stringValue(v), ",", ""), 10, 64)
	return n, err == nil && n >= 0
}

func envelope(b []byte) (json.RawMessage, error) {
	var e map[string]json.RawMessage
	if !decodeJSON(b, &e) || e == nil {
		return nil, errors.New("site_protocol_changed")
	}
	var code any
	if !decodeJSON(e["code"], &code) {
		return nil, errors.New("site_protocol_changed")
	}
	switch stringValue(code) {
	case "0":
	case "401", "401000", "-401":
		return nil, errors.New("site_authentication_failed")
	case "403":
		return nil, errors.New("site_forbidden")
	case "429":
		return nil, errors.New("site_rate_limited")
	default:
		return nil, errors.New("site_business_error")
	}
	data := e["data"]
	if len(data) == 0 || bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		return nil, errors.New("site_protocol_changed")
	}
	return data, nil
}

func (c *Client) searchMTeam(ctx context.Context, query string, page int) (SearchResult, error) {
	if imdbID.MatchString(query) {
		query = "https://www.imdb.com/title/" + query
	}
	payload, _ := json.Marshal(map[string]any{"keyword": query, "pageNumber": page, "pageSize": pageSize, "categories": []string{}, "visible": 1})
	b, _, err := c.request(ctx, http.MethodPost, c.endpoint("api/torrent/search"), payload, c.auth(), htmlLimit)
	if err != nil {
		return SearchResult{}, err
	}
	return c.parseMTeam(b, page)
}

func (c *Client) parseMTeam(b []byte, page int) (SearchResult, error) {
	result := SearchResult{Resources: []Resource{}, Page: page}
	sourceExact := sourceJSONLossless(b)
	data, err := envelope(b)
	if err != nil {
		return result, err
	}
	var list struct {
		Data       []map[string]any `json:"data"`
		Total      any              `json:"total"`
		TotalPages any              `json:"totalPages"`
	}
	if !decodeJSON(data, &list) || list.Data == nil || len(list.Data) > pageSize {
		return result, errors.New("site_protocol_changed")
	}
	seen := map[string]bool{}
	for _, row := range list.Data {
		id, title := stringValue(row["id"]), stringValue(row["name"])
		size, ok := integer(row["size"])
		status, statusOK := row["status"].(map[string]any)
		if !numericID.MatchString(id) || seen[id] || title == "" || len(title) > 4096 || !ok || !statusOK {
			return result, errors.New("site_protocol_changed")
		}
		seen[id] = true
		r := Resource{ID: id, SiteID: c.config.ID, Title: title, Subtitle: stringValue(row["smallDescr"]), Size: size, Seeders: -1, Leechers: -1, DownloadFactor: 1, UploadFactor: 1}
		var labels []string
		if values, ok := row["labelsNew"].([]any); ok {
			for _, value := range values {
				if label, ok := value.(string); ok && len(label) <= 128 && len(labels) < 32 {
					labels = append(labels, label)
				}
			}
		}
		if len(labels) == 0 {
			labels = strings.Fields(map[string]string{"1": "DIY", "2": "国配", "3": "DIY 国配", "4": "中字", "5": "DIY 中字", "6": "国配 中字", "7": "DIY 国配 中字"}[stringValue(row["labels"])])
		}
		sourceSubtitle := r.Subtitle
		r.SourceSubtitle = &sourceSubtitle
		r.Labels = append([]string{}, labels...)
		r.Subtitle = subtitleWithLabels(r.Subtitle, labels)
		for key, dest := range map[string]*int{"seeders": &r.Seeders, "leechers": &r.Leechers} {
			if status[key] == nil {
				continue
			}
			n, ok := integer(status[key])
			if !ok || n > math.MaxInt32 {
				return result, errors.New("site_protocol_changed")
			}
			*dest = int(n)
		}
		r.PublishedAt = dateValue(row["createdDate"])
		r.DownloadFactor, r.UploadFactor = discount(stringValue(status["discount"]))
		r.PromotionEnd = dateValue(status["discountEndTime"])
		if p, ok := status["promotionRule"].(map[string]any); ok && p != nil {
			r.DownloadFactor, _ = discount(stringValue(p["discount"]))
			if end := dateValue(p["endTime"]); end != "" {
				r.PromotionEnd = end
			}
		}
		if p, ok := status["mallSingleFree"].(map[string]any); ok && stringValue(p["status"]) == "ONGOING" {
			r.DownloadFactor = 0
			if end := dateValue(p["endDate"]); end != "" {
				r.PromotionEnd = end
			}
		}
		r.Free = r.DownloadFactor == 0
		// Search returns a secret-free detail link. The API origin is not a UI.
		r.DetailsURL = "https://kp.m-team.cc/detail/" + id
		if strings.EqualFold(c.base.Hostname(), "api.m-team.io") {
			r.DetailsURL = "https://kp.m-team.io/detail/" + id
		}
		classify(&r)
		if sourceExact {
			c.captureMTeamSource(&r, row)
		}
		result.Resources = append(result.Resources, r)
	}
	result.HasMore = len(result.Resources) == pageSize
	if list.Total != nil {
		n, ok := integer(list.Total)
		if !ok {
			return result, errors.New("site_protocol_changed")
		}
		result.HasMore = int64(page*pageSize) < n
	} else if list.TotalPages != nil {
		n, ok := integer(list.TotalPages)
		if !ok {
			return result, errors.New("site_protocol_changed")
		}
		result.HasMore = int64(page) < n
	}
	return result, nil
}

func (c *Client) mteamTorrentURL(ctx context.Context, id string) (*url.URL, error) {
	headers := c.auth()
	headers["Content-Type"] = "application/x-www-form-urlencoded"
	b, _, err := c.request(ctx, http.MethodPost, c.endpoint("api/torrent/genDlToken"), []byte(url.Values{"id": {id}}.Encode()), headers, 1<<20)
	if err != nil {
		return nil, err
	}
	data, err := envelope(b)
	if err != nil {
		return nil, err
	}
	var raw string
	if !decodeJSON(data, &raw) || len(raw) > 8192 {
		return nil, errors.New("site_protocol_changed")
	}
	u, err := url.Parse(raw)
	if err != nil || !c.allowedMaterial(u) {
		return nil, errors.New("site_material_origin_not_allowed")
	}
	return u, nil
}

func discount(v string) (download, upload float64) {
	download, upload = 1, 1
	switch v {
	case "FREE", "_2X_FREE":
		download = 0
	case "PERCENT_50", "_2X_PERCENT_50":
		download = .5
	case "PERCENT_70":
		download = .3
	}
	switch v {
	case "_2X", "_2X_FREE", "_2X_PERCENT_50":
		upload = 2
	}
	return
}

func dateValue(v any) string {
	s := strings.TrimSpace(stringValue(v))
	if s == "" || len(s) > 64 {
		return ""
	}
	if n, err := strconv.ParseInt(s, 10, 64); err == nil && n > 0 {
		if n > 100000000000 {
			return time.UnixMilli(n).UTC().Format(time.RFC3339)
		}
		return time.Unix(n, 0).UTC().Format(time.RFC3339)
	}
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02 15:04:05", "2006-01-02T15:04:05", "2006-01-02"} {
		if t, err := time.ParseInLocation(layout, s, time.FixedZone("CST", 8*3600)); err == nil {
			return t.Format(time.RFC3339)
		}
	}
	return ""
}
