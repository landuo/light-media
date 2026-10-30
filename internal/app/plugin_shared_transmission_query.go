package app

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"light-media/internal/plugins"
	native "light-media/internal/plugins/iyuuautoseed/native"
	"light-media/internal/releasemeta/legacyanime"
)

func originalSharedTransmissionQueryCapability(method string) bool {
	return method == plugins.TransmissionTorrentsQueryCapability
}

func (a *App) originalSharedTransmissionQueryResource(ctx context.Context, scope originalBaseCallScope, method string, raw json.RawMessage) (json.RawMessage, error) {
	if !originalSharedTransmissionQueryCapability(method) || scope.validate == nil {
		return nil, bad("PLUGIN_CAPABILITY_DENIED", "下载器查询未获授权")
	}
	if err := scope.validate(ctx); err != nil {
		return nil, err
	}
	request, err := decodeSharedTransmissionQuery(raw)
	if err != nil {
		return nil, err
	}
	// Explicit configured ID cannot fall back to a name or default. The source
	// DownloaderHelper get_service only exposes enabled service configurations.
	configured, err := a.downloader(request.downloaderID)
	if err != nil {
		return nil, err
	}
	if configured.Kind != "tr" && configured.Kind != "transmission" {
		return nil, bad("DOWNLOADER_UNSUPPORTED", "此查询仅支持 Transmission")
	}
	guard := func(ctx context.Context) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := scope.validate(ctx); err != nil {
			return err
		}
		current, err := a.downloader(request.downloaderID)
		if err != nil {
			return err
		}
		if current.Config != configured.Config {
			return bad("DOWNLOADER_CHANGED", "下载器连接配置已变化，请重新查询")
		}
		return nil
	}
	if err = guard(ctx); err != nil {
		return nil, err
	}
	result := plugins.TransmissionQueryResult{SchemaVersion: 1, DownloaderID: configured.ID, Torrents: []plugins.TransmissionTorrent{}, Error: request.invalidIDs}
	if request.invalidIDs {
		return json.Marshal(result)
	}
	client, err := native.NewTransmissionQueryClient(native.Config{ID: configured.ID, Kind: configured.Kind, BaseURL: configured.BaseURL, Username: configured.Username, Password: configured.Password}, guard)
	if err != nil {
		return nil, err
	}
	defer client.Close()
	rows, queryErr := client.Torrents(ctx, request.ids)
	if err = guard(ctx); err != nil {
		return nil, err
	}
	if queryErr != nil {
		var nativeError *native.Error
		if errors.As(queryErr, &nativeError) && nativeError.Code == "body_limit" {
			return nil, bad("PLUGIN_VALUE_TOO_LARGE", "Transmission 查询响应超过共享传输上限")
		}
		// The original wrapper catches daemon/auth/transport failures and returns
		// ([], True). Never turn that into a successful empty enumeration.
		result.Error = true
		return json.Marshal(result)
	}
	var fields []map[string]json.RawMessage
	if json.Unmarshal(rows, &fields) != nil || fields == nil {
		return nil, bad("DOWNLOADER_INVALID_RESPONSE", "Transmission 查询响应无效")
	}
	for _, row := range fields {
		if err = guard(ctx); err != nil {
			return nil, err
		}
		torrent := plugins.TransmissionTorrent{Fields: row}
		if len(request.status) > 0 {
			status, e := torrent.Status()
			if e != nil {
				return nil, bad("DOWNLOADER_INVALID_RESPONSE", "Transmission 状态无效")
			}
			if !sharedTransmissionContains(request.status, status) {
				continue
			}
		}
		// The source reads labels even when no filter was requested. Keep raw
		// labels in Fields; stripping is only the source subset comparison.
		labels, e := torrent.Labels()
		if e != nil {
			return nil, bad("DOWNLOADER_INVALID_RESPONSE", "Transmission 标签无效")
		}
		for i := range labels {
			labels[i] = legacyanime.Strip(labels[i])
		}
		matches := true
		for _, tag := range request.tags {
			if !sharedTransmissionContains(labels, tag) {
				matches = false
				break
			}
		}
		if matches {
			result.Torrents = append(result.Torrents, torrent)
		}
	}
	output, err := json.Marshal(result)
	if err != nil {
		return nil, err
	}
	if len(output) > plugins.MaxPayloadBytes {
		return nil, bad("PLUGIN_VALUE_TOO_LARGE", "Transmission 查询响应超过共享传输上限")
	}
	if err = guard(ctx); err != nil {
		return nil, err
	}
	return output, nil
}

func sharedTransmissionContains(values []string, value string) bool {
	for _, item := range values {
		if item == value {
			return true
		}
	}
	return false
}

// Keep exact source string/list filter semantics: no lowercasing, trimming or
// reinterpretation of status names, and no implicit UI-owned-task predicate.
func sharedTransmissionFilter(raw json.RawMessage, tags bool) ([]string, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var single string
	if json.Unmarshal(raw, &single) == nil {
		if single == "" {
			return nil, nil
		}
		if tags {
			return strings.Split(single, ","), nil
		}
		return []string{single}, nil
	}
	var list []string
	if json.Unmarshal(raw, &list) != nil {
		return nil, bad("INVALID_PLUGIN_VALUE", "Transmission 筛选必须为字符串或字符串数组")
	}
	return list, nil
}
