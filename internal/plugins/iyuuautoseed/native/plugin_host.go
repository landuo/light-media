package download

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"mime/multipart"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"light-media/internal/releasemeta/legacyanime"
)

func (c *IYUUClient) PluginHostTrackerRows(ctx context.Context, hash string) (json.RawMessage, error) {
	hash, err := c.client.validHash(hash)
	if err != nil {
		return nil, err
	}
	if c.client.config.Kind == "qbittorrent" {
		if err = c.client.nativeLock(ctx); err != nil {
			return nil, err
		}
		defer c.client.mu.Unlock()
		raw, err := c.client.qb(mutationReadOnly(ctx), http.MethodPost, "torrents/trackers", url.Values{"hash": {hash}}.Encode(), "application/x-www-form-urlencoded")
		if err != nil {
			return nil, err
		}
		if _, err = nativeObjectList(raw, "plugin_trackers"); err != nil {
			return nil, err
		}
		return raw, nil
	}
	raw, err := c.Torrents(ctx, []string{hash}, false)
	if err != nil {
		return nil, err
	}
	rows, err := nativeObjectList(raw, "plugin_trackers")
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		if values, ok := row["trackerStats"].([]any); ok {
			for _, value := range values {
				if item, ok := value.(map[string]any); ok {
					if err = nativeAliasKeys(item, nativeTRTrackerKeys); err != nil {
						return nil, err
					}
				}
			}
		}
	}
	return json.Marshal(rows)
}

// The direct vendor torrents_delete_tags call removes definitions only. It
// must not require an unrelated task selector or detach tasks in a first call.
func (c *IYUUClient) PluginHostDeleteTagDefinitions(ctx context.Context, tags []string) (out IYUUReceipt, err error) {
	ctx, phase := beginMutation(ctx)
	defer func() { err = phase.finish(err, &out.Outcome) }()
	out.Outcome = "rejected"
	if c.client.config.Kind != "qbittorrent" || len(tags) == 0 {
		return out, failure("plugin_delete_tags", "invalid_selector")
	}
	if err = c.client.nativeLock(ctx); err != nil {
		return out, err
	}
	defer c.client.mu.Unlock()
	_, err = c.client.qbSelected(ctx, http.MethodPost, "torrents/deleteTags", url.Values{"tags": {strings.Join(tags, ",")}}.Encode(), "application/x-www-form-urlencoded")
	if err != nil {
		out.Outcome = "unknown"
		return out, err
	}
	return IYUUReceipt{Accepted: true, Outcome: "confirmed"}, nil
}

// get_torrent_id_by_tag sleeps before every attempt (nine total). Its cleanup
// return is ignored by the source: an observed ID is still returned when tag
// removal fails. Selected cleanup itself is never automatically retried.
func (c *IYUUClient) PluginHostFindByTag(ctx context.Context, tags []string, status *string) (string, error) {
	return c.pluginHostFindByTag(ctx, tags, status, false)
}
func (c *IYUUClient) PluginHostSourceFindByTag(ctx context.Context, tags []string, status *string) (string, error) {
	return c.pluginHostFindByTag(ctx, tags, status, true)
}
func (c *IYUUClient) pluginHostFindByTag(ctx context.Context, tags []string, status *string, source bool) (string, error) {
	if c.client.config.Kind != "qbittorrent" || len(tags) == 0 {
		return "", failure("plugin_find_tag", "invalid_selector")
	}
	for attempt := 1; attempt < 10; attempt++ {
		if err := c.options.Sleep(ctx, 3*time.Second); err != nil {
			return "", err
		}
		if c.options.Guard != nil {
			if err := c.options.Guard(ctx); err != nil {
				return "", err
			}
		}
		if err := c.client.nativeLock(ctx); err != nil {
			return "", err
		}
		form := url.Values{}
		if status != nil {
			filter := *status
			if filter == "paused" || filter == "stopped" || filter == "resumed" || filter == "running" {
				version, err := c.webVersionLocked(ctx)
				if err != nil {
					c.client.mu.Unlock()
					var denied *iyuuGuardError
					if errors.As(err, &denied) {
						return "", err
					}
					continue
				}
				modern, err := iyuuVersionAtLeast(version, 2, 11)
				if err != nil {
					c.client.mu.Unlock()
					continue
				}
				if modern {
					if filter == "paused" {
						filter = "stopped"
					}
					if filter == "resumed" {
						filter = "running"
					}
				} else {
					if filter == "stopped" {
						filter = "paused"
					}
					if filter == "running" {
						filter = "resumed"
					}
				}
			}
			form.Set("filter", filter)
		}
		raw, err := c.client.qb(mutationReadOnly(ctx), http.MethodPost, "torrents/info", form.Encode(), "application/x-www-form-urlencoded")
		c.client.mu.Unlock()
		if err != nil {
			if ctx.Err() != nil {
				return "", ctx.Err()
			}
			var denied *iyuuGuardError
			if errors.As(err, &denied) {
				return "", err
			}
			if c.options.Guard != nil {
				if denied := c.options.Guard(ctx); denied != nil {
					return "", denied
				}
			}
			continue
		}
		rows, err := nativeObjectList(raw, "plugin_find_tag")
		if err != nil {
			continue
		}
		found := ""
		valid := true
		for _, row := range rows {
			text, ok := row["tags"].(string)
			if !ok {
				valid = false
				break
			}
			actual := map[string]bool{}
			for _, tag := range strings.Split(text, ",") {
				actual[legacyanime.Strip(tag)] = true
			}
			match := true
			for _, tag := range tags {
				match = match && actual[tag]
			}
			if !match {
				continue
			}
			hash, ok := row["hash"].(string)
			if !ok {
				continue
			}
			if _, err := c.client.validHash(hash); err != nil {
				return "", err
			}
			if found == "" {
				found = strings.ToLower(hash)
			}
		}
		if !valid || found == "" {
			continue
		}
		if source {
			// Source calls these separate wrappers even when the first returns
			// ordinary False. Never replay its selected write. Owner revocation
			// or cancellation still stops before the different global action.
			_, cleanupErr := c.PluginHostTags(ctx, "remove_tags", []string{found}, tags)
			var denied *iyuuGuardError
			if errors.As(cleanupErr, &denied) || errors.Is(cleanupErr, context.Canceled) || errors.Is(cleanupErr, context.DeadlineExceeded) || MutationFailureClass(cleanupErr) == "owner_denied" {
				return "", cleanupErr
			}
			if c.options.Guard != nil {
				if err := c.options.Guard(ctx); err != nil {
					return "", err
				}
			}
			_, cleanupErr = c.PluginHostDeleteTagDefinitions(ctx, tags)
			if errors.As(cleanupErr, &denied) || errors.Is(cleanupErr, context.Canceled) || errors.Is(cleanupErr, context.DeadlineExceeded) || MutationFailureClass(cleanupErr) == "owner_denied" {
				return "", cleanupErr
			}
			return found, nil
		}
		_, cleanupErr := c.PluginHostTags(ctx, "delete_tags", []string{found}, tags)
		var denied *iyuuGuardError
		if errors.As(cleanupErr, &denied) || errors.Is(cleanupErr, context.Canceled) || errors.Is(cleanupErr, context.DeadlineExceeded) {
			return "", cleanupErr
		}
		return found, nil
	}
	return "", nil
}

// Match get_torrents(tags) before get_completed_torrents' final predicate.
// In particular malformed tags in an otherwise incomplete response must not be
// silently hidden by applying the completion predicate first.
func (c *IYUUClient) PluginHostCompleted(ctx context.Context, hashes, tags []string) (json.RawMessage, error) {
	return c.pluginHostFilteredTorrents(ctx, hashes, tags, "completed")
}
func (c *IYUUClient) PluginHostDownloading(ctx context.Context, hashes, tags []string) (json.RawMessage, error) {
	return c.pluginHostFilteredTorrents(ctx, hashes, tags, "downloading")
}
func (c *IYUUClient) pluginHostFilteredTorrents(ctx context.Context, hashes, tags []string, filter string) (json.RawMessage, error) {
	ctx = mutationReadOnly(ctx)
	var raw json.RawMessage
	var err error
	if c.client.config.Kind == "qbittorrent" {
		if err = c.client.nativeLock(ctx); err != nil {
			return nil, err
		}
		form := url.Values{"filter": {filter}}
		if hashes != nil {
			form.Set("hashes", strings.Join(hashes, "|"))
		}
		raw, err = c.client.qb(ctx, http.MethodPost, "torrents/info", form.Encode(), "application/x-www-form-urlencoded")
		c.client.mu.Unlock()
	} else {
		raw, err = c.Torrents(ctx, hashes, false)
	}
	if err != nil {
		return nil, err
	}
	rows, err := nativeObjectList(raw, "plugin_completed")
	if err != nil {
		return nil, err
	}
	selected := []map[string]any{}
	for _, row := range rows {
		if c.client.config.Kind == "transmission" {
			status, ok := row["status"].(json.Number)
			selected := status == "5" || status == "6"
			if filter == "downloading" {
				selected = status == "3" || status == "4"
			}
			if !ok || !selected {
				continue
			}
		}
		actual := map[string]bool{}
		if len(tags) > 0 {
			if c.client.config.Kind == "qbittorrent" {
				text, ok := row["tags"].(string)
				if !ok {
					return nil, failure("plugin_completed", "invalid_tags")
				}
				for _, tag := range strings.Split(text, ",") {
					actual[legacyanime.Strip(tag)] = true
				}
			} else if labels, exists := row["labels"]; exists {
				values, ok := labels.([]any)
				if !ok {
					return nil, failure("plugin_completed", "invalid_labels")
				}
				for _, tag := range values {
					actual[legacyanime.Strip(iyuuScalarString(tag))] = true
				}
			}
		}
		match := true
		for _, tag := range tags {
			match = match && actual[tag]
		}
		if !match {
			continue
		}
		selected = append(selected, row)
	}
	if c.client.config.Kind == "qbittorrent" && filter == "completed" {
		return iyuuCompletedQB(selected)
	}
	return json.Marshal(selected)
}

// The shared downloading transport predates the standard SDK and defaults to
// ignore_category_check=true. Apply the fixed wrapper's optional false branch
// before entering that transport, without changing its existing callers.
func (c *IYUUClient) PluginHostAdd(ctx context.Context, spec IYUUAddSpec, paused bool, uploadBytes, downloadBytes *int64) (IYUUAddResult, error) {
	if uploadBytes != nil || downloadBytes != nil {
		return c.pluginHostQBAddWithLimits(ctx, spec, paused, uploadBytes, downloadBytes)
	}
	if paused {
		return c.AddPaused(ctx, spec)
	}
	local := *c
	if !spec.IgnoreCategoryCheck && (spec.Category == nil || *spec.Category == "" || !c.options.AutoCategory) {
		local.options.AutoCategory = false
		spec.Category = nil
	}
	return local.BrushFlowAddDownloading(ctx, spec, nil, nil, nil)
}

func (c *IYUUClient) pluginHostQBAddWithLimits(ctx context.Context, spec IYUUAddSpec, paused bool, uploadBytes, downloadBytes *int64) (out IYUUAddResult, err error) {
	ctx, phase := beginMutation(ctx)
	defer func() { err = phase.finish(err, &out.Outcome) }()
	out = IYUUAddResult{SourceTruthy: true, AddedIDs: []string{}, Outcome: "rejected"}
	if c.client.config.Kind != "qbittorrent" || (len(spec.Torrent) == 0) == (spec.Magnet == "") || len(spec.Torrent) > maxResponse || spec.Magnet != "" && !strings.HasPrefix(spec.Magnet, "magnet:") {
		return out, failure("plugin_add", "invalid_material")
	}
	if err = c.client.nativeLock(ctx); err != nil {
		return out, err
	}
	defer c.client.mu.Unlock()
	if _, err = c.webVersionLocked(ctx); err != nil {
		return out, err
	}
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	fields := [][2]string{{"paused", iyuuBool(paused)}, {"stopped", iyuuBool(paused)}, {"sequentialDownload", iyuuBool(c.options.Sequential)}, {"firstLastPiecePrio", iyuuBool(c.options.FirstLast)}}
	category, auto := c.addCategoryMode(spec)
	fields = append(fields, [2]string{"autoTMM", iyuuBool(auto)})
	if category != nil {
		fields = append(fields, [2]string{"category", *category})
	}
	if spec.SavePath != nil && *spec.SavePath != "" {
		fields = append(fields, [2]string{"savepath", *spec.SavePath})
	}
	if len(spec.Tags) > 0 {
		fields = append(fields, [2]string{"tags", strings.Join(spec.Tags, ",")})
	}
	if value, present := iyuuSkipCheckingField(spec); present {
		fields = append(fields, [2]string{"skip_checking", value})
	}
	if spec.Magnet != "" {
		fields = append(fields, [2]string{"urls", spec.Magnet})
	}
	if uploadBytes != nil {
		fields = append(fields, [2]string{"upLimit", strconv.FormatInt(*uploadBytes, 10)})
	}
	if downloadBytes != nil {
		fields = append(fields, [2]string{"dlLimit", strconv.FormatInt(*downloadBytes, 10)})
	}
	for _, label := range spec.QBLabels {
		fields = append(fields, [2]string{"labels", label})
	}
	for _, field := range fields {
		if err = writer.WriteField(field[0], field[1]); err != nil {
			return out, err
		}
	}
	if len(spec.Torrent) > 0 {
		part, e := writer.CreateFormFile("torrent__0", "torrent__0")
		if e != nil {
			return out, e
		}
		if _, err = part.Write(spec.Torrent); err != nil {
			return out, err
		}
	}
	if err = writer.Close(); err != nil {
		return out, err
	}
	if body.Len() > maxResponse {
		return out, failure("plugin_add", "payload_limit")
	}
	raw, err := c.client.qbSelected(ctx, http.MethodPost, "torrents/add", body.String(), writer.FormDataContentType())
	if err != nil {
		out.Outcome = "unknown"
		return out, err
	}
	accepted, ids, err := iyuuAddResponse(raw)
	if err != nil {
		out.Outcome = "unknown"
		return out, err
	}
	out.Accepted = accepted
	out.AddedIDs = ids
	if accepted {
		out.Outcome = "confirmed"
	}
	return out, nil
}

// Fixed TR change_torrent speed branch: KB/s remain KB/s, zero disables the
// corresponding limit, and a negative nonzero input retains Python bool(value).
func (c *IYUUClient) PluginHostTransmissionLimits(ctx context.Context, hash string, uploadKB, downloadKB *int64) (out IYUUReceipt, err error) {
	ctx, phase := beginMutation(ctx)
	defer func() { err = phase.finish(err, &out.Outcome) }()
	out.Outcome = "rejected"
	if c.client.config.Kind != "transmission" || uploadKB == nil && downloadKB == nil {
		return out, failure("plugin_limits", "invalid_selector")
	}
	hash, err = c.client.validHash(hash)
	if err != nil {
		return out, err
	}
	if err = c.client.nativeLock(ctx); err != nil {
		return out, err
	}
	defer c.client.mu.Unlock()
	if err = c.client.trConnect(ctx); err != nil {
		return out, err
	}
	args := map[string]any{"ids": []string{hash}}
	if uploadKB != nil {
		args["upload_limit"] = *uploadKB
		args["upload_limited"] = *uploadKB != 0
	}
	if downloadKB != nil {
		args["download_limit"] = *downloadKB
		args["download_limited"] = *downloadKB != 0
	}
	if c.client.trDialect == "legacy" {
		for modern, legacy := range map[string]string{"upload_limit": "uploadLimit", "upload_limited": "uploadLimited", "download_limit": "downloadLimit", "download_limited": "downloadLimited"} {
			if value, ok := args[modern]; ok {
				delete(args, modern)
				args[legacy] = value
			}
		}
	}
	_, err = c.client.trSelectedRPC(ctx, "torrent_set", args, c.client.trDialect)
	if err != nil {
		out.Outcome = "unknown"
		return out, err
	}
	return IYUUReceipt{Accepted: true, Outcome: "confirmed"}, nil
}

// PluginHostTags preserves qB append/remove semantics and Transmission's label
// replacement. It uses the existing origin-bound authenticated transport and
// selected-mutation seal; a dispatched write is never retried after rejection.
func (c *IYUUClient) PluginHostTags(ctx context.Context, action string, hashes, tags []string) (out IYUUReceipt, err error) {
	ctx, phase := beginMutation(ctx)
	defer func() { err = phase.finish(err, &out.Outcome) }()
	out.Outcome = "rejected"
	if len(hashes) == 0 {
		return out, failure("plugin_tags", "empty_selector")
	}
	ids := make([]string, len(hashes))
	for i, hash := range hashes {
		ids[i], err = c.client.validHash(hash)
		if err != nil {
			return out, err
		}
	}
	if action != "set_tags" && action != "remove_tags" && action != "delete_tags" {
		return out, failure("plugin_tags", "unsupported_action")
	}
	if c.client.config.Kind == "transmission" && action != "set_tags" {
		return out, failure("plugin_tags", "unsupported_kind")
	}
	if err = c.client.nativeLock(ctx); err != nil {
		return out, err
	}
	defer c.client.mu.Unlock()
	if c.client.config.Kind == "qbittorrent" {
		endpoint := "addTags"
		if action != "set_tags" {
			endpoint = "removeTags"
		}
		form := url.Values{"hashes": {strings.Join(ids, "|")}, "tags": {strings.Join(tags, ",")}}
		if _, err = c.client.qbSelected(ctx, http.MethodPost, "torrents/"+endpoint, form.Encode(), "application/x-www-form-urlencoded"); err != nil {
			out.Outcome = "unknown"
			return out, err
		}
		if action == "delete_tags" {
			if _, err = c.client.qbSelected(ctx, http.MethodPost, "torrents/deleteTags", url.Values{"tags": {strings.Join(tags, ",")}}.Encode(), "application/x-www-form-urlencoded"); err != nil {
				out.Outcome = "unknown"
				return out, err
			}
		}
	} else {
		if err = c.client.trConnect(ctx); err != nil {
			return out, err
		}
		_, err = c.client.trSelectedRPC(ctx, "torrent_set", map[string]any{"ids": ids, "labels": append([]string{}, tags...)}, c.client.trDialect)
		if err != nil {
			out.Outcome = "unknown"
			return out, err
		}
	}
	return IYUUReceipt{Accepted: true, Outcome: "confirmed"}, nil
}

func (c *IYUUClient) PluginHostRemoveTasks(ctx context.Context, hashes []string) (out IYUUReceipt, err error) {
	ctx, phase := beginMutation(ctx)
	defer func() { err = phase.finish(err, &out.Outcome) }()
	out.Outcome = "rejected"
	if len(hashes) == 0 {
		return out, failure("plugin_remove", "empty_selector")
	}
	ids := make([]string, len(hashes))
	for i, hash := range hashes {
		ids[i], err = c.client.validHash(hash)
		if err != nil {
			return out, err
		}
	}
	if err = c.client.nativeLock(ctx); err != nil {
		return out, err
	}
	defer c.client.mu.Unlock()
	if c.client.config.Kind == "qbittorrent" {
		_, err = c.client.qbSelected(ctx, http.MethodPost, "torrents/delete", url.Values{"hashes": {strings.Join(ids, "|")}, "deleteFiles": {"false"}}.Encode(), "application/x-www-form-urlencoded")
	} else if err = c.client.trConnect(ctx); err == nil {
		_, err = c.client.trSelectedRPC(ctx, "torrent_remove", map[string]any{"ids": ids, "delete_local_data": false}, c.client.trDialect)
	}
	if err != nil {
		out.Outcome = "unknown"
		return out, err
	}
	return IYUUReceipt{Accepted: true, Outcome: "confirmed"}, nil
}
