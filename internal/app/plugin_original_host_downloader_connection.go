package app

import (
	"context"
	"encoding/json"

	"light-media/internal/plugins"
)

func (a *App) originalHostDownloaderConnection(ctx context.Context, scope originalBaseCallScope, raw json.RawMessage) (json.RawMessage, error) {
	var request plugins.DownloaderConnectionRequestV1
	if err := originalResourceDecode(raw, &request); err != nil {
		return nil, err
	}
	client, kind, guard, err := a.originalHostDownloaderClient(ctx, scope, request.DownloaderID)
	if err != nil {
		return nil, err
	}
	defer client.Close()
	connectionErr := client.TransferConnected(ctx)
	// An actual auth/network failure is inactive; cancellation or a changed
	// binding is not a successful probe of any current downloader identity.
	if err := guard(ctx); err != nil {
		return nil, err
	}
	return originalHostDownloaderMarshal(plugins.DownloaderConnectionV1{SchemaVersion: 1, DownloaderID: request.DownloaderID, Kind: kind, Inactive: connectionErr != nil})
}
