package download

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/url"
	"strings"
	"unicode/utf8"
)

// SeedRescueAddDownloading is the original SeedRescue bytes-only add call.
// It shares this package's private monotonic selected-dispatch seal and the
// existing read-only login/version preflight. Source defaults differ from
// IYUU's AddPaused: is_paused=false and ignore_category_check=true.
func (c *IYUUClient) SeedRescueAddDownloading(ctx context.Context, spec IYUUAddSpec) (out IYUUAddResult, err error) {
	ctx, phase := beginMutation(ctx)
	defer func() { err = phase.finish(err, &out.Outcome) }()
	if spec.Magnet != "" || len(spec.Torrent) == 0 {
		return out, failure("seed_rescue_add", "torrent_bytes_required")
	}
	spec.IgnoreCategoryCheck = true
	out = IYUUAddResult{AddedIDs: []string{}, Outcome: "rejected", SourceTruthy: c.client.config.Kind == "qbittorrent"}
	if len(spec.Torrent) == 0 && spec.Magnet == "" {
		return out, nil
	}
	if len(spec.Torrent) > maxResponse || len(spec.Magnet) > maxResponse {
		return out, failure("iyuu_add", "payload_limit")
	}
	if spec.Magnet != "" && !strings.HasPrefix(spec.Magnet, "magnet:") {
		return out, failure("iyuu_add", "unsupported_content_uri")
	}
	if spec.Magnet != "" && len(spec.Torrent) > 0 {
		return out, failure("iyuu_add", "ambiguous_content")
	}
	for _, s := range spec.Tags {
		if !utf8.ValidString(s) {
			return out, failure("iyuu_add", "invalid_utf8_tag")
		}
	}
	if e := c.client.nativeLock(ctx); e != nil {
		return out, e
	}
	defer c.client.mu.Unlock()
	if c.client.config.Kind == "qbittorrent" {
		category := spec.Category
		if _, e := c.webVersionLocked(ctx); e != nil {
			return out, e
		}
		auto := c.options.AutoCategory
		if !spec.IgnoreCategoryCheck {
			if category != nil && *category != "" && auto {
				auto = true
			} else {
				auto = false
				category = nil
			}
		}
		var body bytes.Buffer
		w := multipart.NewWriter(&body)
		field := func(name, value string) error { return w.WriteField(name, value) }
		if spec.Magnet != "" {
			if e := field("urls", spec.Magnet); e != nil {
				return out, e
			}
		}
		if category != nil {
			if e := field("category", *category); e != nil {
				return out, e
			}
		}
		if len(spec.Tags) > 0 {
			if e := field("tags", strings.Join(spec.Tags, ",")); e != nil {
				return out, e
			}
		}
		if spec.SavePath != nil && *spec.SavePath != "" {
			if e := field("savepath", *spec.SavePath); e != nil {
				return out, e
			}
		}
		for _, p := range [][2]string{{"sequentialDownload", iyuuBool(c.options.Sequential)}, {"firstLastPiecePrio", iyuuBool(c.options.FirstLast)}, {"paused", "False"}, {"stopped", "False"}} {
			if e := field(p[0], p[1]); e != nil {
				return out, e
			}
		}
		if spec.SkipChecking != nil {
			if e := field("skip_checking", iyuuBool(*spec.SkipChecking)); e != nil {
				return out, e
			}
		}
		if e := field("autoTMM", iyuuBool(auto)); e != nil {
			return out, e
		}
		if len(spec.Torrent) > 0 {
			header := make(map[string][]string)
			header["Content-Disposition"] = []string{`form-data; name="torrent__0"; filename="torrent__0"`}
			part, e := w.CreatePart(header)
			if e != nil {
				return out, e
			}
			if _, e = part.Write(spec.Torrent); e != nil {
				return out, e
			}
		}
		if e := w.Close(); e != nil {
			return out, e
		}
		raw, e := c.client.qbSelected(ctx, http.MethodPost, "torrents/add", body.String(), w.FormDataContentType())
		if e != nil {
			out.Outcome = "unknown"
			return out, e
		}
		accepted, ids, e := iyuuAddResponse(raw)
		if e != nil {
			out.Outcome = "unknown"
			return out, e
		}
		out.Accepted = accepted
		out.AddedIDs = ids
		if accepted {
			out.Outcome = "confirmed"
		}
		return out, nil
	}
	if e := c.client.trConnect(ctx); e != nil {
		return out, e
	}
	args := map[string]any{"paused": false, "labels": append([]string{}, spec.Tags...)}
	if spec.SavePath != nil {
		args["download_dir"] = *spec.SavePath
	}
	if spec.Magnet != "" {
		args["filename"] = spec.Magnet
	} else {
		args["metainfo"] = base64.StdEncoding.EncodeToString(spec.Torrent)
	}
	values, e := c.client.trSelectedRPC(ctx, "torrent_add", args, c.client.trDialect)
	if e != nil {
		out.Outcome = "unknown"
		return out, e
	}
	var raw json.RawMessage
	if v, ok := values["torrent_added"]; ok {
		raw = v
	} else if v, ok := values["torrent_duplicate"]; ok {
		raw = v
	} else {
		out.Outcome = "unknown"
		return out, failure("iyuu_add", "missing_added_object")
	}
	var row map[string]any
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	if d.Decode(&row) != nil || row == nil {
		out.Outcome = "unknown"
		return out, failure("iyuu_add", "invalid_added_object")
	}
	hash, _ := row["hash_string"].(string)
	if hash == "" {
		hash, _ = row["hashString"].(string)
	}
	if _, e := c.client.validHash(hash); e != nil {
		out.Outcome = "unknown"
		return out, failure("iyuu_add", "invalid_added_identity")
	}
	out.Hash = hash
	out.SourceTruthy = true
	out.Accepted = true
	out.Outcome = "confirmed"
	return out, nil
}

// SeedRescueDownloading preserves the original qB filter plus tag selection;
// unlike the application's download page this does not filter by its task tag.
func (c *IYUUClient) SeedRescueDownloading(ctx context.Context, tag string) (json.RawMessage, error) {
	ctx = mutationReadOnly(ctx)
	if tag == "" || strings.ContainsAny(tag, "\x00\r\n") {
		return nil, failure("seed_rescue_tag", "invalid")
	}
	if e := c.client.nativeLock(ctx); e != nil {
		return nil, e
	}
	defer c.client.mu.Unlock()
	if c.client.config.Kind != "qbittorrent" {
		return nil, failure("seed_rescue_downloading", "unsupported_kind")
	}
	form := url.Values{"filter": {"downloading"}, "tag": {tag}}
	raw, e := c.client.qb(ctx, http.MethodPost, "torrents/info", form.Encode(), "application/x-www-form-urlencoded")
	if e != nil {
		return nil, e
	}
	if _, e = nativeObjectList(raw, "seed_rescue_downloading"); e != nil {
		return nil, e
	}
	return raw, nil
}

// SeedRescueConnected performs the original connection role without listing
// every torrent merely to ask whether a downloader service is connected.
func (c *IYUUClient) SeedRescueConnected(ctx context.Context) error {
	ctx = mutationReadOnly(ctx)
	if e := c.client.nativeLock(ctx); e != nil {
		return e
	}
	defer c.client.mu.Unlock()
	if c.client.config.Kind == "qbittorrent" {
		_, e := c.webVersionLocked(ctx)
		return e
	}
	return c.client.trConnect(ctx)
}
