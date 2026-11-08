package plugins

import (
	"context"
	"errors"
)

const DownloaderSourceConnectionCapability = "downloader.source.connection"

// SourceConnection reports the process-local native module constructor fact.
// Exists is wrapper presence; Inactive is its cached native client being nil.
// A later query/network failure does not change a successful constructor fact.
// The ordinary Connection resource retains its fresh per-call probe.
type DownloaderSourceConnectionV1 struct {
	DownloaderConnectionV1
	Exists bool `json:"exists"`
}

func (d Downloaders) SourceConnection(ctx context.Context, request DownloaderConnectionRequestV1) (DownloaderSourceConnectionV1, error) {
	if !downloaderHostIDValid(request.DownloaderID) {
		return DownloaderSourceConnectionV1{}, errors.New("explicit downloader required")
	}
	var wire struct {
		SchemaVersion int    `json:"schema_version"`
		DownloaderID  string `json:"downloader_id"`
		Kind          string `json:"kind"`
		Inactive      *bool  `json:"inactive"`
		Exists        *bool  `json:"exists"`
	}
	if err := d.hostRequest(ctx, DownloaderSourceConnectionCapability, request, &wire); err != nil {
		return DownloaderSourceConnectionV1{}, err
	}
	if wire.SchemaVersion != 1 || wire.DownloaderID != request.DownloaderID || wire.Kind != "qbittorrent" && wire.Kind != "transmission" || wire.Inactive == nil || wire.Exists == nil || !*wire.Exists && *wire.Inactive {
		return DownloaderSourceConnectionV1{}, errors.New("invalid source downloader connection identity")
	}
	return DownloaderSourceConnectionV1{DownloaderConnectionV1: DownloaderConnectionV1{SchemaVersion: wire.SchemaVersion, DownloaderID: wire.DownloaderID, Kind: wire.Kind, Inactive: *wire.Inactive}, Exists: *wire.Exists}, nil
}
