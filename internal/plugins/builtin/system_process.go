package builtin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"

	"light-media/internal/plugins"
)

type SystemProcessConfiguration struct {
	Enable                 bool            `json:"enable"`
	DashboardWidgetSize    json.RawMessage `json:"dashboard_widget_size,omitempty"`
	DashboardWidgetRefresh json.RawMessage `json:"dashboard_widget_refresh,omitempty"`
}

func pluginConfigNumber(raw json.RawMessage, fallback float64) (float64, error) {
	if len(raw) == 0 || string(raw) == "null" || string(raw) == `""` {
		return fallback, nil
	}
	var text string
	if json.Unmarshal(raw, &text) == nil {
		value, err := strconv.ParseFloat(text, 64)
		if err != nil || math.IsInf(value, 0) || math.IsNaN(value) {
			return 0, errors.New("invalid process display value")
		}
		return value, nil
	}
	var value float64
	if json.Unmarshal(raw, &value) != nil {
		return 0, errors.New("invalid process display value")
	}
	return value, nil
}

func (c SystemProcessConfiguration) Display() (int, float64, error) {
	size, err := pluginConfigNumber(c.DashboardWidgetSize, 8)
	if err != nil || (size != 4 && size != 6 && size != 8 && size != 12) {
		return 0, 0, errors.New("invalid dashboard size")
	}
	refresh, err := pluginConfigNumber(c.DashboardWidgetRefresh, 0)
	if err != nil || refresh < 0 || refresh > 86400 {
		return 0, 0, errors.New("invalid dashboard refresh")
	}
	return int(size), refresh, nil
}

type ProcessInfo struct {
	PID        int32   `json:"pid"`
	Name       string  `json:"name"`
	CPU        float64 `json:"cpu"`
	Memory     float64 `json:"memory"`
	CreateTime float64 `json:"create_time"`
	RunTime    int64   `json:"run_time"`
}

func SystemProcessManifest() plugins.Manifest {
	return plugins.Manifest{SchemaVersion: 1, PluginID: "SystemProcess", Name: "系统进程", Description: "查看系统进程，支持仪表板", Icon: "Dsfinder_A.png", Version: "1.1", Author: "hotlcc", AuthorURL: "https://github.com/hotlcc", Runtime: "go-executable", Entry: "light-media", HostAPIMin: 1, HostAPIMax: 1, Methods: []string{"config_form", "invoke", "page", "dashboard", "stop"}, Capabilities: []string{"process.list"}, HasPage: true, Dashboards: []string{"default"}}
}

const systemProcessForm = `[{"component":"VForm","content":[
 {"component":"VRow","content":[{"component":"VCol","props":{"cols":12,"xxl":4,"xl":4,"lg":4,"md":4,"sm":6,"xs":12},"content":[{"component":"VSwitch","props":{"model":"enable","label":"启用插件","hint":"插件总开关"}}]}]},
 {"component":"VRow","content":[{"component":"VCol","props":{"cols":12,"xxl":4,"xl":4,"lg":4,"md":4,"sm":6,"xs":12},"content":[{"component":"VSelect","props":{"model":"dashboard_widget_size","label":"仪表板尺寸","items":[{"title":"100%","value":12},{"title":"2/3","value":8},{"title":"50%","value":6},{"title":"1/3","value":4}],"hint":"选择仪表板系统进程组件尺寸。"}}]},
 {"component":"VCol","props":{"cols":12,"xxl":4,"xl":4,"lg":4,"md":4,"sm":6,"xs":12},"content":[{"component":"VTextField","props":{"model":"dashboard_widget_refresh","label":"仪表板刷新间隔(秒)","placeholder":"5","type":"number","hint":"仪表板组件刷新时间间隔，单位为秒，缺省时不刷新。"}}]}]}
]}]`

func processDuration(seconds int64) string {
	text := fmt.Sprintf("%d秒", seconds%60)
	if minutes := seconds % 3600 / 60; minutes != 0 {
		text = fmt.Sprintf("%d分", minutes) + text
	}
	if hours := seconds / 3600; hours != 0 {
		text = fmt.Sprintf("%d时", hours) + text
	}
	return text
}

func systemProcessTable(items []ProcessInfo, dashboard bool) map[string]any {
	sort.Slice(items, func(i, j int) bool { return items[i].PID > items[j].PID })
	headers := []any{}
	for _, title := range []string{"进程ID", "进程名称", "CPU占用", "内存占用", "运行时长"} {
		headers = append(headers, map[string]any{"component": "th", "props": map[string]any{"class": "text-start ps-4"}, "text": title})
	}
	rows := []any{}
	for _, item := range items {
		cells := []any{}
		for _, value := range []any{item.PID, item.Name, fmt.Sprintf("%.1f%%", item.CPU), fmt.Sprintf("%.1fMB", item.Memory), processDuration(item.RunTime)} {
			cells = append(cells, map[string]any{"component": "td", "props": map[string]any{"class": "whitespace-nowrap"}, "text": value})
		}
		rows = append(rows, map[string]any{"component": "tr", "props": map[string]any{"class": "text-sm"}, "content": cells})
	}
	if len(rows) == 0 {
		rows = append(rows, map[string]any{"component": "tr", "props": map[string]any{"class": "text-sm"}, "content": []any{map[string]any{"component": "td", "props": map[string]any{"colspan": "6", "class": "text-center"}, "text": "暂无数据"}}})
	}
	style := map[string]string{}
	if dashboard {
		style["max-height"] = "242px"
	}
	return map[string]any{"component": "VTable", "props": map[string]any{"hover": true, "fixed-header": true, "density": "compact", "style": style}, "content": []any{map[string]any{"component": "thead", "content": headers}, map[string]any{"component": "tbody", "content": rows}}}
}

func HandleSystemProcess(ctx context.Context, method string, raw json.RawMessage, host plugins.HostCaller) (json.RawMessage, error) {
	if method == "stop" {
		return json.RawMessage(`null`), nil
	}
	if method == "config_form" {
		return json.Marshal(map[string]any{"conf": json.RawMessage(systemProcessForm), "model": map[string]any{"enable": false, "dashboard_widget_size": 8, "dashboard_widget_refresh": 5}})
	}
	var invocation plugins.Invocation
	var config SystemProcessConfiguration
	if json.Unmarshal(raw, &invocation) != nil || json.Unmarshal(invocation.Config, &config) != nil {
		return nil, errors.New("invalid system process config")
	}
	size, refresh, err := config.Display()
	if err != nil {
		return nil, err
	}
	if method == "invoke" && (invocation.Action == "init" || invocation.Action == "reload") {
		return json.RawMessage(`{"initialized":true}`), nil
	}
	if method != "page" && method != "dashboard" {
		return nil, errors.New("invalid system process method")
	}
	raw, err = host(ctx, "process.list", json.RawMessage(`{}`))
	if err != nil {
		return nil, err
	}
	var items []ProcessInfo
	if json.Unmarshal(raw, &items) != nil || len(items) > 1024 {
		return nil, errors.New("invalid process snapshot")
	}
	elements := []any{systemProcessTable(items, method == "dashboard")}
	if method == "page" {
		return json.Marshal(map[string]any{"render_mode": "vuetify", "page": elements})
	}
	cols := map[string]any{"cols": 12, "xxl": size, "xl": size, "lg": size, "md": size, "sm": 12, "xs": 12}
	attrs := map[string]any{"title": "系统进程"}
	if refresh != 0 {
		attrs["refresh"] = refresh
	}
	return json.Marshal(map[string]any{"cols": cols, "attrs": attrs, "elements": elements})
}
