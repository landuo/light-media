package app

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"light-media/internal/catalog"
)

// The source chain posts one Organize notification per transfer. A native
// import job can publish several files at once, so the committed files of one
// run are summarized together instead of producing one message per file.
func (a *App) postOriginalOrganizeSuccessNotice(ctx context.Context, plan ImportPlan, files []PlanFile) {
	if len(files) == 0 {
		return
	}
	fields := a.organizeSuccessNoticeFields(plan, files)
	if _, err := a.postOriginalPluginMessage(context.WithoutCancel(ctx), fields); err != nil {
		a.log("WARN", "import", "organize_notice_failed", "整理完成通知发送失败: "+a.clean(err.Error()), plan.WorkflowID, "")
	}
}

// organizeSuccessNoticeApplies selects the source's organize scope: one
// committed video publication that this host produced itself. Reused
// hardlinks and plugin-owned transfer contexts were already organized.
func organizeSuccessNoticeApplies(file PlanFile, receipt importReceipt, sourced bool) bool {
	return file.MaterialKind == "" && !receipt.ReusedHardlink && !sourced
}

func (a *App) organizeSuccessNoticeFields(plan ImportPlan, files []PlanFile) map[string]any {
	fields := map[string]any{
		"mtype":        "整理入库",
		"ctype":        "organizeSuccess",
		"title":        organizeSuccessNoticeTitle(plan.Media, files),
		"text":         organizeSuccessNoticeText(files),
		"link":         originalChatMediaLinkTo(a.Settings().AppDomain, "#/history"),
		"save_history": true,
	}
	if image := organizeSuccessNoticeImage(plan.Media.PosterPath); image != "" {
		fields["image"] = image
	}
	return fields
}

func organizeSuccessNoticeTitle(media catalog.Media, files []PlanFile) string {
	name := strings.TrimSpace(media.Title)
	if name == "" && len(files) > 0 {
		name = strings.TrimSpace(filepath.Base(files[0].Target))
	}
	labels := []string{}
	for _, file := range files {
		if label := organizeSuccessEpisodeLabel(file); label != "" && !containsString(labels, label) {
			labels = append(labels, label)
		}
	}
	switch {
	case len(labels) == 0:
	case len(labels) <= 3:
		name += " " + strings.Join(labels, "、")
	default:
		name += fmt.Sprintf(" 等 %d 个文件", len(files))
	}
	return "整理完成：" + name
}

// organizeSuccessEpisodeLabel mirrors the source naming scope: one episode or
// a contiguous range, and only when the plan carries a season.
func organizeSuccessEpisodeLabel(file PlanFile) string {
	if file.Season <= 0 {
		return ""
	}
	label := fmt.Sprintf("S%02d", file.Season)
	if len(file.Episodes) == 0 {
		return label
	}
	label += fmt.Sprintf("E%02d", file.Episodes[0])
	if last := file.Episodes[len(file.Episodes)-1]; last != file.Episodes[0] {
		label += fmt.Sprintf("-E%02d", last)
	}
	return label
}

func organizeSuccessNoticeText(files []PlanFile) string {
	lines := []string{}
	var total int64
	listed := 0
	for _, file := range files {
		if listed >= 10 {
			lines = append(lines, fmt.Sprintf("…其余 %d 个文件", len(files)-listed))
			break
		}
		lines = append(lines, "路径："+file.Target)
		listed++
		if file.Size > 0 {
			total += file.Size
		}
	}
	if size := organizeNoticeSize(total); size != "" {
		lines = append(lines, "大小："+size)
	}
	return strings.Join(lines, "\n")
}

func organizeSuccessNoticeImage(poster string) string {
	poster = strings.TrimSpace(poster)
	if poster == "" {
		return ""
	}
	if !strings.HasPrefix(poster, "https://") && !strings.HasPrefix(poster, "http://") {
		return ""
	}
	return strings.ReplaceAll(poster, "original", "w500")
}

func organizeNoticeSize(bytes int64) string {
	if bytes <= 0 {
		return ""
	}
	units := []string{"B", "KB", "MB", "GB", "TB"}
	value, unit := float64(bytes), 0
	for value >= 1024 && unit < len(units)-1 {
		value /= 1024
		unit++
	}
	if unit == 0 {
		return fmt.Sprintf("%.0f %s", value, units[unit])
	}
	return fmt.Sprintf("%.2f %s", value, units[unit])
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
