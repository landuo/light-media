package systemprocess

import (
	"fmt"
	"sort"
)

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

func processTable(items []ProcessInfo, dashboard bool) map[string]any {
	sort.SliceStable(items, func(i, j int) bool { return items[i].PID > items[j].PID })
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
