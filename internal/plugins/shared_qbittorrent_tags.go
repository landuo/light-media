package plugins

import (
	"context"
	"encoding/json"
	"errors"
)

const QBittorrentDeleteTagsCapability = "downloader.qbittorrent.delete_tags"
const QBittorrentGetIDByTagCapability = "downloader.qbittorrent.get_id_by_tag"

// QBTagSourceError carries only a classified escaping original exception. It
// is never a platform boundary, uncertain effect, credential or traceback.
type QBTagSourceError struct{ Class string }

func (e *QBTagSourceError) Error() string { return "qB tag source: " + e.Class }

func (q SharedQBittorrentCommands) DeleteTorrentsTag(ctx context.Context, ids, tag any) (bool, error) {
	if err := downloaderCommandReady(ctx, q.Host, q.DownloaderID); err != nil {
		return false, err
	}
	for _, v := range []any{ids, tag} {
		if err := downloaderCommandJSONValue(v); err != nil {
			return false, err
		}
	}
	request, err := json.Marshal(struct {
		Op           string `json:"op"`
		DownloaderID string `json:"downloader_id"`
		IDs          any    `json:"ids"`
		Tag          any    `json:"tag"`
	}{"invoke", q.DownloaderID, ids, tag})
	if err != nil {
		return false, err
	}
	raw, err := downloaderCommandCall(ctx, q.Host, QBittorrentDeleteTagsCapability, request, downloaderCommandRequestMaxBytes)
	if err != nil {
		return false, err
	}
	fields, err := downloaderCommandObject(raw, "value")
	if err != nil {
		return false, err
	}
	return downloaderCommandBool(fields["value"])
}

// GetTorrentIDByTag preserves source None, False and empty-string identity.
// No poll/cleanup is done in the worker: one host call owns the full operation.
func (q SharedQBittorrentCommands) GetTorrentIDByTag(ctx context.Context, tags, status any) (json.RawMessage, error) {
	if err := downloaderCommandReady(ctx, q.Host, q.DownloaderID); err != nil {
		return nil, err
	}
	for _, v := range []any{tags, status} {
		if err := downloaderCommandJSONValue(v); err != nil {
			return nil, err
		}
	}
	request, err := json.Marshal(struct {
		Op           string `json:"op"`
		DownloaderID string `json:"downloader_id"`
		Tags         any    `json:"tags"`
		Status       any    `json:"status"`
	}{"invoke", q.DownloaderID, tags, status})
	if err != nil {
		return nil, err
	}
	raw, err := downloaderCommandCall(ctx, q.Host, QBittorrentGetIDByTagCapability, request, downloaderCommandRequestMaxBytes)
	if err != nil {
		return nil, err
	}
	fields, valueErr := downloaderCommandObject(raw, "value")
	if valueErr == nil {
		return append(json.RawMessage(nil), fields["value"]...), nil
	}
	fields, err = downloaderCommandObject(raw, "kind", "source_error")
	if err != nil {
		return nil, valueErr
	}
	var kind, class string
	if json.Unmarshal(fields["kind"], &kind) != nil || json.Unmarshal(fields["source_error"], &class) != nil || kind != "source_error" || class != "AttributeError" {
		return nil, errors.New("invalid classified qB tag source exception")
	}
	return nil, &QBTagSourceError{Class: class}
}
