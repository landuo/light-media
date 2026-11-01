package catalog

import (
	"context"
	"encoding/json"
	"net/url"
	"strconv"
)

// SourceRecognitionDetail 读取显式识别所需的完整原始 provider 响应。
// 它复用固定官方 HTTPS 请求边界，但故意不先构造 UI Media：源在未知类型时
// 先取得 TV/movie 两个字典再消歧，不能让被丢弃结果的 compact 投影改变分支。
// provider augmentation、原缓存/fresh 与 MediaInfo 构造属于调用层，不在此伪造。
func (c *Client) SourceRecognitionDetail(ctx context.Context, kind string, id int64) (json.RawMessage, error) {
	if !kindOK(kind) || !validID(id) {
		return nil, &Error{Code: "invalid_input"}
	}
	appendFields := "images,credits,alternative_titles,translations,release_dates,external_ids"
	if kind == "tv" {
		appendFields = "images,credits,alternative_titles,translations,content_ratings,external_ids,episode_groups"
	}
	var raw json.RawMessage
	if err := c.get(ctx, kind+"/"+strconv.FormatInt(id, 10), url.Values{"append_to_response": {appendFields}}, &raw); err != nil {
		return nil, err
	}
	return raw, nil
}
