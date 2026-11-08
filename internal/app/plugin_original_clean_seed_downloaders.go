package app

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"slices"
	"strings"

	"light-media/internal/download"
	"light-media/internal/plugins"
	seed "light-media/internal/plugins/cleaninvalidseed"
	py "light-media/internal/releasemeta/legacyanime"
)

type originalCleanSeedDownloads struct {
	selected   map[string]*originalCleanSeedDownloader
	incomplete bool
	failedList bool
}

type originalCleanSeedDownloader struct {
	name, kind, binding string
	client              *download.Client
	rows                map[string]map[string]json.RawMessage
	contents            []string
	first, latest       map[string][]originalCleanSeedTracker
	liveGroups          map[string]map[string]bool
}

type originalCleanSeedTracker struct {
	Key           string `json:"key"`
	Display       string `json:"display"`
	Status        any    `json:"status"`
	Tier          any    `json:"tier"`
	Message       string `json:"message"`
	ExplicitError bool   `json:"explicit_error"`
	Valid         bool   `json:"valid"`
}

func originalCleanSeedTrackerNumber(value any, expected int) bool {
	switch number := value.(type) {
	case int:
		return number == expected
	case json.Number:
		v, err := number.Float64()
		return err == nil && v == float64(expected)
	default:
		return false
	}
}

func (d *originalCleanSeedDownloader) resetTrackers() {
	d.first = map[string][]originalCleanSeedTracker{}
	d.latest = map[string][]originalCleanSeedTracker{}
	d.liveGroups = map[string]map[string]bool{}
}

func (d *originalCleanSeedDownloader) recordTrackers(hash string, observed []originalCleanSeedTracker) {
	d.latest[hash] = observed
	if _, exists := d.first[hash]; exists {
		return
	}
	d.first[hash] = observed
	for _, item := range observed {
		if originalCleanSeedTrackerNumber(item.Tier, -1) {
			continue
		}
		if !item.Valid {
			break
		}
		if !item.ExplicitError {
			if d.liveGroups[item.Key] == nil {
				d.liveGroups[item.Key] = map[string]bool{}
			}
			d.liveGroups[item.Key][hash] = true
		}
	}
}

func (d *originalCleanSeedDownloader) confirmedInvalid(hash string) bool {
	first, read := d.first[hash]
	latest, checked := d.latest[hash]
	if !read || !checked {
		return false
	}
	for _, item := range first {
		if !originalCleanSeedTrackerNumber(item.Tier, -1) && (!item.Valid || !item.ExplicitError) {
			return false
		}
	}
	for _, item := range latest {
		if originalCleanSeedTrackerNumber(item.Tier, -1) {
			continue
		}
		if !item.Valid {
			return false
		}
		if item.ExplicitError {
			for other := range d.liveGroups[item.Key] {
				if other != hash {
					return true
				}
			}
		}
	}
	return false
}

type originalCleanSeedDownloaderRequest struct {
	DownloaderID string `json:"downloader_id"`
}

type originalCleanSeedTrackerRequest struct {
	DownloaderID string `json:"downloader_id"`
	Hash         string `json:"hash"`
}

type originalCleanSeedControlRequest struct {
	DownloaderID string   `json:"downloader_id"`
	Hash         string   `json:"hash"`
	Action       string   `json:"action"`
	Tags         []string `json:"tags"`
	OriginalTags []string `json:"original_tags"`
}

func originalCleanSeedDownloadBinding(ref DownloaderConfig) string {
	encoded, _ := json.Marshal(ref)
	return tokenHash(string(encoded))
}

func (a *App) originalCleanSeedSelected(scope originalBaseCallScope) *originalCleanSeedDownloads {
	call := scope.cleanSeed
	state := &call.downloads
	if state.selected != nil {
		return state
	}
	state.selected = map[string]*originalCleanSeedDownloader{}
	names := map[string]int{}
	switch selected := call.config["downloaders"].(type) {
	case []any:
		for _, item := range selected {
			if name, ok := item.(string); ok && name != "" {
				names[name] = 0
			} else {
				state.incomplete = true
			}
		}
	case []string:
		for _, name := range selected {
			names[name] = 0
		}
	}
	for _, ref := range a.Settings().Downloaders {
		if count, wanted := names[ref.Name]; wanted {
			names[ref.Name] = count + 1
			kind := ref.Kind
			switch kind {
			case "qb", "qbittorrent":
				kind = "qbittorrent"
			case "tr", "transmission":
				kind = "transmission"
			default:
				state.incomplete = true
				continue
			}
			if !downloaderEnabled(ref) {
				state.incomplete = true
				continue
			}
			state.selected[ref.ID] = &originalCleanSeedDownloader{name: ref.Name, kind: kind, binding: originalCleanSeedDownloadBinding(ref)}
		}
	}
	for _, count := range names {
		if count != 1 {
			state.incomplete = true
		}
	}
	for id, selected := range state.selected {
		if names[selected.name] != 1 {
			delete(state.selected, id)
		}
	}
	return state
}

func (d *originalCleanSeedDownloader) nativeClient(ref DownloaderConfig) (*download.Client, error) {
	if d.client != nil {
		return d.client, nil
	}
	client, err := download.New(ref.Config)
	if err != nil {
		return nil, err
	}
	d.client = client
	return client, nil
}

func (a *App) originalCleanSeedDownloaderRef(ctx context.Context, scope originalBaseCallScope, id string) (*originalCleanSeedDownloader, DownloaderConfig, error) {
	if err := originalCleanSeedValidate(ctx, scope); err != nil {
		return nil, DownloaderConfig{}, err
	}
	selected := a.originalCleanSeedSelected(scope).selected[id]
	if id == "" || selected == nil {
		return nil, DownloaderConfig{}, bad("PLUGIN_CAPABILITY_DENIED", "下载器不在清理插件已选范围内")
	}
	ref, err := a.configuredDownloader(id)
	if err != nil || !downloaderEnabled(ref) || originalCleanSeedDownloadBinding(ref) != selected.binding {
		return nil, DownloaderConfig{}, bad("DOWNLOADER_CHANGED", "已选下载器配置发生变化，请重新执行")
	}
	return selected, ref, nil
}

func originalCleanSeedTaskString(row map[string]json.RawMessage, key string) string {
	var value string
	_ = json.Unmarshal(row[key], &value)
	return value
}

func originalCleanSeedTaskField(key string, raw json.RawMessage) bool {
	if raw == nil {
		return false
	}
	switch key {
	case "id", "size", "status", "totalSize":
		var number *json.Number
		return json.Unmarshal(raw, &number) == nil
	case "labels":
		var labels []string
		return json.Unmarshal(raw, &labels) == nil
	default:
		var value *string
		return json.Unmarshal(raw, &value) == nil
	}
}

func (a *App) originalCleanSeedReadTasks(ctx context.Context, scope originalBaseCallScope, id string) (string, []map[string]json.RawMessage, bool, error) {
	selected, ref, err := a.originalCleanSeedDownloaderRef(ctx, scope, id)
	if err != nil {
		return "", nil, false, err
	}
	selected.resetTrackers()
	client, err := selected.nativeClient(ref)
	var body json.RawMessage
	if err == nil {
		body, err = client.NativeTorrents(ctx)
	}
	if _, _, current := a.originalCleanSeedDownloaderRef(ctx, scope, id); current != nil {
		return "", nil, false, current
	}
	if err := ctx.Err(); err != nil {
		return "", nil, false, err
	}
	var rows []map[string]json.RawMessage
	if err != nil || json.Unmarshal(body, &rows) != nil || rows == nil {
		a.originalCleanSeedSelected(scope).failedList = true
		selected.rows, selected.contents = nil, nil
		return selected.kind, []map[string]json.RawMessage{}, true, nil
	}
	fields := []string{"hash", "name", "state", "category", "tags", "size", "content_path"}
	if selected.kind == "transmission" {
		fields = []string{"id", "hashString", "name", "status", "labels", "totalSize", "downloadDir"}
	}
	byHash := map[string]map[string]json.RawMessage{}
	seen := map[string]bool{}
	projected := []map[string]json.RawMessage{}
	contents := []string{}
	complete := true
	for _, row := range rows {
		if selected.kind == "transmission" && row["id"] == nil {
			a.originalCleanSeedSelected(scope).failedList = true
			selected.rows, selected.contents = nil, nil
			return selected.kind, []map[string]json.RawMessage{}, true, nil
		}
		hash := strings.ToLower(originalCleanSeedTaskString(row, "hash"))
		content := originalCleanSeedTaskString(row, "content_path")
		if selected.kind == "transmission" {
			hash = strings.ToLower(originalCleanSeedTaskString(row, "hashString"))
			if dir, name := originalCleanSeedTaskString(row, "downloadDir"), originalCleanSeedTaskString(row, "name"); dir != "" && name != "" {
				content = dir + "/" + name
			}
		}
		if plugins.DownloaderTaskHashValid(hash) && !seen[hash] {
			byHash[hash] = row
		} else {
			complete = false
			delete(byHash, hash)
		}
		seen[hash] = true
		if strings.HasPrefix(content, "/") && !strings.ContainsRune(content, 0) {
			contents = append(contents, content)
		} else {
			complete = false
		}
		value := map[string]json.RawMessage{}
		for _, key := range fields {
			if item := row[key]; originalCleanSeedTaskField(key, item) {
				value[key] = item
			}
		}
		if selected.kind == "qbittorrent" && value["size"] == nil && originalCleanSeedTaskField("size", row["total_size"]) {
			value["size"] = row["total_size"]
		}
		projected = append(projected, value)
	}
	if !complete {
		contents = nil
	}
	selected.rows, selected.contents = byHash, contents
	return selected.kind, projected, false, nil
}

func (a *App) originalCleanSeedTasks(ctx context.Context, scope originalBaseCallScope, raw json.RawMessage) (json.RawMessage, error) {
	var request originalCleanSeedDownloaderRequest
	if err := originalResourceDecode(raw, &request); err != nil {
		return nil, err
	}
	kind, tasks, failed, err := a.originalCleanSeedReadTasks(ctx, scope, request.DownloaderID)
	if err != nil {
		return nil, err
	}
	return originalHostDownloaderMarshal(struct {
		SchemaVersion int                          `json:"schema_version"`
		Kind          string                       `json:"kind"`
		NativeError   bool                         `json:"native_error"`
		Tasks         []map[string]json.RawMessage `json:"tasks"`
	}{1, kind, failed, tasks})
}

// originalCleanSeedContents is called under the cleanup invocation mutex.
// Refresh performs native read-only calls and may run inside the file barrier.
func (a *App) originalCleanSeedContents(ctx context.Context, scope originalBaseCallScope, refresh bool) ([]string, error) {
	state := a.originalCleanSeedSelected(scope)
	incomplete := bad("CLEAN_SEED_INVENTORY_INCOMPLETE", "未取得所有已选下载器的完整种子内容路径，无法确认源文件未做种")
	if state.incomplete || state.failedList || len(state.selected) == 0 {
		return nil, incomplete
	}
	contents := []string{}
	for id, selected := range state.selected {
		if _, _, err := a.originalCleanSeedDownloaderRef(ctx, scope, id); err != nil {
			return nil, err
		}
		if selected.contents == nil {
			return nil, incomplete
		}
		if refresh {
			if _, _, failed, err := a.originalCleanSeedReadTasks(ctx, scope, id); err != nil {
				return nil, err
			} else if failed || selected.contents == nil {
				return nil, incomplete
			}
		}
		contents = append(contents, selected.contents...)
	}
	return contents, nil
}

func (a *App) originalCleanSeedTrackers(ctx context.Context, scope originalBaseCallScope, raw json.RawMessage) (json.RawMessage, error) {
	var request originalCleanSeedTrackerRequest
	if err := originalResourceDecode(raw, &request); err != nil {
		return nil, err
	}
	hash := strings.ToLower(request.Hash)
	selected, ref, err := a.originalCleanSeedDownloaderRef(ctx, scope, request.DownloaderID)
	if err != nil {
		return nil, err
	}
	row := selected.rows[hash]
	if !plugins.DownloaderTaskHashValid(hash) || row == nil {
		return nil, bad("PLUGIN_CAPABILITY_DENIED", "Tracker 必须对应本次已读取的准确任务")
	}
	body, err := json.Marshal([]map[string]json.RawMessage{row})
	if err != nil {
		return nil, err
	}
	client, err := selected.nativeClient(ref)
	if err != nil {
		return nil, err
	}
	native := seed.NativeDownloader{Service: seed.Service{Kind: selected.kind}, Call: func(ctx context.Context, method string, _ map[string]any) (json.RawMessage, error) {
		if method == "qb.torrents_trackers" {
			if _, _, err := a.originalCleanSeedDownloaderRef(ctx, scope, request.DownloaderID); err != nil {
				return nil, err
			}
			return client.NativeTrackers(ctx, hash)
		}
		return body, nil
	}}
	var trackers []any
	rows, failed, err := native.List(ctx)
	if err != nil {
		return nil, err
	}
	errorMessages := []string{"torrent not registered with this tracker", "Torrent not registered with this tracker", "torrent banned", "err torrent banned", "Torrent not exists"}
	if custom, ok := scope.cleanSeed.config["custom_error_msg"].(string); ok {
		for _, message := range strings.Split(custom, "\n") {
			if message = py.Strip(message); message != "" {
				errorMessages = append(errorMessages, message)
			}
		}
	}
	if !failed && len(rows) == 1 {
		machine := seed.New(nil)
		machine.Config = scope.cleanSeed.config
		trackers = machine.TrackerInfo(rows[0], selected.kind)
	}
	if _, _, current := a.originalCleanSeedDownloaderRef(ctx, scope, request.DownloaderID); current != nil {
		return nil, current
	}
	secrets := originalTrackerSecrets(trackers)
	result := []originalCleanSeedTracker{}
	for _, value := range trackers {
		row, ok := value.(map[string]any)
		if !ok {
			result = append(result, originalCleanSeedTracker{})
			continue
		}
		address, _ := row["url"].(string)
		message, _ := row["msg"].(string)
		redacted := a.originalTrackerText(message, errorMessages, secrets)
		display := a.originalTrackerEndpoint(address).Display
		if strings.HasPrefix(address, "http") {
			if _, host, ok := strings.Cut(display, "://"); ok {
				display = host
			}
		}
		t := originalCleanSeedTracker{Display: display, Message: redacted.Display}
		switch number := row["status"].(type) {
		case json.Number, int:
			t.Status = number
		}
		switch number := row["tier"].(type) {
		case json.Number, int:
			t.Tier = number
		}
		t.ExplicitError = originalCleanSeedTrackerNumber(t.Status, 4) && slices.Contains(redacted.Matches, true)
		if group, err := seed.URLNetloc(row["url"]); err == nil {
			mac := hmac.New(sha256.New, a.Store.key)
			_, _ = mac.Write([]byte("plugin-downloader-tracker-comparison/v1\x00" + group[1]))
			t.Key, t.Valid = hex.EncodeToString(mac.Sum(nil)), true
		}
		result = append(result, t)
	}
	selected.recordTrackers(hash, result)
	return originalHostDownloaderMarshal(struct {
		SchemaVersion int                        `json:"schema_version"`
		Trackers      []originalCleanSeedTracker `json:"trackers"`
	}{1, result})
}

func originalCleanSeedMutationMatches(snapshot, current map[string]json.RawMessage, kind string) bool {
	fields := []string{"hash", "name", "category", "tags", "content_path", "size", "added_on"}
	if kind == "transmission" {
		fields = []string{"id", "hashString", "name", "labels", "downloadDir", "totalSize", "addedDate"}
	}
	for _, key := range fields {
		if !bytes.Equal(snapshot[key], current[key]) {
			return false
		}
	}
	return true
}

func originalCleanSeedExcluded(config map[string]any, row map[string]json.RawMessage, kind string) (bool, error) {
	category, tags := "", ""
	if kind == "qbittorrent" {
		category = originalCleanSeedTaskString(row, "category")
		tags = originalCleanSeedTaskString(row, "tags")
	} else if raw := row["labels"]; raw != nil {
		var labels []string
		if err := json.Unmarshal(raw, &labels); err != nil {
			return false, bad("DOWNLOADER_INVALID_RESPONSE", "Transmission 原标签响应无效")
		}
		tags = strings.Join(labels, ",")
	}
	if categories, _ := config["exclude_categories"].(string); categories != "" && slices.Contains(strings.Split(categories, "\n"), category) {
		return true, nil
	}
	if labels, _ := config["exclude_labels"].(string); labels != "" {
		excluded := strings.Split(labels, "\n")
		for _, tag := range strings.Split(tags, ",") {
			if tag = py.Strip(tag); tag != "" && slices.Contains(excluded, tag) {
				return true, nil
			}
		}
	}
	return false, nil
}

func (a *App) originalCleanSeedControl(ctx context.Context, scope originalBaseCallScope, raw json.RawMessage) (json.RawMessage, error) {
	var request originalCleanSeedControlRequest
	if err := originalResourceDecode(raw, &request); err != nil {
		return nil, err
	}
	call := scope.cleanSeed
	labelOnly := seed.Truth(call.config["label_only"])
	scheduled := call.action == "clean_invalid_seed" || call.action == "private_once"
	denied := bad("PLUGIN_CAPABILITY_DENIED", "当前清理操作未授权修改下载器任务")
	switch request.Action {
	case "label":
		if !labelOnly || !scheduled && call.action != "delete_invalid_torrents" && call.action != "detect_invalid_torrents" {
			return nil, denied
		}
	case "remove_task":
		deleteAllowed := call.action == "delete_invalid_torrents" || scheduled && seed.Truth(call.config["delete_invalid_torrents"])
		if labelOnly || !deleteAllowed || len(request.Tags) != 0 || len(request.OriginalTags) != 0 {
			return nil, denied
		}
	default:
		return nil, denied
	}
	hash := strings.ToLower(request.Hash)
	a.downloadMu.Lock()
	defer a.downloadMu.Unlock()
	selected, ref, err := a.originalCleanSeedDownloaderRef(ctx, scope, request.DownloaderID)
	if err != nil {
		return nil, err
	}
	snapshot := selected.rows[hash]
	if !plugins.DownloaderTaskHashValid(hash) || snapshot == nil {
		return nil, bad("PLUGIN_CAPABILITY_DENIED", "清理必须对应本次已读取的准确任务")
	}
	if !selected.confirmedInvalid(hash) {
		return nil, bad("PLUGIN_CAPABILITY_DENIED", "未确认此任务所有 Tracker 失效，或无同下载器 Tracker 证据")
	}
	if excluded, err := originalCleanSeedExcluded(call.config, snapshot, selected.kind); err != nil {
		return nil, err
	} else if excluded {
		return nil, bad("PLUGIN_CAPABILITY_DENIED", "任务分类或标签在清理排除范围内")
	}
	var tags []string
	if request.Action == "label" {
		label, ok := call.config["label"].(string)
		if label == "" {
			label = "无效做种"
		}
		if !ok && call.config["label"] != nil || len(request.Tags) != 1 || request.Tags[0] != label {
			return nil, bad("PLUGIN_CAPABILITY_DENIED", "清理只能使用本次已保存的标记")
		}
		tags = []string{label}
		if selected.kind == "transmission" {
			previous := []string{}
			if labels := snapshot["labels"]; labels != nil && json.Unmarshal(labels, &previous) != nil || !slices.Equal(previous, request.OriginalTags) {
				return nil, bad("DOWNLOADER_CHANGED", "Transmission 原标签与已读取快照不一致")
			}
			seen := map[string]bool{}
			tags = []string{}
			for _, tag := range append(previous, label) {
				if !seen[tag] {
					tags = append(tags, tag)
					seen[tag] = true
				}
			}
		} else if request.OriginalTags != nil {
			return nil, bad("INVALID_PLUGIN_VALUE", "qBittorrent 原标签由下载器保留")
		}
	}
	client, err := selected.nativeClient(ref)
	if err != nil {
		return nil, err
	}
	body, err := client.SourceTorrentSnapshots(ctx, hash)
	if err != nil {
		return nil, err
	}
	var rows []map[string]json.RawMessage
	if json.Unmarshal(body, &rows) != nil || len(rows) != 1 || !originalCleanSeedMutationMatches(snapshot, rows[0], selected.kind) {
		return nil, bad("DOWNLOADER_CHANGED", "下载器任务与已读取快照不一致，请重新执行")
	}
	if _, _, err = a.originalCleanSeedDownloaderRef(ctx, scope, request.DownloaderID); err != nil {
		return nil, err
	}
	birth := snapshot["added_on"]
	if selected.kind == "transmission" {
		birth = snapshot["addedDate"]
	}
	// Credentials and the other labels can change while the result of the
	// same requested effect is still unknown.
	key, err := json.Marshal([]any{request.DownloaderID, selected.kind, strings.TrimRight(ref.BaseURL, "/"), hash, request.Action, request.Tags, birth})
	if err != nil {
		return nil, err
	}
	release, err := a.beginWrite()
	if err != nil {
		return nil, err
	}
	defer release()
	ok, err := a.executeOriginalCleanSeedEffect(ctx, scope, "downloader:"+tokenHash(string(key)), func() (bool, error) {
		if _, _, current := a.originalCleanSeedDownloaderRef(ctx, scope, request.DownloaderID); current != nil {
			return false, nil
		}
		var mutationErr error
		if request.Action == "label" {
			mutationErr = client.SetTorrentTags(ctx, hash, tags)
		} else {
			mutationErr = client.SourceRemoveKeepData(ctx, hash)
		}
		if download.MutationNotStarted(mutationErr) || download.MutationRejected(mutationErr) {
			return false, nil
		}
		return mutationErr == nil, mutationErr
	})
	if err != nil {
		return nil, err
	}
	return json.Marshal(ok)
}
