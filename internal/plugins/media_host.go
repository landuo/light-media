package plugins

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
)

const MediaServersListCapability = "media.servers.list"
const MediaServersRefreshCapability = "media.servers.refresh"
const MediaServerLibraryRefreshCapability = "media.servers.refresh_library"
const MediaRecognizeCapability = "media.recognize"

type MediaHost struct{ Host HostCaller }

type MediaServersRequest struct {
	Type            string   `json:"type,omitempty"`
	Names           []string `json:"names,omitempty"`
	IncludeDisabled bool     `json:"include_disabled,omitempty"`
	Probe           bool     `json:"probe,omitempty"`
}

type MediaServer struct {
	Name       string               `json:"name"`
	Type       string               `json:"type"`
	Enabled    bool                 `json:"enabled"`
	Configured bool                 `json:"configured"`
	Connected  *bool                `json:"connected,omitempty"`
	Libraries  []MediaServerLibrary `json:"libraries,omitempty"`
}

type MediaServerLibrary struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Directories []string `json:"directories"`
}

type MediaRefreshRequest struct {
	Name      string             `json:"name"`
	LibraryID string             `json:"library_id,omitempty"` // use RefreshLibrary for an explicit GUID
	Paths     []string           `json:"paths"`
	Items     []MediaRefreshItem `json:"items"`
	Root      bool               `json:"root,omitempty"`
}

type MediaRefreshItem struct {
	Title      *string         `json:"title,omitempty"`
	Year       json.RawMessage `json:"year,omitempty"`
	Type       *string         `json:"type,omitempty"`
	Category   *string         `json:"category,omitempty"`
	TargetPath *string         `json:"target_path"`
}

func (m *MediaRefreshItem) UnmarshalJSON(raw []byte) error {
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return errors.New("refresh item must be an object")
	}
	type plain MediaRefreshItem
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	return decoder.Decode((*plain)(m))
}

type MediaRefreshLibraryRequest struct {
	Name      string `json:"name"`
	LibraryID string `json:"library_id"`
}

type MediaRefreshResult struct {
	Accepted     bool     `json:"accepted"`
	Paths        []string `json:"paths"`
	Result       *bool    `json:"result"`
	AllLibraries bool     `json:"all_libraries"`
	LibraryIDs   []string `json:"library_ids"`
}

type MediaRecognizeRequest struct {
	Meta         map[string]any `json:"meta,omitempty"`
	ShareMeta    map[string]any `json:"share_meta,omitempty"`
	Type         string         `json:"mtype,omitempty"`
	Source       string         `json:"source,omitempty"`
	MediaID      string         `json:"mediaid,omitempty"`
	TMDBID       int64          `json:"tmdbid,omitempty"`
	DoubanID     string         `json:"doubanid,omitempty"`
	BangumiID    int64          `json:"bangumiid,omitempty"`
	AniListID    int64          `json:"anilistid,omitempty"`
	EpisodeGroup string         `json:"episode_group,omitempty"`
	Cache        *bool          `json:"cache,omitempty"`
}

func (m MediaHost) Servers(ctx context.Context, request MediaServersRequest) ([]MediaServer, error) {
	var value []MediaServer
	err := hostServiceCall(ctx, m.Host, MediaServersListCapability, request, &value)
	return value, err
}

func (m MediaHost) Refresh(ctx context.Context, request MediaRefreshRequest) (MediaRefreshResult, error) {
	var value MediaRefreshResult
	err := hostServiceCall(ctx, m.Host, MediaServersRefreshCapability, request, &value)
	return value, err
}

func (m MediaHost) RefreshLibrary(ctx context.Context, request MediaRefreshLibraryRequest) (MediaRefreshResult, error) {
	var value MediaRefreshResult
	err := hostServiceCall(ctx, m.Host, MediaServerLibraryRefreshCapability, request, &value)
	return value, err
}

func (m MediaHost) Recognize(ctx context.Context, request MediaRecognizeRequest) (json.RawMessage, error) {
	var value json.RawMessage
	err := hostServiceCall(ctx, m.Host, MediaRecognizeCapability, request, &value)
	return value, err
}

func hostServiceCall(ctx context.Context, host HostCaller, method string, input, output any) error {
	if host == nil {
		return errors.New("host resource requires an active host caller")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	raw, err := json.Marshal(input)
	if err != nil {
		return err
	}
	raw, err = host(ctx, method, raw)
	if err != nil {
		return err
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	if len(raw) > MaxPayloadBytes || !json.Valid(raw) {
		return errors.New("invalid host resource response")
	}
	return json.Unmarshal(raw, output)
}
