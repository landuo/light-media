package download

import (
	"bytes"
	"context"
	"mime/multipart"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

func (c *Client) qbURL(endpoint string) *url.URL {
	u := *c.base
	u.Path += "/api/v2/" + endpoint
	if i := strings.Index(u.Path, "?"); i >= 0 {
		u.RawQuery = u.Path[i+1:]
		u.Path = u.Path[:i]
	}
	return &u
}
func (c *Client) qbLogin(ctx context.Context) error {
	if c.config.APIKey != "" {
		// Original qB API-key authentication never submits username/password or
		// silently falls back to a different credential after rejection.
		c.loggedIn = true
		return nil
	}
	v := url.Values{"username": {c.config.Username}, "password": {c.config.Password}}
	u := c.qbURL("auth/login")
	body, _, status, err := c.request(ctx, http.MethodPost, u, []byte(v.Encode()), "application/x-www-form-urlencoded", "")
	if err != nil {
		return err
	}
	if status != http.StatusOK {
		return statusError(status)
	}
	if strings.TrimSpace(string(body)) != "Ok." {
		return failure("login", "authentication_failed")
	}
	found := false
	for _, cookie := range c.http.Jar.Cookies(u) {
		if cookie.Name == "SID" && cookie.Value != "" {
			found = true
		}
	}
	if !found {
		return failure("login", "missing_session_cookie")
	}
	c.loggedIn = true
	return nil
}
func (c *Client) qb(ctx context.Context, method, endpoint, data, contentType string) ([]byte, error) {
	if !c.loggedIn && (c.config.Username != "" || c.config.Password != "") {
		if err := c.qbLogin(ctx); err != nil {
			return nil, err
		}
	}
	u := c.qbURL(endpoint)
	body, _, status, err := c.request(ctx, method, u, []byte(data), contentType, "")
	if err != nil {
		return nil, err
	}
	// Authentication rejection precedes execution; only this rejection is retried.
	if status == http.StatusForbidden || status == http.StatusUnauthorized {
		if c.config.APIKey != "" {
			return nil, statusError(status)
		}
		c.loggedIn = false
		if err := c.qbLogin(ctx); err != nil {
			return nil, err
		}
		body, _, status, err = c.request(ctx, method, u, []byte(data), contentType, "")
		if err != nil {
			return nil, err
		}
	}
	if status != http.StatusOK {
		return nil, statusError(status)
	}
	return body, nil
}

type qbTask struct {
	Hash      string  `json:"hash"`
	Name      string  `json:"name"`
	State     string  `json:"state"`
	SavePath  string  `json:"save_path"`
	Size      int64   `json:"size"`
	TotalSize int64   `json:"total_size"`
	Completed int64   `json:"completed"`
	Progress  float64 `json:"progress"`
	Ratio     float64 `json:"ratio"`
	ETA       int64   `json:"eta"`
	AddedOn   int64   `json:"added_on"`
	DLSpeed   int64   `json:"dlspeed"`
	UPSpeed   int64   `json:"upspeed"`
	Tags      string  `json:"tags"`
}

func (c *Client) qbList(ctx context.Context, hash string) ([]Task, error) {
	return c.qbListQuery(ctx, hash, "")
}

func (c *Client) qbListQuery(ctx context.Context, hash, filter string) ([]Task, error) {
	endpoint := "torrents/info"
	query := url.Values{}
	if hash != "" {
		query.Set("hashes", hash)
	}
	if filter != "" {
		query.Set("filter", filter)
	}
	if len(query) > 0 {
		endpoint += "?" + query.Encode()
	}
	body, err := c.qb(ctx, http.MethodGet, endpoint, "", "")
	if err != nil {
		return nil, err
	}
	var raw []qbTask
	if err := decode(body, &raw); err != nil {
		return nil, err
	}
	if raw == nil || len(raw) > 10000 {
		return nil, failure("list", "invalid_task_list")
	}
	out := make([]Task, 0, len(raw))
	seen := map[string]bool{}
	for _, v := range raw {
		// A finished task must never appear in the downloading page, even if
		// a compatible daemon incorrectly returns it for filter=downloading.
		if filter == "downloading" && (v.Progress >= 1 || qbStatus(v.State) == "seeding") {
			continue
		}
		h, err := c.validHash(v.Hash)
		if err != nil || seen[h] || (hash != "" && h != hash) || !fraction(v.Progress) || v.TotalSize < 0 || v.Size < 0 || v.Completed < 0 || v.Ratio < 0 {
			return nil, failure("list", "invalid_task")
		}
		seen[h] = true
		total := v.TotalSize
		if total == 0 {
			total = v.Size
		}
		if v.Completed > total {
			return nil, failure("list", "invalid_task_size")
		}
		tags := []string{}
		if v.Tags != "" {
			for _, t := range strings.Split(v.Tags, ",") {
				tags = append(tags, strings.TrimSpace(t))
			}
		}
		out = append(out, Task{ID: c.config.ID + ":" + h, Hash: h, Name: v.Name, InstanceID: c.config.ID, Progress: v.Progress, Status: qbStatus(v.State), DownloadDir: v.SavePath, TotalSize: total, Completed: v.Completed, Ratio: v.Ratio, ETA: v.ETA, AddedAt: v.AddedOn, DownloadSpeed: v.DLSpeed, UploadSpeed: v.UPSpeed, Tags: tags})
	}
	return out, nil
}
func qbStatus(state string) string {
	switch state {
	case "error", "missingFiles":
		return "error"
	case "pausedDL", "pausedUP", "stoppedDL", "stoppedUP":
		return "paused"
	case "checkingDL", "checkingUP", "checkingResumeData":
		return "checking"
	case "queuedDL", "queuedUP":
		return "queued"
	case "uploading", "stalledUP", "forcedUP":
		return "seeding"
	case "downloading", "stalledDL", "forcedDL", "metaDL", "forcedMetaDL", "allocating", "moving":
		return "downloading"
	default:
		return "unknown"
	}
}
func (c *Client) qbExists(ctx context.Context, hash string) error {
	tasks, err := c.qbList(ctx, hash)
	if err != nil {
		return err
	}
	if len(tasks) != 1 {
		return ErrNotFound
	}
	return nil
}
func (c *Client) qbFiles(ctx context.Context, hash string) ([]File, error) {
	if err := c.qbExists(ctx, hash); err != nil {
		return nil, err
	}
	body, err := c.qb(ctx, http.MethodGet, "torrents/files?"+url.Values{"hash": {hash}}.Encode(), "", "")
	if err != nil {
		return nil, err
	}
	var raw []struct {
		Name     string  `json:"name"`
		Size     int64   `json:"size"`
		Progress float64 `json:"progress"`
	}
	if err := decode(body, &raw); err != nil {
		return nil, err
	}
	if raw == nil || len(raw) > 10000 {
		return nil, failure("files", "invalid_file_list")
	}
	out := make([]File, 0, len(raw))
	seen := map[string]bool{}
	for _, v := range raw {
		if !relativePath(v.Name) || seen[v.Name] || v.Size < 0 || !fraction(v.Progress) {
			return nil, failure("files", "unsafe_or_invalid_file")
		}
		seen[v.Name] = true
		completed := int64(float64(v.Size) * v.Progress)
		if v.Progress == 1 {
			completed = v.Size
		}
		out = append(out, File{Path: v.Name, Size: v.Size, Completed: completed, Progress: v.Progress})
	}
	return out, nil
}
func (c *Client) qbFind(ctx context.Context, hashes []string) (string, error) {
	for _, hash := range hashes {
		tasks, err := c.qbList(ctx, hash)
		if err != nil {
			return "", err
		}
		if len(tasks) == 1 {
			return hash, nil
		}
	}
	return "", nil
}
func (c *Client) qbAdd(ctx context.Context, in AddRequest, hashes []string) (AddResult, error) {
	out := AddResult{Hash: hashes[0]}
	found, err := c.qbFind(ctx, hashes)
	if err != nil {
		return out, beforeMutation(err)
	}
	if found != "" {
		out.Hash = found
		out.Duplicate = true
		return out, nil
	}
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	if in.Magnet != "" {
		if err := w.WriteField("urls", in.Magnet); err != nil {
			return out, beforeMutation(failure("add", "multipart_failed"))
		}
	} else {
		f, err := w.CreateFormFile("torrents", "resource.torrent")
		if err != nil {
			return out, beforeMutation(failure("add", "multipart_failed"))
		}
		_, _ = f.Write(in.Torrent)
	}
	autoCategory := c.config.AutoCategory
	fields := map[string]string{"savepath": in.SavePath, "stopped": strconv.FormatBool(in.Paused), "tags": strings.Join(in.Tags, ","), "sequentialDownload": strconv.FormatBool(c.config.Sequential), "firstLastPiecePrio": strconv.FormatBool(c.config.FirstLast), "skip_checking": "false", "contentLayout": "Original"}
	if in.SourceCategorySet {
		autoCategory = autoCategory && in.SourceCategory != nil && *in.SourceCategory != ""
		if autoCategory {
			fields["category"] = *in.SourceCategory
		}
	}
	fields["autoTMM"] = strconv.FormatBool(autoCategory)
	for key, value := range fields {
		if err := w.WriteField(key, value); err != nil {
			return out, beforeMutation(failure("add", "multipart_failed"))
		}
	}
	if err := w.Close(); err != nil {
		return out, beforeMutation(failure("add", "multipart_failed"))
	}
	response, err := c.qb(ctx, http.MethodPost, "torrents/add", body.String(), w.FormDataContentType())
	if err != nil {
		return out, err
	}
	// qB returns HTTP 200 for business rejection as well as acceptance.
	if body := strings.TrimSpace(string(response)); body != "Ok." {
		if body == "Fails." {
			return out, rejectedMutation(failure("add", "torrent_rejected"))
		}
		return out, failure("add", "invalid_add_response")
	}
	for attempt := 0; attempt < 5; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return out, ctx.Err()
			case <-time.After(time.Duration(attempt) * 100 * time.Millisecond):
			}
		}
		found, err = c.qbFind(ctx, hashes)
		if err != nil {
			return out, err
		}
		if found != "" {
			out.Hash = found
			return out, nil
		}
	}
	return out, failure("add", "accepted_but_not_observed")
}
func (c *Client) qbControl(ctx context.Context, hash, action string) error {
	if err := c.qbExists(ctx, hash); err != nil {
		return beforeMutation(err)
	}
	endpoint := map[string]string{"start": "start", "pause": "stop", "verify": "recheck", "remove_keep_data": "delete", "remove_with_data": "delete"}[action]
	args := url.Values{"hashes": {hash}}
	if action == "remove_keep_data" || action == "remove_with_data" {
		args.Set("deleteFiles", strconv.FormatBool(action == "remove_with_data"))
	}
	body, err := c.qb(ctx, http.MethodPost, "torrents/"+endpoint, args.Encode(), "application/x-www-form-urlencoded")
	if err != nil {
		return err
	}
	if b := strings.TrimSpace(string(body)); b != "" && b != "Ok." {
		return failure("control", "unexpected_response")
	}
	return nil
}
