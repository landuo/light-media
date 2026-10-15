package download

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
)

func (c *Client) trConnect(ctx context.Context) error {
	if c.trDialect != "" {
		return nil
	}
	// Protocol negotiation is read-only. Never retry a mutation in another dialect.
	if result, err := c.trRPC(ctx, "session_get", map[string]any{"fields": []string{"version"}}, "jsonrpc"); err == nil {
		var version string
		if json.Unmarshal(result["version"], &version) != nil || version == "" {
			return failure("connect", "missing_version")
		}
		c.trDialect = "jsonrpc"
		return nil
	} else if e, ok := err.(*Error); !ok || (e.Code != "rpc_rejected" && e.Code != "legacy_response") {
		return err
	}
	result, err := c.trRPC(ctx, "session_get", map[string]any{"fields": []string{"version"}}, "legacy")
	if err != nil {
		return err
	}
	var version string
	if json.Unmarshal(result["version"], &version) != nil || version == "" {
		return failure("connect", "missing_version")
	}
	c.trDialect = "legacy"
	return nil
}
func (c *Client) trRPC(ctx context.Context, method string, args map[string]any, dialect string) (map[string]json.RawMessage, error) {
	c.requestID++
	id := c.requestID
	var request any
	if dialect == "legacy" {
		request = map[string]any{"method": strings.ReplaceAll(method, "_", "-"), "arguments": legacyArgs(args), "tag": id}
	} else {
		request = map[string]any{"jsonrpc": "2.0", "method": method, "params": args, "id": id}
	}
	payload, err := json.Marshal(request)
	if err != nil {
		return nil, failure("rpc", "invalid_arguments")
	}
	var body []byte
	for attempt := 0; attempt < 3; attempt++ {
		var header http.Header
		var status int
		body, header, status, err = c.request(ctx, http.MethodPost, c.base, payload, "application/json", c.trSession)
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
		if err := decode(body, &envelope); err != nil {
			return nil, err
		}
		if dialect == "legacy" {
			var result string
			var tag uint64
			if json.Unmarshal(envelope["tag"], &tag) != nil || tag != id {
				return nil, failure("rpc", "response_id_mismatch")
			}
			if json.Unmarshal(envelope["result"], &result) != nil || result != "success" {
				return nil, failure("rpc", "rpc_rejected")
			}
			var values map[string]json.RawMessage
			if json.Unmarshal(envelope["arguments"], &values) != nil || values == nil {
				return nil, failure("rpc", "invalid_arguments_response")
			}
			return normalizeLegacy(values), nil
		}
		// Old servers can emit their bespoke envelope for an unknown session_get.
		if _, ok := envelope["arguments"]; ok {
			return nil, failure("rpc", "legacy_response")
		}
		var protocol string
		var responseID uint64
		if json.Unmarshal(envelope["jsonrpc"], &protocol) != nil || protocol != "2.0" || json.Unmarshal(envelope["id"], &responseID) != nil || responseID != id {
			return nil, failure("rpc", "response_id_mismatch")
		}
		if e, ok := envelope["error"]; ok && string(e) != "null" {
			return nil, failure("rpc", "rpc_rejected")
		}
		var values map[string]json.RawMessage
		if json.Unmarshal(envelope["result"], &values) != nil || values == nil {
			return nil, failure("rpc", "invalid_result")
		}
		return values, nil
	}
	return nil, failure("rpc", "session_handshake_limit")
}

var oldKeys = map[string]string{
	"download_dir": "download-dir", "delete_local_data": "delete-local-data",
	"hash_string": "hashString", "total_size": "totalSize", "percent_done": "percentDone", "left_until_done": "leftUntilDone", "upload_ratio": "uploadRatio", "added_date": "addedDate", "rate_download": "rateDownload", "rate_upload": "rateUpload", "download_dir_field": "downloadDir", "bytes_completed": "bytesCompleted",
}

func legacyArgs(args map[string]any) map[string]any {
	out := make(map[string]any, len(args))
	for k, v := range args {
		key := k
		if old, ok := oldKeys[k]; ok {
			key = old
		}
		if k == "fields" {
			if fields, ok := v.([]string); ok {
				converted := make([]string, len(fields))
				for i, f := range fields {
					if f == "download_dir" {
						converted[i] = "downloadDir"
					} else if old, ok := oldKeys[f]; ok {
						converted[i] = old
					} else {
						converted[i] = f
					}
				}
				v = converted
			}
		}
		out[key] = v
	}
	return out
}
func normalizeLegacy(raw map[string]json.RawMessage) map[string]json.RawMessage {
	// Normalize only known keys recursively, keeping payloads out of errors/logs.
	var normalize func(any) any
	keys := map[string]string{"torrent-added": "torrent_added", "torrent-duplicate": "torrent_duplicate", "hashString": "hash_string", "totalSize": "total_size", "percentDone": "percent_done", "leftUntilDone": "left_until_done", "uploadRatio": "upload_ratio", "addedDate": "added_date", "rateDownload": "rate_download", "rateUpload": "rate_upload", "downloadDir": "download_dir", "bytesCompleted": "bytes_completed"}
	normalize = func(v any) any {
		switch x := v.(type) {
		case map[string]any:
			r := map[string]any{}
			for k, item := range x {
				key := k
				if modern, ok := keys[k]; ok {
					key = modern
				}
				r[key] = normalize(item)
			}
			return r
		case []any:
			r := make([]any, len(x))
			for i, item := range x {
				r[i] = normalize(item)
			}
			return r
		default:
			return v
		}
	}
	b, _ := json.Marshal(raw)
	var values any
	d := json.NewDecoder(strings.NewReader(string(b)))
	d.UseNumber()
	_ = d.Decode(&values)
	b, _ = json.Marshal(normalize(values))
	var out map[string]json.RawMessage
	_ = json.Unmarshal(b, &out)
	return out
}

type trTask struct {
	Hash        string   `json:"hash_string"`
	Name        string   `json:"name"`
	Status      int      `json:"status"`
	DownloadDir string   `json:"download_dir"`
	TotalSize   int64    `json:"total_size"`
	Progress    float64  `json:"percent_done"`
	Left        int64    `json:"left_until_done"`
	Ratio       float64  `json:"upload_ratio"`
	Added       int64    `json:"added_date"`
	ETA         int64    `json:"eta"`
	DLSpeed     int64    `json:"rate_download"`
	UPSpeed     int64    `json:"rate_upload"`
	Error       int      `json:"error"`
	Labels      []string `json:"labels"`
}

func (c *Client) trList(ctx context.Context, hash string) ([]Task, error) {
	args := map[string]any{"fields": []string{"hash_string", "name", "status", "download_dir", "total_size", "percent_done", "left_until_done", "upload_ratio", "added_date", "eta", "rate_download", "rate_upload", "error", "labels"}}
	if hash != "" {
		args["ids"] = []string{hash}
	}
	values, err := c.trRPC(ctx, "torrent_get", args, c.trDialect)
	if err != nil {
		return nil, err
	}
	var raw []trTask
	if json.Unmarshal(values["torrents"], &raw) != nil || raw == nil || len(raw) > 10000 {
		return nil, failure("list", "invalid_task_list")
	}
	out := make([]Task, 0, len(raw))
	seen := map[string]bool{}
	for _, v := range raw {
		h, err := c.validHash(v.Hash)
		if err != nil || seen[h] || (hash != "" && h != hash) || !fraction(v.Progress) || v.TotalSize < 0 || v.Left < 0 || v.Left > v.TotalSize || v.Status < 0 || v.Status > 6 {
			return nil, failure("list", "invalid_task")
		}
		seen[h] = true
		status := []string{"paused", "checking", "checking", "queued", "downloading", "queued", "seeding"}[v.Status]
		if v.Error != 0 {
			status = "error"
		}
		tags := v.Labels
		if tags == nil {
			tags = []string{}
		}
		out = append(out, Task{ID: c.config.ID + ":" + h, Hash: h, Name: v.Name, InstanceID: c.config.ID, Status: status, DownloadDir: v.DownloadDir, Progress: v.Progress, TotalSize: v.TotalSize, Completed: v.TotalSize - v.Left, Ratio: v.Ratio, ETA: v.ETA, AddedAt: v.Added, DownloadSpeed: v.DLSpeed, UploadSpeed: v.UPSpeed, Tags: tags})
	}
	return out, nil
}
func (c *Client) trExists(ctx context.Context, hash string) error {
	tasks, err := c.trList(ctx, hash)
	if err != nil {
		return err
	}
	if len(tasks) != 1 {
		return ErrNotFound
	}
	return nil
}
func (c *Client) trFiles(ctx context.Context, hash string) ([]File, error) {
	values, err := c.trRPC(ctx, "torrent_get", map[string]any{"ids": []string{hash}, "fields": []string{"hash_string", "files"}}, c.trDialect)
	if err != nil {
		return nil, err
	}
	var raw []struct {
		Hash  string `json:"hash_string"`
		Files []struct {
			Name      string `json:"name"`
			Size      int64  `json:"length"`
			Completed int64  `json:"bytes_completed"`
		} `json:"files"`
	}
	if json.Unmarshal(values["torrents"], &raw) != nil || len(raw) > 1 {
		return nil, failure("files", "invalid_task_response")
	}
	if len(raw) == 0 {
		return nil, ErrNotFound
	}
	if strings.ToLower(raw[0].Hash) != hash || raw[0].Files == nil || len(raw[0].Files) > 10000 {
		return nil, failure("files", "invalid_file_list")
	}
	out := make([]File, 0, len(raw[0].Files))
	seen := map[string]bool{}
	for _, v := range raw[0].Files {
		if !relativePath(v.Name) || seen[v.Name] || v.Size < 0 || v.Completed < 0 || v.Completed > v.Size {
			return nil, failure("files", "unsafe_or_invalid_file")
		}
		seen[v.Name] = true
		progress := float64(1)
		if v.Size > 0 {
			progress = float64(v.Completed) / float64(v.Size)
		}
		out = append(out, File{Path: v.Name, Size: v.Size, Completed: v.Completed, Progress: progress})
	}
	return out, nil
}
func (c *Client) trAdd(ctx context.Context, in AddRequest, hash string) (AddResult, error) {
	out := AddResult{Hash: hash}
	tasks, err := c.trList(ctx, hash)
	if err != nil {
		return out, err
	}
	if len(tasks) == 1 {
		out.Duplicate = true
		return out, nil
	}
	args := map[string]any{"metainfo": base64.StdEncoding.EncodeToString(in.Torrent), "download_dir": in.SavePath, "paused": in.Paused, "labels": in.Tags}
	if in.Tags == nil {
		args["labels"] = []string{}
	}
	values, err := c.trRPC(ctx, "torrent_add", args, c.trDialect)
	if err != nil {
		return out, err
	}
	raw, ok := values["torrent_added"]
	if duplicate, present := values["torrent_duplicate"]; present {
		raw = duplicate
		ok = true
		out.Duplicate = true
	}
	var added struct {
		Hash string `json:"hash_string"`
	}
	if !ok || json.Unmarshal(raw, &added) != nil || strings.ToLower(added.Hash) != hash {
		return out, failure("add", "invalid_added_identity")
	}
	return out, nil
}
func (c *Client) trControl(ctx context.Context, hash, action string) error {
	if err := c.trExists(ctx, hash); err != nil {
		return err
	}
	method := map[string]string{"start": "torrent_start", "pause": "torrent_stop", "verify": "torrent_verify", "remove_keep_data": "torrent_remove"}[action]
	args := map[string]any{"ids": []string{hash}}
	if action == "remove_keep_data" {
		args["delete_local_data"] = false
	}
	_, err := c.trRPC(ctx, method, args, c.trDialect)
	return err
}
