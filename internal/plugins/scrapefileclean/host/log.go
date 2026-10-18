package scrapehost

import "context"

type Log struct{ Level, Event, Message string }

// Diagnostic output cannot introduce a path, execute a callback or alter the
// source machine. The App binds this private sink to the installed owner.
func (h *Host) log(ctx context.Context, level, event, message string) {
	if h.opts.Log != nil {
		h.opts.Log(ctx, h.binding, Log{level, event, message})
	}
}

func sourceEventName(kind string) string {
	switch kind {
	case "created":
		return "新增"
	case "deleted":
		return "删除"
	case "moved":
		return "移动"
	}
	return kind
}
func sourceEffectName(kind string) string {
	switch kind {
	case "unlink":
		return "删除文件"
	case "rmdir":
		return "清理空目录"
	case "history_src":
		return "按源路径清理整理记录"
	case "history_dest":
		return "按目的路径清理整理记录"
	case "notification":
		return "通知已进入持久队列"
	case "download_file_deleted":
		return "文件删除事件已提交"
	}
	return kind
}
