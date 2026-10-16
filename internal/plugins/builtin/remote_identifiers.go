package builtin

import (
	"context"
	"encoding/json"
	"errors"

	"light-media/internal/plugins"
)

const RemoteIdentifiersCron = "30 4 * * *"
const RemoteIdentifiersURLs = "https://raw.githubusercontent.com/Putarku/MoviePilot-Help/main/Words/TV.txt\nhttps://raw.githubusercontent.com/Putarku/MoviePilot-Help/main/Words/anime.txt"

type RemoteIdentifiersConfiguration struct {
	Enable   bool   `json:"enable"`
	OnlyOnce bool   `json:"onlyonce"`
	Flitter  bool   `json:"flitter"`
	Cron     string `json:"cron"`
	FileURLs string `json:"file_urls"`
}

func RemoteIdentifiersManifest() plugins.Manifest {
	return plugins.Manifest{SchemaVersion: 1, PluginID: "RemoteIdentifiers", Name: "共享识别词", Description: "从Github、Etherpad远程文件中获取共享识别词并应用", Icon: "words.png", Version: "2.4.1", Author: "honue", AuthorURL: "https://github.com/honue", Runtime: "go-executable", Entry: "light-media", HostAPIMin: 1, HostAPIMax: 1, Methods: []string{"config_form", "invoke", "stop"}, Capabilities: []string{"identifiers.configure", "identifiers.refresh"}}
}

const remoteIdentifiersForm = `[{"component":"VForm","content":[
 {"component":"VRow","content":[
 {"component":"VCol","props":{"cols":12,"md":2},"content":[{"component":"VSwitch","props":{"model":"enable","label":"启用插件"}}]},
 {"component":"VCol","props":{"cols":12,"md":2},"content":[{"component":"VSwitch","props":{"model":"flitter","label":"过滤空白行"}}]},
 {"component":"VCol","props":{"cols":12,"md":3},"content":[{"component":"VSwitch","props":{"model":"onlyonce","label":"立即运行一次"}}]},
 {"component":"VCol","props":{"cols":12,"md":4},"content":[{"component":"VTextField","props":{"model":"cron","label":"定时任务周期","placeholder":"30 4 * * *"}}]}]},
 {"component":"VRow","content":[{"component":"VCol","props":{"cols":12,"md":12},"content":[{"component":"VTextarea","props":{"model":"file_urls","rows":6,"label":"远程文件地址（一行一个）","placeholder":"如果是Github文件地址请注意填写包含raw的! 这个才是文件地址，其他的是这个文件的页面地址"}}]}]},
 {"component":"VRow","content":[{"component":"VCol","props":{"cols":12},"content":[{"component":"VAlert","props":{"type":"success","variant":"tonal","text":"可自己创建分享地址（支持Github及Etherpad），Github需要raw的源文件地址，文件格式与系统中配置格式一致即可。"}}]}]},
 {"component":"VRow","content":[{"component":"VCol","props":{"cols":12},"content":[{"component":"VAlert","props":{"type":"info","variant":"tonal","text":"以下共享识别词由第三方审核维护，推荐使用：https://raw.githubusercontent.com/Putarku/MoviePilot-Help/main/Words/TV.txt、https://raw.githubusercontent.com/Putarku/MoviePilot-Help/main/Words/anime.txt"}}]}]}
]}]`

func HandleRemoteIdentifiers(ctx context.Context, method string, raw json.RawMessage, host plugins.HostCaller) (json.RawMessage, error) {
	if method == "stop" {
		return json.RawMessage(`null`), nil
	}
	if method == "config_form" {
		return json.Marshal(map[string]any{"conf": json.RawMessage(remoteIdentifiersForm), "model": RemoteIdentifiersConfiguration{Flitter: true, Cron: RemoteIdentifiersCron, FileURLs: RemoteIdentifiersURLs}})
	}
	var invocation plugins.Invocation
	if method != "invoke" || json.Unmarshal(raw, &invocation) != nil {
		return nil, errors.New("invalid identifiers invocation")
	}
	switch invocation.Action {
	case "init", "reload":
		return host(ctx, "identifiers.configure", json.RawMessage(`{}`))
	case "service":
		var input struct {
			OccurrenceID string `json:"occurrence_id"`
		}
		if json.Unmarshal(invocation.Input, &input) != nil || input.OccurrenceID == "" {
			return nil, errors.New("missing identifiers service occurrence")
		}
		request, _ := json.Marshal(input)
		return host(ctx, "identifiers.refresh", request)
	default:
		return nil, errors.New("unsupported identifiers action")
	}
}
