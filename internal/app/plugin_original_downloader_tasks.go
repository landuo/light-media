package app

import (
	"context"
	"encoding/json"
	"net/url"
	"strings"

	"light-media/internal/plugins"
	native "light-media/internal/plugins/iyuuautoseed/native"
)

// This helper is reached only after the ordinary current-call capability and
// identity checks. It reuses the existing native protocol client, NOT its old
// plugin-specific owner, journal, receipt, queue or business implementation.
func (a *App) originalDownloaderTasks(ctx context.Context, scope originalBaseCallScope, raw json.RawMessage) (json.RawMessage, error) {
	return a.originalDownloaderTasksOutcome(ctx, scope, raw, false)
}
func (a *App) originalDownloaderTasksOutcome(ctx context.Context, scope originalBaseCallScope, raw json.RawMessage, source bool) (json.RawMessage, error) {
	var request plugins.DownloaderTasksRequestV1
	if err := originalBrushDownloaderBatchDecode(ctx, scope, raw, &request); err != nil {
		return nil, err
	}
	hashes, err := request.CanonicalSelection()
	if err != nil {
		return nil, bad("INVALID_PLUGIN_VALUE", "下载器读取必须指定实例 ID 和准确 Hash 数组")
	}
	// The explicit ID was validated above: configuredDownloader cannot fall
	// back to a default/name. Enabled controls automatic work, not existence
	// or this explicit read. No connection/Active claim is inferred from it.
	configured, err := a.configuredDownloader(request.DownloaderID)
	if err != nil {
		return nil, err
	}
	kind := configured.Kind
	switch kind {
	case "qb", "qbittorrent":
		kind = "qbittorrent"
	case "tr", "transmission":
		kind = "transmission"
	default:
		return nil, bad("DOWNLOADER_UNSUPPORTED", "下载器类型不支持任务读取")
	}
	guard := func(ctx context.Context) (err error) {
		defer func() {
			if err != nil {
				err = &originalDownloaderGuardError{cause: err}
			}
		}()
		if err := ctx.Err(); err != nil {
			return err
		}
		if scope.validate == nil {
			return bad("PLUGIN_CAPABILITY_DENIED", "插件读取不在当前调用范围内")
		}
		if err := scope.validate(ctx); err != nil {
			return err
		}
		current, err := a.configuredDownloader(request.DownloaderID)
		if err != nil {
			return err
		}
		if current.Config != configured.Config {
			return bad("DOWNLOADER_CHANGED", "下载器连接配置已变化，请重新读取")
		}
		return nil
	}
	if err := guard(ctx); err != nil {
		return nil, err
	}
	out := plugins.DownloaderTasksV1{SchemaVersion: 1, DownloaderID: configured.ID, Kind: kind, Tasks: []plugins.DownloaderTaskV1{}}
	if len(hashes) == 0 {
		// Native nil and empty slices have distinct wire behavior, and qB may
		// interpret an empty hashes parameter as all. Never dispatch either.
		return originalHostDownloaderTasksOutcome(out, source, false)
	}
	client, err := native.NewIYUUClient(native.Config{ID: configured.ID, Kind: kind, BaseURL: configured.BaseURL, Username: configured.Username, Password: configured.Password}, native.IYUUOptions{APIKey: configured.APIKey, SourceQBLogin: source, SourceTRResponse: source, Guard: guard})
	if err != nil {
		return nil, err
	}
	defer client.Close()
	body, err := client.Torrents(ctx, hashes, false)
	// Recover live-scope cancellation/revocation before returning a sanitized
	// transport error, and reject a response received after the call closed.
	if currentErr := guard(ctx); currentErr != nil {
		return nil, currentErr
	}
	if err != nil {
		if source && originalDownloaderNativeError(ctx, err) {
			return originalHostDownloaderTasksOutcome(out, true, true)
		}
		return nil, err
	}
	out.Tasks, err = originalDownloaderTaskProjectionForBrush(kind, hashes, body, false, originalBrushSourceScope(ctx, scope))
	if err != nil {
		return nil, err
	}
	result, err := originalHostDownloaderTasksOutcome(out, source, false)
	if err != nil {
		return nil, err
	}
	if len(result) > plugins.MaxPayloadBytes {
		return nil, bad("PLUGIN_VALUE_TOO_LARGE", "下载器读取响应超出共享资源上限")
	}
	if err := guard(ctx); err != nil {
		return nil, err
	}
	return result, nil
}

// Brush sends one daemon selection at a time, including large groups. Do not
// split it into several mutations or widen the ordinary resources' decoder.
func originalBrushDownloaderBatchDecode(ctx context.Context, scope originalBaseCallScope, raw json.RawMessage, output any) error {
	if originalBrushSourceScope(ctx, scope) {
		return originalResourceDecodeBounded(raw, output, plugins.MaxPayloadBytes)
	}
	return originalResourceDecode(raw, output)
}

func originalDownloaderTaskProjection(kind string, hashes []string, raw json.RawMessage) ([]plugins.DownloaderTaskV1, error) {
	return originalDownloaderTaskProjectionWithOptions(kind, hashes, raw, false)
}

func originalDownloaderTaskProjectionWithOptions(kind string, hashes []string, raw json.RawMessage, includeQBPackFields bool) ([]plugins.DownloaderTaskV1, error) {
	return originalDownloaderTaskProjectionForBrush(kind, hashes, raw, includeQBPackFields, false)
}

func originalDownloaderTaskProjectionForBrush(kind string, hashes []string, raw json.RawMessage, includeQBPackFields, includeBrushTrackers bool) ([]plugins.DownloaderTaskV1, error) {
	invalid := func() error { return bad("DOWNLOADER_INVALID_RESPONSE", "下载器任务响应无效") }
	var rows []map[string]json.RawMessage
	if json.Unmarshal(raw, &rows) != nil || rows == nil {
		return nil, invalid()
	}
	selected := make(map[string]bool, len(hashes))
	for _, hash := range hashes {
		selected[hash] = true
	}
	observed := make(map[string]plugins.DownloaderTaskV1, len(hashes))
	key := "hash"
	if kind == "transmission" {
		key = "hashString" // Existing core client already resolves modern aliases.
	}
	for _, row := range rows {
		var hash string
		if row == nil || json.Unmarshal(row[key], &hash) != nil || !plugins.DownloaderTaskHashValid(hash) {
			return nil, invalid()
		}
		hash = strings.ToLower(hash)
		if !selected[hash] {
			continue // A daemon cannot expand the caller's exact selection.
		}
		if _, duplicate := observed[hash]; duplicate {
			return nil, invalid()
		}
		// json.Number itself accepts quoted numbers. Native counters/statuses
		// are JSON numbers, so reject a type-changing scalar before decoding.
		numeric := originalQBTaskNumericFields
		if kind == "transmission" {
			numeric = originalTRTaskNumericFields
		} else if !includeQBPackFields {
			// Older workers reject unknown fields in a resource response.
			delete(row, "completed")
			delete(row, "availability")
		}
		// Credentials may appear in any tracker URL segment. Only the actual
		// Brush worker needs the tracker origin for tracker-to-site grouping.
		trackerKey := "tracker"
		if kind == "transmission" {
			trackerKey = "trackerList"
		}
		var tracker string
		if includeBrushTrackers {
			_ = json.Unmarshal(row[trackerKey], &tracker)
			tracker = originalBrushTrackerOrigins(tracker)
		}
		delete(row, "tracker")
		delete(row, "trackerList")
		for _, field := range numeric {
			value := strings.TrimSpace(string(row[field]))
			if value != "" && value != "null" && (value[0] < '0' || value[0] > '9') && value[0] != '-' {
				return nil, invalid()
			}
		}
		body, err := json.Marshal(row)
		if err != nil {
			return nil, invalid()
		}
		item := plugins.DownloaderTaskV1{Hash: hash}
		// Typed projection drops unknown keys, tracker/announce URLs, magnet
		// links, private comments, raw errors and nested extension objects.
		// It retains native numeric lexemes without a float64 intermediate.
		if kind == "qbittorrent" {
			item.QBittorrent = &plugins.QBittorrentTaskInfoV1{}
			err = json.Unmarshal(body, item.QBittorrent)
			item.QBittorrent.Tracker = tracker
		} else {
			item.Transmission = &plugins.TransmissionTaskInfoV1{}
			err = json.Unmarshal(body, item.Transmission)
			item.Transmission.TrackerList = tracker
		}
		if err != nil {
			return nil, invalid()
		}
		observed[hash] = item
	}
	out := []plugins.DownloaderTaskV1{}
	for _, hash := range hashes {
		if item, ok := observed[hash]; ok {
			out = append(out, item)
		}
	}
	return out, nil
}

func originalBrushTrackerOrigins(value string) string {
	origins := []string{}
	for _, line := range strings.Split(value, "\n") {
		tracker, err := url.Parse(strings.TrimSpace(line))
		if err != nil || tracker.Hostname() == "" {
			continue
		}
		switch tracker.Scheme {
		case "http", "https", "udp":
			origins = append(origins, (&url.URL{Scheme: tracker.Scheme, Host: tracker.Host}).String())
		}
	}
	return strings.Join(origins, "\n")
}

var originalQBTaskNumericFields = []string{"size", "total_size", "completed", "availability", "progress", "amount_left", "downloaded", "uploaded", "ratio", "dlspeed", "upspeed", "added_on", "completion_on", "last_activity", "seeding_time", "eta"}
var originalTRTaskNumericFields = []string{"id", "status", "totalSize", "percentDone", "leftUntilDone", "downloadedEver", "uploadedEver", "uploadRatio", "rateDownload", "rateUpload", "addedDate", "doneDate", "activityDate", "queuePosition", "recheckProgress", "error"}
