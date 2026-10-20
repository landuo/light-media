package download

// Fixed operations used by the original downloader modules after adding an
// episode-selected torrent in the paused state. Ownership and source receipts
// are enforced by the acquisition host; these methods retain the configured
// Client's origin, authentication, dialect and transport boundaries.
import (
	"context"
	"encoding/json"
	"math/big"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

type SourceEpisodeFile struct {
	ID   json.Number
	Name string
}

func sourceEpisodeID(v any) (json.Number, bool) {
	n, ok := v.(json.Number)
	if !ok {
		return "", false
	}
	i, ok := new(big.Int).SetString(n.String(), 10)
	return n, ok && i.Sign() >= 0
}

// SourceEpisodeFiles preserves qBittorrent-api's index -> id alias (including
// the enumerate fallback when index is absent). Transmission's get_files uses
// the original zip(files, priorities, wanted), whose length may be truncated.
func (c *Client) SourceEpisodeFiles(ctx context.Context, selector string) ([]SourceEpisodeFile, error) {
	hash, e := c.validHash(selector)
	if e != nil {
		return nil, e
	}
	if e = c.nativeLock(ctx); e != nil {
		return nil, e
	}
	defer c.mu.Unlock()
	if c.config.Kind == "qbittorrent" {
		body, e := c.qb(ctx, http.MethodPost, "torrents/files", url.Values{"hash": {hash}}.Encode(), "application/x-www-form-urlencoded")
		if e != nil {
			return nil, e
		}
		rows, e := nativeObjectList(body, "episode_files")
		if e != nil {
			return nil, e
		}
		out := make([]SourceEpisodeFile, 0, len(rows))
		for i, row := range rows {
			index, exists := row["index"]
			if !exists {
				index = json.Number(strconv.Itoa(i))
			}
			id, valid := sourceEpisodeID(index)
			name, stringName := row["name"].(string)
			if !valid || !stringName {
				return nil, failure("episode_files", "invalid_file")
			}
			out = append(out, SourceEpisodeFile{id, name})
		}
		return out, nil
	}
	if e = c.trConnect(ctx); e != nil {
		return nil, e
	}
	values, e := c.trRPC(ctx, "torrent_get", map[string]any{"ids": []string{hash}, "fields": []string{"id", "hash_string", "files", "priorities", "wanted"}}, c.trDialect)
	if e != nil {
		return nil, e
	}
	rows, e := nativeObjectList(values["torrents"], "episode_files")
	if e != nil || len(rows) != 1 || rows[0]["hash_string"] != hash {
		return nil, failure("episode_files", "invalid_task")
	}
	files, okFiles := rows[0]["files"].([]any)
	priorities, okPriorities := rows[0]["priorities"].([]any)
	wanted, okWanted := rows[0]["wanted"].([]any)
	if !okFiles || !okPriorities || !okWanted {
		return nil, failure("episode_files", "invalid_file_list")
	}
	n := min(len(files), len(priorities), len(wanted))
	out := make([]SourceEpisodeFile, 0, n)
	for i := 0; i < n; i++ {
		file, ok := files[i].(map[string]any)
		name, stringName := file["name"].(string)
		priority, integerPriority := priorities[i].(json.Number)
		if !ok || !stringName || !integerPriority || priority != "-1" && priority != "0" && priority != "1" {
			return nil, failure("episode_files", "invalid_file")
		}
		out = append(out, SourceEpisodeFile{json.Number(strconv.Itoa(i)), name})
	}
	return out, nil
}

// SourceEpisodeFileSelection returns the actual protocol acknowledgment. The
// original module ignores False from these setters and still attempts Start;
// its caller must preserve that behavior separately from native uncertainty.
func (c *Client) SourceEpisodeFileSelection(ctx context.Context, selector string, ids []json.Number, wanted bool) (bool, error) {
	hash, e := c.validHash(selector)
	if e != nil {
		return false, beforeMutation(e)
	}
	for _, id := range ids {
		if _, ok := sourceEpisodeID(id); !ok {
			return false, beforeMutation(failure("episode_selection", "invalid_file_id"))
		}
	}
	if e = c.nativeLock(ctx); e != nil {
		return false, beforeMutation(e)
	}
	defer c.mu.Unlock()
	if c.config.Kind == "qbittorrent" {
		if len(ids) == 0 {
			return false, nil
		}
		values := make([]string, len(ids))
		for i, id := range ids {
			values[i] = id.String()
		}
		priority := "0"
		if wanted {
			priority = "1"
		}
		e = c.sourceQBMutation(ctx, "torrents/filePrio", url.Values{"hash": {hash}, "id": {strings.Join(values, "|")}, "priority": {priority}})
		return e == nil, e
	}
	if e = c.trConnect(ctx); e != nil {
		return false, beforeMutation(e)
	}
	key := "files_unwanted"
	if wanted {
		key = "files_wanted"
	}
	if c.trDialect == "legacy" {
		key = strings.ReplaceAll(key, "_", "-")
	}
	if ids == nil {
		ids = []json.Number{}
	}
	e = c.sourceTRMutation(ctx, "torrent_set", map[string]any{"ids": []string{hash}, key: ids})
	return e == nil, e
}

func (c *Client) SourceStartEpisodeDownload(ctx context.Context, selector string, force bool) (bool, error) {
	hash, e := c.validHash(selector)
	if e != nil {
		return false, beforeMutation(e)
	}
	if e = c.nativeLock(ctx); e != nil {
		return false, beforeMutation(e)
	}
	defer c.mu.Unlock()
	if c.config.Kind == "qbittorrent" {
		endpoint := "torrents/start"
		args := url.Values{"hashes": {hash}}
		if force {
			endpoint = "torrents/setForceStart"
			args.Set("value", "true")
		}
		e = c.sourceQBMutation(ctx, endpoint, args)
		return e == nil, e
	}
	if e = c.trConnect(ctx); e != nil {
		return false, beforeMutation(e)
	}
	e = c.sourceTRMutation(ctx, "torrent_start", map[string]any{"ids": []string{hash}})
	return e == nil, e
}
