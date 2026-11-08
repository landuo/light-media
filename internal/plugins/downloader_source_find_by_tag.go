package plugins

import (
	"context"
	"errors"
)

const DownloaderSourceFindByTagCapability = "downloader.source.find_by_tag"

// SourceFindByTag uses the fixed source marker lookup/cleanup with source QB
// authentication. Ordinary FindByTag keeps its existing strict login behavior.
func (d Downloaders) SourceFindByTag(ctx context.Context, request DownloaderFindByTagRequestV1) (DownloaderFindByTagV1, error) {
	var out DownloaderFindByTagV1
	if err := request.Validate(); err != nil {
		return out, err
	}
	if err := d.hostRequest(ctx, DownloaderSourceFindByTagCapability, request, &out); err != nil {
		return out, err
	}
	if out.SchemaVersion != 1 || out.DownloaderID != request.DownloaderID || out.Hash != nil && !DownloaderTaskHashValid(*out.Hash) {
		return DownloaderFindByTagV1{}, errors.New("invalid source find-by-tag response")
	}
	return out, nil
}
