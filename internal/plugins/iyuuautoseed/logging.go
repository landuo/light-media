package iyuuautoseed

import "context"

// SourceLog is ephemeral diagnostic progress, never a replayable side effect
// or an SDK supplied capability. The App binds its immutable run identity and
// removes credentials before writing the actual shared log sink.
type SourceLog struct {
	Line                   int
	Level, Method, Message string
}
type SourceLogSink func(context.Context, SourceLog)
type sourceLogKey struct{}
type sourceLogDefinition struct{ Level, Method string }

// Exact installed __init__.py SHA256 a47d36432e87da046af025fc76281d99c714064a8fe05bdf8b11e83d2c683a3f.
var sourceLogDefinitions = map[int]sourceLogDefinition{
	153:  {Level: "INFO", Method: "init_plugin"},
	179:  {Level: "WARN", Method: "service_infos"},
	184:  {Level: "WARN", Method: "service_infos"},
	190:  {Level: "WARN", Method: "service_infos"},
	195:  {Level: "WARN", Method: "service_infos"},
	206:  {Level: "DEBUG", Method: "auto_service_info"},
	211:  {Level: "WARN", Method: "auto_service_info"},
	215:  {Level: "WARN", Method: "auto_service_info"},
	746:  {Level: "INFO", Method: "auto_seed"},
	759:  {Level: "INFO", Method: "auto_seed"},
	763:  {Level: "INFO", Method: "auto_seed"},
	765:  {Level: "INFO", Method: "auto_seed"},
	770:  {Level: "INFO", Method: "auto_seed"},
	775:  {Level: "INFO", Method: "auto_seed"},
	784:  {Level: "INFO", Method: "auto_seed"},
	796:  {Level: "INFO", Method: "auto_seed"},
	804:  {Level: "INFO", Method: "auto_seed"},
	813:  {Level: "INFO", Method: "auto_seed"},
	825:  {Level: "INFO", Method: "auto_seed"},
	842:  {Level: "INFO", Method: "auto_seed"},
	875:  {Level: "INFO", Method: "check_recheck_service"},
	886:  {Level: "INFO", Method: "check_recheck_service"},
	893:  {Level: "INFO", Method: "check_recheck_service"},
	896:  {Level: "INFO", Method: "check_recheck_service"},
	905:  {Level: "INFO", Method: "__seed_torrents"},
	919:  {Level: "WARN", Method: "__seed_torrents"},
	921:  {Level: "WARN", Method: "__seed_torrents"},
	924:  {Level: "INFO", Method: "__seed_torrents"},
	944:  {Level: "INFO", Method: "__seed_torrents"},
	947:  {Level: "INFO", Method: "__seed_torrents"},
	950:  {Level: "INFO", Method: "__seed_torrents"},
	972:  {Level: "INFO", Method: "__seed_torrents"},
	1061: {Level: "ERROR", Method: "__download"},
	1080: {Level: "ERROR", Method: "__download"},
	1116: {Level: "DEBUG", Method: "__download_torrent"},
	1119: {Level: "INFO", Method: "__download_torrent"},
	1126: {Level: "INFO", Method: "__download_torrent"},
	1132: {Level: "WARN", Method: "__download_torrent"},
	1166: {Level: "ERROR", Method: "__download_torrent"},
	1169: {Level: "INFO", Method: "__download_torrent"},
	1186: {Level: "INFO", Method: "__download_torrent"},
	1190: {Level: "INFO", Method: "__download_torrent"},
	1198: {Level: "INFO", Method: "__download_torrent"},
	1205: {Level: "INFO", Method: "__add_recheck_torrents"},
	1306: {Level: "ERROR", Method: "__get_mteam_enclosure"},
	1325: {Level: "WARN", Method: "__get_mteam_enclosure"},
	1334: {Level: "ERROR", Method: "__get_monika_torrent"},
	1346: {Level: "WARN", Method: "__get_gpw_torrent_url_from_page"},
	1351: {Level: "INFO", Method: "__get_gpw_torrent_url_from_page"},
	1361: {Level: "ERROR", Method: "__get_gpw_torrent_url_from_page"},
	1370: {Level: "WARN", Method: "__get_gpw_torrent_url_from_page"},
	1375: {Level: "WARN", Method: "__get_gpw_torrent_url_from_page"},
	1382: {Level: "WARN", Method: "__get_gpw_torrent_url_from_page"},
	1393: {Level: "WARN", Method: "__get_gpw_torrent_url_from_page"},
	1398: {Level: "INFO", Method: "__get_gpw_torrent_url_from_page"},
	1401: {Level: "WARN", Method: "__get_gpw_torrent_url_from_page"},
	1453: {Level: "WARN", Method: "__get_download_url"},
	1462: {Level: "WARN", Method: "__get_download_url"},
	1471: {Level: "WARN", Method: "__get_torrent_url_from_page"},
	1475: {Level: "INFO", Method: "__get_torrent_url_from_page"},
	1487: {Level: "WARN", Method: "__get_torrent_url_from_page"},
	1495: {Level: "INFO", Method: "__get_torrent_url_from_page"},
	1502: {Level: "WARN", Method: "__get_torrent_url_from_page"},
	1505: {Level: "ERROR", Method: "__get_torrent_url_from_page"},
	1508: {Level: "WARN", Method: "__get_torrent_url_from_page"},
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

func (c *Core) queueAdd(ctx context.Context, service Service, id string) {
	sourceLog(ctx, 1205, "添加校验检查任务："+id+" ...")
	c.queue[service.Name] = append(c.queue[service.Name], id)
}
func (c *Core) logInventory(ctx context.Context, inventory []Service, mode string) {
	auto := c.config.values["auto_downloader"]
	autoActive := false
	if mode == "auto" || mode == "recheck" {
		if !truth(auto) {
			sourceLog(ctx, 206, "尚未配置主辅分离下载器，辅种不分离")
		} else {
			found := false
			for _, s := range inventory {
				if scalarEqual(auto, s.Name) {
					found = true
					if s.Inactive {
						sourceLog(ctx, 215, "下载器 "+s.Name+" 未连接，请检查配置")
					} else {
						autoActive = true
					}
					break
				}
			}
			if !found {
				sourceLog(ctx, 211, "获取主辅分离下载器实例失败，请检查配置")
			}
		}
	}
	if mode == "auto" || mode == "recheck" && autoActive {
		return
	}
	selected := c.config.values["downloaders"]
	if !truth(selected) {
		sourceLog(ctx, 179, "尚未配置下载器，请检查配置")
		return
	}
	matching, active := 0, 0
	for _, s := range inventory {
		if selectedName(selected, s.Name) {
			matching++
			if s.Inactive {
				sourceLog(ctx, 190, "下载器 "+s.Name+" 未连接，请检查配置")
			} else {
				active++
			}
		}
	}
	if matching == 0 {
		sourceLog(ctx, 184, "获取下载器实例失败，请检查配置")
	} else if active == 0 {
		sourceLog(ctx, 195, "没有已连接的下载器，请检查配置")
	}
}

func logString(value any) string {
	text, err := sourceString(value)
	if err != nil {
		return "[unsupported source value]"
	}
	return text
}
