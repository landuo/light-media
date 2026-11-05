package plugins

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"unicode/utf8"
)

const (
	DownloaderCompletedCapability   = "downloader.tasks.completed"
	DownloaderAddCapability         = "downloader.tasks.add"
	DownloaderControlCapability     = "downloader.tasks.control"
	DownloaderTagsCapability        = "downloader.tags.read"
	DownloaderFindByTagCapability   = "downloader.tasks.find_by_tag"
	DownloaderDeleteTagsCapability  = "downloader.tags.delete"
	DownloaderListCapability        = "downloader.tasks.list"
	DownloaderDownloadingCapability = "downloader.tasks.downloading"
)

// Completed explicitly selects the ordinary completed-task query. Unlike
// Tasks, omitted hashes mean all completed tasks; an explicit [] means none.
type DownloaderCompletedRequestV1 struct {
	DownloaderID string   `json:"downloader_id"`
	Hashes       []string `json:"hashes"`
	Tags         []string `json:"tags,omitempty"`
}

type DownloaderDownloadingRequestV1 = DownloaderCompletedRequestV1

type DownloaderAddRequestV1 struct {
	DownloaderID        string   `json:"downloader_id"`
	Torrent             []byte   `json:"torrent,omitempty"`
	Magnet              string   `json:"magnet,omitempty"`
	URL                 *string  `json:"url,omitempty"`
	Cookie              *string  `json:"cookie,omitempty"`
	SavePath            *string  `json:"save_path,omitempty"`
	Paused              bool     `json:"paused"`
	Tags                []string `json:"tags,omitempty"`
	Category            *string  `json:"category,omitempty"`
	SkipChecking        *bool    `json:"skip_checking,omitempty"`
	IgnoreCategoryCheck *bool    `json:"ignore_category_check,omitempty"`
	UploadLimitBytes    *int64   `json:"upload_limit_bytes,omitempty"`
	DownloadLimitBytes  *int64   `json:"download_limit_bytes,omitempty"`
}

// There is deliberately no delete-files option. remove_task always preserves
// downloaded bytes. delete_tags additionally removes qB's global definitions,
// matching the source wrapper; remove_tags only detaches selected tasks.
type DownloaderControlRequestV1 struct {
	DownloaderID    string   `json:"downloader_id"`
	Hashes          []string `json:"hashes"`
	Action          string   `json:"action"`
	Tags            []string `json:"tags"`
	OriginalTags    []string `json:"original_tags,omitempty"`
	Category        *string  `json:"category,omitempty"`
	UploadLimitKB   *int64   `json:"upload_limit_kb,omitempty"`
	DownloadLimitKB *int64   `json:"download_limit_kb,omitempty"`
}

type DownloaderMutationV1 struct {
	SchemaVersion int      `json:"schema_version"`
	DownloaderID  string   `json:"downloader_id"`
	Accepted      bool     `json:"accepted"`
	Outcome       string   `json:"outcome"`
	Hash          string   `json:"hash,omitempty"`
	AddedIDs      []string `json:"added_ids,omitempty"`
	// A confirmed external effect can outlive its old host reference. A
	// nonempty FailureCode requires stopping that reference's follow-up chain;
	// it never certifies that retrying the mutation is safe.
	FailureCode string `json:"failure_code,omitempty"`
}

type DownloaderTagsRequestV1 struct {
	DownloaderID string `json:"downloader_id"`
	Hash         string `json:"hash,omitempty"`
}

type DownloaderTagsV1 struct {
	SchemaVersion int      `json:"schema_version"`
	DownloaderID  string   `json:"downloader_id"`
	Tags          []string `json:"tags"`
}

type DownloaderDeleteTagsRequestV1 struct {
	DownloaderID string   `json:"downloader_id"`
	Tags         []string `json:"tags"`
}

func (d Downloaders) DeleteTags(ctx context.Context, request DownloaderDeleteTagsRequestV1) (DownloaderMutationV1, error) {
	var out DownloaderMutationV1
	if !downloaderHostIDValid(request.DownloaderID) || len(request.Tags) == 0 || !downloaderHostTagsValid(request.Tags) {
		return out, errors.New("explicit downloader and nonempty tag definitions required")
	}
	err := d.hostRequest(ctx, DownloaderDeleteTagsCapability, request, &out)
	return validateDownloaderMutation(request.DownloaderID, out, err)
}

func downloaderHostIDValid(id string) bool {
	_, err := (DownloaderTasksRequestV1{DownloaderID: id, Hashes: []string{}}).CanonicalSelection()
	return err == nil
}

func downloaderHostTagsValid(tags []string) bool {
	for _, tag := range tags {
		if !utf8.ValidString(tag) {
			return false
		}
	}
	return true
}

func (r DownloaderCompletedRequestV1) Validate() error {
	if !downloaderHostIDValid(r.DownloaderID) || !downloaderHostTagsValid(r.Tags) {
		return errors.New("invalid completed-task selection")
	}
	if r.Hashes != nil {
		_, err := (DownloaderTasksRequestV1{DownloaderID: r.DownloaderID, Hashes: r.Hashes}).CanonicalSelection()
		return err
	}
	return nil
}

func (r DownloaderAddRequestV1) Validate() error {
	materials := 0
	if r.Torrent != nil {
		materials++
	}
	if r.Magnet != "" {
		materials++
	}
	if r.URL != nil {
		materials++
	}
	if !downloaderHostIDValid(r.DownloaderID) || !downloaderHostTagsValid(r.Tags) || materials > 1 {
		return errors.New("at most one torrent material and explicit downloader required")
	}
	if r.Magnet != "" && (!strings.HasPrefix(r.Magnet, "magnet:?") || strings.ContainsAny(r.Magnet, "\x00\r\n")) {
		return errors.New("only torrent bytes or magnet materials supported")
	}
	if r.SavePath != nil && !utf8.ValidString(*r.SavePath) {
		return errors.New("invalid downloader save path")
	}
	return nil
}

func (r DownloaderControlRequestV1) Validate() error {
	hashes, err := (DownloaderTasksRequestV1{DownloaderID: r.DownloaderID, Hashes: r.Hashes}).CanonicalSelection()
	if err != nil || len(hashes) == 0 || !downloaderHostTagsValid(r.Tags) || !downloaderHostTagsValid(r.OriginalTags) {
		return errors.New("nonempty exact task selection required")
	}
	if r.Action != "set_limits" && (r.UploadLimitKB != nil || r.DownloadLimitKB != nil) {
		return errors.New("unexpected speed-limit parameters")
	}
	switch r.Action {
	case "start", "recheck", "pause", "remove_task", "reannounce":
		if len(r.Tags) != 0 || r.OriginalTags != nil || r.Category != nil {
			return errors.New("unexpected control parameters")
		}
	case "set_tags", "remove_tags", "delete_tags":
		if r.Tags == nil || r.Category != nil {
			return errors.New("explicit tags required")
		}
		if r.Action != "set_tags" && r.OriginalTags != nil {
			return errors.New("original tags only apply to set_tags")
		}
	case "set_category":
		if r.Category == nil || len(r.Tags) != 0 || r.OriginalTags != nil {
			return errors.New("explicit category required")
		}
	case "set_limits":
		if len(hashes) != 1 || r.UploadLimitKB == nil && r.DownloadLimitKB == nil || r.Category != nil || len(r.Tags) != 0 || r.OriginalTags != nil {
			return errors.New("limits require one exact hash and explicit speed values")
		}
	default:
		return errors.New("unsupported downloader control")
	}
	return nil
}

// List is the source's explicit get_torrents() operation. It is a separate
// capability from exact Tasks and fixed Completed; no arbitrary daemon RPC or
// caller-provided native filter is accepted.
type DownloaderListRequestV1 struct {
	DownloaderID string `json:"downloader_id"`
}

func (d Downloaders) List(ctx context.Context, request DownloaderListRequestV1) (DownloaderTasksV1, error) {
	var out DownloaderTasksV1
	if !downloaderHostIDValid(request.DownloaderID) {
		return out, errors.New("explicit downloader required")
	}
	err := d.hostRequest(ctx, DownloaderListCapability, request, &out)
	if err != nil {
		return out, err
	}
	if out.SchemaVersion != 1 || out.DownloaderID != request.DownloaderID || out.Tasks == nil || out.Kind != "qbittorrent" && out.Kind != "transmission" {
		return DownloaderTasksV1{}, errors.New("invalid task-list response")
	}
	seen := map[string]bool{}
	for _, task := range out.Tasks {
		if !DownloaderTaskHashValid(task.Hash) || seen[task.Hash] || out.Kind == "qbittorrent" && (task.QBittorrent == nil || task.Transmission != nil) || out.Kind == "transmission" && (task.Transmission == nil || task.QBittorrent != nil) {
			return DownloaderTasksV1{}, errors.New("invalid task-list projection")
		}
		seen[task.Hash] = true
	}
	return out, nil
}

type DownloaderFindByTagRequestV1 struct {
	DownloaderID string   `json:"downloader_id"`
	Tags         []string `json:"tags"`
	Status       *string  `json:"status,omitempty"`
}
type DownloaderFindByTagV1 struct {
	SchemaVersion int     `json:"schema_version"`
	DownloaderID  string  `json:"downloader_id"`
	Hash          *string `json:"hash"`
}

func (r DownloaderFindByTagRequestV1) Validate() error {
	if !downloaderHostIDValid(r.DownloaderID) || len(r.Tags) == 0 || !downloaderHostTagsValid(r.Tags) {
		return errors.New("explicit downloader and tags required")
	}
	return nil
}
func (d Downloaders) FindByTag(ctx context.Context, request DownloaderFindByTagRequestV1) (DownloaderFindByTagV1, error) {
	var out DownloaderFindByTagV1
	if err := request.Validate(); err != nil {
		return out, err
	}
	if err := d.hostRequest(ctx, DownloaderFindByTagCapability, request, &out); err != nil {
		return out, err
	}
	if out.SchemaVersion != 1 || out.DownloaderID != request.DownloaderID || out.Hash != nil && !DownloaderTaskHashValid(*out.Hash) {
		return DownloaderFindByTagV1{}, errors.New("invalid find-by-tag response")
	}
	return out, nil
}

func downloaderHostDecode(raw []byte, out any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(out); err != nil {
		return err
	}
	if decoder.Decode(new(any)) != io.EOF {
		return errors.New("invalid downloader resource response")
	}
	return nil
}

func (d Downloaders) hostRequest(ctx context.Context, method string, request, out any) error {
	if d.Host == nil {
		return errors.New("downloader operation requires an active host caller")
	}
	raw, err := json.Marshal(request)
	if err != nil {
		return err
	}
	raw, err = d.Host(ctx, method, raw)
	if err != nil {
		return err
	}
	return downloaderHostDecode(raw, out)
}

func (d Downloaders) Completed(ctx context.Context, request DownloaderCompletedRequestV1) (DownloaderTasksV1, error) {
	return d.filteredTasks(ctx, DownloaderCompletedCapability, request)
}

func (d Downloaders) Downloading(ctx context.Context, request DownloaderDownloadingRequestV1) (DownloaderTasksV1, error) {
	return d.filteredTasks(ctx, DownloaderDownloadingCapability, request)
}

func (d Downloaders) filteredTasks(ctx context.Context, capability string, request DownloaderCompletedRequestV1) (DownloaderTasksV1, error) {
	var out DownloaderTasksV1
	if err := request.Validate(); err != nil {
		return out, err
	}
	if err := d.hostRequest(ctx, capability, request, &out); err != nil {
		return DownloaderTasksV1{}, err
	}
	if out.SchemaVersion != 1 || out.DownloaderID != request.DownloaderID || out.Tasks == nil || out.Kind != "qbittorrent" && out.Kind != "transmission" {
		return DownloaderTasksV1{}, errors.New("invalid completed-task response")
	}
	selected := map[string]bool{}
	for _, hash := range request.Hashes {
		selected[strings.ToLower(hash)] = true
	}
	seen := map[string]bool{}
	for _, task := range out.Tasks {
		if !DownloaderTaskHashValid(task.Hash) || seen[task.Hash] || request.Hashes != nil && !selected[task.Hash] || out.Kind == "qbittorrent" && (task.QBittorrent == nil || task.Transmission != nil) || out.Kind == "transmission" && (task.Transmission == nil || task.QBittorrent != nil) {
			return DownloaderTasksV1{}, errors.New("invalid completed-task projection")
		}
		seen[task.Hash] = true
	}
	return out, nil
}

func (d Downloaders) Add(ctx context.Context, request DownloaderAddRequestV1) (DownloaderMutationV1, error) {
	var out DownloaderMutationV1
	if err := request.Validate(); err != nil {
		return out, err
	}
	err := d.hostRequest(ctx, DownloaderAddCapability, request, &out)
	return validateDownloaderMutation(request.DownloaderID, out, err)
}

func (d Downloaders) Control(ctx context.Context, request DownloaderControlRequestV1) (DownloaderMutationV1, error) {
	var out DownloaderMutationV1
	if err := request.Validate(); err != nil {
		return out, err
	}
	err := d.hostRequest(ctx, DownloaderControlCapability, request, &out)
	return validateDownloaderMutation(request.DownloaderID, out, err)
}

func validateDownloaderMutation(id string, out DownloaderMutationV1, err error) (DownloaderMutationV1, error) {
	if err != nil {
		return DownloaderMutationV1{}, err
	}
	if out.SchemaVersion != 1 || out.DownloaderID != id || out.Outcome != "confirmed" && out.Outcome != "rejected" && out.Outcome != "unknown" || out.Accepted && out.Outcome != "confirmed" {
		return DownloaderMutationV1{}, errors.New("invalid downloader mutation response")
	}
	if out.FailureCode != "" && out.FailureCode != "CALL_CHANGED_AFTER_MUTATION" && out.FailureCode != "PARTIAL_MUTATION" || out.FailureCode != "" && !out.Accepted && out.Outcome != "unknown" {
		return DownloaderMutationV1{}, errors.New("invalid downloader post-mutation receipt")
	}
	return out, nil
}

func (d Downloaders) Tags(ctx context.Context, request DownloaderTagsRequestV1) (DownloaderTagsV1, error) {
	var out DownloaderTagsV1
	if !downloaderHostIDValid(request.DownloaderID) || request.Hash != "" && !DownloaderTaskHashValid(request.Hash) {
		return out, errors.New("invalid tag selection")
	}
	if err := d.hostRequest(ctx, DownloaderTagsCapability, request, &out); err != nil {
		return out, err
	}
	if out.SchemaVersion != 1 || out.DownloaderID != request.DownloaderID || out.Tags == nil {
		return DownloaderTagsV1{}, errors.New("invalid downloader tags response")
	}
	return out, nil
}
