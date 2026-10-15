package fnos

import (
	"bytes"
	"context"
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// SampleDetails is a limited read of an existing item, not provider identity
// verification or a claim that all service state has been compared.
type SampleDetails struct {
	Status        string            `json:"status"`
	Code          string            `json:"code,omitempty"`
	GUID          string            `json:"guid,omitempty"`
	AncestorGUID  string            `json:"ancestor_guid,omitempty"`
	ParentGUID    *string           `json:"parent_guid,omitempty"`
	Type          string            `json:"type,omitempty"`
	Title         string            `json:"title,omitempty"`
	TrimID        *string           `json:"trim_id,omitempty"`
	Year          *int              `json:"year,omitempty"`
	Dates         map[string]string `json:"dates,omitempty"`
	Season        *int              `json:"season,omitempty"`
	Episode       *int              `json:"episode,omitempty"`
	MissingFields []string          `json:"missing_fields,omitempty"`
}

type searchItem struct {
	GUID     json.RawMessage `json:"guid"`
	Type     string          `json:"type"`
	Title    string          `json:"title"`
	SourceID string          `json:"trim_id"`
	Season   *int            `json:"season_number"`
	Episode  *int            `json:"episode_number"`
}

var detailGUID = regexp.MustCompile(`^[0-9a-fA-F]{32}$`)
var detailTypes = map[string]string{"夜王": "Movie", "生活大爆炸": "TV", "诛仙": "TV"}

func fixedDetailTitles(titles []string) bool {
	if len(titles) != len(detailTypes) {
		return false
	}
	seen := map[string]bool{}
	for _, title := range titles {
		if detailTypes[title] == "" || seen[title] {
			return false
		}
		seen[title] = true
	}
	return true
}

func (c *client) captureDetails(ctx context.Context, title string, items []searchItem, truncated bool, libraries map[string]bool) *SampleDetails {
	out := &SampleDetails{Status: "unverified"}
	if truncated {
		out.Code = "SEARCH_TRUNCATED"
		return out
	}
	var matches []searchItem
	for _, item := range items {
		if item.Title == title && item.Type == detailTypes[title] {
			matches = append(matches, item)
		}
	}
	if len(matches) != 1 {
		out.Code = "EXACT_CANDIDATE_NOT_UNIQUE"
		return out
	}
	candidate := matches[0]
	var guid string
	if json.Unmarshal(candidate.GUID, &guid) != nil || !detailGUID.MatchString(guid) {
		out.Code = "INVALID_CANDIDATE_GUID"
		return out
	}
	data, err := c.request(ctx, "/item/"+guid, nil)
	if err != nil {
		out.Code = err.Error()
		return out
	}
	return parseDetails(data, guid, candidate, libraries)
}

func parseDetails(data []byte, requestedGUID string, candidate searchItem, libraries map[string]bool) *SampleDetails {
	out := &SampleDetails{Status: "unverified"}
	var fields map[string]json.RawMessage
	if json.Unmarshal(data, &fields) != nil || fields == nil {
		out.Code = "INVALID_DETAIL_DATA"
		return out
	}
	stringField := func(key string) (string, bool) {
		raw, exists := fields[key]
		var v *string
		if !exists || json.Unmarshal(raw, &v) != nil || v == nil {
			return "", false
		}
		return *v, true
	}
	core := map[string]string{}
	for _, key := range []string{"guid", "ancestor_guid", "parent_guid", "type", "title", "trim_id"} {
		v, ok := stringField(key)
		if !ok {
			out.Code = "MISSING_OR_INVALID_CORE_FIELD"
			out.MissingFields = append(out.MissingFields, key)
		} else {
			core[key] = v
		}
	}
	if out.Code != "" {
		return out
	}
	if !detailGUID.MatchString(core["guid"]) || core["guid"] != requestedGUID {
		out.Code = "DETAIL_GUID_MISMATCH"
		return out
	}
	if !detailGUID.MatchString(core["ancestor_guid"]) || !libraries[core["ancestor_guid"]] {
		out.Code = "DETAIL_LIBRARY_UNVERIFIED"
		return out
	}
	if core["parent_guid"] != "" {
		out.Code = "ROOT_PARENT_NOT_EMPTY"
		return out
	}
	if core["title"] != candidate.Title || core["type"] != candidate.Type || len(core["trim_id"]) > 1024 {
		out.Code = "DETAIL_IDENTITY_MISMATCH"
		return out
	}
	out.GUID, out.AncestorGUID = core["guid"], core["ancestor_guid"]
	out.Type, out.Title = core["type"], core["title"]
	parent, trimID := core["parent_guid"], core["trim_id"]
	out.ParentGUID, out.TrimID = &parent, &trimID
	out.Dates = map[string]string{}
	for _, key := range []string{"release_date", "premiere_date", "first_air_date", "air_date"} {
		raw, exists := fields[key]
		if !exists || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			out.MissingFields = append(out.MissingFields, key)
			continue
		}
		v, ok := stringField(key)
		if !ok {
			out.Code = "INVALID_OPTIONAL_FIELD"
			return out
		}
		if v == "" {
			out.MissingFields = append(out.MissingFields, key)
			continue
		}
		if date, err := time.Parse("2006-01-02", v); err != nil || date.Format("2006-01-02") != v {
			out.Code = "INVALID_OPTIONAL_FIELD"
			return out
		}
		out.Dates[key] = v
	}
	for _, key := range []string{"year", "season_number", "episode_number"} {
		raw, exists := fields[key]
		if !exists || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			out.MissingFields = append(out.MissingFields, key)
			continue
		}
		var value *int
		if json.Unmarshal(raw, &value) != nil {
			value = nil
			if key == "year" {
				// Some clients render an explicit four-digit string year. It is
				// accepted only as this field, never inferred from a date.
				v, ok := stringField(key)
				if ok && len(v) == 4 && strings.Trim(v, "0123456789") == "" {
					n, err := strconv.Atoi(v)
					if err == nil {
						value = &n
					}
				}
			}
		}
		if value == nil {
			out.Code = "INVALID_OPTIONAL_FIELD"
			return out
		}
		if !validNumber(value) || (key == "year" && (*value < 1000 || *value > 9999)) {
			out.Code = "INVALID_OPTIONAL_FIELD"
			return out
		}
		switch key {
		case "year":
			out.Year = value
		case "season_number":
			out.Season = value
		case "episode_number":
			out.Episode = value
		}
	}
	out.Status = "observed"
	return out
}
