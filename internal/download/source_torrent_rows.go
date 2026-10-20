package download

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
)

// SourceTorrentRows is a private-source read projection, never the public Task.
// Fields retain daemon key presence and numeric text for the original module
// builder. It deliberately does not apply public-page ownership or state rules.
type SourceTorrentRows struct {
	Kind string
	Rows []map[string]any
}

func (c *Client) SourceDownloadingRows(ctx context.Context) (SourceTorrentRows, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := SourceTorrentRows{Kind: c.config.Kind, Rows: []map[string]any{}}
	var raw []byte
	if c.config.Kind == "qbittorrent" {
		var e error
		raw, e = c.qb(ctx, http.MethodGet, "torrents/info?filter=downloading", "", "")
		if e != nil {
			return out, e
		}
	} else {
		if e := c.trConnect(ctx); e != nil {
			return out, e
		}
		// Exact installed _trarg order, including its repeated speed fields. Native
		// JSON-RPC4.1 uses snake names; legacy RPC retains the original field names.
		fields := []string{"id", "name", "status", "labels", "hashString", "totalSize", "percentDone", "addedDate", "trackerList", "trackerStats", "leftUntilDone", "rateDownload", "rateUpload", "recheckProgress", "rateDownload", "rateUpload", "peersGettingFromUs", "peersSendingToUs", "uploadRatio", "uploadedEver", "downloadedEver", "downloadDir", "error", "errorString", "doneDate", "queuePosition", "activityDate", "trackers"}
		if c.trDialect != "legacy" {
			for i, k := range fields {
				if n, ok := sourceTRSnake[k]; ok {
					fields[i] = n
				}
			}
		}
		values, e := c.trRPC(ctx, "torrent_get", map[string]any{"fields": fields}, c.trDialect)
		if e != nil {
			return out, e
		}
		raw = values["torrents"]
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if dec.Decode(&out.Rows) != nil || out.Rows == nil || len(out.Rows) > 10000 {
		return out, failure("source_list", "invalid_task_list")
	}
	var trailing any
	if dec.Decode(&trailing) != io.EOF {
		return out, failure("source_list", "invalid_task_list")
	}
	if c.config.Kind == "transmission" {
		for i, row := range out.Rows {
			normalized := map[string]any{}
			for k, v := range row {
				if camel, ok := sourceTRCamel[k]; ok {
					k = camel
				}
				normalized[k] = v
			}
			out.Rows[i] = normalized
		}
	}
	return out, nil
}

var sourceTRSnake = map[string]string{"hashString": "hash_string", "totalSize": "total_size", "percentDone": "percent_done", "addedDate": "added_date", "trackerList": "tracker_list", "trackerStats": "tracker_stats", "leftUntilDone": "left_until_done", "rateDownload": "rate_download", "rateUpload": "rate_upload", "recheckProgress": "recheck_progress", "peersGettingFromUs": "peers_getting_from_us", "peersSendingToUs": "peers_sending_to_us", "uploadRatio": "upload_ratio", "uploadedEver": "uploaded_ever", "downloadedEver": "downloaded_ever", "downloadDir": "download_dir", "errorString": "error_string", "doneDate": "done_date", "queuePosition": "queue_position", "activityDate": "activity_date", "downloadLimit": "download_limit", "uploadLimit": "upload_limit", "seedRatioLimit": "seed_ratio_limit", "seedIdleLimit": "seed_idle_limit", "sizeWhenDone": "size_when_done"}
var sourceTRCamel = func() map[string]string {
	r := map[string]string{}
	for k, v := range sourceTRSnake {
		r[v] = k
	}
	return r
}()
