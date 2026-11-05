package plugins

import (
	"context"
	"errors"
)

const DownloaderConnectionCapability = "downloader.connection.read"

type DownloaderConnectionRequestV1 struct {
	DownloaderID string `json:"downloader_id"`
}

// Inactive is the source wrapper's is_inactive eligibility fact. It is
// separate from configured Enabled. The Go host performs a bounded read-only
// authentication/session probe per request rather than retaining a Python
// client object. Cancellation/revocation returns an error, never a status.
type DownloaderConnectionV1 struct {
	SchemaVersion int    `json:"schema_version"`
	DownloaderID  string `json:"downloader_id"`
	Kind          string `json:"kind"`
	Inactive      bool   `json:"inactive"`
}

func (d Downloaders) Connection(ctx context.Context, request DownloaderConnectionRequestV1) (DownloaderConnectionV1, error) {
	var out DownloaderConnectionV1
	if !downloaderHostIDValid(request.DownloaderID) {
		return out, errors.New("explicit downloader required")
	}
	var wire struct {
		SchemaVersion int    `json:"schema_version"`
		DownloaderID  string `json:"downloader_id"`
		Kind          string `json:"kind"`
		Inactive      *bool  `json:"inactive"`
	}
	if err := d.hostRequest(ctx, DownloaderConnectionCapability, request, &wire); err != nil {
		return out, err
	}
	if wire.SchemaVersion != 1 || wire.DownloaderID != request.DownloaderID || wire.Kind != "qbittorrent" && wire.Kind != "transmission" || wire.Inactive == nil {
		return DownloaderConnectionV1{}, errors.New("invalid downloader connection identity")
	}
	return DownloaderConnectionV1{SchemaVersion: wire.SchemaVersion, DownloaderID: wire.DownloaderID, Kind: wire.Kind, Inactive: *wire.Inactive}, nil
}
