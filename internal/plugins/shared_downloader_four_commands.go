package plugins

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
)

const (
	QBittorrentSetTagsCapability     = "downloader.qbittorrent.set_tags"
	QBittorrentSetCategoryCapability = "downloader.qbittorrent.set_category"
	QBittorrentStopCapability        = "downloader.qbittorrent.stop"
	TransmissionStopCapability       = "downloader.transmission.stop"
)

// SetTorrentsTag preserves the source None return. A nil error is not a
// successful-mutation receipt: the source also returns None for no client and
// caught business exceptions. Host/authority/unknown-effect errors stay errors.
func (q SharedQBittorrentCommands) SetTorrentsTag(ctx context.Context, ids, tags any) error {
	if err := downloaderCommandReady(ctx, q.Host, q.DownloaderID); err != nil {
		return err
	}
	for _, value := range []any{ids, tags} {
		if err := downloaderCommandJSONValue(value); err != nil {
			return err
		}
	}
	request, err := json.Marshal(struct {
		Op           string `json:"op"`
		DownloaderID string `json:"downloader_id"`
		IDs          any    `json:"ids"`
		Tags         any    `json:"tags"`
	}{"invoke", q.DownloaderID, ids, tags})
	if err != nil {
		return err
	}
	raw, err := downloaderCommandCall(ctx, q.Host, QBittorrentSetTagsCapability, request, downloaderCommandRequestMaxBytes)
	if err != nil {
		return err
	}
	fields, err := downloaderCommandObject(raw, "value")
	if err != nil {
		return err
	}
	if !bytes.Equal(bytes.TrimSpace(fields["value"]), []byte("null")) {
		return errors.New("downloader tag command expected explicit source None")
	}
	return nil
}

func (q SharedQBittorrentCommands) SetTorrentCategory(ctx context.Context, hashString, category any) (bool, error) {
	if err := downloaderCommandReady(ctx, q.Host, q.DownloaderID); err != nil {
		return false, err
	}
	for _, value := range []any{hashString, category} {
		if err := downloaderCommandJSONValue(value); err != nil {
			return false, err
		}
	}
	request, err := json.Marshal(struct {
		Op           string `json:"op"`
		DownloaderID string `json:"downloader_id"`
		HashString   any    `json:"hash_string"`
		Category     any    `json:"category"`
	}{"invoke", q.DownloaderID, hashString, category})
	if err != nil {
		return false, err
	}
	raw, err := downloaderCommandCall(ctx, q.Host, QBittorrentSetCategoryCapability, request, downloaderCommandRequestMaxBytes)
	if err != nil {
		return false, err
	}
	fields, err := downloaderCommandObject(raw, "value")
	if err != nil {
		return false, err
	}
	return downloaderCommandBool(fields["value"])
}

func (q SharedQBittorrentCommands) StopTorrents(ctx context.Context, ids any) (bool, error) {
	return downloaderCommandControl(ctx, q.Host, q.DownloaderID, QBittorrentStopCapability, ids)
}

func (t SharedTransmissionCommands) StopTorrents(ctx context.Context, ids any) (bool, error) {
	return downloaderCommandControl(ctx, t.Host, t.DownloaderID, TransmissionStopCapability, ids)
}
