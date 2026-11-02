package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const originalPluginServerDefaultHost = "https://movie-pilot.org"

func originalPluginDefaultServerURL(lookup func(string) (string, bool)) string {
	if value, present := lookup("MP_SERVER_HOST"); present {
		return strings.TrimSpace(value)
	}
	return originalPluginServerDefaultHost
}

// This implements the observable MoviePilotServerHelper HTTP contract, not
// the closed-source center's identity, authorization or aggregation algorithms.
// Runtime calls use the configured source-compatible endpoint and sharing gate.
func originalPluginStatisticShared(settings Settings) bool {
	return settings.PluginStatisticShare == nil || *settings.PluginStatisticShare
}

func originalPluginServerURL(raw string) (*url.URL, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, bad("INVALID_PLUGIN_SERVER", "插件统计服务地址须为不含账号、查询参数或片段的 HTTP(S) 地址")
	}
	return u, nil
}

func (a *App) originalPluginServerState(ctx context.Context) error {
	_, err := a.Store.DB.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS plugin_market_server_state (key TEXT PRIMARY KEY, value TEXT NOT NULL)`)
	return err
}

func (a *App) requestOriginalPluginServer(ctx context.Context, method, path string, query url.Values, payload any, timeout time.Duration) ([]byte, error) {
	rawURL := *a.Settings().PluginServerURL
	if rawURL == "" {
		return nil, &APIError{"PLUGIN_SERVER_UNCONFIGURED", "尚未配置插件统计与评分服务", http.StatusServiceUnavailable}
	}
	target, err := originalPluginServerURL(rawURL)
	if err != nil {
		return nil, err
	}
	target.RawPath = strings.TrimRight(target.EscapedPath(), "/") + path
	target.Path, err = url.PathUnescape(target.RawPath)
	if err != nil {
		return nil, bad("INVALID_PLUGIN_SERVER", "插件统计与评分服务路径无效")
	}
	target.RawQuery = query.Encode()
	uid, err := a.originalPluginServerUID(ctx)
	if err != nil {
		return nil, err
	}
	var data []byte
	if payload != nil {
		data, err = json.Marshal(payload)
		if err != nil {
			return nil, err
		}
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, method, target.String(), bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	if uid != "" {
		req.Header.Set("X-MoviePilot-User-Uid", uid)
	}
	req.Header.Set("User-Agent", originalPluginServerUserAgent())
	req.Header.Set("Accept", "*/*")
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	client, err := a.originalPluginHTTPClientWithBase(timeout, false, originalPluginSourceTLSBase())
	if err != nil {
		return nil, &APIError{"PLUGIN_SERVER_UNAVAILABLE", "插件统计与评分服务代理配置无效", http.StatusBadGateway}
	}
	defer client.CloseIdleConnections()
	port := func(value *url.URL) string {
		if value.Port() != "" {
			return value.Port()
		}
		if value.Scheme == "https" {
			return "443"
		}
		return "80"
	}
	client.CheckRedirect = func(next *http.Request, via []*http.Request) error {
		if len(via) > 20 {
			return errors.New("plugin server redirect limit reached")
		}
		if next.URL.Scheme != target.Scheme || !strings.EqualFold(next.URL.Hostname(), target.Hostname()) || port(next.URL) != port(target) {
			return errors.New("plugin server identity cannot leave configured origin")
		}
		return nil
	}
	res, err := client.Do(req)
	if err != nil {
		return nil, &APIError{"PLUGIN_SERVER_UNAVAILABLE", "插件统计与评分服务连接失败", http.StatusBadGateway}
	}
	defer res.Body.Close()
	body, err := readOriginalPluginCenterBody(ctx, res.Body)
	if err != nil || res.StatusCode != http.StatusOK {
		return nil, &APIError{"PLUGIN_SERVER_UNAVAILABLE", "插件统计与评分服务返回失败", http.StatusBadGateway}
	}
	return body, nil
}

type originalPluginRating struct {
	PluginID      string      `json:"plugin_id"`
	AverageRating float64     `json:"average_rating"`
	RatingCount   json.Number `json:"rating_count"`
	UserRating    *float64    `json:"user_rating"`
}

func (rating *originalPluginRating) UnmarshalJSON(raw []byte) error {
	value, err := parseOriginalCenterJSON(raw)
	if err != nil {
		return err
	}
	return rating.fromCenterValue(value)
}
func (rating *originalPluginRating) fromCenterValue(value any) error {
	fields, ok := value.(map[string]any)
	if !ok {
		return errors.New("invalid rating object")
	}
	*rating = originalPluginRating{RatingCount: json.Number("0")}
	id, ok := fields["plugin_id"].(string)
	if !ok {
		return errors.New("missing rating plugin ID")
	}
	rating.PluginID = id
	if value, exists := fields["average_rating"]; exists {
		v, kind := originalCenterFloat(value)
		if kind != "" {
			return errors.New("invalid average rating")
		}
		rating.AverageRating = v
	}
	if value, exists := fields["rating_count"]; exists {
		raw, err := json.Marshal(value)
		if err != nil {
			return errors.New("invalid rating count")
		}
		rating.RatingCount, err = originalRatingInteger(raw)
		if err != nil {
			return err
		}
	}
	if value, exists := fields["user_rating"]; exists && value != nil {
		v, kind := originalCenterFloat(value)
		if kind != "" {
			return errors.New("invalid user rating")
		}
		rating.UserRating = &v
	}
	return nil
}
func (rating originalPluginRating) MarshalJSON() ([]byte, error) {
	average, _ := originalCenterJSONValue(rating.AverageRating)
	var user any
	if rating.UserRating != nil {
		user, _ = originalCenterJSONValue(*rating.UserRating)
	}
	return json.Marshal(struct {
		PluginID      string      `json:"plugin_id"`
		AverageRating any         `json:"average_rating"`
		RatingCount   json.Number `json:"rating_count"`
		UserRating    any         `json:"user_rating"`
	}{rating.PluginID, average, rating.RatingCount, user})
}

func validOriginalPluginRating(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0) && value >= 0.1 && value <= 5 && math.Abs(value-math.Round(value/0.1)*0.1) <= 1e-9
}

func originalPluginServerRoute(path, method string) bool {
	return method == http.MethodGet && (path == "statistic" || path == "rating" || strings.HasPrefix(path, "rating/")) || method == http.MethodPost && strings.HasPrefix(path, "rating/")
}

func (a *App) originalPluginServerHTTP(w http.ResponseWriter, r *http.Request, path string) (any, error) {
	invalid := func() (any, error) {
		return nil, originalPluginServerFailure()
	}
	if path == "statistic" {
		if !originalPluginStatisticShared(a.Settings()) {
			return map[string]int64{}, nil
		}
		body, err := a.requestOriginalPluginServer(r.Context(), http.MethodGet, "/plugin/statistic", nil, nil, 10*time.Second)
		if err != nil {
			return map[string]any{}, nil
		}
		value, err := parseOriginalCenterJSON(body)
		values, ok := value.(map[string]any)
		if err != nil || !ok {
			return invalid()
		}
		output, err := originalCenterJSONValue(values)
		if err != nil {
			return invalid()
		}
		return output, nil
	}
	if path == "rating" {
		query := url.Values{}
		// Only the fixed versioned adapter adopts source QueryParams decoding.
		// Native extensions retain their established URL.Query contract.
		queryValue, present := "", false
		if r.URL.Path == "/api/v1/plugin/rating" || r.URL.Path == "/api/v2/plugin/rating" {
			queryValue, present = originalPluginServerQuery(r.URL.RawQuery, "plugin_ids")
		} else if raw, exists := r.URL.Query()["plugin_ids"]; exists {
			queryValue, present = raw[len(raw)-1], true
		}
		if present {
			ids := strings.Split(queryValue, ",")
			query.Set("plugin_ids", strings.Join(ids, ","))
		}
		body, err := a.requestOriginalPluginServer(r.Context(), http.MethodGet, "/plugin/rating", query, nil, 10*time.Second)
		if err != nil {
			return map[string]originalPluginRating{}, nil
		}
		value, err := parseOriginalCenterJSON(body)
		if err != nil {
			return map[string]originalPluginRating{}, nil
		}
		fields, ok := value.(map[string]any)
		if !ok {
			return invalid()
		}
		ratings := make(map[string]originalPluginRating, len(fields))
		for id, value := range fields {
			var rating originalPluginRating
			if rating.fromCenterValue(value) != nil {
				return invalid()
			}
			ratings[id] = rating
		}
		return ratings, nil
	}
	id := strings.TrimPrefix(path, "rating/")
	if id == "" || strings.Contains(id, "/") {
		return nil, bad("INVALID_PLUGIN_ID", "插件标识无效")
	}
	var payload any
	if r.Method == http.MethodPost {
		rating, err := originalPluginRatingRequest(w, r)
		if err != nil {
			return nil, err
		}
		ids, err := a.originalInstalledPluginIDs(r.Context())
		if err != nil {
			return nil, err
		}
		installed := false
		for _, candidate := range ids {
			if candidate == id {
				installed = true
				break
			}
		}
		if !installed {
			return nil, &originalPluginServerHTTPError{400, map[string]any{"success": false, "message": "插件 " + id + " 未安装，无法评分", "data": map[string]any{}}, false}
		}
		payload = map[string]any{"rating": rating}
	}
	body, err := a.requestOriginalPluginServer(r.Context(), r.Method, "/plugin/rating/"+strings.ReplaceAll(url.QueryEscape(id), "+", "%20"), nil, payload, 10*time.Second)
	if err != nil {
		if r.Method == http.MethodPost {
			return originalPluginCenterResponse(false, "连接MoviePilot服务器失败", map[string]any{}), nil
		}
		return originalPluginRating{PluginID: id, RatingCount: json.Number("0")}, nil
	}
	value, parseErr := parseOriginalCenterJSON(body)
	if r.Method == http.MethodPost {
		if parseErr != nil || value == nil {
			return originalPluginCenterResponse(false, "连接MoviePilot服务器失败", map[string]any{}), nil
		}
		output, err := originalCenterJSONValue(value)
		if err != nil {
			return invalid()
		}
		return originalPluginCenterResponse(true, "", output), nil
	}
	if parseErr != nil {
		return originalPluginRating{PluginID: id, RatingCount: json.Number("0")}, nil
	}
	var rating originalPluginRating
	if rating.fromCenterValue(value) != nil {
		return invalid()
	}
	return rating, nil
}

func originalPluginReportedRepository(raw string) any {
	if raw == "" {
		return ""
	}
	if strings.HasPrefix(raw, "local://") {
		return originalPluginLocalReportRepository(raw)
	}
	u, err := url.Parse(raw)
	if err != nil {
		return nil
	}
	u.User, u.RawQuery, u.Fragment = nil, "", ""
	return u.String()
}

func (a *App) reportOriginalPluginInstallation(ctx context.Context, id, repository string) {
	settings := a.Settings()
	if *settings.PluginServerURL == "" || !originalPluginStatisticShared(settings) {
		return
	}
	_, err := a.requestOriginalPluginServer(ctx, http.MethodPost, "/plugin/install/"+url.PathEscape(id), nil, map[string]any{"plugin_id": id, "repo_url": originalPluginReportedRepository(repository)}, 5*time.Second)
	if err != nil {
		a.log("WARN", "plugins", "install_report_failed", "插件安装完成，但安装统计上报失败", "", "")
	}
}

func (a *App) reportOriginalPluginInstallations(ctx context.Context) {
	settings := a.Settings()
	if *settings.PluginServerURL == "" || !originalPluginStatisticShared(settings) {
		return
	}
	if err := a.originalPluginServerState(ctx); err != nil {
		return
	}
	key := "install_report"
	var completed string
	if err := a.Store.DB.QueryRowContext(ctx, `SELECT value FROM plugin_market_server_state WHERE key=?`, key).Scan(&completed); err == nil && completed == "1" {
		return
	}
	ids, err := a.originalInstalledPluginIDs(ctx)
	if err != nil || len(ids) == 0 {
		return
	}
	values := make([]map[string]any, 0, len(ids))
	for _, id := range ids {
		values = append(values, map[string]any{"plugin_id": id, "repo_url": nil})
	}
	if _, err = a.requestOriginalPluginServer(ctx, http.MethodPost, "/plugin/install", nil, map[string]any{"plugins": values}, 5*time.Second); err != nil {
		return
	}
	// This marks receipt by the configured endpoint, not proof of center-side
	// deduplication or aggregation, which is outside the observable client API.
	a.Store.DB.ExecContext(ctx, `INSERT OR REPLACE INTO plugin_market_server_state(key,value) VALUES(?, '1')`, key)
}
