package app

import (
	"context"
	"time"
)

// Only fixed operation/phase names and elapsed times are recorded. This uses
// the bounded async logger, never the database whose wait is being measured.
func (a *App) performanceStages(ctx context.Context, operation, phase string) func(string) {
	started := time.Now()
	trace, _ := ctx.Value(jobTraceKey{}).(jobTrace)
	return func(next string) {
		elapsed := time.Since(started)
		if phase != "" && elapsed >= 250*time.Millisecond {
			a.writeLog(map[string]any{
				"time": now(), "id": id("log_"), "level": "INFO", "module": "performance",
				"event": "slow_phase", "operation": operation, "phase": phase, "message": performanceLogMessage(operation, phase),
				"duration_ms": elapsed.Milliseconds(), "request_id": trace.Request, "job_id": trace.Job,
			})
		}
		started, phase = time.Now(), next
	}
}

// Shared by the live writer and the reader of earlier phase-only messages.
var perfLabels = map[string]string{
	"import_prepare":            "准备整理计划",
	"asset_lookup":              "查找媒体资产",
	"settings_save":             "保存系统设置",
	"plugin_delete_files":       "插件删除下载文件",
	"organize_accept":           "接收整理请求",
	"organize_delivery":         "创建整理任务",
	"organize_scan":             "扫描整理目录",
	"organize_submit":           "提交整理请求",
	"manual_organize":           "手动整理",
	"organize_queue":            "读取整理队列",
	"organize_request":          "处理整理请求",
	"job_enqueue":               "后台任务入队",
	"transfer_identify":         "识别整理文件",
	"transfer_native_identity":  "识别媒体资料",
	"import_submit":             "提交整理计划",
	"history_list":              "读取整理历史",
	"organize_file_preview":     "预览文件整理",
	"extensions_and_settings":   "读取扩展和设置",
	"target_configuration":      "读取目标目录配置",
	"target_write_probe":        "检查目标目录可写性",
	"metadata":                  "读取元数据",
	"ownership_and_policy":      "检查归属与整理规则",
	"file_plans":                "生成文件计划",
	"source_snapshot":           "读取源文件快照",
	"episode_information":       "读取分集资料",
	"naming_context":            "准备命名信息",
	"module_preview":            "生成模块预览",
	"related_files":             "查找关联文件",
	"target_and_slots":          "核对目标文件和占用",
	"writer_wait":               "等待数据库写入",
	"persist_plan":              "保存整理计划",
	"read_by_path":              "按路径查找",
	"download_lock":             "等待下载操作锁",
	"import_lock":               "等待整理操作锁",
	"validate_and_persist":      "校验并保存配置",
	"commit":                    "提交事务",
	"publish_and_plugins":       "发布配置并更新插件",
	"brush_validate_and_delete": "校验并删除刷流文件",
	"validate_and_delete":       "校验并删除文件",
	"inbox_wait":                "等候整理请求队列",
	"inbox_lookup":              "查找待处理请求",
	"existing_reader_wait":      "等待读取已有任务",
	"existing_lookup":           "查询已有任务",
	"request_insert":            "写入整理请求",
	"durable_commit":            "持久保存整理请求",
	"main_writer":               "等待主库写入",
	"refresh_subjects":          "刷新待整理对象",
	"materialize":               "生成任务记录",
	"audit":                     "登记审计记录",
	"main_commit":               "提交主库事务",
	"retire_receipt":            "移除已完成回执",
	"storage_candidates":        "查找可用存储",
	"inspect_sources":           "检查源文件",
	"validate_source":           "校验整理来源",
	"durable_acceptance":        "保存已接收请求",
	"prepare_history":           "读取整理历史",
	"scan_files":                "扫描源文件",
	"plan_and_submit":           "生成并提交计划",
	"seed_legacy":               "载入历史队列",
	"active_jobs":               "读取活动任务",
	"hydrate":                   "补全任务详情",
	"audit_write":               "写入操作审计",
	"identity":                  "核对任务身份",
	"job_insert":                "写入任务",
	"event_insert":              "写入任务事件",
	"queue_group":               "登记队列分组",
	"parse_path":                "解析文件路径",
	"recognize_media":           "识别影视作品",
	"parse_recognition":         "解析识别结果",
	"directory_lookup":          "查找目录规则",
	"tmdb_detail":               "读取 TMDB 详情",
	"obtain_images":             "获取图片",
	"download_guard":            "检查下载任务状态",
	"plan_and_directory":        "核对整理计划和目录",
	"directory_authorization":   "核对目录授权",
	"existing_job":              "查询既有任务",
	"download_retry_proof":      "核对下载重试记录",
	"intent":                    "写入整理意图",
	"reservations":              "登记资源占用",
	"persist_queue":             "保存待执行队列",
	"read_snapshot":             "读取历史快照",
}

func performanceLogMessage(operation, phase string) string {
	if name := perfLabels[operation]; name != "" {
		operation = name
	}
	if name := perfLabels[phase]; name != "" {
		phase = name
	}
	return operation + "：" + phase + "耗时较长"
}
