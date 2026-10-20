// Package commandrouter models the installed Command.command_event dispatch.
// Business consumers are explicit callbacks; a route is not an execution receipt.
package commandrouter

import (
	"strings"
	"unicode"
)

type Command struct {
	Name, ID, Kind, Description string
	Category                    *string
	Arguments                   int
}

func category(s string) *string { return &s }

// Presets preserves source insertion order, including redo's absent category.
func Presets() []Command {
	return []Command{
		{"/cookiecloud", "cookiecloud", "scheduler", "同步站点", category("站点"), 0},
		{"/sites", "", "function", "管理站点", category("站点"), 4},
		{"/mediaserver_sync", "mediaserver_sync", "scheduler", "同步媒体服务器", category("管理"), 0},
		{"/subscribes", "", "function", "管理订阅", category("订阅"), 4},
		{"/downloading", "", "function", "正在下载", category("管理"), 3},
		{"/transfer", "transfer", "scheduler", "下载文件整理", category("管理"), 0},
		{"/redo", "", "function", "手动整理", nil, 4},
		{"/clear_cache", "", "function", "清理缓存", category("管理"), 3},
		{"/restart", "", "function", "重启系统", category("管理"), 3},
		{"/version", "", "function", "当前版本", category("管理"), 3},
		{"/clear_session", "", "function", "清除会话", category("管理"), 3},
		{"/stop_agent", "", "function", "停止推理", category("管理"), 3},
		{"/session_status", "", "function", "会话状态", category("智能体"), 3},
		{"/skills", "", "function", "管理技能", category("智能体"), 4},
	}
}

// Python str.split() includes the four information separators in addition to
// Unicode White_Space. Command names are case-sensitive and argument spaces join.
func Split(text string) (string, string) {
	parts := strings.FieldsFunc(text, func(r rune) bool { return unicode.IsSpace(r) || r >= 0x1c && r <= 0x1f })
	if len(parts) == 0 {
		return "", ""
	}
	return parts[0], strings.Join(parts[1:], " ")
}
func Lookup(name string) (Command, bool) {
	for _, c := range Presets() {
		if c.Name == name {
			return c, true
		}
	}
	return Command{}, false
}
