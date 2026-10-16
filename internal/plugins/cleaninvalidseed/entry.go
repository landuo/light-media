package cleaninvalidseed

import (
	"context"
	_ "embed"
	"encoding/json"
	"strings"

	"light-media/internal/plugins/schedule"
)

//go:embed form.json
var form json.RawMessage

//go:embed commands.json
var commands json.RawMessage

func Commands() json.RawMessage { return append(json.RawMessage(nil), commands...) }
func Form(names []string) json.RawMessage {
	var v map[string]any
	_ = json.Unmarshal(form, &v)
	var replace func(any)
	replace = func(v any) {
		switch x := v.(type) {
		case map[string]any:
			if x["model"] == "downloaders" {
				items := []any{}
				for _, name := range names {
					items = append(items, map[string]any{"title": name, "value": name})
				}
				x["items"] = items
			}
			for _, v := range x {
				replace(v)
			}
		case []any:
			for _, v := range x {
				replace(v)
			}
		}
	}
	replace(v)
	raw, _ := json.Marshal(v)
	return raw
}
func (m *Machine) GetState() any { return m.Config["enabled"] }

// ServiceTrigger is the reusable host scheduling contract. It computes a
// trigger definition; it does not start a timer or authorize an execution.
func (m *Machine) ServiceTrigger() (*schedule.Spec, error) {
	if !Truth(m.Config["enabled"]) || !Truth(m.Config["cron"]) {
		return nil, nil
	}
	text, ok := m.Config["cron"].(string)
	if !ok {
		return nil, source("AttributeError", "cron split")
	}
	if _, e := schedule.ParseCrontab(text, m.Timezone); e != nil {
		return nil, source("ValueError", "cron")
	}
	return &schedule.Spec{Kind: "cron", Timezone: m.Timezone, Cron: text}, nil
}
func (m *Machine) GetService() (any, error) {
	spec, e := m.ServiceTrigger()
	if e != nil || spec == nil {
		return nil, e
	}
	p := strings.Fields(spec.Cron)
	fields := []string{"*", p[3], p[2], "*", p[4], p[1], p[0], "0"}
	return []any{map[string]any{"id": "CleanInvalidSeed", "name": "清理无效做种", "trigger": map[string]any{"cron": spec.Cron, "fields": fields}, "func": "clean_invalid_seed", "kwargs": map[string]any{}}}, nil
}

var commandActions = map[string]bool{"detect_invalid_torrents": true, "delete_invalid_torrents": true, "detect_invalid_files": true, "delete_invalid_files": true, "toggle_notify_all": true}

func (m *Machine) HandleCommand(ctx context.Context, event map[string]any) error {
	if len(event) == 0 {
		return nil
	}
	action, ok := event["action"].(string)
	if !ok || !commandActions[action] {
		return nil
	}
	post := func(title string) error {
		return m.Host.Notice(ctx, map[string]any{"channel": event["channel"], "title": title, "userid": event["user"]})
	}
	if e := post("🚀 开始执行远程命令..."); e != nil {
		return e
	}
	oldDelete, oldDetect, oldFiles := m.Config["delete_invalid_torrents"], m.Config["detect_invalid_files"], m.Config["delete_invalid_files"]
	var e error
	switch action {
	case "detect_invalid_torrents":
		m.Config["delete_invalid_torrents"] = false
		m.Config["detect_invalid_files"] = false
		m.Config["delete_invalid_files"] = false
		e = m.Clean(ctx)
	case "delete_invalid_torrents":
		m.Config["delete_invalid_torrents"] = true
		m.Config["detect_invalid_files"] = false
		m.Config["delete_invalid_files"] = false
		e = m.Clean(ctx)
	case "detect_invalid_files":
		m.Config["delete_invalid_files"] = false
		e = m.DetectFiles(ctx)
	case "delete_invalid_files":
		m.Config["delete_invalid_files"] = true
		e = m.DetectFiles(ctx)
	case "toggle_notify_all":
		m.Config["notify_all"] = !Truth(m.Config["notify_all"])
		if e = m.updateConfig(ctx); e != nil {
			return e
		}
		if Truth(m.Config["notify_all"]) {
			return post("🔔 已开启全量通知")
		}
		return post("🔕 已关闭全量通知")
	}
	if e != nil {
		return e
	}
	m.Config["delete_invalid_torrents"], m.Config["detect_invalid_files"], m.Config["delete_invalid_files"] = oldDelete, oldDetect, oldFiles
	return post("✅ 远程命令执行完成！")
}
