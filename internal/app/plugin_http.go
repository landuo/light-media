package app

import (
	"encoding/json"
	"io"
	"light-media/internal/plugins"
	"light-media/internal/plugins/autosignin"
	"light-media/internal/plugins/builtin"
	"light-media/internal/plugins/remoteidentifiers"
	"light-media/internal/plugins/removelink"
	"light-media/internal/plugins/scrapefileclean"
	serverstatus "light-media/internal/plugins/serverstatus"
	"light-media/internal/plugins/subscribegroup"
	"light-media/internal/plugins/uninstall"
	"net/http"
	"strings"
)

type pluginCard struct {
	Available                  bool                        `json:"available"`
	UnavailableReason          string                      `json:"unavailable_reason,omitempty"`
	RepositoryURL              string                      `json:"repo_url,omitempty"`
	RepositoryVersion          string                      `json:"repository_version,omitempty"`
	RestoreReview              *pluginReviewRestoreRequest `json:"restore_review,omitempty"`
	RestoreReviewBlockedReason string                      `json:"restore_review_blocked_reason,omitempty"`
	UpdateReviewPending        bool                        `json:"update_review_pending,omitempty"`
	UpdateBlockedReason        string                      `json:"update_blocked_reason,omitempty"`
	Update                     *pluginUpdateRequest        `json:"update,omitempty"`
	HasUpdate                  bool                        `json:"has_update,omitempty"`
	DisplayVersion             string                      `json:"display_version,omitempty"`
	SourceInstanceID           string                      `json:"source_instance_id,omitempty"`
	plugins.Manifest
	InstanceID string `json:"instance_id"`
	Installed  bool   `json:"installed"`
	State      bool   `json:"state"`
}

func decodePluginBody(w http.ResponseWriter, r *http.Request, value any) error {
	r.Body = http.MaxBytesReader(w, r.Body, 2<<20)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if decoder.Decode(value) != nil || decoder.Decode(new(any)) != io.EOF {
		return bad("INVALID_PLUGIN_JSON", "插件请求含无效或尚未支持的字段")
	}
	return nil
}

func decodePluginJSONBody(body io.Reader, value any) error {
	decoder := json.NewDecoder(body)
	decoder.DisallowUnknownFields()
	if decoder.Decode(value) != nil || decoder.Decode(new(any)) != io.EOF {
		return bad("INVALID_PLUGIN_JSON", "插件请求含无效或尚未支持的字段")
	}
	return nil
}

func (a *App) validateNativePluginConfig(pluginID string, raw json.RawMessage) (any, error) {
	if !strings.HasPrefix(strings.TrimSpace(string(raw)), "{") {
		return nil, bad("INVALID_PLUGIN_JSON", "插件配置必须是对象")
	}
	decode := func(value any) error {
		decoder := json.NewDecoder(strings.NewReader(string(raw)))
		decoder.DisallowUnknownFields()
		if decoder.Decode(value) != nil || decoder.Decode(new(any)) != io.EOF {
			return bad("INVALID_PLUGIN_JSON", "插件请求含无效或尚未支持的字段")
		}
		return nil
	}
	switch pluginID {
	case "AutoSignIn":
		var config autosignin.Object
		if autosignin.Decode(raw, &config) != nil || config == nil {
			return nil, bad("INVALID_PLUGIN_CONFIG", "签到配置必须为有效对象")
		}
		return append(json.RawMessage(nil), raw...), nil
	case "CloudflareSpeedTest":
		if _, e := cloudflareConfig(raw); e != nil {
			return nil, e
		}
		return append(json.RawMessage(nil), raw...), nil
	case "AutoBackup":
		if _, err := autoBackupConfig(raw); err != nil {
			return nil, err
		}
		return append(json.RawMessage(nil), raw...), nil
	case "TorrentTransfer", "TorrentTransfermusic":
		fields, e := transferSourceMap(raw)
		if e != nil {
			return nil, bad("INVALID_PLUGIN_CONFIG", "转移做种配置必须为有效对象")
		}
		for _, key := range []string{"cron", "nolabels", "includelabels", "includecategory", "frompath", "topath", "fromdownloader", "todownloader", "fromtorrentpath", "nopaths", "add_torrent_tags"} {
			if value := fields[key]; value != nil {
				if _, ok := value.(string); !ok {
					return nil, bad("INVALID_PLUGIN_CONFIG", "转移做种文本字段必须是字符串或空值")
				}
			}
		}
		return append(json.RawMessage{}, raw...), nil
	case "IYUUAutoSeed":
		return iyuuTypedConfig(raw)
	case "CleanInvalidSeed":
		if _, err := cleanSeedConfigMap(raw); err != nil {
			return nil, bad("INVALID_PLUGIN_CONFIG", "清理无效做种配置必须是有效对象")
		}
		return append(json.RawMessage(nil), raw...), nil
	case "ScrapeFileClean":
		config := scrapefileclean.NewConfig()
		if config.Apply(raw) != nil {
			return nil, bad("INVALID_PLUGIN_CONFIG", "清理媒体文件配置格式无效")
		}
		return append(json.RawMessage(nil), raw...), nil
	case "RemoveLink":
		config := removelink.NewConfig()
		if config.Apply(raw) != nil {
			return nil, bad("INVALID_PLUGIN_CONFIG", "清理媒体文件配置格式无效")
		}
		return append(json.RawMessage(nil), raw...), nil
	case "MPServerStatus":
		machine := serverstatus.New(nil, nil)
		if !json.Valid(raw) || machine.Init(raw) != nil {
			return nil, bad("INVALID_PLUGIN_CONFIG", "服务监控插件配置格式无效")
		}
		return append(json.RawMessage(nil), raw...), nil
	case "PluginUnInstall":
		if _, err := uninstall.ParseConfiguration(raw); err != nil {
			return nil, bad("INVALID_PLUGIN_CONFIG", "卸载插件配置不是原版支持的格式")
		}
		// The original reset preserves missing/null flags and drops unknown
		// fields only after execution. Do not normalize those distinctions here.
		return append(json.RawMessage(nil), raw...), nil
	case "SubscribeGroup":
		var config subscribegroup.Configuration
		if err := decode(&config); err != nil {
			return nil, err
		}
		if config.UpdateConfs != nil && len(*config.UpdateConfs) > 256<<10 || len(config.UpdateDetails) > 1000 {
			return nil, bad("INVALID_PLUGIN_CONFIG", "订阅规则配置超过大小上限")
		}
		// Preserve unknown/duplicate selected strings and rule whitespace: the
		// original uses direct membership and sequential '#' / ':' splitting.
		for _, detail := range config.UpdateDetails {
			if len(detail) > 64<<10 {
				return nil, bad("INVALID_PLUGIN_CONFIG", "订阅规则填充字段超过大小上限")
			}
		}
		return config, nil
	case "SiteStatistic":
		var config builtin.SiteStatisticConfiguration
		if err := decode(&config); err != nil {
			return nil, err
		}
		if config.Validate() != nil {
			return nil, bad("INVALID_PLUGIN_CONFIG", "站点统计显示或通知选项无效")
		}
		return config, nil
	case "MergeSiteSwitch":
		var config builtin.SiteControls
		if err := decode(&config); err != nil {
			return nil, err
		}
		if config.Validate() != nil {
			return nil, bad("INVALID_PLUGIN_CONFIG", "聚合站点开关字段或站点引用无效")
		}
		return config, nil
	case "RemoteIdentifiers":
		var fields map[string]json.RawMessage
		if identifiersSurrogate(raw) || json.Unmarshal(raw, &fields) != nil || fields == nil {
			return nil, bad("INVALID_PLUGIN_CONFIG", "共享识别词配置无效或超出原生Unicode范围")
		}
		state := remoteidentifiers.Cold()
		if state.Init(raw) != nil {
			return nil, bad("INVALID_PLUGIN_CONFIG", "共享识别词配置无效")
		}
		// Preserve source values and unknown keys. Invalid cron/type failures occur
		// during initialization, after the source's one-shot normalization order.
		if urls, ok := state.FileURLs.(string); ok {
			if len(urls) > 64<<10 || len(strings.Split(urls, "\n")) > 128 {
				return nil, bad("INVALID_IDENTIFIERS_URL", "远程词表地址过多或过长")
			}
		}
		return append(json.RawMessage(nil), raw...), nil

	case "CategoryEditor":
		var config struct {
			Enabled bool   `json:"enabled"`
			Content string `json:"content"`
		}
		if err := decode(&config); err != nil {
			return nil, err
		}
		if config.Enabled {
			if _, _, err := categoryPluginReload([]byte(config.Content)); err != nil {
				return nil, err
			}
		}
		return config, nil
	case "CustomHosts":
		var config builtin.CustomHostsConfiguration
		if err := decode(&config); err != nil {
			return nil, err
		}
		if err := config.Validate(); err != nil {
			return nil, bad("INVALID_HOSTS_VALUE", "Hosts配置必须为文本或文本行数组，且不超过128 KiB")
		}
		return config, nil
	case "SystemProcess":
		var config builtin.SystemProcessConfiguration
		if err := json.Unmarshal(raw, &config); err != nil {
			return nil, bad("INVALID_PLUGIN_CONFIG", "系统进程配置必须是对象")
		}
		return config, nil
	case "MediaServerRefresh":
		var config builtin.MediaServerRefreshConfiguration
		if err := decode(&config); err != nil {
			return nil, err
		}
		if _, err := config.Duration(); err != nil {
			return nil, bad("INVALID_PLUGIN_CONFIG", "媒体库刷新延迟或媒体服务器引用无效")
		}
		for _, server := range config.MediaServers {
			if server != "fnos" {
				return nil, bad("PLUGIN_MEDIA_SERVER_UNMAPPED", "媒体服务器引用尚未映射")
			}
		}
		return config, nil
	default:
		return nil, bad("PLUGIN_IMPLEMENTATION_UNAVAILABLE", "此插件尚无配置校验实现")
	}
}
