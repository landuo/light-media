package download

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
)

func (c *IYUUClient) validateDestinationLocked(ctx context.Context, spec IYUUAddSpec, want IYUUDestination) error {
	got, e := c.effectiveAddDestinationLocked(ctx, spec)
	if e != nil {
		return e
	}
	if got != want {
		return failure("brush_destination", "daemon_directory_changed")
	}
	return nil
}

// BrushFlowControl performs one source-selected write. It never negotiates or
// retries an already selected mutation in another session/dialect.
func (c *IYUUClient) BrushFlowControl(ctx context.Context, op string, hashes []string, tags []string, uploadKB, downloadKB *int64) (out IYUUReceipt, err error) {
	ctx, phase := beginMutation(ctx)
	defer func() { err = phase.finish(err, &out.Outcome) }()
	out.Outcome = "rejected"
	switch op {
	case "pause", "delete", "reannounce", "delete_tags", "limits":
	default:
		return out, failure("brush_control", "unsupported_action")
	}
	ids := []string{}
	if op != "delete_tags" {
		if len(hashes) == 0 {
			return out, failure("brush_control", "empty_selector")
		}
		for _, h := range hashes {
			v, e := c.client.validHash(h)
			if e != nil {
				return out, e
			}
			ids = append(ids, v)
		}
	} else if len(tags) == 0 || !validTags(tags) {
		return out, failure("brush_control", "invalid_tags")
	}
	if e := c.client.nativeLock(ctx); e != nil {
		return out, e
	}
	defer c.client.mu.Unlock()
	if c.client.config.Kind == "qbittorrent" {
		endpoint := ""
		form := url.Values{}
		switch op {
		case "pause":
			v, e := c.webVersionLocked(ctx)
			if e != nil {
				return out, e
			}
			modern, e := iyuuVersionAtLeast(v, 2, 11)
			if e != nil {
				return out, e
			}
			endpoint = "pause"
			if modern {
				endpoint = "stop"
			}
			form.Set("hashes", strings.Join(ids, "|"))
		case "delete":
			endpoint = "delete"
			form.Set("hashes", strings.Join(ids, "|"))
			form.Set("deleteFiles", "True")
		case "reannounce":
			endpoint = "reannounce"
			form.Set("hashes", strings.Join(ids, "|"))
		case "delete_tags":
			endpoint = "deleteTags"
			form.Set("tags", strings.Join(tags, ","))
		default:
			return out, failure("brush_control", "limits_are_add_arguments_for_qb")
		}
		if _, e := c.client.qbSelected(ctx, http.MethodPost, "torrents/"+endpoint, form.Encode(), "application/x-www-form-urlencoded"); e != nil {
			out.Outcome = "unknown"
			return out, e
		}
		out.Accepted = true
		out.Outcome = "confirmed"
		return out, nil
	}
	if op == "delete_tags" || op == "reannounce" {
		return out, failure("brush_control", "wrong_kind")
	}
	if e := c.client.trConnect(ctx); e != nil {
		return out, e
	}
	method := ""
	args := map[string]any{"ids": ids}
	switch op {
	case "pause":
		method = "torrent_stop"
	case "delete":
		method = "torrent_remove"
		args["delete_local_data"] = true
	case "limits":
		method = "torrent_set"
		if uploadKB != nil {
			args["upload_limit"] = *uploadKB
			args["upload_limited"] = *uploadKB > 0
		}
		if downloadKB != nil {
			args["download_limit"] = *downloadKB
			args["download_limited"] = *downloadKB > 0
		}
	}
	if c.client.trDialect == "legacy" && op == "limits" {
		for modern, legacy := range map[string]string{"upload_limit": "uploadLimit", "upload_limited": "uploadLimited", "download_limit": "downloadLimit", "download_limited": "downloadLimited"} {
			if v, ok := args[modern]; ok {
				delete(args, modern)
				args[legacy] = v
			}
		}
	}
	if _, e := c.client.trSelectedRPC(ctx, method, args, c.client.trDialect); e != nil {
		out.Outcome = "unknown"
		return out, e
	}
	out.Accepted = true
	out.Outcome = "confirmed"
	return out, nil
}
func (c *IYUUClient) BrushFlowTags(ctx context.Context) ([]string, error) {
	ctx = mutationReadOnly(ctx)
	if c.client.config.Kind != "qbittorrent" {
		return nil, failure("brush_tags", "wrong_kind")
	}
	if e := c.client.nativeLock(ctx); e != nil {
		return nil, e
	}
	defer c.client.mu.Unlock()
	raw, e := c.client.qb(ctx, http.MethodGet, "torrents/tags", "", "")
	if e != nil {
		return nil, e
	}
	var tags []string
	if decode(raw, &tags) != nil {
		return nil, failure("brush_tags", "invalid_tags")
	}
	return tags, nil
}
func (c *IYUUClient) BrushFlowTransfer(ctx context.Context) (json.RawMessage, error) {
	ctx = mutationReadOnly(ctx)
	if e := c.client.nativeLock(ctx); e != nil {
		return nil, e
	}
	defer c.client.mu.Unlock()
	if c.client.config.Kind == "qbittorrent" {
		raw, e := c.client.qb(ctx, http.MethodGet, "transfer/info", "", "")
		if e != nil {
			return nil, e
		}
		var v map[string]any
		if decode(raw, &v) != nil || v == nil {
			return nil, failure("brush_transfer", "invalid_response")
		}
		return raw, nil
	}
	if e := c.client.trConnect(ctx); e != nil {
		return nil, e
	}
	v, e := c.client.trRPC(ctx, "session_stats", map[string]any{}, c.client.trDialect)
	if e != nil {
		return nil, e
	}
	return json.Marshal(v)
}
