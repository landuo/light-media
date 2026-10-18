package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"path/filepath"
	"strings"

	"light-media/internal/plugins/cleaninvalidseed"
	"light-media/internal/releasemeta"
)

func coreNativeRows(raw []byte) ([]map[string]any, error) {
	var rows []map[string]any
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	if d.Decode(&rows) != nil || rows == nil {
		return nil, coreDeleteInvalid()
	}
	return rows, nil
}
func coreValue(row map[string]any, keys ...string) any {
	for _, key := range keys {
		if v, ok := row[key]; ok {
			return v
		}
	}
	return nil
}
func coreText(v any) string { s, _ := v.(string); return s }
func coreNumber(v any) float64 {
	switch v := v.(type) {
	case json.Number:
		f, _ := v.Float64()
		return f
	case float64:
		return v
	}
	return 0
}
func coreNativeHash(kind string, row map[string]any) string {
	if kind == "qbittorrent" {
		return coreText(row["hash"])
	}
	return coreText(coreValue(row, "hashString", "hash_string"))
}
func coreNativeContains(kind, hash string, raw []byte) (bool, error) {
	rows, err := coreNativeRows(raw)
	if err != nil {
		return false, err
	}
	for _, row := range rows {
		if sameHash(coreNativeHash(kind, row), hash) {
			return true, nil
		}
	}
	return false, nil
}

func coreSeconds(value float64) string {
	hours := math.Floor(value / 3600)
	remainder := value - hours*3600
	minutes := math.Floor(remainder / 60)
	seconds := math.Mod(remainder, 60)
	out := fmt.Sprintf("%d秒", int64(seconds))
	if minutes != 0 {
		out = fmt.Sprintf("%d分", int64(minutes)) + out
	}
	if hours != 0 {
		out = fmt.Sprintf("%d时", int64(hours)) + out
	}
	return out
}
func coreTaskState(kind string, raw any) string {
	state := strings.ToLower(strings.TrimSpace(coreText(raw)))
	if kind == "transmission" {
		if n, ok := raw.(json.Number); ok {
			i, e := n.Int64()
			if e == nil && i >= 0 && i <= 6 {
				state = []string{"stopped", "check_pending", "checking", "download_pending", "downloading", "seed_pending", "seeding"}[i]
			}
		}
		if state == "stopped" {
			return "paused"
		}
		if state == "download_pending" || state == "downloading" {
			return "downloading"
		}
		return "completed"
	}
	switch state {
	case "paused", "pauseddl", "pausedup", "stoppeddl", "stoppedup":
		return "paused"
	case "allocating", "checkingdl", "downloading", "forceddl", "metadl", "queueddl", "stalleddl":
		return "downloading"
	}
	return "completed"
}

// This is the source DownloaderTorrent schema, not the download page's Task
// projection. The raw daemon objects remain separately sealed for provenance.
func (a *App) coreDeletedTorrentSnapshots(ctx context.Context, d DownloaderConfig, hash string, raw []byte) ([]map[string]any, error) {
	rows, err := coreNativeRows(raw)
	if err != nil {
		return nil, err
	}
	options, err := a.globalRecognitionOptions(ctx)
	if err != nil {
		return nil, err
	}
	parser, err := releasemeta.NewMetaInfoParser(releasemeta.MetaInfoEnvironment{Options: options})
	if err != nil {
		return nil, err
	}
	out := []map[string]any{}
	kind := coreDownloaderKind(d.Kind)
	for _, row := range rows {
		if !sameHash(coreNativeHash(kind, row), hash) {
			continue
		}
		tags := coreText(row["tags"])
		if kind == "transmission" {
			labels, _ := row["labels"].([]any)
			parts := []string{}
			for _, label := range labels {
				parts = append(parts, coreText(label))
			}
			tags = strings.Join(parts, ",")
		}
		owned := false
		for _, tag := range strings.Split(tags, ",") {
			if strings.TrimSpace(tag) == "light-media" {
				owned = true
				break
			}
		}
		if !owned {
			continue
		}
		name := coreText(row["name"])
		meta, e := parser.ParseTitle(name, nil, nil)
		if e != nil {
			return nil, e
		}
		derived, e := meta.Derived()
		if e != nil {
			return nil, e
		}
		v := map[string]any{"downloader": d.Name, "hash": coreNativeHash(kind, row), "title": name, "site_name": nil, "name": meta.Name(), "year": meta.Get("year"), "season_episode": derived["season_episode"], "path": nil, "size": float64(0), "progress": float64(0), "state": "downloading", "upspeed": nil, "dlspeed": nil, "tags": tags, "save_path": nil, "content_path": nil, "category": nil, "download_limit": nil, "upload_limit": nil, "ratio_limit": nil, "seeding_time_limit": nil, "trackers": []string{}, "media": map[string]any{}, "userid": nil, "username": nil, "left_time": ""}
		if d.Name == "" {
			v["downloader"] = d.ID
		}
		var save, content string
		var dl, up, left float64
		if kind == "qbittorrent" {
			save = coreText(row["save_path"])
			content = coreText(row["content_path"])
			if content == "" {
				content = filepath.Join(save, name)
			}
			v["size"] = coreNumber(row["total_size"])
			v["progress"] = coreNumber(row["progress"]) * 100
			v["state"] = coreTaskState(kind, row["state"])
			v["category"] = row["category"]
			v["download_limit"] = coreNumber(row["dl_limit"]) / 1024
			v["upload_limit"] = coreNumber(row["up_limit"]) / 1024
			v["ratio_limit"] = row["ratio_limit"]
			v["seeding_time_limit"] = row["seeding_time_limit"]
			dl = coreNumber(row["dlspeed"])
			up = coreNumber(row["upspeed"])
			left = coreNumber(row["total_size"]) - coreNumber(row["completed"])
			// None and zero differ in the original size formatter.
			v["upspeed"] = cleaninvalidseed.FileSize(row["upspeed"])
		} else {
			save = coreText(coreValue(row, "downloadDir", "download_dir"))
			content = filepath.Join(save, name)
			v["size"] = coreNumber(coreValue(row, "totalSize", "total_size"))
			v["progress"] = coreNumber(coreValue(row, "percentDone", "percent_done")) * 100
			v["state"] = coreTaskState(kind, row["status"])
			v["download_limit"] = coreValue(row, "downloadLimit", "download_limit")
			v["upload_limit"] = coreValue(row, "uploadLimit", "upload_limit")
			v["ratio_limit"] = coreValue(row, "seedRatioLimit", "seed_ratio_limit")
			v["seeding_time_limit"] = coreValue(row, "seedIdleLimit", "seed_idle_limit")
			dl = coreNumber(coreValue(row, "rateDownload", "rate_download"))
			up = coreNumber(coreValue(row, "rateUpload", "rate_upload"))
			left = coreNumber(coreValue(row, "leftUntilDone", "left_until_done"))
			v["upspeed"] = cleaninvalidseed.FileSize(up)
		}
		mapped, e := coreDeletedDaemonPath(d, content)
		if e != nil {
			return nil, e
		}
		v["path"] = mapped
		v["content_path"] = mapped
		if save != "" {
			mapped, e = coreDeletedDaemonPath(d, save)
			if e != nil {
				return nil, e
			}
			v["save_path"] = mapped
		}
		v["dlspeed"] = cleaninvalidseed.FileSize(dl)
		if dl > 0 {
			v["left_time"] = coreSeconds(left / dl)
		}
		out = append(out, v)
	}
	return out, nil
}

// A daemon path in an event snapshot does not grant application filesystem IO.
// Preserve existing legacy normalization without imposing organizer mounts.
func coreDeletedDaemonPath(d DownloaderConfig, path string) (string, error) {
	if len(d.Mappings) > 0 {
		return mapPath(path, d.Mappings)
	}
	return filepath.Clean(path), nil
}
