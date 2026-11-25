package iyuuautoseed

import (
	"context"
	"strconv"
	"sync"

	sdk "light-media/builtinplugins/internal/sdk"
)

// SourceLog carries ephemeral diagnostics. The SDK adapter binds run identity
// and removes credentials before writing to the shared log sink.
type SourceLog struct {
	Line                   int
	Level, Method, Message string
}
type SourceLogSink func(context.Context, SourceLog)
type sourceLogKey struct{}
type sourceLogDefinition struct{ Level, Method string }

// A scheduled pass can produce the same warning for every candidate torrent
// (for example a site without a configured address). Keep the source text and
// level, but emit each distinct warning once per run and summarize the rest.
type sourceLogRepeat struct {
	level, message string
	count          int
}
type sourceLogDedup struct {
	mu      sync.Mutex
	entries map[string]*sourceLogRepeat
	order   []string
}
type sourceLogDedupKey struct{}

func newSourceLogDedup() *sourceLogDedup {
	return &sourceLogDedup{entries: map[string]*sourceLogRepeat{}}
}

// record returns true when this exact warning or error already occurred in the
// current run and only its repeat counter needs to grow.
func (d *sourceLogDedup) record(level, message string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	key := level + "\x00" + message
	if entry, seen := d.entries[key]; seen {
		entry.count++
		return true
	}
	d.entries[key] = &sourceLogRepeat{level: level, message: message, count: 1}
	d.order = append(d.order, key)
	return false
}

func (d *sourceLogDedup) repeated() []sourceLogRepeat {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := []sourceLogRepeat{}
	for _, key := range d.order {
		if entry := d.entries[key]; entry != nil && entry.count > 1 {
			out = append(out, *entry)
		}
	}
	return out
}

// flushSourceLogRepeats reports the merged counts at the end of one run. The
// summary keeps the original level so the operator still sees that these were
// warnings, not ordinary progress lines.
func flushSourceLogRepeats(ctx context.Context, caller sdk.HostCaller) {
	dedup, _ := ctx.Value(sourceLogDedupKey{}).(*sourceLogDedup)
	if dedup == nil || caller == nil {
		return
	}
	logger := sdk.Logger{Host: caller}
	for _, entry := range dedup.repeated() {
		message := "同类日志已合并 " + strconv.Itoa(entry.count) + " 条：" + entry.message
		_ = logger.Log(ctx, sdk.LogLevel(entry.level), message)
	}
}

// Exact installed __init__.py SHA256 3cde4efa9ddd36f82318bed62a6a62ab9004257d8570538f937351b65970bdb7.
var sourceLogDefinitions = map[int]sourceLogDefinition{
	156:  {Level: "INFO", Method: "init_plugin"},
	182:  {Level: "WARN", Method: "service_infos"},
	187:  {Level: "WARN", Method: "service_infos"},
	193:  {Level: "WARN", Method: "service_infos"},
	198:  {Level: "WARN", Method: "service_infos"},
	209:  {Level: "DEBUG", Method: "auto_service_info"},
	214:  {Level: "WARN", Method: "auto_service_info"},
	218:  {Level: "WARN", Method: "auto_service_info"},
	749:  {Level: "INFO", Method: "auto_seed"},
	762:  {Level: "INFO", Method: "auto_seed"},
	766:  {Level: "INFO", Method: "auto_seed"},
	768:  {Level: "INFO", Method: "auto_seed"},
	775:  {Level: "INFO", Method: "auto_seed"},
	781:  {Level: "INFO", Method: "auto_seed"},
	790:  {Level: "INFO", Method: "auto_seed"},
	802:  {Level: "INFO", Method: "auto_seed"},
	810:  {Level: "INFO", Method: "auto_seed"},
	815:  {Level: "INFO", Method: "auto_seed"},
	818:  {Level: "INFO", Method: "auto_seed"},
	827:  {Level: "INFO", Method: "auto_seed"},
	838:  {Level: "INFO", Method: "auto_seed"},
	841:  {Level: "INFO", Method: "auto_seed"},
	858:  {Level: "INFO", Method: "auto_seed"},
	891:  {Level: "INFO", Method: "check_recheck_service"},
	911:  {Level: "INFO", Method: "check_recheck_service"},
	918:  {Level: "INFO", Method: "check_recheck_service"},
	922:  {Level: "INFO", Method: "check_recheck_service"},
	925:  {Level: "INFO", Method: "check_recheck_service"},
	938:  {Level: "INFO", Method: "__seed_torrents"},
	952:  {Level: "WARN", Method: "__seed_torrents"},
	954:  {Level: "WARN", Method: "__seed_torrents"},
	957:  {Level: "INFO", Method: "__seed_torrents"},
	977:  {Level: "INFO", Method: "__seed_torrents"},
	980:  {Level: "INFO", Method: "__seed_torrents"},
	983:  {Level: "INFO", Method: "__seed_torrents"},
	1005: {Level: "INFO", Method: "__seed_torrents"},
	1094: {Level: "ERROR", Method: "__download"},
	1113: {Level: "ERROR", Method: "__download"},
	1149: {Level: "DEBUG", Method: "__download_torrent"},
	1152: {Level: "INFO", Method: "__download_torrent"},
	1159: {Level: "INFO", Method: "__download_torrent"},
	1165: {Level: "WARN", Method: "__download_torrent"},
	1199: {Level: "ERROR", Method: "__download_torrent"},
	1202: {Level: "INFO", Method: "__download_torrent"},
	1219: {Level: "INFO", Method: "__download_torrent"},
	1223: {Level: "INFO", Method: "__download_torrent"},
	1231: {Level: "INFO", Method: "__download_torrent"},
	1238: {Level: "INFO", Method: "__add_recheck_torrents"},
	1339: {Level: "ERROR", Method: "__get_mteam_enclosure"},
	1358: {Level: "WARN", Method: "__get_mteam_enclosure"},
	1367: {Level: "ERROR", Method: "__get_monika_torrent"},
	1379: {Level: "WARN", Method: "__get_gpw_torrent_url_from_page"},
	1384: {Level: "INFO", Method: "__get_gpw_torrent_url_from_page"},
	1394: {Level: "ERROR", Method: "__get_gpw_torrent_url_from_page"},
	1403: {Level: "WARN", Method: "__get_gpw_torrent_url_from_page"},
	1408: {Level: "WARN", Method: "__get_gpw_torrent_url_from_page"},
	1415: {Level: "WARN", Method: "__get_gpw_torrent_url_from_page"},
	1426: {Level: "WARN", Method: "__get_gpw_torrent_url_from_page"},
	1431: {Level: "INFO", Method: "__get_gpw_torrent_url_from_page"},
	1434: {Level: "WARN", Method: "__get_gpw_torrent_url_from_page"},
	1486: {Level: "WARN", Method: "__get_download_url"},
	1495: {Level: "WARN", Method: "__get_download_url"},
	1504: {Level: "WARN", Method: "__get_torrent_url_from_page"},
	1508: {Level: "INFO", Method: "__get_torrent_url_from_page"},
	1520: {Level: "WARN", Method: "__get_torrent_url_from_page"},
	1528: {Level: "INFO", Method: "__get_torrent_url_from_page"},
	1535: {Level: "WARN", Method: "__get_torrent_url_from_page"},
	1538: {Level: "ERROR", Method: "__get_torrent_url_from_page"},
	1541: {Level: "WARN", Method: "__get_torrent_url_from_page"},
}

func WithSourceLogSink(ctx context.Context, sink SourceLogSink) context.Context {
	return context.WithValue(ctx, sourceLogKey{}, sink)
}
func sourceLog(ctx context.Context, line int, message string) {
	def, ok := sourceLogDefinitions[line]
	if !ok {
		return
	}
	if sink, ok := ctx.Value(sourceLogKey{}).(SourceLogSink); ok && sink != nil {
		sink(ctx, SourceLog{Line: line, Level: def.Level, Method: def.Method, Message: message})
	}
}

// The cause may contain a private URL or credentials. Only log the stage.
func logSeedWarning(ctx context.Context, message string) {
	if sink, ok := ctx.Value(sourceLogKey{}).(SourceLogSink); ok && sink != nil {
		sink(ctx, SourceLog{Level: "WARN", Method: "auto_seed", Message: message})
	}
}

func (c *Core) queueAdd(ctx context.Context, service Service, id string) {
	sourceLog(ctx, 1238, "添加校验检查任务："+id+" ...")
	c.queue[service.Name] = append(c.queue[service.Name], id)
}
func (c *Core) logInventory(ctx context.Context, inventory []Service, mode string) {
	auto := c.config.values["auto_downloader"]
	if mode == "auto" {
		if !truth(auto) {
			sourceLog(ctx, 209, "尚未配置主辅分离下载器，辅种不分离")
		} else {
			found := false
			for _, s := range inventory {
				if scalarEqual(auto, s.Name) {
					found = true
					if s.Inactive {
						sourceLog(ctx, 218, "下载器 "+s.Name+" 未连接，请检查配置")
					}
					break
				}
			}
			if !found {
				sourceLog(ctx, 214, "获取主辅分离下载器实例失败，请检查配置")
			}
		}
		return
	}
	selected := c.config.values["downloaders"]
	if !truth(selected) {
		sourceLog(ctx, 182, "尚未配置下载器，请检查配置")
		return
	}
	matching, active := 0, 0
	for _, s := range inventory {
		if selectedName(selected, s.Name) {
			matching++
			if s.Inactive {
				sourceLog(ctx, 193, "下载器 "+s.Name+" 未连接，请检查配置")
			} else {
				active++
			}
		}
	}
	if matching == 0 {
		sourceLog(ctx, 187, "获取下载器实例失败，请检查配置")
	} else if active == 0 {
		sourceLog(ctx, 198, "没有已连接的下载器，请检查配置")
	}
}
