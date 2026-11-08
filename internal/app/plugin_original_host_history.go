package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"light-media/internal/plugins"
)

func originalHostHistoryCapability(method string) bool {
	return method == plugins.DownloadHistoryReadCapability || method == plugins.TransferHistoryReadCapability || method == plugins.TransferHistoryDeleteCapability
}
func (a *App) originalHostHistoryResource(ctx context.Context, scope originalBaseCallScope, method string, raw json.RawMessage) (json.RawMessage, error) {
	switch method {
	case plugins.DownloadHistoryReadCapability:
		var in plugins.DownloadHistoryRequestV1
		if err := originalResourceDecode(raw, &in); err != nil {
			return nil, err
		}
		if !plugins.DownloaderTaskHashValid(in.Hash) {
			return nil, bad("INVALID_PLUGIN_VALUE", "下载历史查询需要准确Hash")
		}
		if in.SourceProjection {
			if scope.phase != "download_notice" || !originalSubscribeGroupEvent(ctx, scope) {
				return nil, bad("PLUGIN_CAPABILITY_DENIED", "来源下载历史须绑定实际订阅填充事件")
			}
			out, err := a.originalSubscribeGroupDownloadHistory(ctx, in.Hash)
			if err != nil {
				return nil, err
			}
			if err = scope.validate(ctx); err != nil {
				return nil, err
			}
			return json.Marshal(out)
		}
		var b []byte
		err := a.Store.DB.QueryRowContext(ctx, `SELECT payload FROM records WHERE kind='intent' AND json_extract(payload,'$.hash')=? ORDER BY json_extract(payload,'$.created_at') DESC,id DESC LIMIT 1`, in.Hash).Scan(&b)
		out := plugins.DownloadHistoryResultV1{SchemaVersion: 1}
		if errors.Is(err, sql.ErrNoRows) {
			return json.Marshal(out)
		}
		if err != nil {
			return nil, err
		}
		var i Intent
		if err = json.Unmarshal(b, &i); err != nil {
			return nil, err
		}
		out.Item = &plugins.DownloadHistoryRowV1{ID: i.ID, Hash: i.Hash, Title: i.Media.Title, Type: i.Media.MediaType, TMDBID: i.Media.ID, Season: i.Season, Episodes: i.Episodes, SiteID: i.SiteID, DownloaderID: i.DownloaderID, SavePath: i.DownloadPath, State: i.State, CreatedAt: i.CreatedAt}
		if err = scope.validate(ctx); err != nil {
			return nil, err
		}
		return json.Marshal(out)
	case plugins.TransferHistoryReadCapability:
		var in plugins.TransferHistoryRequestV1
		if err := originalResourceDecode(raw, &in); err != nil {
			return nil, err
		}
		count := 0
		for _, s := range []string{in.ID, in.Source, in.Target} {
			if s != "" {
				count++
			}
			if len(s) > 4096 {
				return nil, bad("INVALID_PLUGIN_VALUE", "整理历史查询条件过长")
			}
		}
		if count != 1 {
			return nil, bad("INVALID_PLUGIN_VALUE", "整理历史查询须选择一个准确ID或路径")
		}
		query := transferHistoryCTE + `SELECT ` + transferHistoryColumns + ` FROM history WHERE `
		value := in.ID
		if in.ID != "" {
			query += `history_id=?`
		} else if in.Source != "" {
			query += `json_extract(file,'$.source')=?`
			value = in.Source
		} else {
			query += `json_extract(file,'$.target')=?`
			value = in.Target
		}
		item, err := scanTransferHistory(a.Store.DB.QueryRowContext(ctx, query+` ORDER BY history_created_at,history_id LIMIT 1`, value))
		out := plugins.TransferHistoryResultV1{SchemaVersion: 1}
		if errors.Is(err, sql.ErrNoRows) {
			return json.Marshal(out)
		}
		if err != nil {
			return nil, err
		}
		out.Item = &plugins.TransferHistoryRowV1{ID: item.ID, Source: item.Source, Target: item.Target, Title: item.Media.Title, Type: item.Media.MediaType, TMDBID: item.Media.ID, Season: item.Season, Episodes: item.Episodes, Status: item.Status == "success", Mode: item.Mode, Size: item.Size, CreatedAt: item.CreatedAt}
		if err = scope.validate(ctx); err != nil {
			return nil, err
		}
		return json.Marshal(out)
	case plugins.TransferHistoryDeleteCapability:
		var in plugins.TransferHistoryDeleteV1
		if err := originalResourceDecode(raw, &in); err != nil {
			return nil, err
		}
		if in.ID == "" || len(in.ID) > 128 {
			return nil, bad("INVALID_PLUGIN_VALUE", "删除整理历史须指定准确ID")
		}
		item, err := a.transferHistoryItem(ctx, in.ID)
		if errors.Is(err, sql.ErrNoRows) {
			return json.Marshal(plugins.HostMutationV1{SchemaVersion: 1})
		}
		if err != nil {
			return nil, err
		}
		if err = scope.validate(ctx); err != nil {
			return nil, err
		}
		_, err = a.deleteTransferHistory(ctx, item, historyDeleteRequest{Confirm: true})
		if err != nil {
			return nil, err
		}
		return json.Marshal(plugins.HostMutationV1{SchemaVersion: 1, Changed: true})
	}
	return nil, bad("PLUGIN_CAPABILITY_DENIED", "未知历史资源操作")
}
