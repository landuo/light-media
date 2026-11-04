package plugins

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
)

// A dedicated current-invocation read of explicit MediaServers configuration.
// This includes the original config dictionary; it is not a public settings
// response, a vendor request capability, or a general SystemConfig getter.
const MediaServerConfigsCapability = "mediaserver.configs.get"

type MediaServerConfigsRequest struct {
	IncludeDisabled bool `json:"include_disabled"`
}

type MediaServerConfigsResponse struct {
	Result           *MediaServerConfigsResult `json:"result,omitempty"`
	SourceErrorClass string                    `json:"source_error_class,omitempty"`
}

type SharedMediaServerConfigs struct{ Host HostCaller }

// Entries retain source dict order and nullable keys. Diagnostics are safe
// observer metadata, not additional MediaServerConf fields or secret values.
// The callback must be supplied by the current invocation, never retained for
// later background access. A host guard failure is never a source exception.
func (s SharedMediaServerConfigs) GetConfigs(ctx context.Context, includeDisabled bool) (MediaServerConfigsResult, error) {
	var empty MediaServerConfigsResult
	if ctx == nil || s.Host == nil {
		return empty, errors.New("media server configurations require an active context and host caller")
	}
	if err := ctx.Err(); err != nil {
		return empty, err
	}
	request, err := json.Marshal(MediaServerConfigsRequest{IncludeDisabled: includeDisabled})
	if err != nil {
		return empty, err
	}
	raw, err := s.Host(ctx, MediaServerConfigsCapability, request)
	if err != nil {
		return empty, err
	}
	if err = ctx.Err(); err != nil {
		return empty, err
	}
	if len(raw) == 0 || len(raw) > MaxPayloadBytes {
		return empty, errors.New("invalid media server configuration response size")
	}
	var response MediaServerConfigsResponse
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err = d.Decode(&response); err != nil {
		return empty, errors.New("invalid media server configuration response")
	}
	if err = d.Decode(new(any)); err != io.EOF {
		return empty, errors.New("invalid media server configuration response")
	}
	if response.SourceErrorClass != "" {
		if response.Result != nil || response.SourceErrorClass != "TypeError" && response.SourceErrorClass != "OverflowError" {
			return empty, errors.New("invalid media server configuration source error")
		}
		return empty, &MediaServerConfigSourceError{Class: response.SourceErrorClass}
	}
	if response.Result == nil {
		return empty, errors.New("missing media server configuration result")
	}
	return *response.Result, nil
}
