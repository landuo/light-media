package sitemessageautoread

import (
	"context"
	"encoding/json"

	sdk "light-media/builtinplugins/internal/sdk"
)

type formNode struct {
	Component string         `json:"component"`
	Props     map[string]any `json:"props,omitempty"`
	Content   []formNode     `json:"content,omitempty"`
}

func formColumn(width int, component string, props map[string]any) formNode {
	return formNode{
		Component: "VCol",
		Props:     map[string]any{"cols": 12, "md": width},
		Content:   []formNode{{Component: component, Props: props}},
	}
}

func getForm(ctx context.Context, host sdk.HostCaller) (json.RawMessage, error) {
	sites, err := (sdk.Sites{Host: host}).Read(ctx, sdk.SiteHostRequestV1{ActiveOnly: true, OrderByPriority: true})
	if err != nil {
		return nil, err
	}
	options := make([]map[string]string, 0, len(sites.Items))
	for _, site := range sites.Items {
		options = append(options, map[string]string{"title": site.Name, "value": site.ID})
	}
	nodes := []formNode{{Component: "VForm", Content: []formNode{
		{Component: "VRow", Content: []formNode{
			formColumn(6, "VSwitch", map[string]any{"model": "enabled", "label": "启用定时处理"}),
			formColumn(6, "VSwitch", map[string]any{"model": "onlyonce", "label": "立即运行一次"}),
		}},
		{Component: "VRow", Content: []formNode{
			formColumn(6, "VSelect", map[string]any{
				"model": "sites", "label": "处理站点", "multiple": true, "chips": true, "clearable": true, "variant": "outlined", "items": options,
			}),
			formColumn(6, "VTextField", map[string]any{
				"model": "cron", "label": "执行周期（Cron）", "placeholder": defaultCron, "variant": "outlined",
			}),
		}},
		{Component: "VRow", Content: []formNode{
			formColumn(12, "VTextarea", map[string]any{
				"model": "titles", "label": "自动已读的短讯标题", "rows": 5, "variant": "outlined",
				"hint": "每行填写一个完整标题。匹配时忽略首尾空白，区分大小写。", "persistent-hint": true,
			}),
		}},
		{Component: "VRow", Content: []formNode{
			formColumn(12, "VAlert", map[string]any{
				"type": "info", "variant": "tonal", "text": "开启定时处理后，所选站点的自动已读由本插件接管。",
			}),
		}},
	}}}
	return json.Marshal([]any{nodes, Config{Cron: defaultCron, Sites: []string{}}})
}
