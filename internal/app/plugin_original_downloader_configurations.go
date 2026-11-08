package app

import (
	"context"
	"encoding/json"

	"light-media/internal/plugins"
)

func (a *App) originalDownloaderConfigurations(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	var input struct{}
	if err := originalResourceDecode(raw, &input); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	out := plugins.DownloaderConfigurationsV1{SchemaVersion: 1, Downloaders: []plugins.BrushDownloaderReferenceV1{}}
	for _, ref := range a.Settings().Downloaders {
		out.Downloaders = append(out.Downloaders, plugins.BrushDownloaderReferenceV1{ID: ref.ID, Name: ref.Name, Kind: ref.Kind, Enabled: downloaderEnabled(ref)})
	}
	return originalHostDownloaderMarshal(out)
}
