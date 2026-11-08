package plugins

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
)

// DownloaderTasksReadCapability is a finite, exact-selection read. It grants no
// arbitrary daemon RPC, URL, mutation, tracker credentials or all-task selector.
const DownloaderTasksReadCapability = "downloader.tasks.read"

type DownloaderTasksRequestV1 struct {
	DownloaderID string   `json:"downloader_id"`
	Hashes       []string `json:"hashes"`
}

// CanonicalSelection requires an explicit array. nil (missing/null) is not all;
// [] is an empty selection. The host's existing request byte ceiling bounds the
// request; there is no new business count limit or silent result truncation.
func (r DownloaderTasksRequestV1) CanonicalSelection() ([]string, error) {
	if r.DownloaderID == "" || len(r.DownloaderID) > 64 || r.Hashes == nil {
		return nil, errors.New("explicit downloader ID and hashes array required")
	}
	for _, c := range r.DownloaderID {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '.' || c == '-') {
			return nil, errors.New("invalid downloader ID")
		}
	}
	out := []string{}
	seen := map[string]bool{}
	for _, hash := range r.Hashes {
		if !DownloaderTaskHashValid(hash) {
			return nil, errors.New("invalid exact torrent hash")
		}
		hash = strings.ToLower(hash)
		if !seen[hash] {
			seen[hash] = true
			out = append(out, hash)
		}
	}
	return out, nil
}

func DownloaderTaskHashValid(hash string) bool {
	if len(hash) != 40 && len(hash) != 64 {
		return false
	}
	for _, c := range hash {
		if !(c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F' || c >= '0' && c <= '9') {
			return false
		}
	}
	return true
}

type DownloaderTasksV1 struct {
	SchemaVersion int                `json:"schema_version"`
	DownloaderID  string             `json:"downloader_id"`
	Kind          string             `json:"kind"`
	Tasks         []DownloaderTaskV1 `json:"tasks"`
}

// Exactly one source-specific projection is populated. Native field names and
// units are retained; no completed/downloading/status business predicate is
// invented. Nil fields mean missing/null, never a fabricated numeric zero.
// json.Number preserves integer IDs and counters beyond float64's exact range.
type DownloaderTaskV1 struct {
	Hash         string                  `json:"hash"`
	QBittorrent  *QBittorrentTaskInfoV1  `json:"qbittorrent,omitempty"`
	Transmission *TransmissionTaskInfoV1 `json:"transmission,omitempty"`
}

type QBittorrentTaskInfoV1 struct {
	Name         *string      `json:"name,omitempty"`
	State        *string      `json:"state,omitempty"`
	Tags         *string      `json:"tags,omitempty"`
	Category     *string      `json:"category,omitempty"`
	SavePath     *string      `json:"save_path,omitempty"`
	Size         *json.Number `json:"size,omitempty"`
	TotalSize    *json.Number `json:"total_size,omitempty"`
	Completed    *json.Number `json:"completed,omitempty"`
	Availability *json.Number `json:"availability,omitempty"`
	Progress     *json.Number `json:"progress,omitempty"`
	AmountLeft   *json.Number `json:"amount_left,omitempty"`
	Downloaded   *json.Number `json:"downloaded,omitempty"`
	Uploaded     *json.Number `json:"uploaded,omitempty"`
	Ratio        *json.Number `json:"ratio,omitempty"`
	DownloadRate *json.Number `json:"dlspeed,omitempty"`
	UploadRate   *json.Number `json:"upspeed,omitempty"`
	AddedOn      *json.Number `json:"added_on,omitempty"`
	CompletionOn *json.Number `json:"completion_on,omitempty"`
	LastActivity *json.Number `json:"last_activity,omitempty"`
	SeedingTime  *json.Number `json:"seeding_time,omitempty"`
	ETA          *json.Number `json:"eta,omitempty"`
	Tracker      string       `json:"tracker,omitempty"` // BrushFlow only, origin without credentials or path.
}

type TransmissionTaskInfoV1 struct {
	ID              *json.Number `json:"id,omitempty"`
	Name            *string      `json:"name,omitempty"`
	Status          *json.Number `json:"status,omitempty"`
	Labels          *[]string    `json:"labels,omitempty"`
	DownloadDir     *string      `json:"downloadDir,omitempty"`
	TotalSize       *json.Number `json:"totalSize,omitempty"`
	PercentDone     *json.Number `json:"percentDone,omitempty"`
	LeftUntilDone   *json.Number `json:"leftUntilDone,omitempty"`
	DownloadedEver  *json.Number `json:"downloadedEver,omitempty"`
	UploadedEver    *json.Number `json:"uploadedEver,omitempty"`
	UploadRatio     *json.Number `json:"uploadRatio,omitempty"`
	RateDownload    *json.Number `json:"rateDownload,omitempty"`
	RateUpload      *json.Number `json:"rateUpload,omitempty"`
	AddedDate       *json.Number `json:"addedDate,omitempty"`
	DoneDate        *json.Number `json:"doneDate,omitempty"`
	ActivityDate    *json.Number `json:"activityDate,omitempty"`
	QueuePosition   *json.Number `json:"queuePosition,omitempty"`
	RecheckProgress *json.Number `json:"recheckProgress,omitempty"`
	Error           *json.Number `json:"error,omitempty"`
	TrackerList     string       `json:"trackerList,omitempty"` // BrushFlow only, origins in source order.
}

// Downloaders must be constructed from the current invocation's HostCaller,
// not retained by a worker for later/background calls.
type Downloaders struct{ Host HostCaller }

func (d Downloaders) Tasks(ctx context.Context, request DownloaderTasksRequestV1) (DownloaderTasksV1, error) {
	var out DownloaderTasksV1
	if d.Host == nil {
		return out, errors.New("downloader tasks require an active host caller")
	}
	selected, err := request.CanonicalSelection()
	if err != nil {
		return out, err
	}
	raw, err := json.Marshal(request)
	if err != nil {
		return out, err
	}
	raw, err = d.Host(ctx, DownloaderTasksReadCapability, raw)
	if err != nil {
		return out, err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&out); err != nil {
		return DownloaderTasksV1{}, err
	}
	if err := decoder.Decode(new(any)); err != io.EOF || out.SchemaVersion != 1 || out.DownloaderID != request.DownloaderID || out.Tasks == nil || out.Kind != "qbittorrent" && out.Kind != "transmission" {
		return DownloaderTasksV1{}, errors.New("invalid downloader task response")
	}
	allowed := make(map[string]bool, len(selected))
	for _, hash := range selected {
		allowed[hash] = true
	}
	seen := map[string]bool{}
	for _, task := range out.Tasks {
		if !allowed[task.Hash] || seen[task.Hash] || out.Kind == "qbittorrent" && (task.QBittorrent == nil || task.Transmission != nil) || out.Kind == "transmission" && (task.Transmission == nil || task.QBittorrent != nil) {
			return DownloaderTasksV1{}, errors.New("invalid downloader task response selection")
		}
		seen[task.Hash] = true
	}
	return out, nil
}
