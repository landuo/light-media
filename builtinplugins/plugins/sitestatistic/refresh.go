package sitestatistic

import (
	"bytes"
	"context"
	"fmt"
)

type Response struct {
	Success     bool   `json:"success"`
	Message     string `json:"message"`
	MessageI18n string `json:"message_i18n"`
	Data        any    `json:"data"`
}

func sourceResponse(success bool, message string, data any) Response {
	if data == nil {
		data = map[string]any{}
	}
	return Response{success, message, message, data}
}
func RefreshByDomain(ctx context.Context, h Host, domain string, keyMatches bool) (Response, error) {
	if !keyMatches {
		return sourceResponse(false, "API密钥错误", nil), nil
	}
	site, e := h.Indexer(ctx, domain)
	if e != nil {
		return Response{}, e
	}
	if len(site) == 0 {
		return sourceResponse(false, fmt.Sprintf("站点 %s 不存在", domain), nil), nil
	}
	dto, e := h.Refresh(ctx, site)
	if e != nil {
		return Response{}, e
	}
	if len(dto) > 0 && !bytes.Equal(bytes.TrimSpace(dto), []byte("null")) {
		return sourceResponse(true, fmt.Sprintf("站点 %s 刷新成功", domain), dto), nil
	}
	return sourceResponse(false, fmt.Sprintf("站点 %s 刷新数据失败，未获取到数据", domain), nil), nil
}
