package brushflowlanduo

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strconv"
	"strings"

	sdk "light-media/builtinplugins/internal/sdk"
)

func brushCapabilities() []string {
	return []string{
		sdk.BaseGetData, sdk.BaseSaveData, sdk.BaseDelData, sdk.BaseUpdateConfig, sdk.BaseGetInstanceMetadata,
		sdk.BackgroundSubmitCapability, sdk.ServicesRefreshCapability, sdk.PostMessageCapability,
		sdk.SystemMessagePutCapability, sdk.EventSendCapability, sdk.LogWriteCapability,
		sdk.BrushInventoryReadCapability, sdk.SitesReadCapability, sdk.SitesRequestCapability, sdk.SitesSourceRequestCapability,
		sdk.SubscriptionsReadCapability, sdk.MediaRecognizeCapability,
		sdk.DownloaderConfigurationsReadCapability, sdk.DownloaderSourceConnectionCapability,
		sdk.DownloaderListCapability, sdk.DownloaderDownloadingCapability, sdk.DownloaderSourceCompletedCapability,
		sdk.DownloaderSourceControlCapability, sdk.DownloaderTagsCapability, sdk.DownloaderDeleteTagsCapability,
		sdk.DownloaderDeleteFilesCapability, sdk.BrushSourceCandidatesCapability, sdk.BrushSourceTransferCapability, sdk.BrushSourceAddCapability,
	}
}

func sourceSites(ctx context.Context) ([]sdk.SiteHostRowV1, error) {
	result, err := (sdk.Sites{Host: brushCaller(ctx)}).Read(ctx, sdk.SiteHostRequestV1{SourceProjection: true})
	return result.Items, err
}

func (*standardHost) Sites(ctx context.Context) ([]Site, error) {
	items, err := sourceSites(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Site, 0, len(items))
	var keyPresence map[int64]bool
	for _, item := range items {
		fields := Row{}
		if len(item.SourceFields) != 0 {
			fields, err = standardObject(item.SourceFields)
			if err != nil {
				return nil, err
			}
		}
		if strings.Contains(strings.ToLower(item.Domain), "m-team.") {
			if keyPresence == nil {
				inventory, err := brushInventory(ctx)
				if err != nil {
					return nil, err
				}
				keyPresence = map[int64]bool{}
				for _, site := range inventory.Sites {
					keyPresence[site.SourceID] = site.HasAPIKey
				}
			}
			fields["apikey"] = keyPresence[item.SourceID]
		}
		out = append(out, Site{ID: int(item.SourceID), Name: item.Name, URL: item.URL, Domain: item.Domain,
			Schema: text(fields["schema"]), Public: item.Public, Raw: fields})
	}
	return out, nil
}

func sourceSite(ctx context.Context, id int) (sdk.SiteHostRowV1, error) {
	items, err := sourceSites(ctx)
	if err != nil {
		return sdk.SiteHostRowV1{}, err
	}
	for _, item := range items {
		if item.SourceID == int64(id) {
			return item, nil
		}
	}
	return sdk.SiteHostRowV1{}, errors.New("刷流站点引用不存在")
}

func present[T any](value *T) any {
	if value == nil {
		return nil
	}
	return *value
}

func brushInventory(ctx context.Context) (sdk.BrushInventoryV1, error) {
	var result sdk.BrushInventoryV1
	raw, err := brushCaller(ctx)(ctx, sdk.BrushInventoryReadCapability, json.RawMessage(`{}`))
	if err != nil {
		return result, err
	}
	if err := json.Unmarshal(raw, &result); err != nil || result.SchemaVersion != 1 || result.Sites == nil {
		return result, errors.New("invalid BrushFlow site statistics projection")
	}
	return result, nil
}

func (*standardHost) SiteUsers(ctx context.Context) ([]UserData, error) {
	result, err := brushInventory(ctx)
	if err != nil {
		return nil, err
	}
	rows := []UserData{}
	for _, site := range result.Sites {
		stat := site.LocalStatistics
		if site.Domain == nil || stat.Status != "available" {
			continue
		}
		rows = append(rows, UserData{Domain: *site.Domain, Ratio: present(stat.Ratio), Upload: present(stat.Upload),
			Download: present(stat.Download), UpdatedDay: text(present(stat.UpdatedDay)), UpdatedTime: text(present(stat.UpdatedTime))})
	}
	return rows, nil
}

func sourceDownloaders(ctx context.Context) ([]sdk.BrushDownloaderReferenceV1, error) {
	result, err := (sdk.Downloaders{Host: brushCaller(ctx)}).Configurations(ctx)
	if err != nil {
		return nil, err
	}
	active := []sdk.BrushDownloaderReferenceV1{}
	for _, item := range result.Downloaders {
		if item.Enabled && item.Name != "" && item.Kind != "" {
			active = append(active, item)
		}
	}
	return active, nil
}

func sourceDownloader(ctx context.Context, name string) (sdk.BrushDownloaderReferenceV1, error) {
	items, err := sourceDownloaders(ctx)
	if err != nil {
		return sdk.BrushDownloaderReferenceV1{}, err
	}
	for _, item := range items {
		if item.Name == name {
			return item, nil
		}
	}
	return sdk.BrushDownloaderReferenceV1{}, errors.New("刷流下载器引用不存在")
}

func (*standardHost) Downloaders(ctx context.Context) ([]Downloader, error) {
	items, err := sourceDownloaders(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Downloader, 0, len(items))
	for _, item := range items {
		out = append(out, Downloader{Name: item.Name, Kind: item.Kind})
	}
	return out, nil
}

func (*standardHost) Downloader(ctx context.Context, name string) (*Downloader, error) {
	items, err := sourceDownloaders(ctx)
	if err != nil {
		return nil, err
	}
	for _, item := range items {
		if item.Name != name {
			continue
		}
		out := &Downloader{Name: item.Name, Kind: item.Kind}
		connection, err := (sdk.Downloaders{Host: brushCaller(ctx)}).SourceConnection(ctx, sdk.DownloaderConnectionRequestV1{DownloaderID: item.ID})
		if err != nil {
			return nil, err
		}
		if !connection.Exists {
			return nil, nil
		}
		out.Active = !connection.Inactive
		return out, nil
	}
	return nil, nil
}

func (*standardHost) TransferInfo(ctx context.Context) ([]Transfer, error) {
	items, err := sourceDownloaders(ctx)
	if err != nil {
		return nil, err
	}
	var out []Transfer
	caller := brushCaller(ctx)
	for _, item := range items {
		connection, err := (sdk.Downloaders{Host: caller}).SourceConnection(ctx, sdk.DownloaderConnectionRequestV1{DownloaderID: item.ID})
		if err != nil {
			return nil, err
		}
		if !connection.Exists || connection.Inactive {
			continue
		}
		stats, err := (sdk.BrushSource{Host: caller}).Transfer(ctx, item.ID)
		if err != nil {
			return nil, err
		}
		out = append(out, Transfer{UploadSpeed: stats.UploadSpeed, DownloadSpeed: stats.DownloadSpeed, UploadSize: stats.UploadSize, DownloadSize: stats.DownloadSize})
	}
	return out, nil
}

func brushTaskRows(result sdk.DownloaderTasksV1) ([]DownloadState, error) {
	out := make([]DownloadState, 0, len(result.Tasks))
	for _, torrent := range result.Tasks {
		var native any = torrent.QBittorrent
		if result.Kind == "transmission" {
			native = torrent.Transmission
		}
		raw, err := json.Marshal(native)
		if err != nil {
			return nil, err
		}
		row, err := standardObject(raw)
		if err != nil {
			return nil, err
		}
		if result.Kind == "qbittorrent" {
			row["hash"] = torrent.Hash
		} else {
			row["hashString"] = torrent.Hash
			for property, key := range map[string]string{"added_date": "addedDate", "done_date": "doneDate", "activity_date": "activityDate",
				"total_size": "totalSize", "uploaded_ever": "uploadedEver", "downloaded_ever": "downloadedEver", "ratio": "uploadRatio"} {
				if value, ok := row[key]; ok {
					row[property] = value
				}
			}
			if value, ok := row["trackerList"].(string); ok {
				row["tracker_list"] = strings.FieldsFunc(value, func(c rune) bool { return strings.ContainsRune("\n\r\v\f\u0085\u2028\u2029\u001c\u001d\u001e", c) })
			}
			progress, _ := strconv.ParseFloat(strconv.FormatFloat(number(row["percentDone"])*100, 'f', 2, 64), 64)
			row["progress"] = progress
		}
		out = append(out, DownloadState{Kind: result.Kind, Raw: row})
	}
	return out, nil
}

func (*standardHost) Torrents(ctx context.Context, name string) ([]DownloadState, error) {
	d, err := sourceDownloader(ctx, name)
	if err != nil {
		return nil, err
	}
	client := sdk.Downloaders{Host: brushCaller(ctx)}
	request := sdk.DownloaderListRequestV1{DownloaderID: d.ID, IncludeActivityTime: true}
	result, err := client.List(ctx, request)
	if err == nil {
		return brushTaskRows(result)
	}
	// Older hosts reject unknown request fields. Only a host-operation reply
	// can mean this; cancellation, revoked calls and invalid wire data stop here.
	var rejection *sdk.Error
	if ctx.Err() != nil || boundary(err) || !errors.As(err, &rejection) || rejection.Code != "host_operation_failed" {
		return nil, err
	}
	request.IncludeActivityTime = false
	result, err = client.List(ctx, request)
	if err != nil {
		return nil, err
	}
	// This read was authorized without activity counters. Other brush rules
	// can use the snapshot, but the download-time rule must remain inactive.
	for _, task := range result.Tasks {
		if task.QBittorrent != nil {
			task.QBittorrent.TimeActive = nil
		}
		if task.Transmission != nil {
			task.Transmission.SecondsDownloading = nil
		}
	}
	return brushTaskRows(result)
}

func (*standardHost) Completed(ctx context.Context, name string, hashes []string) ([]DownloadState, error) {
	d, err := sourceDownloader(ctx, name)
	if err != nil {
		return nil, err
	}
	result, err := (sdk.Downloaders{Host: brushCaller(ctx)}).SourceCompleted(ctx, sdk.DownloaderCompletedRequestV1{DownloaderID: d.ID, Hashes: hashes})
	if err != nil || result.NativeError {
		return nil, err
	}
	return brushTaskRows(result.DownloaderTasksV1)
}

func (*standardHost) Browse(ctx context.Context, id int, rss bool) ([]Row, error) {
	site, err := sourceSite(ctx, id)
	if err != nil {
		return nil, err
	}
	result, err := (sdk.BrushSource{Host: brushCaller(ctx)}).Candidates(ctx, sdk.BrushCandidatesRequestV1{SiteID: site.ID, RSS: rss})
	if err != nil {
		return nil, err
	}
	out := make([]Row, 0, len(result.Candidates))
	for _, candidate := range result.Candidates {
		row, err := standardObject(candidate.Fields)
		if err != nil {
			return nil, err
		}
		row["brush_material_id"] = candidate.MaterialID
		out = append(out, row)
	}
	return out, nil
}

func (*standardHost) SiteRequest(ctx context.Context, id int, method, address string, fields Row) (Row, error) {
	site, err := sourceSite(ctx, id)
	if err != nil {
		return nil, err
	}
	resolved, err := url.Parse(address)
	if err != nil {
		return nil, err
	}
	client := sdk.Sites{Host: brushCaller(ctx)}
	var result sdk.SiteHTTPResultV1
	if method == "POST" {
		if resolved.Path != "/api/torrent/detail" || resolved.Scheme != "https" || resolved.User != nil {
			return nil, errors.New("刷流 M-Team 详情请求无效")
		}
		result, err = client.SourceRequest(ctx, sdk.SiteSourceHTTPRequestV1{
			Reference: sdk.SiteSourceReferenceV1{SiteID: site.ID}, OriginProfile: "brush-mteam", CookieMode: "none", ExplicitHeaders: true,
			Request: sdk.SiteHTTPRequestV1{Method: "POST", Path: resolved.Path, Form: []sdk.SiteHTTPPairV1{{Name: "id", Value: text(fields["id"])}}},
		})
	} else {
		base, err := url.Parse(site.URL)
		if err != nil || !strings.EqualFold(resolved.Host, base.Host) || resolved.Scheme != base.Scheme || resolved.User != nil {
			return nil, errors.New("刷流详情页与站点来源不一致")
		}
		result, err = client.Request(ctx, sdk.SiteHTTPRequestV1{ID: site.ID, Method: method, Path: resolved.RequestURI()})
		if err != nil {
			return nil, err
		}
	}
	if err != nil || result.Response == nil {
		return nil, err
	}
	response := result.Response
	out := Row{"ok": response.StatusCode < 400, "text": string(response.Content)}
	if method == "POST" && out["ok"] == true {
		out["json"], err = standardObject(response.Content)
	}
	return out, err
}

func (*standardHost) Download(ctx context.Context, task Task, candidate, _ Row, marker string) (string, error) {
	site, err := sourceSite(ctx, task.SiteID())
	if err != nil {
		return "", err
	}
	d, err := sourceDownloader(ctx, task.S("downloader"))
	if err != nil {
		return "", err
	}
	category, ignoreCategory := task.S("qb_category"), true
	request := sdk.DownloaderAddRequestV1{DownloaderID: d.ID, Paused: task.B("del_no_free"), Category: &category,
		Tags: []string{"已整理", GlobalTag, task.Tag()}, IgnoreCategoryCheck: &ignoreCategory}
	if d.Kind == "qbittorrent" {
		request.Tags = append(request.Tags, marker)
	}
	if value := task.Row["save_path"]; value != nil {
		path := text(value)
		request.SavePath = &path
	}
	if value := task.N("up_speed"); value != 0 {
		bytes := int64(value) * 1024
		request.UploadLimitBytes = &bytes
	}
	if value := task.N("dl_speed"); value != 0 {
		bytes := int64(value) * 1024
		request.DownloadLimitBytes = &bytes
	}
	result, err := (sdk.BrushSource{Host: brushCaller(ctx)}).Add(ctx, sdk.BrushAddRequestV1{SiteID: site.ID,
		MaterialID: text(candidate["brush_material_id"]), TaskID: task.ID(),
		DownloaderSourceAddRequestV1: sdk.DownloaderSourceAddRequestV1{DownloaderAddRequestV1: request}})
	if err != nil {
		return "", err
	}
	if err := brushEffectBoundary(result.Outcome, result.FailureCode); err != nil || !result.Accepted {
		return "", err
	}
	return result.Hash, nil
}

type uncertainBrushEffect struct{}

func (uncertainBrushEffect) Error() string {
	return "刷流外部操作结果不确定或操作后权限发生变化"
}
func (uncertainBrushEffect) StopExternalEffects() bool { return true }

func brushEffectBoundary(outcome, failure string) error {
	if outcome == "unknown" || failure != "" {
		return uncertainBrushEffect{}
	}
	return nil
}

func brushControl(ctx context.Context, name string, hashes []string, action string) (bool, error) {
	d, err := sourceDownloader(ctx, name)
	if err != nil {
		return false, err
	}
	result, err := (sdk.Downloaders{Host: brushCaller(ctx)}).SourceControl(ctx, sdk.DownloaderControlRequestV1{DownloaderID: d.ID, Hashes: hashes, Action: action})
	return result.Accepted, errors.Join(err, brushEffectBoundary(result.Outcome, result.FailureCode))
}

func (*standardHost) Delete(ctx context.Context, name string, hashes []string, files bool) (bool, error) {
	if !files {
		return brushControl(ctx, name, hashes, "remove_task")
	}
	d, err := sourceDownloader(ctx, name)
	if err != nil {
		return false, err
	}
	result, err := (sdk.Downloaders{Host: brushCaller(ctx)}).DeleteFiles(ctx, sdk.DownloaderDeleteFilesRequestV1{DownloaderID: d.ID, Hashes: hashes, BrushOwnership: true})
	return result.DaemonApplied && result.BookkeepingCompleted, errors.Join(err, brushEffectBoundary(result.Outcome, result.FailureCode))
}

func (*standardHost) Pause(ctx context.Context, name string, hashes []string) (bool, error) {
	return brushControl(ctx, name, hashes, "pause")
}

func (*standardHost) Start(ctx context.Context, name string, hashes []string) (bool, error) {
	return brushControl(ctx, name, hashes, "start")
}

func (*standardHost) Reannounce(ctx context.Context, name string, hashes []string) error {
	_, err := brushControl(ctx, name, hashes, "reannounce")
	return err
}

func (*standardHost) Tags(ctx context.Context, name string) ([]string, error) {
	d, err := sourceDownloader(ctx, name)
	if err != nil {
		return nil, err
	}
	result, err := (sdk.Downloaders{Host: brushCaller(ctx)}).Tags(ctx, sdk.DownloaderTagsRequestV1{DownloaderID: d.ID})
	return result.Tags, err
}

func (*standardHost) DeleteTags(ctx context.Context, name string, tags []string) (bool, error) {
	d, err := sourceDownloader(ctx, name)
	if err != nil {
		return false, err
	}
	result, err := (sdk.Downloaders{Host: brushCaller(ctx)}).DeleteTags(ctx, sdk.DownloaderDeleteTagsRequestV1{DownloaderID: d.ID, Tags: tags})
	return result.Accepted, errors.Join(err, brushEffectBoundary(result.Outcome, result.FailureCode))
}
