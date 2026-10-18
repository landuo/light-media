package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	bf "light-media/internal/plugins/brushflow"
	native "light-media/internal/plugins/iyuuautoseed/native"
)

func brushFlowDownloaderOptions(settings Settings) []bf.Downloader {
	out := []bf.Downloader{}
	for _, d := range settings.Downloaders {
		out = append(out, bf.Downloader{Name: d.Name, Kind: seedRescueNativeConfig(d).Kind, Active: downloaderEnabled(d)})
	}
	return out
}
func (o *pluginBrushFlowOwner) Downloaders(ctx context.Context) ([]bf.Downloader, error) {
	if e := o.callIOGuard(ctx); e != nil {
		return nil, e
	}
	return brushFlowDownloaderOptions(o.a.Settings()), nil
}
func (o *pluginBrushFlowOwner) downloader(ctx context.Context, name string) (*native.IYUUClient, DownloaderConfig, error) {
	c := o.call(ctx)
	if c == nil {
		return nil, DownloaderConfig{}, brushFlowEvidenceError()
	}
	if e := c.ioGuard(ctx); e != nil {
		return nil, DownloaderConfig{}, e
	}
	d, e := seedRescueDownloader(o.a.Settings(), name)
	if e != nil {
		return nil, d, e
	}
	stamp := seedRescueDownloaderSHA(d)
	client, e := native.NewIYUUClient(seedRescueNativeConfig(d), native.IYUUOptions{Sleep: o.Sleep, APIKey: d.APIKey, AutoCategory: d.AutoCategory, Sequential: d.Sequential, FirstLast: d.FirstLast, Guard: func(ctx context.Context) error {
		if e := c.ioGuard(ctx); e != nil {
			return e
		}
		current, e := seedRescueDownloader(o.a.Settings(), name)
		if e != nil || seedRescueDownloaderSHA(current) != stamp {
			return &brushFlowBoundaryError{bad("BRUSH_FLOW_SCOPE_DENIED", "下载器配置已经变化")}
		}
		return nil
	}})
	return client, d, e
}
func brushFlowHash(v bf.DownloadState) string {
	if v.Kind == "qbittorrent" {
		return brushFlowString(v.Raw["hash"])
	}
	return brushFlowString(v.Raw["hashString"])
}
func brushFlowString(v any) string {
	if v == nil {
		return ""
	}
	return fmt.Sprint(v)
}
func brushFlowNumber(v any) float64 {
	var n float64
	json.Unmarshal(mustCleanSeedJSON(v), &n)
	return n
}
func brushFlowLabels(v bf.DownloadState) []string {
	out := []string{}
	if v.Kind == "qbittorrent" {
		for _, s := range strings.Split(brushFlowString(v.Raw["tags"]), ",") {
			if s = strings.TrimSpace(s); s != "" {
				out = append(out, s)
			}
		}
	} else {
		var a []string
		json.Unmarshal(mustCleanSeedJSON(v.Raw["labels"]), &a)
		out = a
	}
	return out
}
func brushFlowHas(ss []string, s string) bool {
	for _, v := range ss {
		if v == s {
			return true
		}
	}
	return false
}
func (o *pluginBrushFlowOwner) torrents(ctx context.Context, name string, hashes []string, completed bool) ([]bf.DownloadState, error) {
	client, d, e := o.downloader(ctx, name)
	if e != nil {
		return nil, e
	}
	defer client.Close()
	raw, e := client.Torrents(ctx, hashes, completed)
	if e != nil {
		return nil, e
	}
	return o.decodeTorrents(ctx, d, name, raw)
}
func (o *pluginBrushFlowOwner) decodeTorrents(ctx context.Context, d DownloaderConfig, name string, raw []byte) ([]bf.DownloadState, error) {
	var e error
	var rows []bf.Row
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if e = decoder.Decode(&rows); e != nil {
		return nil, e
	}
	c := o.call(ctx)
	if c.observations == nil {
		c.observations = map[string]map[string]bf.DownloadState{}
	}
	if c.observations[name] == nil {
		c.observations[name] = map[string]bf.DownloadState{}
	}
	out := []bf.DownloadState{}
	for _, r := range rows {
		if seedRescueNativeConfig(d).Kind == "transmission" {
			r = brushFlowTRProperties(r)
		}
		v := bf.DownloadState{Kind: seedRescueNativeConfig(d).Kind, Raw: r}
		h := brushFlowHash(v)
		if h != "" {
			c.observations[name][h] = v
		}
		out = append(out, v)
	}
	return out, nil
}
func (o *pluginBrushFlowOwner) Torrents(ctx context.Context, name string) ([]bf.DownloadState, error) {
	return o.torrents(ctx, name, nil, false)
}
func (o *pluginBrushFlowOwner) Downloading(ctx context.Context, name, tag string) ([]bf.DownloadState, error) {
	client, d, e := o.downloader(ctx, name)
	if e != nil {
		return nil, e
	}
	defer client.Close()
	if seedRescueNativeConfig(d).Kind == "qbittorrent" {
		raw, e := client.SeedRescueDownloading(ctx, tag)
		if e != nil {
			return nil, e
		}
		return o.decodeTorrents(ctx, d, name, raw)
	}
	raw, e := client.Torrents(ctx, nil, false)
	if e != nil {
		return nil, e
	}
	rows, e := o.decodeTorrents(ctx, d, name, raw)
	if e != nil {
		return nil, e
	}
	out := []bf.DownloadState{}
	for _, v := range rows {
		state := brushFlowString(v.Raw["status"])
		if brushFlowHas(brushFlowLabels(v), tag) && (state == "3" || state == "4") {
			out = append(out, v)
		}
	}
	return out, nil
}
func (o *pluginBrushFlowOwner) Completed(ctx context.Context, name string, tags []string) ([]bf.DownloadState, error) {
	d, e := seedRescueDownloader(o.a.Settings(), name)
	if e != nil {
		return nil, e
	}
	tr := seedRescueNativeConfig(d).Kind == "transmission"
	rows, e := o.torrents(ctx, name, nil, !tr)
	if e != nil {
		return nil, e
	}
	out := []bf.DownloadState{}
	for _, v := range rows {
		ok := true
		if tr {
			state := brushFlowString(v.Raw["status"])
			ok = state == "5" || state == "6"
		}
		for _, tag := range tags {
			if !brushFlowHas(brushFlowLabels(v), tag) {
				ok = false
			}
		}
		if ok {
			out = append(out, v)
		}
	}
	return out, nil
}
func brushFlowTRProperties(raw bf.Row) bf.Row {
	r := bf.Row{}
	for k, v := range raw {
		r[k] = v
	}
	for property, key := range map[string]string{"added_date": "addedDate", "done_date": "doneDate", "activity_date": "activityDate", "total_size": "totalSize", "uploaded_ever": "uploadedEver", "downloaded_ever": "downloadedEver", "ratio": "uploadRatio", "magnet_link": "magnetLink"} {
		if v, ok := raw[key]; ok {
			r[property] = v
		}
	}
	if v, ok := raw["trackerList"].(string); ok {
		ss := []string{}
		for _, s := range strings.FieldsFunc(v, func(r rune) bool {
			return r == '\n' || r == '\r' || r == '\v' || r == '\f' || r == '\u0085' || r == '\u2028' || r == '\u2029' || r == '\u001c' || r == '\u001d' || r == '\u001e'
		}) {
			if s != "" {
				ss = append(ss, s)
			}
		}
		r["tracker_list"] = ss
	}
	n := 100 * brushFlowNumber(raw["percentDone"])
	r["progress"], _ = strconv.ParseFloat(strconv.FormatFloat(n, 'f', 2, 64), 64)
	return r
}
func (o *pluginBrushFlowOwner) TransferInfo(ctx context.Context) ([]bf.Transfer, error) {
	out := []bf.Transfer{}
	for _, d := range o.a.Settings().Downloaders {
		if !downloaderEnabled(d) {
			continue
		}
		client, _, e := o.downloader(ctx, d.Name)
		if e != nil {
			return nil, e
		}
		raw, e := client.BrushFlowTransfer(ctx)
		client.Close()
		if e != nil {
			return nil, e
		}
		var r bf.Row
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.UseNumber()
		if e = dec.Decode(&r); e != nil {
			return nil, e
		}
		v := bf.Transfer{}
		if seedRescueNativeConfig(d).Kind == "qbittorrent" {
			v = bf.Transfer{UploadSpeed: brushFlowNumber(r["up_info_speed"]), DownloadSpeed: brushFlowNumber(r["dl_info_speed"]), UploadSize: brushFlowNumber(r["up_info_data"]), DownloadSize: brushFlowNumber(r["dl_info_data"])}
		} else {
			v.UploadSpeed = brushFlowNumber(r["upload_speed"])
			if r["upload_speed"] == nil {
				v.UploadSpeed = brushFlowNumber(r["uploadSpeed"])
			}
			v.DownloadSpeed = brushFlowNumber(r["download_speed"])
			if r["download_speed"] == nil {
				v.DownloadSpeed = brushFlowNumber(r["downloadSpeed"])
			}
			stats, _ := r["cumulative_stats"].(map[string]any)
			if stats == nil {
				stats, _ = r["cumulative-stats"].(map[string]any)
			}
			v.UploadSize = brushFlowNumber(stats["uploadedBytes"])
			if stats["uploadedBytes"] == nil {
				v.UploadSize = brushFlowNumber(stats["uploaded_bytes"])
			}
			v.DownloadSize = brushFlowNumber(stats["downloadedBytes"])
			if stats["downloadedBytes"] == nil {
				v.DownloadSize = brushFlowNumber(stats["downloaded_bytes"])
			}
		}
		out = append(out, v)
	}
	return out, nil
}
func (c *brushFlowCall) beginEffect(ctx context.Context, kind string, args any) (int, error) {
	if e := c.ioGuard(ctx); e != nil {
		return 0, e
	}
	ordinal := 0
	e := c.o.update(ctx, func(s *brushFlowState) error {
		r, ok := s.Runs[c.run.ID]
		if !ok || r.Phase != "started" {
			return brushFlowEvidenceError()
		}
		ordinal = len(r.Effects)
		r.Effects = append(r.Effects, brushFlowEffect{Kind: kind, Phase: "started", Args: mustCleanSeedJSON(args)})
		s.Runs[c.run.ID] = r
		return nil
	})
	return ordinal, e
}
func (c *brushFlowCall) finishEffect(ctx context.Context, ordinal int, kind string, result any) error {
	finish, cancel := seedRescueFinishContext(ctx)
	defer cancel()
	e := c.o.update(finish, func(s *brushFlowState) error { return c.finishEffectState(s, ordinal, kind, result) })
	if e != nil {
		c.uncertain.Store(true)
	}
	return e
}
func (c *brushFlowCall) finishEffectState(s *brushFlowState, ordinal int, kind string, result any) error {
	r := s.Runs[c.run.ID]
	if ordinal < 0 || ordinal >= len(r.Effects) || r.Effects[ordinal].Kind != kind || r.Effects[ordinal].Phase != "started" {
		return brushFlowEvidenceError()
	}
	r.Effects[ordinal].Phase = "confirmed"
	r.Effects[ordinal].Result = mustCleanSeedJSON(result)
	s.Runs[c.run.ID] = r
	return nil
}
func (c *brushFlowCall) unknown(e error) error {
	c.uncertain.Store(true)
	return &brushFlowBoundaryError{e}
}
func (o *pluginBrushFlowOwner) control(ctx context.Context, name, op string, hashes, tags []string, up, down *int64) (bool, error) {
	c := o.call(ctx)
	client, d, e := o.downloader(ctx, name)
	if e != nil {
		return false, e
	}
	defer client.Close()
	if op != "delete_tags" {
		for _, h := range hashes {
			v, ok := c.observations[name][h]
			allowed := brushFlowHas(brushFlowLabels(v), bf.GlobalTag)
			for _, task := range brushFlowTasks(c.config) {
				allowed = allowed || brushFlowHas(brushFlowLabels(v), task.Tag())
			}
			if !ok || !allowed {
				return false, brushFlowEvidenceError()
			}
		}
	} else {
		for _, tag := range tags {
			if c.temporaryTags[tag] == name {
				continue
			}
			allowed := tag == bf.GlobalTag
			if c.operation == "cleanup_tags" && strings.HasPrefix(tag, "刷流-") && brushFlowHas(c.observedTags[name], tag) {
				allowed = true
			}
			cfg, _ := bf.LoadApplied(c.config)
			for _, t := range cfg.Tasks {
				allowed = allowed || tag == t.Tag()
			}
			if c.operation == "cleanup_task_tag" && tag == (bf.Task{Row: bf.Row{"id": c.taskID}}).Tag() {
				allowed = true
			}
			if !allowed {
				return false, brushFlowEvidenceError()
			}
		}
	}
	args := map[string]any{"downloader_id": d.ID, "downloader_sha": seedRescueDownloaderSHA(d), "hashes": hashes, "tags": tags, "upload_kb": up, "download_kb": down}
	ordinal, e := c.beginEffect(ctx, op, args)
	if e != nil {
		return false, e
	}
	result, callErr := client.BrushFlowControl(ctx, op, hashes, tags, up, down)
	if callErr != nil && !native.MutationNotDispatched(callErr) {
		return false, c.unknown(callErr)
	}
	if hook := o.a.pluginBrushFlow.afterEffect; hook != nil {
		if e = hook(op); e != nil {
			return false, c.unknown(e)
		}
	}
	// A successful RPC is a source return value. Persist it before any subsequent
	// observation; a lost ACK remains unknown and never reaches this block.
	if e = c.finishEffect(ctx, ordinal, op, map[string]any{"accepted": result.Accepted, "not_dispatched": callErr != nil, "failure_class": native.MutationFailureClass(callErr)}); e != nil {
		return false, e
	}
	if callErr != nil {
		if native.MutationFailureClass(callErr) == "owner_denied" || native.MutationFailureClass(callErr) == "cancelled" {
			return false, &brushFlowBoundaryError{callErr}
		}
		return false, nil
	}
	return result.Accepted, nil
}
func (o *pluginBrushFlowOwner) Delete(ctx context.Context, name string, hashes []string, deleteFiles bool) (bool, error) {
	if !deleteFiles {
		return false, brushFlowEvidenceError()
	}
	return o.control(ctx, name, "delete", hashes, nil, nil, nil)
}
func (o *pluginBrushFlowOwner) Pause(ctx context.Context, name string, hashes []string) (bool, error) {
	return o.control(ctx, name, "pause", hashes, nil, nil, nil)
}
func (o *pluginBrushFlowOwner) Reannounce(ctx context.Context, name string, hashes []string) error {
	d, e := seedRescueDownloader(o.a.Settings(), name)
	if e != nil {
		return e
	}
	if seedRescueNativeConfig(d).Kind != "qbittorrent" {
		return nil
	}
	_, e = o.control(ctx, name, "reannounce", hashes, nil, nil, nil)
	return e
}
func (o *pluginBrushFlowOwner) Tags(ctx context.Context, name string) ([]string, error) {
	client, _, e := o.downloader(ctx, name)
	if e != nil {
		return nil, e
	}
	defer client.Close()
	tags, e := client.BrushFlowTags(ctx)
	if e == nil {
		if o.call(ctx).observedTags == nil {
			o.call(ctx).observedTags = map[string][]string{}
		}
		o.call(ctx).observedTags[name] = tags
	}
	return tags, e
}
func (o *pluginBrushFlowOwner) DeleteTags(ctx context.Context, name string, tags []string) (bool, error) {
	return o.control(ctx, name, "delete_tags", nil, tags, nil, nil)
}
func (o *pluginBrushFlowOwner) removeTemporaryTag(ctx context.Context, client *native.IYUUClient, d DownloaderConfig, hash, tag string) error {
	c := o.call(ctx)
	if c.temporaryTags[tag] != d.Name {
		return brushFlowEvidenceError()
	}
	ordinal, e := c.beginEffect(ctx, "remove_temporary_tag", map[string]any{"downloader_id": d.ID, "downloader_sha": seedRescueDownloaderSHA(d), "hash": hash, "tag": tag})
	if e != nil {
		return e
	}
	result, callErr := client.RemoveTag(ctx, hash, tag)
	if callErr != nil && !native.MutationNotDispatched(callErr) {
		return c.unknown(callErr)
	}
	if e = c.finishEffect(ctx, ordinal, "remove_temporary_tag", map[string]any{"accepted": result.Accepted, "not_dispatched": callErr != nil, "failure_class": native.MutationFailureClass(callErr)}); e != nil {
		return e
	}
	if callErr != nil && (native.MutationFailureClass(callErr) == "owner_denied" || native.MutationFailureClass(callErr) == "cancelled") {
		return &brushFlowBoundaryError{callErr}
	}
	return nil
}
