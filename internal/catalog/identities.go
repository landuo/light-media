package catalog

import (
	"regexp"
	"sort"
	"strings"
	"unicode"
)

type wireTranslations struct {
	Translations []struct {
		ISO639 string `json:"iso_639_1"`
		Data   struct {
			Name  string `json:"name"`
			Title string `json:"title"`
		} `json:"data"`
	} `json:"translations"`
}

type wireExternalIDs struct {
	ID      int64  `json:"id"`
	IMDbID  string `json:"imdb_id"`
	Success *bool  `json:"success"`
}

type wireAlternativeTitles struct {
	ID      int64                  `json:"id"`
	Titles  []wireAlternativeTitle `json:"titles"`
	Results []wireAlternativeTitle `json:"results"`
	Success *bool                  `json:"success"`
}

type wireAlternativeTitle struct {
	Country string `json:"iso_3166_1"`
	Title   string `json:"title"`
}

var imdbTitleID = regexp.MustCompile(`^tt[0-9]{7,10}$`)
var aliasCountries = map[string]int{"CN": 0, "TW": 1, "HK": 2, "SG": 3, "US": 4, "GB": 5, "CA": 6, "AU": 7}

// Only the IMDb namespace and useful Chinese/English release-title regions
// leave the provider decoder. Social IDs, arbitrary fields and foreign URLs do
// not become search keywords. Appended objects without an ID are bound by the
// already-verified parent detail; when supplied, their ID must match it.
func (c *Client) searchIdentities(raw wireMedia, kind, title, original string) (string, []string, error) {
	identifier := strings.ToLower(strings.TrimSpace(raw.IMDbID))
	if raw.ExternalIDs != nil {
		if (raw.ExternalIDs.ID != 0 && raw.ExternalIDs.ID != raw.ID) || (raw.ExternalIDs.Success != nil && !*raw.ExternalIDs.Success) {
			return "", nil, &Error{Code: "invalid_response"}
		}
		external := strings.ToLower(strings.TrimSpace(raw.ExternalIDs.IMDbID))
		if external != "" {
			if identifier != "" && identifier != external {
				return "", nil, &Error{Code: "invalid_response"}
			}
			identifier = external
		}
	}
	if identifier != "" && (!imdbTitleID.MatchString(identifier) || c.text(identifier, 32) != identifier) {
		return "", nil, &Error{Code: "invalid_response"}
	}
	aliases := []string{}
	if raw.AlternativeTitles == nil {
		return identifier, aliases, nil
	}
	alternative := raw.AlternativeTitles
	if (alternative.ID != 0 && alternative.ID != raw.ID) || (alternative.Success != nil && !*alternative.Success) || len(alternative.Titles)+len(alternative.Results) > 500 {
		return "", nil, &Error{Code: "invalid_response"}
	}
	rows := alternative.Titles
	if kind == "tv" {
		rows = alternative.Results
		if rows == nil && alternative.Titles != nil {
			return "", nil, &Error{Code: "invalid_response"}
		}
	} else if rows == nil && alternative.Results != nil {
		return "", nil, &Error{Code: "invalid_response"}
	}
	sort.SliceStable(rows, func(i, j int) bool {
		left, leftOK := aliasCountries[strings.ToUpper(rows[i].Country)]
		right, rightOK := aliasCountries[strings.ToUpper(rows[j].Country)]
		if !leftOK {
			left = 100
		}
		if !rightOK {
			right = 100
		}
		return left < right
	})
	seen := map[string]bool{strings.ToLower(strings.Join(strings.Fields(title), " ")): true, strings.ToLower(strings.Join(strings.Fields(original), " ")): true}
	for _, row := range rows {
		if _, allowed := aliasCountries[strings.ToUpper(row.Country)]; !allowed {
			continue
		}
		value := strings.Join(strings.Fields(row.Title), " ")
		if !aliasText(row.Title) || !queryOK(value) || c.text(value, 512) != value {
			continue
		}
		key := strings.ToLower(value)
		if seen[key] {
			continue
		}
		seen[key] = true
		aliases = append(aliases, value)
		if len(aliases) >= 16 {
			break
		}
	}
	// The English name is the identity a release usually carries; a Chinese title
	// is not what a site writes for an English-subbed pack. MoviePilot searches
	// title, original title and en_title for exactly this reason.
	if raw.Translations != nil {
		for _, entry := range raw.Translations.Translations {
			if !strings.EqualFold(strings.TrimSpace(entry.ISO639), "en") {
				continue
			}
			name := strings.Join(strings.Fields(entry.Data.Name), " ")
			if name == "" {
				name = strings.Join(strings.Fields(entry.Data.Title), " ")
			}
			if name == "" || !aliasText(name) || !queryOK(name) || c.text(name, 512) != name {
				continue
			}
			key := strings.ToLower(name)
			if !seen[key] {
				seen[key] = true
				aliases = append([]string{name}, aliases...)
			}
			break
		}
	}
	return identifier, aliases, nil
}

func aliasText(value string) bool {
	if strings.Contains(value, "://") || strings.Contains(strings.ToLower(value), "www.") || imdbTitleID.MatchString(strings.ToLower(strings.TrimSpace(value))) {
		return false
	}
	useful := false
	for _, r := range value {
		if unicode.IsControl(r) {
			return false
		}
		useful = useful || unicode.IsLetter(r) || unicode.IsNumber(r)
	}
	return useful
}
