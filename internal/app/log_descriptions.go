package app

import (
	"fmt"
	"net/http"
	"strings"
)

// Only server-owned route labels enter logs. IDs, file names, plugin URL
// parameters and query strings in an incoming URL can contain credentials.
var requestLogModules = map[string]string{
	"auth":            "登录认证",
	"ready":           "服务就绪状态",
	"status":          "系统状态",
	"control":         "自动化状态",
	"settings":        "系统设置",
	"directories":     "目录规则",
	"files":           "文件管理",
	"history":         "整理历史",
	"catalog":         "影视资料",
	"search":          "资源搜索",
	"sites":           "站点配置",
	"downloaders":     "下载器配置",
	"downloads":       "下载任务",
	"subscriptions":   "订阅",
	"calendar":        "订阅日历",
	"schedule":        "订阅计划",
	"import":          "文件整理",
	"assets":          "媒体资产",
	"jobs":            "后台任务",
	"logs":            "系统日志",
	"fnos":            "飞牛影视",
	"backups":         "备份",
	"restore":         "恢复",
	"notifications":   "后台任务通知",
	"audit":           "操作审计",
	"frontend-errors": "前端错误报告",
	"plugins":         "插件",
	"system":          "系统服务",
	"scheduler":       "定时任务",
	"recommend":       "推荐片单",
	"discover":        "探索片单",
	"media":           "影视资料",
	"command":         "交互指令",
}

var requestLogActions = map[string]string{
	"auth/status":                    "检查登录状态",
	"auth/providers":                 "读取登录方式",
	"auth/setup":                     "创建管理员",
	"auth/exchange":                  "交换登录凭据",
	"auth/login":                     "登录",
	"auth/logout":                    "退出登录",
	"settings/test":                  "测试服务连接",
	"settings/api-token/reveal":      "查看接口令牌",
	"directories/options":            "读取可选目录",
	"directories/transfer-types":     "读取整理方式",
	"files/content":                  "读取文件内容",
	"files/organize":                 "处理手动整理请求",
	"files/recognize":                "识别文件",
	"files/roots":                    "读取存储目录",
	"files/list":                     "列出目录内容",
	"files/scan":                     "扫描文件",
	"files/scrape":                   "刮削文件元数据",
	"files/recycle":                  "查看回收站",
	"files/operation-preview":        "预览文件操作",
	"files/operation-confirm":        "执行文件操作",
	"files/operation-restore":        "恢复回收站文件",
	"files/episode-format/recommend": "推荐集数识别规则",
	"history/transfers/queue":        "读取整理队列",
	"sites/priorities":               "保存站点排序",
	"sites/import":                   "导入站点",
	"sites/export":                   "导出站点",
	"catalog/recommend":              "读取推荐片单",
	"catalog/explore":                "读取探索片单",
	"catalog/search":                 "搜索影视资料",
	"catalog/browse":                 "浏览影视列表",
	"catalog/seasons":                "读取剧集季列表",
	"catalog/episode-groups":         "列出分集组",
	"catalog/episode-group":          "读取分集组",
	"catalog/detail/credits":         "读取影视演职员",
	"catalog/detail/recommendations": "读取相似影视",
	"catalog/person/credits":         "读取人物作品",
	"catalog/recommend/sources":      "读取推荐来源",
	"catalog/explore/sources":        "读取探索来源",
	"media/search":                   "搜索影视资料",
	"catalog/detail":                 "读取影视详情",
	"catalog/season":                 "读取分季资料",
	"catalog/person":                 "读取人物资料",
	"search":                         "搜索影视资源",
	"search/resources":               "按关键词搜索资源",
	"schedule/preview":               "预览订阅计划",
	"downloads/add":                  "添加下载任务",
	"downloads/preview":              "预览资源下载",
	"downloads/confirm":              "确认提交下载",
	"downloads/task-action":          "控制下载任务",
	"subscriptions/matching":         "查找匹配的订阅",
	"import/scan":                    "扫描待整理文件",
	"import/preview":                 "预览整理计划",
	"import/repair":                  "修复整理源文件",
	"import/execute":                 "执行整理计划",
	"import/plans":                   "读取整理计划",
	"assets/register":                "登记媒体资产",
	"assets/scan":                    "扫描媒体资产",
	"assets/candidates":              "读取候选资产",
	"logs/stream":                    "跟随实时日志",
	"logs/export":                    "导出日志",
	"logs/health":                    "检查日志写入状态",
	"fnos/libraries":                 "读取飞牛影视媒体库",
	"system/message":                 "接收系统消息",
	"notifications/read-all":         "将全部后台任务通知标为已读",
	"system/nettest/targets":         "读取网站连通性测试列表",
	"system/nettest":                 "测试网站连通性",
}

var requestLogItemActions = map[string]string{
	"sites/refresh":            "刷新站点数据",
	"sites/history":            "读取站点数据历史",
	"sites/resources":          "搜索站点资源",
	"sites/icon":               "读取站点图标",
	"downloaders/test":         "测试下载器连接",
	"directories/check":        "检查目录",
	"directories/files":        "列出受管目录文件",
	"subscriptions/check":      "检查订阅更新",
	"subscriptions/reset":      "重置订阅",
	"subscriptions/status":     "更新订阅状态",
	"subscriptions/seasons":    "读取订阅季列表",
	"subscriptions/files":      "读取订阅文件",
	"downloads/action":         "控制下载任务",
	"downloads/files":          "读取下载文件",
	"downloads/import-preview": "预览下载文件整理",
	"downloads/import-confirm": "提交下载文件整理",
	"history/retry":            "重试整理记录",
	"history/cancel":           "取消排队的整理文件",
	"jobs/retry":               "重试后台任务",
	"jobs/cancel":              "取消后台任务",
	"notifications/read":       "将后台任务通知标为已读",
	"plugins/logs":             "查看插件日志",
	"plugins/reload":           "重载插件",
	"plugins/reset":            "重置插件配置",
	"plugins/config":           "更新插件配置",
}

func logModuleAction(module, method string) string {
	target := requestLogModules[module]
	if target == "" {
		return "访问未识别的接口"
	}
	switch method {
	case http.MethodGet, http.MethodHead:
		return "读取" + target
	case http.MethodPut, http.MethodPatch:
		return "更新" + target
	case http.MethodDelete:
		return "删除" + target
	default:
		return "提交" + target + "请求"
	}
}

func requestLogAction(path, method string) (string, string) {
	path = strings.TrimPrefix(path, "/api/")
	path = strings.TrimPrefix(strings.TrimPrefix(path, "v1/"), "v2/")
	module, _, _ := strings.Cut(path, "/")
	if module == "plugin" {
		return "plugins", "调用插件接口"
	} else if requestLogModules[module] == "" {
		module = "unknown"
	}
	if module == "plugins" {
		_, suffix, _ := strings.Cut(path, "/")
		switch {
		case originalPluginInstallRoute(suffix, method):
			return module, "安装插件"
		case originalPluginUninstallRoute(suffix, method):
			return module, "卸载插件"
		case originalPluginHistoryRoute(suffix, method):
			return module, "读取插件版本历史"
		}
	}
	if action := requestLogActions[path]; action != "" {
		return module, action
	}
	if strings.Count(path, "/") >= 2 {
		last := path[strings.LastIndexByte(path, '/')+1:]
		if action := requestLogItemActions[module+"/"+last]; action != "" {
			return module, action
		}
	}
	return module, logModuleAction(module, method)
}

func requestLogOutcome(action string, status int) string {
	switch {
	case status >= 500:
		return action + "：服务处理失败"
	case status == 401:
		return action + "：需要登录认证"
	case status == 403:
		return action + "：请求被拒绝"
	case status == 404:
		return action + "：未找到请求的资源"
	case status >= 400:
		return action + "：请求失败"
	case status >= 300 && status != http.StatusNotModified:
		return action + "：服务器返回重定向"
	default:
		return action + "：已响应"
	}
}

// Some runtime producers pass through the underlying error as the entire
// message. Keep that diagnostic and supply the operation that encountered it.
var logErrorActions = map[string]string{
	"builtin_catalog_failed":          "读取内置插件清单失败",
	"monitor_failed":                  "检查受监控目录失败",
	"torrent_completion_check_failed": "核对种子整理完成状态失败",
	"acceptance_reconcile_failed":     "核对下载器接收记录失败",
	"plan_waiting":                    "等待生成整理计划",
	"source_interval_clock_paused":    "插件定时调度已暂停",
	"torrent_completion_query_failed": "查询种子整理状态失败",
	"torrent_completion_tag_failed":   "更新整理完成标签失败",
	"memory_reclaim_disabled":         "自动回收内存已关闭",
	"watch_unavailable":               "启动文件监听失败",
	"watch_dispatch_failed":           "分发文件变更事件失败",
	"subscription_filter_failed":      "应用订阅筛选规则失败",
	"metadata_image_failed":           "刮削媒体图片失败",
}

// Older JSONL records contain the module and verb but no route. Describe only
// what was recorded, so earlier requests aren't attributed to a guessed endpoint.
func describeApplicationLog(entry map[string]any) {
	message, _ := entry["message"].(string)
	module, _ := entry["module"].(string)
	event, _ := entry["event"].(string)
	switch {
	case logErrorActions[event] != "" && !strings.HasPrefix(message, logErrorActions[event]):
		entry["message"] = logErrorActions[event] + "：" + message
	case message == "API 请求处理完成" && event == "request_completed" && strings.HasPrefix(module, "http."):
		method, _ := entry["method"].(string)
		status, _ := entry["http_status"].(float64)
		entry["message"] = requestLogOutcome(logModuleAction(strings.TrimPrefix(module, "http."), method), int(status))
	case message == "外部接口请求" && (module == "external" || strings.HasPrefix(module, "external.")):
		entry["message"] = externalLogMessage(entry)
	case event == "slow_phase" && module == "performance" && perfLabels[message] != "":
		op, _ := entry["operation"].(string)
		entry["phase"] = message
		entry["message"] = performanceLogMessage(op, message)
	case entry["job_id"] != nil && entry["job_id"] != "" && (message == "开始执行任务" && event == "started" || message == "任务完成" && event == "completed"):
		stage := "：开始执行"
		if event == "completed" {
			stage = "：已完成"
		}
		entry["message"] = jobActionLabel(module) + stage
	}
}

var externalActionLabels = map[string]string{
	"fnos_login":           "登录飞牛影视",
	"fnos_version":         "查询飞牛影视版本",
	"fnos_user":            "查询飞牛用户",
	"fnos_libraries":       "列出飞牛媒体库",
	"fnos_library_create":  "创建飞牛媒体库",
	"fnos_library_scan":    "扫描飞牛媒体库",
	"fnos_library_detail":  "读取飞牛媒体库详情",
	"fnos_item_list":       "列出飞牛媒体条目",
	"fnos_item_detail":     "读取飞牛媒体详情",
	"fnos_persons":         "读取飞牛人物资料",
	"fnos_search":          "搜索飞牛媒体库",
	"fnos_provider_search": "搜索飞牛刮削来源",
	"fnos_image":           "读取飞牛媒体图片",
	"metadata_episode":     "查询 TMDB 分集资料",
	"metadata_movie":       "查询 TMDB 电影资料",
	"metadata_tv":          "查询 TMDB 剧集资料",
	"qb_trackers":          "查询 qBittorrent Tracker",
	"qb_add":               "添加 qBittorrent 种子",
	"qb_start":             "开始 qBittorrent 下载",
	"qb_stop":              "暂停 qBittorrent 下载",
	"qb_force_start":       "设置 qBittorrent 强制开始",
	"qb_recheck":           "校验 qBittorrent 种子",
	"qb_delete":            "删除 qBittorrent 种子",
	"qb_tag":               "添加 qBittorrent 标签",
	"qb_file_priority":     "更新 qBittorrent 文件优先级",
	"qb_location":          "更新 qBittorrent 保存目录",
	"metadata_search":      "搜索 TMDB 资料",
	"metadata_discover":    "浏览 TMDB 片单",
	"metadata_person":      "查询 TMDB 人物资料",
	"qb_login":             "登录 qBittorrent",
	"qb_version":           "查询 qBittorrent 版本",
	"qb_api_version":       "查询 qBittorrent API 版本",
	"qb_preferences":       "读取 qBittorrent 设置",
	"qb_torrents":          "查询 qBittorrent 种子列表",
	"qb_files":             "查询 qBittorrent 种子文件",
	"transmission_rpc":     "调用 Transmission RPC",
	"mteam_profile":        "查询馒头账户资料",
	"site_search":          "搜索站点资源",
	"site_check":           "检查站点连接",
	"material_token":       "获取种子下载凭据",
	"material_fetch":       "获取种子文件",
}

func externalLogMessage(entry map[string]any) string {
	kind, _ := entry["target_kind"].(string)
	target := map[string]string{"downloader": "下载器", "site": "资源网站", "material": "种子来源", "fnos": "飞牛影视", "metadata": "元数据服务", "fnos_experiment": "飞牛测试库"}[kind]
	if target == "" {
		target = "外部服务"
	}
	op, _ := entry["operation"].(string)
	action := externalActionLabels[op]
	if action == "" {
		action = "请求" + target
	}
	switch entry["event"] {
	case "run_started":
		return "开始记录外部服务调用"
	case "run_completed", "target_completed":
		if code, _ := entry["error_code"].(string); code != "" && code != "none" {
			return "结束" + target + "检查：" + code
		}
		return "结束" + target + "检查"
	case "http_started":
		return action + "：发送请求"
	case "http_completed":
		if entry["error_code"] == "transport_error" {
			return action + "：网络传输失败"
		}
		code, _ := entry["http_status"].(float64)
		return requestLogOutcome(action, int(code))
	default:
		return action
	}
}

func jobActionLabel(kind string) string {
	if label := jobTypeLabels[kind]; label != "" {
		return label
	}
	return fmt.Sprintf("执行后台任务（%s）", kind)
}
