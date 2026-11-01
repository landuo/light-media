package plugins

import (
	"context"
	"encoding/json"
	"errors"
)

const TransmissionSetTagsCapability = "downloader.transmission.set_tags"
const TransmissionChangeCapability = "downloader.transmission.change"

// TRChangeSourceError contains only a classified original preparation failure.
// Native/authority/unknown-effect errors never become this source envelope.
type TRChangeSourceError struct{ Class string }

func (e *TRChangeSourceError) Error() string { return "TR change source: " + e.Class }

func (t SharedTransmissionCommands) SetTorrentTag(ctx context.Context, ids, tags, orgTags any) (bool, error) {
	if err := downloaderCommandReady(ctx, t.Host, t.DownloaderID); err != nil {
		return false, err
	}
	for _, v := range []any{ids, tags, orgTags} {
		if err := downloaderCommandJSONValue(v); err != nil {
			return false, err
		}
	}
	request, err := json.Marshal(struct {
		Op           string `json:"op"`
		DownloaderID string `json:"downloader_id"`
		IDs          any    `json:"ids"`
		Tags         any    `json:"tags"`
		OrgTags      any    `json:"org_tags"`
	}{"invoke", t.DownloaderID, ids, tags, orgTags})
	if err != nil {
		return false, err
	}
	raw, err := downloaderCommandCall(ctx, t.Host, TransmissionSetTagsCapability, request, downloaderCommandRequestMaxBytes)
	if err != nil {
		return false, err
	}
	fields, err := downloaderCommandObject(raw, "value")
	if err != nil {
		return false, err
	}
	return downloaderCommandBool(fields["value"])
}

func (t SharedTransmissionCommands) ChangeTorrent(ctx context.Context, hashString, uploadLimit, downloadLimit, ratioLimit, seedingTimeLimit any) (bool, error) {
	if err := downloaderCommandReady(ctx, t.Host, t.DownloaderID); err != nil {
		return false, err
	}
	for _, v := range []any{hashString, uploadLimit, downloadLimit, ratioLimit, seedingTimeLimit} {
		if err := downloaderCommandJSONValue(v); err != nil {
			return false, err
		}
	}
	request, err := json.Marshal(struct {
		Op               string `json:"op"`
		DownloaderID     string `json:"downloader_id"`
		HashString       any    `json:"hash_string"`
		UploadLimit      any    `json:"upload_limit"`
		DownloadLimit    any    `json:"download_limit"`
		RatioLimit       any    `json:"ratio_limit"`
		SeedingTimeLimit any    `json:"seeding_time_limit"`
	}{"invoke", t.DownloaderID, hashString, uploadLimit, downloadLimit, ratioLimit, seedingTimeLimit})
	if err != nil {
		return false, err
	}
	raw, err := downloaderCommandCall(ctx, t.Host, TransmissionChangeCapability, request, downloaderCommandRequestMaxBytes)
	if err != nil {
		return false, err
	}
	fields, valueErr := downloaderCommandObject(raw, "value")
	if valueErr == nil {
		return downloaderCommandBool(fields["value"])
	}
	fields, err = downloaderCommandObject(raw, "kind", "source_error")
	if err != nil {
		return false, valueErr
	}
	var kind, class string
	if json.Unmarshal(fields["kind"], &kind) != nil || json.Unmarshal(fields["source_error"], &class) != nil || kind != "source_error" || class != "ValueError" {
		return false, errors.New("invalid classified TR preparation exception")
	}
	return false, &TRChangeSourceError{Class: class}
}
