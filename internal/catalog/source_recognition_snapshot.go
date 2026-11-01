package catalog

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"light-media/internal/logging"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// SourceRecognitionSnapshot 保存已验证 JSON 的原响应，不把标准错误 JSON 丢弃。
// 识别请求缓存发生在源 _handle_errors 之前；Header/Body 只在宿主内存中使用。
type SourceRecognitionSnapshot struct {
	Body    json.RawMessage
	Headers http.Header
	Status  int
}

// Clone 返回独立响应快照，避免后续原字段增强污染缓存。
func (s *SourceRecognitionSnapshot) Clone() *SourceRecognitionSnapshot {
	if s == nil {
		return nil
	}
	return &SourceRecognitionSnapshot{Body: append(json.RawMessage(nil), s.Body...), Headers: s.Headers.Clone(), Status: s.Status}
}

// SourceRecognitionDetailSnapshot 仅读取固定电影或电视剧详情。
func (c *Client) SourceRecognitionDetailSnapshot(ctx context.Context, kind string, id int64, guard func() error) (*SourceRecognitionSnapshot, error) {
	if !kindOK(kind) || !validID(id) {
		return nil, &Error{Code: "invalid_input"}
	}
	fields := "images,credits,alternative_titles,translations,release_dates,external_ids"
	if kind == "tv" {
		fields = "images,credits,alternative_titles,translations,content_ratings,external_ids,episode_groups"
	}
	return c.sourceRecognitionSnapshot(ctx, kind+"/"+strconv.FormatInt(id, 10), url.Values{"append_to_response": {fields}}, guard)
}

// SourceRecognitionGroupSnapshot 仅读取 TMDB 的固定剧集组路径。
// 组 ID 属当前有限 path-segment 域，不开放任意 URL 或路径。
func (c *Client) SourceRecognitionGroupSnapshot(ctx context.Context, id string, guard func() error) (*SourceRecognitionSnapshot, error) {
	if id == "" || len(id) > 256 {
		return nil, &Error{Code: "invalid_input"}
	}
	for _, r := range id {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-') {
			return nil, &Error{Code: "invalid_input"}
		}
	}
	return c.sourceRecognitionSnapshot(ctx, "tv/episode_group/"+id, nil, guard)
}

// 此识别专用读取复用现有 client、超时、并发槽、凭据与官方 origin，不改旧 get。
// 使用新独立 recognition session；有效对象/数组错误 JSON 在源错误处理前保留。
func (c *Client) sourceRecognitionSnapshot(ctx context.Context, path string, query url.Values, guard func() error) (*SourceRecognitionSnapshot, error) {
	if ctx == nil || guard == nil {
		return nil, &Error{Code: "invalid_input"}
	}
	if err := guard(); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, requestLimit)
	defer cancel()
	if c == nil {
		return nil, &Error{Code: "not_configured"}
	}
	select {
	case c.inflight <- struct{}{}:
		defer func() { <-c.inflight }()
	case <-ctx.Done():
		return nil, &Error{Code: "canceled"}
	}
	if err := c.wait(ctx); err != nil {
		return nil, err
	}
	values := url.Values{}
	for k, v := range query {
		values[k] = append([]string(nil), v...)
	}
	values.Set("language", "zh-CN")
	if c.credentials.APIKey != "" {
		values.Set("api_key", c.credentials.APIKey)
	}
	ctx, _ = logging.Target(ctx, "metadata")
	u := url.URL{Scheme: "https", Host: apiHost, Path: "/3/" + path, RawQuery: values.Encode()}
	req, err := http.NewRequestWithContext(logging.EndpointContext(ctx, "metadata", u.String()), http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, &Error{Code: "invalid_input"}
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "light-media/0.1")
	if c.credentials.AccessToken != "" {
		req.Header.Set("Authorization", "Bearer "+c.credentials.AccessToken)
	}
	if err := guard(); err != nil {
		return nil, err
	}
	client, err := c.recognition.acquire()
	if err != nil {
		return nil, err
	}
	response, err := client.Do(req)
	if sourceRecognitionDefiniteNoResponse(ctx, response, err) {
		if ctx.Err() != nil {
			return nil, &Error{Code: "canceled"}
		}
		if guardErr := guard(); guardErr != nil {
			return nil, guardErr
		}
		client, rebuildErr := c.recognition.rebuild(client)
		if rebuildErr != nil {
			return nil, rebuildErr
		}
		if ctx.Err() != nil {
			return nil, &Error{Code: "canceled"}
		}
		if guardErr := guard(); guardErr != nil {
			return nil, guardErr
		}
		response, err = client.Do(req.Clone(ctx)) // One retry only; never a loop or another origin.
	}
	if err != nil {
		if ctx.Err() != nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, &Error{Code: "canceled"}
		}
		if sourceRecognitionDefiniteNoResponse(ctx, response, err) {
			return nil, &Error{Code: "unavailable"}
		}
		// Generic proxy/policy errors are not proof of source RequestException.
		// No retry and no successful None conversion at the App boundary.
		return nil, &Error{Code: "recognition_transport_unknown"}
	}
	defer response.Body.Close()
	if response.StatusCode >= 300 && response.StatusCode < 400 {
		return nil, &Error{Code: "redirect_blocked", StatusCode: response.StatusCode}
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxBody+1))
	if err != nil {
		if ctx.Err() != nil {
			return nil, &Error{Code: "canceled"}
		}
		return nil, &Error{Code: "unavailable"}
	}
	if len(body) > maxBody {
		return nil, &Error{Code: "recognition_response_limit"}
	}
	body = bytes.TrimSpace(body)
	if len(body) == 0 || !strings.ContainsRune("{[", rune(body[0])) || !json.Valid(body) {
		return nil, &Error{Code: "invalid_response"}
	}
	if err := guard(); err != nil {
		return nil, err
	}
	return &SourceRecognitionSnapshot{Body: append(json.RawMessage(nil), body...), Headers: response.Header.Clone(), Status: response.StatusCode}, nil
}
