package sites

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Rousi's API key profile and web Authorization mailbox are independent.
// Profile conversion happens before mailbox IO, DTO validation afterwards.
type sourceRousiReader struct {
	c   *Client
	ctx context.Context
	s   *UserStats
	now time.Time

	messageBytes int
}

func sourceZero(v any) bool {
	if b, ok := v.(bool); ok {
		return !b
	}
	if n, ok := v.(json.Number); ok {
		f, e := n.Float64()
		return e == nil && f == 0
	}
	return false
}

func (r *sourceRousiReader) request(method, path string, query url.Values, headers map[string]string) ([]byte, int, error) {
	u := *r.c.base
	u.Path = "/" + path
	u.RawPath = ""
	u.RawQuery = query.Encode()
	u.Fragment = ""
	if t := sourceStatsTrace(r.ctx); t != nil {
		t.status = 0
	}
	b, _, e := r.c.request(r.ctx, method, &u, nil, headers, htmlLimit)
	status := 0
	if t := sourceStatsTrace(r.ctx); t != nil {
		status = t.status
		if t.guardError != nil {
			return nil, status, t.guardError
		}
	}
	if r.ctx.Err() != nil {
		return nil, status, r.ctx.Err()
	}
	if e != nil {
		if e.Error() == "site_body_limit" || e.Error() == "site_credential_origin_mismatch" {
			return nil, status, e
		}
		r.s.Warnings = append(r.s.Warnings, e.Error())
	}
	return b, status, nil
}

func rousiDate(v any, now time.Time) any {
	if s, ok := v.(string); ok && s != "" {
		return sourceNexusJoinedAt(s, now)
	}
	return v
}

func sourceRousiString(v any) (*string, error) {
	if v == nil {
		return nil, nil
	}
	s, ok := v.(string)
	if !ok {
		return nil, sourceModuleError(nil)
	}
	return &s, nil
}
func sourceRousiID(v any) (any, error) {
	switch x := v.(type) {
	case nil, string:
		return x, nil
	case bool:
		if x {
			return int64(1), nil
		}
		return int64(0), nil
	case json.Number:
		n, e := sourceInteger(x)
		if e != nil {
			return nil, sourceModuleError(nil)
		}
		if strings.ContainsAny(string(x), ".eE") {
			f, e := x.Float64()
			if e != nil || math.Trunc(f) != f || math.IsNaN(f) || math.IsInf(f, 0) {
				return nil, sourceModuleError(nil)
			}
		}
		if n.IsInt64() {
			return n.Int64(), nil
		}
		return json.Number(n.String()), nil
	}
	return nil, sourceModuleError(nil)
}

func (r *sourceRousiReader) messages(d *SourceUserData) error {
	token := r.c.config.MessageToken
	if token == "" {
		return nil
	}
	if !strings.HasPrefix(token, "Bearer ") {
		token = "Bearer " + token
	}
	headers := map[string]string{"Authorization": token, "Accept": "application/json, text/plain, */*", "User-Agent": r.c.config.UserAgent}
	rows := []any{}
	total := float64(0)
	for page := int64(0); page == 0 || float64(page) < total; page++ {
		if page >= 50 {
			return errors.New("site_account_pagination_limit")
		}
		b, status, e := r.request(http.MethodGet, "api/messages", url.Values{"page": {strconv.FormatInt(page, 10)}, "page_size": {"100"}, "unread_only": {"true"}}, headers)
		if e != nil {
			return e
		}
		var root map[string]any
		var data map[string]any
		if status != 200 {
			data = map[string]any{"messages": []any{}, "total_pages": json.Number("0")}
		} else {
			if !decodeJSON(b, &root) || root == nil {
				return sourceModuleError(nil)
			}
			if !sourceZero(root["code"]) {
				data = map[string]any{"messages": []any{}, "total_pages": json.Number("0")}
			} else {
				var ok bool
				data, ok = root["data"].(map[string]any)
				if !ok {
					return sourceModuleError(nil)
				}
			}
		}
		var list []any
		if v, exists := data["messages"]; exists {
			var ok bool
			list, ok = v.([]any)
			if !ok {
				return sourceModuleError(nil)
			}
		}
		rows = append(rows, list...)
		r.messageBytes += len(b)
		if len(rows) > 10000 || r.messageBytes > htmlLimit {
			return errors.New("site_message_contents_incomplete")
		}
		if page == 0 {
			if v, exists := data["total_pages"]; exists {
				switch v.(type) {
				case json.Number, bool:
					total, e = sourceFloat(v)
				default:
					return sourceModuleError(nil)
				}
				if e != nil {
					return e
				}
			}
		}
	}
	d.MessageUnread = int64(len(rows))
	r.s.MessageUnread = new(int64)
	*r.s.MessageUnread = d.MessageUnread
	r.s.Sources["message_unread"] = "api/messages"
	r.s.Messages = [][3]*string{}
	native := true
	for _, v := range rows {
		row, ok := v.(map[string]any)
		if !ok {
			return sourceModuleError(nil)
		}
		values := []any{row["title"], rousiDate(row["created_at"], r.now), row["content"]}
		d.Messages = append(d.Messages, values)
		var observed [3]*string
		for i, v := range values {
			p, e := sourceRousiString(v)
			if e != nil {
				native = false
			}
			observed[i] = p
		}
		if native {
			r.s.Messages = append(r.s.Messages, observed)
		}
	}
	if !native {
		r.s.Messages = nil
		r.s.Warnings = append(r.s.Warnings, "site_message_native_type_unknown")
	}
	r.s.Sources["message_unread_contents"] = "api/messages"
	// The original posts even for zero messages and ignores status/body.
	_, _, e := r.request(http.MethodPost, "api/messages/read-all", nil, headers)
	return e
}

func (c *Client) sourceRousiUserStats(ctx context.Context, options UserStatsOptions, now time.Time) (s UserStats, d *SourceUserData, err error) {
	s = UserStats{CapturedAt: now.UTC().Format(time.RFC3339), Sources: map[string]string{}, Warnings: []string{}}
	defer func() {
		if err != nil {
			v := err.Error()
			s.Error = &v
		}
	}()
	d = emptySourceUserData()
	if c.config.APIKey == "" && c.config.MessageToken == "" {
		return s, d, errors.New("site_api_key_required")
	}
	client := *c
	hc := *c.http
	hc.Timeout = 60 * time.Second
	client.http = &hc
	r := sourceRousiReader{c: &client, ctx: ctx, s: &s, now: now}
	b, status, e := r.request(http.MethodGet, "api/v1/profile", url.Values{"include_fields[user]": {"seeding_leeching_data"}}, map[string]string{"Content-Type": "application/json", "Accept": "application/json", "Authorization": "Bearer " + c.config.APIKey})
	if e != nil {
		return s, nil, e
	}
	var raw any
	var user map[string]any
	var message any
	if (status == 200 || status == 403 || status == 500) && len(b) > 0 && decodeJSON(b, &raw) {
		root, ok := raw.(map[string]any)
		if !ok {
			return s, nil, sourceModuleError(nil)
		}
		if !sourceZero(root["code"]) {
			message = "未知错误"
			if v, exists := root["message"]; exists {
				message = v
			}
		} else if sourceTruth(root["data"]) {
			user, ok = root["data"].(map[string]any)
			if !ok {
				return s, nil, sourceModuleError(nil)
			}
			sl := map[string]any{}
			if v, exists := user["seeding_leeching_data"]; exists {
				sl, ok = v.(map[string]any)
				if !ok {
					return s, nil, sourceModuleError(nil)
				}
			}
			for _, field := range []struct {
				key    string
				values map[string]any
				dst    *int64
			}{{"uploaded", user, &d.Upload}, {"downloaded", user, &d.Download}, {"seeding_count", sl, &d.Seeding}, {"seeding_size", sl, &d.SeedingSize}, {"leeching_count", sl, &d.Leeching}, {"leeching_size", sl, &d.LeechingSize}} {
				*field.dst, e = sourceUserInteger(field.values[field.key])
				if e != nil {
					return s, nil, e
				}
			}
			d.Ratio, e = sourceFloat(user["ratio"])
			if e != nil {
				return s, nil, e
			}
			d.Ratio, _ = strconv.ParseFloat(strconv.FormatFloat(d.Ratio, 'f', 2, 64), 64)
			d.Bonus, e = sourceFloat(user["karma"])
			if e != nil {
				return s, nil, e
			}
		}
	}
	if options.ReadMessages {
		if e = r.messages(d); e != nil {
			return s, nil, e
		}
	}
	// Match Indexer/Pydantic validation only after the parser's complete IO.
	if d.Error, e = sourceRousiString(message); e != nil {
		return s, nil, e
	}
	if user != nil {
		if d.UserID, e = sourceRousiID(user["id"]); e != nil {
			return s, nil, e
		}
		if d.Username, e = sourceRousiString(user["username"]); e != nil {
			return s, nil, e
		}
		level := user["level_text"]
		if !sourceTruth(level) {
			level = user["role_text"]
		}
		if d.UserLevel, e = sourceRousiString(level); e != nil {
			return s, nil, e
		}
		joined := rousiDate(user["registered_at"], now)
		if text, ok := joined.(string); ok && len([]rune(text)) >= 19 {
			joined = string([]rune(text)[:19])
		}
		if sourceTruth(joined) {
			if d.JoinAt, e = sourceRousiString(joined); e != nil {
				return s, nil, e
			}
		}
		if d.UserID != nil {
			s.UserID = sourceString(d.UserID)
		}
		if d.Username != nil {
			s.Username = *d.Username
			s.Sources["username"] = "api/v1/profile"
		}
		if d.UserLevel != nil {
			s.Level = *d.UserLevel
			s.Sources["level"] = "api/v1/profile"
		}
		if d.JoinAt != nil {
			s.JoinedAt = *d.JoinAt
		}
		sl, _ := user["seeding_leeching_data"].(map[string]any)
		for _, f := range []struct {
			key, name string
			values    map[string]any
			value     int64
			dst       **int64
		}{{"uploaded", "uploaded", user, d.Upload, &s.Uploaded}, {"downloaded", "downloaded", user, d.Download, &s.Downloaded}, {"seeding_count", "seeding", sl, d.Seeding, &s.Seeding}, {"seeding_size", "seeding_bytes", sl, d.SeedingSize, &s.SeedingBytes}, {"leeching_count", "leeching", sl, d.Leeching, &s.Leeching}, {"leeching_size", "leeching_bytes", sl, d.LeechingSize, &s.LeechingBytes}} {
			if v, exists := f.values[f.key]; exists && v != nil && f.value >= 0 {
				value := f.value
				*f.dst = &value
				s.Sources[f.name] = "api/v1/profile:" + f.key
			}
		}
		if v, ok := user["ratio"]; ok && v != nil {
			value := d.Ratio
			s.Ratio = &value
			s.Sources["ratio"] = "api/v1/profile:ratio"
		}
		if v, ok := user["karma"]; ok && v != nil {
			value := d.Bonus
			s.Bonus = &value
			s.Sources["bonus"] = "api/v1/profile:karma"
		}
	}
	if status != 200 && status != 403 && status != 500 {
		return s, d, errors.New("site_http_error")
	}
	if d.Error != nil && *d.Error != "" {
		return s, d, errors.New("site_authentication_failed")
	}
	if s.UserID == "" || s.Username == "" {
		return s, d, errors.New("site_user_identity_missing")
	}
	return s, d, nil
}
