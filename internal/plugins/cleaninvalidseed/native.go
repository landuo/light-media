package cleaninvalidseed

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
)

// NativeCall is supplied by an authorized host and pinned downloader instance.
// There is no URL, credential lookup, default HTTP client or wildcard selection.
// The host owns authentication, cancellation, response limits and mutation guards.
// Paths below are actual daemon API operation names; data is JSON, not user logs.
type NativeCall func(context.Context, string, map[string]any) (json.RawMessage, error)
type NativeDownloader struct {
	Service Service
	Call    NativeCall
}

func nativeDecode(raw json.RawMessage, out any) error { return json.Unmarshal(raw, out) }

// List preserves native library fields instead of projecting download.Task.
// Failed RPC/list construction maps to the original wrapper's error=true tuple.
func (d NativeDownloader) List(ctx context.Context) ([]*Object, bool, error) {
	if d.Call == nil || (d.Service.Kind != "qbittorrent" && d.Service.Kind != "transmission") {
		return nil, false, source("ValueError", "native downloader adapter")
	}
	op := "qb.torrents_info"
	args := map[string]any{}
	if d.Service.Kind == "transmission" {
		op = "tr.torrent_get"
		args["arguments"] = append([]string(nil), transmissionFields...)
	}
	raw, e := d.Call(ctx, op, args)
	if e != nil {
		return []*Object{}, true, nil
	}
	var values []map[string]any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if decoder.Decode(&values) != nil || decoder.Decode(new(any)) != io.EOF || values == nil {
		return []*Object{}, true, nil
	}
	out := []*Object{}
	for _, fields := range values {
		if fields == nil {
			return []*Object{}, true, nil
		}
		if d.Service.Kind == "qbittorrent" {
			f := fields
			o := nativeQB(f, func() (any, error) {
				if _, ok := f["hash"]; !ok {
					return nil, source("KeyError", "native torrent hash")
				}
				raw, e := d.Call(ctx, "qb.torrents_trackers", map[string]any{"torrent_hash": f["hash"]})
				if e != nil {
					return nil, e
				}
				var v any
				dec := json.NewDecoder(bytes.NewReader(raw))
				dec.UseNumber()
				if e = dec.Decode(&v); e != nil {
					return nil, e
				}
				return v, nil
			})
			out = append(out, o)
		} else {
			o, e := nativeTR(fields)
			if e != nil {
				return []*Object{}, true, nil
			}
			out = append(out, o)
		}
	}
	return out, false, nil
}
func (d NativeDownloader) SetTags(ctx context.Context, hash any, tags []any) (any, error) {
	if d.Call == nil || (d.Service.Kind != "qbittorrent" && d.Service.Kind != "transmission") {
		return nil, source("ValueError", "native downloader adapter")
	}
	if d.Service.Kind == "qbittorrent" {
		_, _ = d.Call(ctx, "qb.torrents_add_tags", map[string]any{"torrent_hashes": hash, "tags": tags})
		return nil, nil
	}
	if !Truth(hash) || len(tags) == 0 {
		return false, nil
	}
	unique := []any{}
	for _, v := range tags {
		switch v.(type) {
		case []any, []string, map[string]any:
			return false, nil
		}
		found := false
		for _, w := range unique {
			if eq(v, w) {
				found = true
				break
			}
		}
		if !found {
			unique = append(unique, v)
		}
	}
	_, e := d.Call(ctx, "tr.change_torrent", map[string]any{"ids": hash, "labels": unique})
	return e == nil, nil
}
func (d NativeDownloader) Delete(ctx context.Context, hash any, deleteFiles bool) (any, error) {
	if d.Call == nil || (d.Service.Kind != "qbittorrent" && d.Service.Kind != "transmission") {
		return nil, source("ValueError", "native downloader adapter")
	}
	if !Truth(hash) {
		return false, nil
	}
	op := "qb.torrents_delete"
	args := map[string]any{"torrent_hashes": hash, "delete_files": deleteFiles}
	if d.Service.Kind == "transmission" {
		op = "tr.remove_torrent"
		args = map[string]any{"ids": hash, "delete_data": deleteFiles}
	}
	_, e := d.Call(ctx, op, args)
	return e == nil, nil
}

var transmissionFields = []string{"id", "name", "status", "labels", "hashString", "totalSize", "percentDone", "addedDate", "trackerList", "trackerStats", "leftUntilDone", "rateDownload", "rateUpload", "recheckProgress", "rateDownload", "rateUpload", "peersGettingFromUs", "peersSendingToUs", "uploadRatio", "uploadedEver", "downloadedEver", "downloadDir", "error", "errorString", "doneDate", "queuePosition", "activityDate", "trackers"}

func nativeQB(fields map[string]any, trackers func() (any, error)) *Object {
	o := &Object{Attrs: fields, Mapping: true}
	o.Accessor = func(k string) (any, bool, error) {
		switch k {
		case "trackers":
			v, e := trackers()
			return v, true, e
		case "state_enum":
			v, ok := fields["state"]
			if !ok {
				return nil, false, nil
			}
			paused := v == "pausedUP" || v == "pausedDL" || v == "stoppedUP" || v == "stoppedDL"
			return &Object{Attrs: map[string]any{"is_paused": paused}}, true, nil
		}
		return nil, false, nil
	}
	return o
}
func nativeTR(fields map[string]any) (*Object, error) {
	if _, ok := fields["id"]; !ok {
		return nil, source("ValueError", "Torrent requires id")
	}
	o := &Object{Attrs: map[string]any{}, Faults: map[string]string{}}
	for attr, key := range map[string]string{"name": "name", "total_size": "totalSize", "download_dir": "downloadDir", "hashString": "hashString", "hash_string": "hashString", "error": "error", "error_string": "errorString", "labels": "labels"} {
		if v, ok := fields[key]; ok {
			o.Attrs[attr] = v
		} else {
			o.Faults[attr] = "KeyError"
		}
	}
	if v, ok := fields["status"]; ok {
		statuses := []string{"stopped", "check pending", "checking", "download pending", "downloading", "seed pending", "seeding"}
		found := false
		for i, s := range statuses {
			if eq(v, i) {
				o.Attrs["status"] = s
				found = true
			}
		}
		if !found {
			o.Faults["status"] = "KeyError"
		}
	} else {
		o.Faults["status"] = "KeyError"
	}
	for attr, key := range map[string]string{"trackers": "trackers", "tracker_stats": "trackerStats"} {
		v, ok := fields[key]
		if !ok {
			o.Faults[attr] = "KeyError"
			continue
		}
		a, e := listValue(v)
		if e != nil {
			o.Faults[attr] = "TypeError"
			continue
		}
		out := []any{}
		bad := false
		for _, v := range a {
			f, ok := v.(map[string]any)
			if !ok {
				bad = true
				break
			}
			out = append(out, nativeTRTracker(f, attr == "tracker_stats"))
		}
		if bad {
			o.Faults[attr] = "TypeError"
		} else {
			o.Attrs[attr] = out
		}
	}
	return o, nil
}
func nativeTRTracker(f map[string]any, stats bool) *Object {
	o := &Object{Attrs: map[string]any{}, Faults: map[string]string{}}
	mapping := map[string]string{"announce": "announce", "tier": "tier"}
	if stats {
		mapping["last_announce_result"] = "lastAnnounceResult"
		mapping["last_announce_succeeded"] = "lastAnnounceSucceeded"
	}
	for attr, key := range mapping {
		if v, ok := f[key]; ok {
			o.Attrs[attr] = v
		} else {
			o.Faults[attr] = "KeyError"
		}
	}
	text := fmt.Sprintf("<%s>", "native Tracker")
	o.Text = &text
	return o
}
