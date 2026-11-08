package plugins

import (
	"context"
	"errors"
)

const (
	DownloadHistoryReadCapability   = "history.download.read"
	TransferHistoryReadCapability   = "history.transfer.read"
	TransferHistoryDeleteCapability = "history.transfer.delete"
)

type DownloadHistoryRequestV1 struct {
	Hash             string `json:"hash"`
	SourceProjection bool   `json:"source_projection,omitempty"`
}
type DownloadHistoryRowV1 struct {
	ID           string  `json:"id"`
	Hash         string  `json:"hash"`
	Title        string  `json:"title"`
	Type         string  `json:"type"`
	TMDBID       int64   `json:"tmdbid"`
	Season       int     `json:"season"`
	Seasons      *string `json:"seasons,omitempty"`
	Episodes     []int   `json:"episodes"`
	SiteID       string  `json:"site_id"`
	DownloaderID string  `json:"downloader_id"`
	SavePath     string  `json:"save_path"`
	State        string  `json:"state"`
	CreatedAt    string  `json:"created_at"`
}
type DownloadHistoryResultV1 struct {
	SchemaVersion int                   `json:"schema_version"`
	Item          *DownloadHistoryRowV1 `json:"item"`
}
type TransferHistoryRequestV1 struct {
	Source string `json:"source,omitempty"`
	Target string `json:"target,omitempty"`
	ID     string `json:"id,omitempty"`
}
type TransferHistoryRowV1 struct {
	ID        string `json:"id"`
	Source    string `json:"source"`
	Target    string `json:"target"`
	Title     string `json:"title"`
	Type      string `json:"type"`
	TMDBID    int64  `json:"tmdbid"`
	Season    int    `json:"season"`
	Episodes  []int  `json:"episodes"`
	Status    bool   `json:"status"`
	Mode      string `json:"mode"`
	Size      int64  `json:"size"`
	CreatedAt string `json:"created_at"`
}
type TransferHistoryResultV1 struct {
	SchemaVersion int                   `json:"schema_version"`
	Item          *TransferHistoryRowV1 `json:"item"`
}
type TransferHistoryDeleteV1 struct {
	ID string `json:"id"`
}
type HostMutationV1 struct {
	SchemaVersion int  `json:"schema_version"`
	Changed       bool `json:"changed"`
}
type History struct{ Host HostCaller }

func (h History) Download(ctx context.Context, r DownloadHistoryRequestV1) (DownloadHistoryResultV1, error) {
	var out DownloadHistoryResultV1
	err := hostBusinessCall(ctx, h.Host, DownloadHistoryReadCapability, r, &out)
	if err == nil && out.SchemaVersion != 1 {
		err = errors.New("invalid history response")
	}
	return out, err
}
func (h History) Transfer(ctx context.Context, r TransferHistoryRequestV1) (TransferHistoryResultV1, error) {
	var out TransferHistoryResultV1
	err := hostBusinessCall(ctx, h.Host, TransferHistoryReadCapability, r, &out)
	if err == nil && out.SchemaVersion != 1 {
		err = errors.New("invalid history response")
	}
	return out, err
}
func (h History) DeleteTransfer(ctx context.Context, id string) (HostMutationV1, error) {
	var out HostMutationV1
	err := hostBusinessCall(ctx, h.Host, TransferHistoryDeleteCapability, TransferHistoryDeleteV1{ID: id}, &out)
	if err == nil && out.SchemaVersion != 1 {
		err = errors.New("invalid history response")
	}
	return out, err
}
