package app

import (
	"bytes"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
)

// Kept as JSON text so MoviePilot's free-form note survives an export without
// making configuration snapshots share mutable maps or slices.
type siteJSON string

func (s siteJSON) MarshalJSON() ([]byte, error) {
	if s == "" {
		return []byte("null"), nil
	}
	return []byte(s), nil
}
func (s *siteJSON) UnmarshalJSON(raw []byte) error {
	if !json.Valid(raw) {
		return errors.New("invalid site note")
	}
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		*s = ""
	} else {
		*s = siteJSON(raw)
	}
	return nil
}

type SiteOptions struct {
	Domain        string   `json:"domain,omitempty"`
	Token         string   `json:"token,omitempty"`
	RSS           string   `json:"rss,omitempty"`
	Downloader    string   `json:"downloader,omitempty"`
	Filter        string   `json:"filter,omitempty"`
	Proxy         bool     `json:"proxy"`
	Render        bool     `json:"render"`
	Public        bool     `json:"public"`
	Priority      int      `json:"pri"`
	LimitInterval int      `json:"limit_interval"`
	LimitCount    int      `json:"limit_count"`
	LimitSeconds  int      `json:"limit_seconds"`
	Note          siteJSON `json:"note,omitempty"`
}

// MoviePilot's edit controls can serialize flags as booleans or integers and
// number inputs as strings. Normalize those forms before decoding the model.
func (s *SiteConfig) UnmarshalJSON(raw []byte) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil || fields == nil {
		return errors.New("invalid site configuration")
	}
	for target, alias := range map[string]string{"base_url": "url", "api_key": "apikey", "enabled": "is_active", "ua": "user_agent"} {
		if _, exists := fields[target]; !exists && fields[alias] != nil {
			fields[target] = fields[alias]
		}
	}
	for _, key := range []string{"enabled", "proxy", "render", "public"} {
		if value, ok := fields[key]; ok {
			parsed, err := siteImportBool(value)
			if err != nil {
				return err
			}
			fields[key], _ = json.Marshal(parsed)
		}
	}
	for _, key := range []string{"daily_budget", "pri", "timeout", "limit_interval", "limit_count", "limit_seconds"} {
		if value, ok := fields[key]; ok {
			parsed, err := siteImportInt(value)
			if err != nil {
				return err
			}
			fields[key], _ = json.Marshal(parsed)
		}
	}
	type plain SiteConfig
	normalized, _ := json.Marshal(fields)
	return json.Unmarshal(normalized, (*plain)(s))
}

func siteImportInt(raw json.RawMessage) (int, error) {
	value := strings.TrimSpace(string(raw))
	if value == "null" || value == `""` {
		return 0, nil
	}
	if len(value) > 0 && value[0] == '"' {
		if err := json.Unmarshal(raw, &value); err != nil {
			return 0, errors.New("invalid site number")
		}
		value = strings.TrimSpace(value)
	}
	number, err := strconv.Atoi(value)
	if err != nil {
		return 0, errors.New("invalid site number")
	}
	return number, nil
}

func siteImportBool(raw json.RawMessage) (bool, error) {
	value := strings.TrimSpace(string(raw))
	if len(value) > 0 && value[0] == '"' {
		if err := json.Unmarshal(raw, &value); err != nil {
			return false, errors.New("invalid site flag")
		}
	}
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "true", "1":
		return true, nil
	case "false", "0", "null", "":
		return false, nil
	default:
		return false, errors.New("invalid site flag")
	}
}

func siteSecretState(s SiteConfig) map[string]bool {
	return map[string]bool{"cookie": s.Cookie != "", "api_key": s.APIKey != "", "token": s.Token != "", "rss": s.RSS != "", "note": s.Note != ""}
}

func hideSiteSecrets(s *SiteConfig) {
	s.Cookie, s.APIKey, s.Token, s.RSS, s.Note = "", "", "", "", ""
}

func preserveSiteSecrets(in *SiteConfig, old SiteConfig) {
	if in.Cookie == "" {
		in.Cookie = old.Cookie
	}
	if in.APIKey == "" {
		in.APIKey = old.APIKey
	}
	if in.Token == "" {
		in.Token = old.Token
	}
	if in.RSS == "" {
		in.RSS = old.RSS
	}
	if in.Note == "" {
		in.Note = old.Note
	}
}
