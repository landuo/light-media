package plugins

import (
	"context"
	"encoding/json"
	"errors"
)

// Configurations preserves the saved settings order. Inventory's existing ID
// sorting remains unchanged. These references contain no daemon credentials,
// and reading one never grants authority for a later operation.
const DownloaderConfigurationsReadCapability = "downloader.configurations.read"

type DownloaderConfigurationsV1 struct {
	SchemaVersion int                          `json:"schema_version"`
	Downloaders   []BrushDownloaderReferenceV1 `json:"downloaders"`
}

func (d Downloaders) Configurations(ctx context.Context) (DownloaderConfigurationsV1, error) {
	var out DownloaderConfigurationsV1
	if err := d.hostRequest(ctx, DownloaderConfigurationsReadCapability, json.RawMessage(`{}`), &out); err != nil {
		return out, err
	}
	if out.SchemaVersion != 1 || out.Downloaders == nil {
		return DownloaderConfigurationsV1{}, errors.New("invalid ordered downloader configurations")
	}
	seen := map[string]bool{}
	for _, ref := range out.Downloaders {
		if !downloaderHostIDValid(ref.ID) || seen[ref.ID] {
			return DownloaderConfigurationsV1{}, errors.New("invalid or repeated downloader configuration identity")
		}
		seen[ref.ID] = true
	}
	return out, nil
}
