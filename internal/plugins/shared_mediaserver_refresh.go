package plugins

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"math/big"
)

const (
	MediaServerRefreshRootCapability  = "mediaserver.instance.refresh_root_library"
	MediaServerRefreshItemsCapability = "mediaserver.instance.refresh_library_by_items"
)

// A normalized original RefreshMediaItem, not an arbitrary Python object or
// raw Pydantic model-admission API. Enum identity is distinct from its .value.
type MediaServerRefreshMediaType uint8

const (
	MediaServerRefreshTypeNone MediaServerRefreshMediaType = iota
	MediaServerRefreshTypeMovie
	MediaServerRefreshTypeTV
	MediaServerRefreshTypeCollection
	MediaServerRefreshTypeUnknown
)

type MediaServerRefreshItem struct {
	Title      json.RawMessage             `json:"title"`
	Year       json.RawMessage             `json:"year"`
	Type       MediaServerRefreshMediaType `json:"type"`
	Category   json.RawMessage             `json:"category"`
	TargetPath json.RawMessage             `json:"target_path"`
}

// Only Ugreen has this optional keyword. Host dispatch must inspect the actual
// retained vendor; omitted and explicitly supplied null remain distinct.
type MediaServerRefreshOptions struct {
	ScanMode json.RawMessage `json:"scan_mode,omitempty"`
}
type MediaServerRefreshRootRequest struct {
	Reference string          `json:"reference"`
	ScanMode  json.RawMessage `json:"scan_mode,omitempty"`
}
type MediaServerRefreshItemsRequest struct {
	Reference string                   `json:"reference"`
	Items     []MediaServerRefreshItem `json:"items"`
	ScanMode  json.RawMessage          `json:"scan_mode,omitempty"`
}

// Plex Library.update returns self. Preserve that exact host-retained object,
// not true or a serialized map of private SDK fields. No arbitrary Library
// methods are exposed by this two-operation bridge.
type MediaServerLibraryObject struct {
	Reference string `json:"reference"`
}
type MediaServerRefreshResult struct {
	Kind    string                    `json:"kind"`
	Boolean *bool                     `json:"boolean,omitempty"`
	Library *MediaServerLibraryObject `json:"library,omitempty"`
}

func mediaServerRefreshReference(token, prefix string) bool {
	if len(token) != 67 || token[:3] != prefix {
		return false
	}
	for _, c := range token[3:] {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}
func (r MediaServerRefreshResult) valid() bool {
	switch r.Kind {
	case "none":
		return r.Boolean == nil && r.Library == nil
	case "bool":
		return r.Boolean != nil && r.Library == nil
	case "plex_library":
		return r.Boolean == nil && r.Library != nil && mediaServerRefreshReference(r.Library.Reference, "ml_")
	}
	return false
}
func (r MediaServerRefreshResult) SourceTruthy() (bool, error) {
	if !r.valid() {
		return false, errors.New("invalid media refresh result")
	}
	if r.Kind == "bool" {
		return *r.Boolean, nil
	}
	return r.Kind == "plex_library", nil
}
func mediaServerRefreshItemsOwned(input []MediaServerRefreshItem) ([]MediaServerRefreshItem, error) {
	out := make([]MediaServerRefreshItem, len(input))
	for i, item := range input {
		if item.Type > MediaServerRefreshTypeUnknown {
			return nil, errors.New("invalid validated media refresh enum")
		}
		fields := []json.RawMessage{item.Title, item.Year, item.Category, item.TargetPath}
		for index, raw := range fields {
			raw = bytes.TrimSpace(raw)
			if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
				fields[index] = json.RawMessage("null")
				continue
			}
			if !json.Valid(raw) {
				return nil, errors.New("invalid validated media refresh field")
			}
			if raw[0] == '"' {
				if _, err := DecodeSystemConfigJSONString(raw); err != nil {
					return nil, errors.New("invalid validated media refresh string")
				}
			} else if index == 1 && !bytes.ContainsAny(raw, ".eE") {
				if _, ok := new(big.Int).SetString(string(raw), 10); !ok {
					return nil, errors.New("invalid validated media refresh year")
				}
			} else {
				return nil, errors.New("invalid validated media refresh field type")
			}
			fields[index] = append(json.RawMessage(nil), raw...)
		}
		out[i] = MediaServerRefreshItem{Title: fields[0], Year: fields[1], Type: item.Type, Category: fields[2], TargetPath: fields[3]}
	}
	return out, nil
}
func mediaServerRefreshOption(input MediaServerRefreshOptions) (json.RawMessage, error) {
	if len(input.ScanMode) == 0 {
		return nil, nil
	}
	if !json.Valid(input.ScanMode) {
		return nil, errors.New("invalid media scan mode JSON")
	}
	return append(json.RawMessage(nil), input.ScanMode...), nil
}
func (instance MediaServerInstance) RefreshRootLibrary(ctx context.Context, current HostCaller, options MediaServerRefreshOptions) (MediaServerRefreshResult, error) {
	var out MediaServerRefreshResult
	if !mediaServerRefreshReference(instance.Reference, "ms_") {
		return out, errors.New("invalid media instance reference")
	}
	mode, err := mediaServerRefreshOption(options)
	if err != nil {
		return out, err
	}
	err = mediaServerServiceCall(ctx, current, MediaServerRefreshRootCapability, MediaServerRefreshRootRequest{Reference: instance.Reference, ScanMode: mode}, &out)
	if err != nil {
		return MediaServerRefreshResult{}, err
	}
	if !out.valid() {
		return MediaServerRefreshResult{}, errors.New("invalid media refresh result")
	}
	return out, nil
}
func (instance MediaServerInstance) RefreshLibraryByItems(ctx context.Context, current HostCaller, items []MediaServerRefreshItem, options MediaServerRefreshOptions) (MediaServerRefreshResult, error) {
	var out MediaServerRefreshResult
	if !mediaServerRefreshReference(instance.Reference, "ms_") {
		return out, errors.New("invalid media instance reference")
	}
	owned, err := mediaServerRefreshItemsOwned(items)
	if err != nil {
		return out, err
	}
	mode, err := mediaServerRefreshOption(options)
	if err != nil {
		return out, err
	}
	err = mediaServerServiceCall(ctx, current, MediaServerRefreshItemsCapability, MediaServerRefreshItemsRequest{Reference: instance.Reference, Items: owned, ScanMode: mode}, &out)
	if err != nil {
		return MediaServerRefreshResult{}, err
	}
	if !out.valid() {
		return MediaServerRefreshResult{}, errors.New("invalid media refresh result")
	}
	return out, nil
}
