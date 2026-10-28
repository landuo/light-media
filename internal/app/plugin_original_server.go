package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
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
// No default remote is contacted by this independent Go application.
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
	client, err := a.originalPluginHTTPClient(timeout, false)
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
	body, err := io.ReadAll(io.LimitReader(res.Body, (2<<20)+1))
	if err != nil || len(body) > 2<<20 || res.StatusCode != http.StatusOK {
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
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || fields == nil {
		return errors.New("invalid rating object")
	}
	*rating = originalPluginRating{RatingCount: json.Number("0")}
	id, exists := fields["plugin_id"]
	if !exists || bytes.Equal(bytes.TrimSpace(id), []byte("null")) || json.Unmarshal(id, &rating.PluginID) != nil {
		return errors.New("missing rating plugin ID")
	}
	var err error
	if value, exists := fields["average_rating"]; exists {
		rating.AverageRating, err = originalRatingFloat(value)
		if err != nil {
			return err
		}
	}
	if value, exists := fields["rating_count"]; exists {
		rating.RatingCount, err = originalRatingInteger(value)
		if err != nil {
			return err
		}
	}
	if value, exists := fields["user_rating"]; exists && !bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
		parsed, err := originalRatingFloat(value)
		if err != nil {
			return err
		}
		rating.UserRating = &parsed
	}
	return nil
}

func validOriginalPluginRating(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0) && value >= 0.1 && value <= 5 && math.Abs(value-math.Round(value/0.1)*0.1) <= 1e-9
}

func originalPluginServerRoute(path, method string) bool {
	return method == http.MethodGet && (path == "statistic" || path == "rating" || strings.HasPrefix(path, "rating/")) || method == http.MethodPost && strings.HasPrefix(path, "rating/")
}

func (a *App) originalPluginServerHTTP(w http.ResponseWriter, r *http.Request, path string) (any, error) {
	invalid := func() (any, error) {
		return nil, &APIError{"PLUGIN_SERVER_INVALID_RESPONSE", "插件统计与评分服务返回了无效数据", http.StatusBadGateway}
	}
	if path == "statistic" {
		if !originalPluginStatisticShared(a.Settings()) {
			return map[string]int64{}, nil
		}
		body, err := a.requestOriginalPluginServer(r.Context(), http.MethodGet, "/plugin/statistic", nil, nil, 10*time.Second)
		if err != nil {
			return map[string]any{}, nil
		}
		var values map[string]json.RawMessage
		if json.Unmarshal(body, &values) != nil || values == nil {
			return invalid()
		}
		return values, nil
	}
	if path == "rating" {
		query := url.Values{}
		if raw, exists := r.URL.Query()["plugin_ids"]; exists {
			ids := strings.Split(raw[len(raw)-1], ",")
			query.Set("plugin_ids", strings.Join(ids, ","))
		}
		body, err := a.requestOriginalPluginServer(r.Context(), http.MethodGet, "/plugin/rating", query, nil, 10*time.Second)
		if err != nil {
			return map[string]originalPluginRating{}, nil
		}
		var ratings map[string]originalPluginRating
		if !json.Valid(body) {
			return map[string]originalPluginRating{}, nil
		}
		if json.Unmarshal(body, &ratings) != nil || ratings == nil {
			return invalid()
		}
		return ratings, nil
	}
	id := strings.TrimPrefix(path, "rating/")
	if id == "" || strings.Contains(id, "/") {
		return nil, bad("INVALID_PLUGIN_ID", "插件标识无效")
	}
	var payload any
	if r.Method == http.MethodPost {
		var input struct {
			Rating json.RawMessage `json:"rating"`
		}
		r.Body = http.MaxBytesReader(w, r.Body, 2<<20)
		decoder := json.NewDecoder(r.Body)
		if decoder.Decode(&input) != nil || decoder.Decode(new(any)) != io.EOF {
			return nil, bad("INVALID_PLUGIN_JSON", "插件评分请求无效")
		}
		rating, parseErr := originalRatingFloat(input.Rating)
		if parseErr != nil || !validOriginalPluginRating(rating) {
			return nil, bad("INVALID_PLUGIN_RATING", "评分须为0.1至5.0，步长0.1")
		}
		in, err := a.pluginStore.Installation(r.Context(), id)
		if err != nil || !in.Installed {
			return nil, bad("PLUGIN_NOT_INSTALLED", "插件未安装，无法评分")
		}
		payload = map[string]any{"rating": rating}
	}
	body, err := a.requestOriginalPluginServer(r.Context(), r.Method, "/plugin/rating/"+url.PathEscape(id), nil, payload, 10*time.Second)
	if err != nil {
		if r.Method == http.MethodPost {
			return map[string]any{"success": false, "message": "连接MoviePilot服务器失败"}, nil
		}
		return originalPluginRating{PluginID: id, RatingCount: json.Number("0")}, nil
	}
	if r.Method == http.MethodPost {
		if !json.Valid(body) || bytes.Equal(bytes.TrimSpace(body), []byte("null")) {
			return map[string]any{"success": false, "message": "连接MoviePilot服务器失败"}, nil
		}
		return map[string]any{"success": true, "data": json.RawMessage(body)}, nil
	}
	if !json.Valid(body) {
		return originalPluginRating{PluginID: id, RatingCount: json.Number("0")}, nil
	}
	var rating originalPluginRating
	if json.Unmarshal(body, &rating) != nil {
		return invalid()
	}
	return rating, nil
}

func originalPluginReportedRepository(raw string) any {
	if raw == "" {
		return nil
	}
	if strings.HasPrefix(raw, "local://") {
		u, err := url.Parse(raw)
		if err != nil {
			return nil
		}
		query := url.Values{}
		if version := u.Query().Get("version"); version != "" {
			query.Set("version", version)
		}
		return (&url.URL{Scheme: "local", Host: u.Host, RawQuery: query.Encode()}).String()
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
