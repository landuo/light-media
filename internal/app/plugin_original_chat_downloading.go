package app

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"light-media/internal/download"
)

// DownloadChain.remote_downloading reports the current downloader results
// without saving another notice to history.
func (a *App) originalChatDownloading(ctx context.Context, reply originalSlashReply) error {
	instances, err := a.downloadInstances(ctx, "")
	if err != nil {
		return err
	}
	var lines []string
	for _, instance := range instances {
		for _, task := range instance["tasks"].([]download.Task) {
			lines = append(lines, fmt.Sprintf("%d. %s %s %.1f%%", len(lines)+1, task.Name,
				originalChatDownloadSize(float64(task.TotalSize)), task.Progress*100))
		}
	}
	title := "没有正在下载的任务！"
	if len(lines) != 0 {
		title = fmt.Sprintf("共 %d 个任务正在下载：", len(lines))
	}
	_, err = a.postOriginalPluginMessage(ctx, map[string]any{
		"channel": reply.info.Channel, "source": reply.info.Source, "userid": reply.info.UserID,
		"mtype": "资源下载", "title": title, "text": strings.Join(lines, "\n"),
		"link": originalChatMediaLinkTo(a.Settings().AppDomain, "#/downloading"), "save_history": false,
	})
	return err
}

func originalChatDownloadSize(size float64) string {
	units := []string{"B", "K", "M", "G", "T"}
	scale, unit := float64(1), 0
	for unit < len(units)-1 && size > scale*1024-1 {
		scale *= 1024
		unit++
	}
	value := strconv.FormatFloat(size/scale, 'f', 2, 64)
	value = strings.TrimRight(value, "0")
	if strings.HasSuffix(value, ".") {
		value += "0"
	}
	return value + units[unit]
}
