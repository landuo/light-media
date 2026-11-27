package app

import (
	"context"
	"fmt"
	"light-media/internal/catalog"
)

type workflowKey struct{}

func (a *App) workflow(ctx context.Context, provided string, m catalog.Media) string {
	if value, ok := ctx.Value(workflowKey{}).(string); ok && value != "" {
		return value
	}
	if provided != "" {
		var v struct {
			MediaKey string `json:"media_key"`
		}
		if a.Store.Get(ctx, "workflow", provided, &v) == nil && v.MediaKey == mediaKey(m) {
			return provided
		}
	}
	key := id("wf_")
	_ = a.Store.Put(ctx, "workflow", key, map[string]string{"media_key": mediaKey(m), "created_at": now()})
	return key
}
func (a *App) logSite(s SiteStatus, wf string) {
	level := "INFO"
	if s.Status == "failed" || s.Status == "partial" || s.Status == "timeout" {
		level = "WARN"
	}
	name := s.ID
	for _, site := range a.settingsSnapshot().Sites {
		if site.ID == s.ID && site.Name != "" {
			name = site.Name
			break
		}
	}
	result := map[string]string{"ok": "完成", "empty": "未找到资源", "failed": "失败", "timeout": "超时", "cancelled": "已停止", "partial": "返回部分结果"}[s.Status]
	if result == "" {
		result = s.Status
	}
	message := fmt.Sprintf("搜索站点「%s」%s，找到 %d 个候选资源", name, result, s.Count)
	if s.Message != "" {
		message += "。" + s.Message
	}
	a.writeLog(map[string]any{"id": id("log_"), "time": now(), "level": level, "module": "sites", "event": "search_result", "message": message, "duration_ms": s.ElapsedMS, "site_id": s.ID, "workflow_id": wf})
}
func (a *App) saveDecision(ctx context.Context, out SearchOutput, m catalog.Media) {
	candidates := out.Candidates
	if len(candidates) > 500 {
		candidates = candidates[:500]
	}
	_ = a.Store.Put(ctx, "decision", out.WorkflowID+"_"+id(""), map[string]any{"workflow_id": out.WorkflowID, "media_key": mediaKey(m), "time": now(), "candidates": candidates, "sites": out.Sites})
	accepted := 0
	for _, c := range candidates {
		if c.Accepted {
			accepted++
		}
	}
	a.log("INFO", "decision", "evaluated", fmt.Sprintf("筛选%d个候选，%d个可选；逐项原因已保存", len(candidates), accepted), out.WorkflowID, "")
}
