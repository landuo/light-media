package plugins

import (
	"context"
	"encoding/json"
	"errors"
)

const (
	BrushSourceCandidatesCapability = "brush.source.candidates"
	BrushSourceTransferCapability   = "brush.source.transfer"
	BrushSourceAddCapability        = "brush.source.add"
)

type BrushCandidatesRequestV1 struct {
	SiteID string `json:"site_id"`
	RSS    bool   `json:"rss"`
}

// The opaque material belongs to this exact callback. Fields is the consumed
// TorrentInfo subset without the private enclosure or any site credentials.
type BrushCandidateV1 struct {
	MaterialID string          `json:"material_id"`
	Fields     json.RawMessage `json:"fields"`
}

type BrushCandidatesResultV1 struct {
	SchemaVersion int                `json:"schema_version"`
	Candidates    []BrushCandidateV1 `json:"candidates"`
}

type BrushTransferRequestV1 struct {
	DownloaderID string `json:"downloader_id"`
}

type BrushTransferResultV1 struct {
	SchemaVersion int     `json:"schema_version"`
	DownloaderID  string  `json:"downloader_id"`
	UploadSpeed   float64 `json:"upload_speed"`
	DownloadSpeed float64 `json:"download_speed"`
	UploadSize    float64 `json:"upload_size"`
	DownloadSize  float64 `json:"download_size"`
}

type BrushAddRequestV1 struct {
	SiteID     string `json:"site_id"`
	MaterialID string `json:"material_id"`
	TaskID     string `json:"task_id"`
	DownloaderSourceAddRequestV1
}

func (r BrushAddRequestV1) Validate() error {
	if r.SiteID == "" || r.MaterialID == "" || r.TaskID == "" || r.Torrent != nil || r.Magnet != "" || r.URL != nil || r.Cookie != nil {
		return errors.New("brush add requires bound site material and task")
	}
	return r.DownloaderSourceAddRequestV1.Validate()
}

type BrushSource struct{ Host HostCaller }

func (b BrushSource) Candidates(ctx context.Context, request BrushCandidatesRequestV1) (BrushCandidatesResultV1, error) {
	var out BrushCandidatesResultV1
	err := hostBusinessCall(ctx, b.Host, BrushSourceCandidatesCapability, request, &out)
	if err == nil && (out.SchemaVersion != 1 || out.Candidates == nil) {
		err = errors.New("invalid brush candidate response")
	}
	return out, err
}

func (b BrushSource) Transfer(ctx context.Context, downloaderID string) (BrushTransferResultV1, error) {
	var out BrushTransferResultV1
	err := hostBusinessCall(ctx, b.Host, BrushSourceTransferCapability, BrushTransferRequestV1{DownloaderID: downloaderID}, &out)
	if err == nil && (out.SchemaVersion != 1 || out.DownloaderID != downloaderID) {
		err = errors.New("invalid brush transfer response")
	}
	return out, err
}

func (b BrushSource) Add(ctx context.Context, request BrushAddRequestV1) (DownloaderSourceMutationResultV1, error) {
	if err := request.Validate(); err != nil {
		return DownloaderSourceMutationResultV1{}, err
	}
	return (Downloaders{Host: b.Host}).sourceMutation(ctx, BrushSourceAddCapability, request, request.DownloaderID)
}
