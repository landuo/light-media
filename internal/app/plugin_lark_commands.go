package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"sort"
	"strings"

	"light-media/internal/commandrouter"
	"light-media/internal/download"
	"light-media/internal/downloadersource"
	"light-media/internal/notice"
	"light-media/internal/releasemeta"
)

type larkCommandTorrentQuery struct {
	DownloaderID, ConfigSHA, Kind, CaughtCode string
	Rows                                      []map[string]any
	Meta                                      []json.RawMessage
}
type larkCommandDownloading struct {
	Queries  []larkCommandTorrentQuery
	Torrents []downloadersource.Torrent
}

func (a *App) larkDispatchCommand(ctx context.Context, core *pluginLarkCoreExecution, text, user string) error {
	if e := a.larkCommandPresetAdmission(ctx); e != nil {
		return e
	}
	name, args := commandrouter.Split(text)
	if command, instance, found := a.lookupOriginalPluginCommand(name); found {
		return a.dispatchLarkPluginCommandInstance(ctx, command, instance, user)
	}
	command, ok := commandrouter.Lookup(name)
	if !ok {
		// Installed plugin get_command declarations (frozen registry) are a
		// separate ingress: their action goes through the plugin's own durable
		// command fact instead of a builtin preset handler.
		if pluginCommand, found := commandrouter.LookupPluginCommand(name); found {
			return a.dispatchLarkPluginCommand(ctx, pluginCommand, user)
		}
		return bad("LARK_COMMAND_REGISTRY_PENDING", "插件与动态命令注册来源尚未全部接入")
	}
	// A native read step freezes the exact source event split and builtin dispatch.
	// It never stands in for the consumer's actual query/side effect.
	route, e := larkCoreRead(ctx, a, core, "command.dispatch", struct{ Text, User string }{text, user}, func(context.Context) (larkCommandDispatch, error) {
		return larkCommandDispatch{command, args}, nil
	})
	if e != nil {
		return e
	}
	if !larkSameRaw(mustCleanSeedJSON(route), mustCleanSeedJSON(larkCommandDispatch{command, args})) {
		return larkEvidenceError()
	}
	switch name {
	case "/downloading":
		return a.larkCommandDownloading(ctx, core, user)
	case "/version":
		return a.larkCommandVersion(ctx, core, user)
	}
	return bad("LARK_COMMAND_CONSUMER_PENDING", "该原版命令的实际业务消费者尚未接入")
}
func (a *App) larkCommandDownloading(ctx context.Context, core *pluginLarkCoreExecution, user string) error {
	downloaders := []DownloaderConfig{}
	for _, d := range a.Settings().Downloaders {
		if downloaderEnabled(d) {
			downloaders = append(downloaders, d)
		}
	}
	// ChainBase executes system modules by get_priority(): qB=1, TR=2.
	// Each module's configured instance insertion order stays unchanged.
	sort.SliceStable(downloaders, func(i, j int) bool {
		priority := func(d DownloaderConfig) int {
			if d.Kind == "qb" || d.Kind == "qbittorrent" {
				return 1
			}
			return 2
		}
		return priority(downloaders[i]) < priority(downloaders[j])
	})
	identity := []struct{ ID, SHA string }{}
	for _, d := range downloaders {
		identity = append(identity, struct{ ID, SHA string }{d.ID, downloaderSnapshot(d)})
	}
	snapshot, e := larkCoreRead(ctx, a, core, "command.downloading.list", identity, func(ctx context.Context) (larkCommandDownloading, error) {
		out := larkCommandDownloading{Queries: []larkCommandTorrentQuery{}, Torrents: []downloadersource.Torrent{}}
		options, e := a.globalRecognitionOptions(ctx)
		if e != nil {
			return out, e
		}
		parser, e := releasemeta.NewMetaInfoParser(releasemeta.MetaInfoEnvironment{Options: options})
		if e != nil {
			return out, e
		}
		for _, d := range downloaders {
			current, e := a.configuredDownloader(d.ID)
			if e != nil || downloaderSnapshot(current) != downloaderSnapshot(d) {
				return out, bad("REVIEW_DOWNLOADER_CHANGED", "命令查询的下载器配置已变化")
			}
			if len(d.Mappings) > 0 {
				return out, bad("SOURCE_DOWNLOADER_MAPPING_PENDING", "原下载器路径映射来源尚未接入")
			}
			client, e := download.New(d.Config)
			if e != nil {
				return out, e
			}
			wire, e := client.SourceDownloadingRows(ctx)
			query := larkCommandTorrentQuery{DownloaderID: d.ID, ConfigSHA: downloaderSnapshot(d), Kind: wire.Kind, Rows: wire.Rows, Meta: []json.RawMessage{}}
			if e != nil {
				if ctx.Err() != nil {
					return out, ctx.Err()
				}
				var failure *download.Error
				if !errors.As(e, &failure) {
					return out, e
				}
				// Original get_torrents catches daemon/protocol exceptions; its outer
				// list_torrents sees None as []. Keep that caught result independently.
				query.CaughtCode = failure.Operation + ":" + failure.Code
				a.event(ctx, core.job, "ERROR", "downloaders", "source_query_failed", "下载器 "+d.ID+" 查询失败，原消息流程返回空列表（"+query.CaughtCode+"）")
				query.Rows = []map[string]any{}
				out.Queries = append(out.Queries, query)
				continue
			}
			// The native application actually submits this exact tag on Add. It is an
			// explicit native ownership identity, not a guessed old TORRENT_TAG setting.
			rows, caught, e := downloadersource.Filter(wire.Kind, wire.Rows, "light-media")
			if e != nil {
				return out, e
			}
			query.CaughtCode = caught
			if caught != "" {
				a.event(ctx, core.job, "ERROR", "downloaders", "source_query_failed", "下载器 "+d.ID+" 任务标签解析失败（"+caught+"）")
			}
			for _, raw := range rows {
				title, ok := raw["name"].(string)
				if !ok {
					return out, downloadersource.ErrDependency
				}
				meta, e := parser.ParseTitle(title, nil, nil)
				if e != nil {
					return out, e
				}
				state, e := meta.EncodeState()
				if e != nil {
					return out, e
				}
				query.Meta = append(query.Meta, state)
				derived, e := meta.Derived()
				if e != nil {
					return out, e
				}
				var year *string
				if v := meta.Get("year"); v != nil {
					s, ok := v.(string)
					if !ok {
						return out, downloadersource.ErrDependency
					}
					year = &s
				}
				fields := downloadersource.Meta{Name: meta.Name(), Year: year, SeasonEpisode: sourceText(derived["season_episode"])}
				norm := func(p string) (string, error) {
					return strings.TrimPrefix(downloadersource.PosixPath(p), "local:"), nil
				}
				name := d.Name
				if name == "" {
					name = d.ID
				}
				var torrent downloadersource.Torrent
				if wire.Kind == "qbittorrent" {
					torrent, e = downloadersource.BuildQB(raw, name, fields, norm)
				} else {
					var attrs map[string]any
					attrs, e = downloadersource.TRAttributes(raw)
					if e == nil {
						torrent, e = downloadersource.BuildTR(attrs, name, fields, norm)
					}
				}
				if e != nil {
					return out, e
				}
				out.Torrents = append(out.Torrents, torrent)
			}
			out.Queries = append(out.Queries, query)
		}
		return out, nil
	})
	if e != nil {
		return e
	}
	title, text, e := downloadersource.DownloadingMessage(snapshot.Torrents)
	if e != nil {
		return e
	}
	var content any = text
	if len(snapshot.Torrents) == 0 {
		content = nil
	}
	payload := sourceBusinessNotice{AbsentContext: true, Envelope: notice.Object{"channel": "飞书", "source": "Lark", "mtype": "资源下载", "title": title, "text": content, "userid": user, "link": sourceCommandDownloadingLink(a.Settings().AppDomain), "save_history": false}, Kwargs: notice.Object{}}
	return a.larkPostBusinessNotice(ctx, core, payload)
}

type larkCommandDispatch struct {
	Command commandrouter.Command
	Args    string
}

func sourceCommandDownloadingLink(domain string) any {
	if domain == "" {
		return nil
	}
	if !strings.HasSuffix(domain, "/") {
		domain += "/"
	}
	if !strings.HasPrefix(domain, "http://") && !strings.HasPrefix(domain, "https://") {
		domain = "http://" + domain
	}
	u, e := url.Parse(domain)
	if e != nil {
		return nil
	}
	return u.ResolveReference(&url.URL{Fragment: "/downloading"}).String()
}

func (a *App) larkCommandPresetAdmission(ctx context.Context) error {
	rows, e := a.Store.DB.QueryContext(ctx, `SELECT plugin_id,version FROM plugin_installations WHERE installed=1 ORDER BY instance_id`)
	if e != nil {
		return e
	}
	defer rows.Close()
	installed := []commandrouter.Installation{}
	for rows.Next() {
		var i commandrouter.Installation
		if e = rows.Scan(&i.ID, &i.Version); e != nil {
			return e
		}
		installed = append(installed, i)
	}
	if e = rows.Err(); e != nil {
		return e
	}
	return commandrouter.AdmitPreset(installed)
}

// dispatchLarkPluginCommand carries one installed plugin declaration into its
// existing durable command fact. The Lark message gate has already rejected
// non-admin senders when the plugin configures admin_users, so this path never
// invents a second authority: the channel channel/user fields are the same
// source values the original PluginAction event carried.
func (a *App) dispatchLarkPluginCommand(ctx context.Context, command commandrouter.PluginCommand, user string) error {
	return a.dispatchLarkPluginCommandInstance(ctx, command, command.PluginID, user)
}
func (a *App) dispatchLarkPluginCommandInstance(ctx context.Context, command commandrouter.PluginCommand, instance, user string) error {
	if command.Name == "" || command.PluginID == "" || strings.TrimSpace(user) == "" {
		return bad("LARK_PLUGIN_COMMAND_DENIED", "插件命令来源无效")
	}
	if loaded, ok := a.pluginRunningInstances.Load(instance); ok && loaded.(*originalPluginInstance).external {
		return a.enqueueOriginalExternalCommand(ctx, loaded.(*originalPluginInstance), command, user)
	}
	return bad("PLUGIN_ADAPTER_UNAVAILABLE", "插件尚未适配新的标准模块")
}
