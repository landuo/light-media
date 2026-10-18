package download

// These fixed native operations serve the installed CleanInvalidSeed 2.1
// wrapper. They are separate from Task/ListDownloading and do not change the
// download page's ownership or downloading-only scope. The configured Client
// continues to own all credentials, origin, transport, CSRF and dialect state.
import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"time"
	"unicode/utf8"
)

// NativeTorrents returns complete daemon objects, never the Task projection.
// qB bytes are retained verbatim after structural validation. Transmission
// requests the installed wrapper's complete field list and returns its legacy
// field names, adapting known 4.1 snake_case keys only in the torrent and
// trackerStats domains. Unknown keys/values and JSON numbers remain intact.
func (c *Client) NativeTorrents(ctx context.Context) (json.RawMessage, error) {
	return c.nativeTorrentSnapshots(ctx, "")
}

// SourceTorrentSnapshots reads complete source fields for exactly one original
// hash selector. An empty selector cannot widen the core deletion consumer.
func (c *Client) SourceTorrentSnapshots(ctx context.Context, hash string) (json.RawMessage, error) {
	hash, err := c.validHash(hash)
	if err != nil {
		return nil, err
	}
	return c.nativeTorrentSnapshots(ctx, hash)
}

func (c *Client) nativeTorrentSnapshots(ctx context.Context, hash string) (json.RawMessage, error) {
	if err := c.nativeLock(ctx); err != nil {
		return nil, err
	}
	defer c.mu.Unlock()
	if c.config.Kind == "qbittorrent" {
		endpoint := "torrents/info"
		if hash != "" {
			endpoint += "?" + url.Values{"hashes": {hash}}.Encode()
		}
		body, err := c.qb(ctx, http.MethodGet, endpoint, "", "")
		if err != nil {
			return nil, err
		}
		if _, err = nativeObjectList(body, "native_torrents"); err != nil {
			return nil, err
		}
		return append(json.RawMessage(nil), body...), nil
	}
	if err := c.trConnect(ctx); err != nil {
		return nil, err
	}
	body, err := c.trNativeTorrentSnapshots(ctx, hash)
	if err != nil {
		return nil, err
	}
	rows, err := nativeObjectList(body, "native_torrents")
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		if err = nativeAliasKeys(row, nativeTRKeys); err != nil {
			return nil, err
		}
		// Only trackerStats has changed field spellings consumed by the Python
		// library. Never recursively rewrite arbitrary unknown extension objects.
		if stats, ok := row["trackerStats"].([]any); ok {
			for _, v := range stats {
				if tracker, ok := v.(map[string]any); ok {
					if err = nativeAliasKeys(tracker, nativeTRTrackerKeys); err != nil {
						return nil, err
					}
				}
			}
		}
	}
	out, err := json.Marshal(rows)
	if err != nil {
		return nil, failure("native_torrents", "invalid_json")
	}
	return out, nil
}

// NativeTrackers is intentionally qB-only. Each call performs its own read;
// the original library's lazy tracker getter must not become a cached snapshot.
func (c *Client) NativeTrackers(ctx context.Context, hash string) (json.RawMessage, error) {
	hash, err := c.validHash(hash)
	if err != nil {
		return nil, err
	}
	if c.config.Kind != "qbittorrent" {
		return nil, failure("native_trackers", "unsupported_kind")
	}
	if err = c.nativeLock(ctx); err != nil {
		return nil, err
	}
	defer c.mu.Unlock()
	body, err := c.qb(ctx, http.MethodGet, "torrents/trackers?"+url.Values{"hash": {hash}}.Encode(), "", "")
	if err != nil {
		return nil, err
	}
	if _, err = nativeObjectList(body, "native_trackers"); err != nil {
		return nil, err
	}
	return append(json.RawMessage(nil), body...), nil
}

// SetTorrentTags appends qB tags and replaces Transmission labels, as the
// original daemon methods do. Exactly one validated hash can be selected.
// A successful return acknowledges the protocol operation, not proof that a
// torrent still exists or that a later read will show the requested labels.
func (c *Client) SetTorrentTags(ctx context.Context, hash string, tags []string) error {
	hash, err := c.validHash(hash)
	if err != nil {
		return beforeMutation(err)
	}
	size := 0
	for _, tag := range tags {
		if !utf8.ValidString(tag) || len(tag) > maxResponse-size {
			return beforeMutation(failure("native_tags", "invalid_tags"))
		}
		size += len(tag)
	}
	// The native wrapper may preserve existing labels far beyond Add's small
	// tag list. Do not reuse validTags, trim, split, deduplicate, or cap at 32.
	labelBytes, err := json.Marshal(tags)
	if err != nil || len(labelBytes) > maxResponse {
		return beforeMutation(failure("native_tags", "invalid_tags"))
	}
	if err = c.nativeLock(ctx); err != nil {
		return beforeMutation(err)
	}
	defer c.mu.Unlock()
	if c.config.Kind == "qbittorrent" {
		form := url.Values{"hashes": {hash}, "tags": {strings.Join(tags, ",")}}.Encode()
		if len(form) > maxResponse {
			return beforeMutation(failure("native_tags", "invalid_tags"))
		}
		// This is the same initial authentication performed by qb. Move only
		// its pre-request boundary here, without adding a target existence
		// check or changing the daemon's append semantics.
		if !c.loggedIn && (c.config.Username != "" || c.config.Password != "") {
			if err = c.qbLogin(ctx); err != nil {
				return beforeMutation(err)
			}
		}
		body, err := c.qb(ctx, http.MethodPost, "torrents/addTags", form, "application/x-www-form-urlencoded")
		if err != nil {
			return err
		}
		if v := strings.TrimSpace(string(body)); v != "" && v != "Ok." {
			return failure("native_tags", "unexpected_response")
		}
		return nil
	}
	if err = c.trConnect(ctx); err != nil {
		return beforeMutation(err)
	}
	labels := append([]string{}, tags...)
	_, err = c.trRPC(ctx, "torrent_set", map[string]any{"ids": []string{hash}, "labels": labels}, c.trDialect)
	return err
}

// Waiting for an existing Client operation must respect the caller's deadline
// too. No new mutex or authentication/session state is introduced.
func (c *Client) nativeLock(ctx context.Context) error {
	if ctx == nil {
		return failure("native", "invalid_context")
	}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if c.mu.TryLock() {
			if err := ctx.Err(); err != nil {
				c.mu.Unlock()
				return err
			}
			return nil
		}
		timer := time.NewTimer(5 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}
func nativeObjectList(body []byte, operation string) ([]map[string]any, error) {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	var rows []map[string]any
	if decoder.Decode(&rows) != nil || decoder.Decode(new(any)) != io.EOF || rows == nil {
		return nil, failure(operation, "invalid_object_list")
	}
	for _, row := range rows {
		if row == nil {
			return nil, failure(operation, "invalid_object_list")
		}
	}
	return rows, nil
}

// Original _trarg order (including the two repeated rate fields) is retained.
var nativeTRFields = []string{"id", "name", "status", "labels", "hashString", "totalSize", "percentDone", "addedDate", "trackerList", "trackerStats", "leftUntilDone", "rateDownload", "rateUpload", "recheckProgress", "rateDownload", "rateUpload", "peersGettingFromUs", "peersSendingToUs", "uploadRatio", "uploadedEver", "downloadedEver", "downloadDir", "error", "errorString", "doneDate", "queuePosition", "activityDate", "trackers"}
var nativeTRKeys = map[string]string{
	"hash_string": "hashString", "total_size": "totalSize", "percent_done": "percentDone", "added_date": "addedDate", "tracker_list": "trackerList", "tracker_stats": "trackerStats", "left_until_done": "leftUntilDone", "rate_download": "rateDownload", "rate_upload": "rateUpload", "recheck_progress": "recheckProgress", "peers_getting_from_us": "peersGettingFromUs", "peers_sending_to_us": "peersSendingToUs", "upload_ratio": "uploadRatio", "uploaded_ever": "uploadedEver", "downloaded_ever": "downloadedEver", "download_dir": "downloadDir", "error_string": "errorString", "done_date": "doneDate", "queue_position": "queuePosition", "activity_date": "activityDate",
}
var nativeTRTrackerKeys = map[string]string{
	"announce_state": "announceState", "download_count": "downloadCount", "downloader_count": "downloaderCount", "has_announced": "hasAnnounced", "has_scraped": "hasScraped", "is_backup": "isBackup", "last_announce_peer_count": "lastAnnouncePeerCount", "last_announce_result": "lastAnnounceResult", "last_announce_start_time": "lastAnnounceStartTime", "last_announce_succeeded": "lastAnnounceSucceeded", "last_announce_time": "lastAnnounceTime", "last_announce_timed_out": "lastAnnounceTimedOut", "last_scrape_result": "lastScrapeResult", "last_scrape_start_time": "lastScrapeStartTime", "last_scrape_succeeded": "lastScrapeSucceeded", "last_scrape_time": "lastScrapeTime", "last_scrape_timed_out": "lastScrapeTimedOut", "leecher_count": "leecherCount", "next_announce_time": "nextAnnounceTime", "next_scrape_time": "nextScrapeTime", "scrape_state": "scrapeState", "seeder_count": "seederCount",
}

func nativeAliasKeys(row map[string]any, aliases map[string]string) error {
	for modern, original := range aliases {
		if v, ok := row[modern]; ok {
			if old, present := row[original]; present && !reflect.DeepEqual(old, v) {
				return failure("native_torrents", "conflicting_field_alias")
			}
			row[original] = v
			delete(row, modern)
		}
	}
	return nil
}

// This is deliberately a fixed torrent_get reader, not a generic RPC API.
// trRPC's normalizeLegacy recursively rewrites unknown extension objects for
// Task consumers. Native objects need the raw response instead. Authentication,
// session state, request IDs, origin and the only retry (HTTP409 rejection) use
// the existing Client helpers and the same envelope checks.
func (c *Client) trNativeTorrents(ctx context.Context) (json.RawMessage, error) {
	return c.trNativeTorrentSnapshots(ctx, "")
}

func (c *Client) trNativeTorrentSnapshots(ctx context.Context, hash string) (json.RawMessage, error) {
	fields := append([]string(nil), nativeTRFields...)
	if c.trDialect == "jsonrpc" {
		for i, f := range fields {
			for modern, original := range nativeTRKeys {
				if f == original {
					fields[i] = modern
					break
				}
			}
		}
	}
	c.requestID++
	id := c.requestID
	args := map[string]any{"fields": fields}
	if hash != "" {
		args["ids"] = []string{hash}
	}
	var request any
	if c.trDialect == "legacy" {
		request = map[string]any{"method": "torrent-get", "arguments": args, "tag": id}
	} else {
		request = map[string]any{"jsonrpc": "2.0", "method": "torrent_get", "params": args, "id": id}
	}
	payload, err := json.Marshal(request)
	if err != nil {
		return nil, failure("native_torrents", "invalid_arguments")
	}
	for attempt := 0; attempt < 3; attempt++ {
		body, header, status, err := c.request(ctx, http.MethodPost, c.base, payload, "application/json", c.trSession)
		if err != nil {
			return nil, err
		}
		if status == http.StatusConflict {
			session := header.Get("X-Transmission-Session-Id")
			if session == "" || len(session) > 1024 || strings.ContainsAny(session, "\r\n\x00") {
				return nil, failure("rpc", "invalid_session_header")
			}
			c.trSession = session
			continue
		}
		if status != http.StatusOK {
			return nil, statusError(status)
		}
		var envelope map[string]json.RawMessage
		if err = decode(body, &envelope); err != nil {
			return nil, err
		}
		var values map[string]json.RawMessage
		if c.trDialect == "legacy" {
			var tag uint64
			var result string
			if json.Unmarshal(envelope["tag"], &tag) != nil || tag != id {
				return nil, failure("rpc", "response_id_mismatch")
			}
			if json.Unmarshal(envelope["result"], &result) != nil || result != "success" {
				return nil, failure("rpc", "rpc_rejected")
			}
			if json.Unmarshal(envelope["arguments"], &values) != nil || values == nil {
				return nil, failure("rpc", "invalid_arguments_response")
			}
		} else {
			var protocol string
			var responseID uint64
			if json.Unmarshal(envelope["jsonrpc"], &protocol) != nil || protocol != "2.0" || json.Unmarshal(envelope["id"], &responseID) != nil || responseID != id {
				return nil, failure("rpc", "response_id_mismatch")
			}
			if e, ok := envelope["error"]; ok && string(e) != "null" {
				return nil, failure("rpc", "rpc_rejected")
			}
			if json.Unmarshal(envelope["result"], &values) != nil || values == nil {
				return nil, failure("rpc", "invalid_result")
			}
		}
		raw, ok := values["torrents"]
		if !ok {
			return nil, failure("native_torrents", "missing_torrents")
		}
		return append(json.RawMessage(nil), raw...), nil
	}
	return nil, failure("rpc", "session_handshake_limit")
}
