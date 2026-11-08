package plugins

import (
	"context"
	"encoding/json"
	"errors"
)

const (
	SitesReadCapability            = "sites.read"
	SitesAccountReadCapability     = "sites.account.read"
	SitesRefreshCapability         = "sites.refresh"
	SitesResultWriteCapability     = "sites.result.write"
	SitesBrowseCapability          = "sites.torrents.browse"
	SitesRSSCapability             = "sites.torrents.rss"
	SitesTorrentDownloadCapability = "sites.torrents.download"
)

type SiteHostRequestV1 struct {
	ID               string `json:"id,omitempty"`
	ActiveOnly       bool   `json:"active_only,omitempty"`
	OrderByPriority  bool   `json:"order_by_priority,omitempty"`
	SourceProjection bool   `json:"source_projection,omitempty"`
}
type SiteHostRowV1 struct {
	ID       string `json:"id"`
	SourceID int64  `json:"source_id"`
	Name     string `json:"name"`
	Kind     string `json:"kind"`
	Domain   string `json:"domain"`
	URL      string `json:"url"`
	Priority int    `json:"priority"`
	Enabled  bool   `json:"enabled"`
	Public   bool   `json:"public"`
	Proxy    bool   `json:"proxy"`
	Render   bool   `json:"render"`
	// Returned only by explicit source_projection requests. This finite public
	// metadata subset never contains cookies, user agents or API credentials.
	SourceFields json.RawMessage `json:"source_fields,omitempty"`
}
type SiteHostResultV1 struct {
	SchemaVersion int             `json:"schema_version"`
	Items         []SiteHostRowV1 `json:"items"`
}
type SiteAccountHostRequestV1 struct {
	ID     string `json:"id,omitempty"`
	Date   string `json:"date,omitempty"`
	Latest bool   `json:"latest,omitempty"`
}

// Fields retain the source's nullable account columns and nested JSON. No
// site credentials or raw configuration rows are part of an account result.
type SiteAccountHostRowV1 struct {
	SiteID string          `json:"site_id"`
	Fields json.RawMessage `json:"fields"`
}
type SiteAccountHostResultV1 struct {
	SchemaVersion int                    `json:"schema_version"`
	Items         []SiteAccountHostRowV1 `json:"items"`
}
type SiteHostRefreshV1 struct {
	ID string `json:"id"`
}
type SiteHostAccessResultV1 struct {
	ID      string `json:"id"`
	Success bool   `json:"success"`
	Seconds *int64 `json:"seconds,omitempty"`
}
type SiteHostAccessV1 struct {
	SchemaVersion int              `json:"schema_version"`
	Success       int64            `json:"success"`
	Fail          int64            `json:"fail"`
	Seconds       *int64           `json:"seconds"`
	LastState     int              `json:"last_state"`
	LastModified  string           `json:"last_modified"`
	Note          map[string]int64 `json:"note"`
}

// Page follows MoviePilot's zero-based browse page; the native site client
// conversion to one-based pages is owned by the host.
type SiteBrowseHostRequestV1 struct {
	ID       string  `json:"id"`
	Keyword  string  `json:"keyword,omitempty"`
	Category *string `json:"category,omitempty"`
	Page     int     `json:"page,omitempty"`
}
type SiteTorrentHostRowV1 struct {
	ID             string      `json:"id"`
	SiteID         string      `json:"site_id"`
	Title          string      `json:"title"`
	Subtitle       string      `json:"subtitle"`
	Size           json.Number `json:"size"`
	Seeders        int         `json:"seeders"`
	Leechers       int         `json:"leechers"`
	PublishedAt    string      `json:"published_at"`
	DownloadFactor float64     `json:"download_factor"`
	UploadFactor   float64     `json:"upload_factor"`
	Free           bool        `json:"free"`
	Hash           string      `json:"hash,omitempty"`
}
type SiteBrowseHostResultV1 struct {
	SchemaVersion int                    `json:"schema_version"`
	Items         []SiteTorrentHostRowV1 `json:"items"`
	Page          int                    `json:"page"`
	HasMore       bool                   `json:"has_more"`
}
type SiteTorrentHostRequestV1 struct {
	ID         string `json:"id"`
	ResourceID string `json:"resource_id"`
}

// Torrent bytes are explicitly secret material. Plugins must not log, persist
// outside their private instance data, or embed them into UI/API responses.
type SiteTorrentHostResultV1 struct {
	SchemaVersion int    `json:"schema_version"`
	Torrent       []byte `json:"torrent"`
}
type Sites struct{ Host HostCaller }

func (s Sites) Read(ctx context.Context, r SiteHostRequestV1) (SiteHostResultV1, error) {
	var out SiteHostResultV1
	err := hostBusinessCall(ctx, s.Host, SitesReadCapability, r, &out)
	if err == nil && (out.SchemaVersion != 1 || out.Items == nil) {
		err = errors.New("invalid site response")
	}
	return out, err
}
func (s Sites) Accounts(ctx context.Context, r SiteAccountHostRequestV1) (SiteAccountHostResultV1, error) {
	var out SiteAccountHostResultV1
	err := hostBusinessCall(ctx, s.Host, SitesAccountReadCapability, r, &out)
	if err == nil && (out.SchemaVersion != 1 || out.Items == nil) {
		err = errors.New("invalid site account response")
	}
	return out, err
}
func (s Sites) Refresh(ctx context.Context, id string) (SiteAccountHostResultV1, error) {
	var out SiteAccountHostResultV1
	err := hostBusinessCall(ctx, s.Host, SitesRefreshCapability, SiteHostRefreshV1{id}, &out)
	if err == nil && (out.SchemaVersion != 1 || out.Items == nil) {
		err = errors.New("invalid site account response")
	}
	return out, err
}
func (s Sites) RecordAccess(ctx context.Context, r SiteHostAccessResultV1) (SiteHostAccessV1, error) {
	var out SiteHostAccessV1
	err := hostBusinessCall(ctx, s.Host, SitesResultWriteCapability, r, &out)
	if err == nil && out.SchemaVersion != 1 {
		err = errors.New("invalid site access response")
	}
	return out, err
}
func (s Sites) Browse(ctx context.Context, r SiteBrowseHostRequestV1) (SiteBrowseHostResultV1, error) {
	var out SiteBrowseHostResultV1
	err := hostBusinessCall(ctx, s.Host, SitesBrowseCapability, r, &out)
	if err == nil && (out.SchemaVersion != 1 || out.Items == nil || out.Page != r.Page) {
		err = errors.New("invalid site torrent response")
	}
	return out, err
}
func (s Sites) Torrent(ctx context.Context, r SiteTorrentHostRequestV1) (SiteTorrentHostResultV1, error) {
	var out SiteTorrentHostResultV1
	err := hostBusinessCall(ctx, s.Host, SitesTorrentDownloadCapability, r, &out)
	if err == nil && (out.SchemaVersion != 1 || len(out.Torrent) == 0) {
		err = errors.New("invalid site material response")
	}
	return out, err
}

func (s Sites) RSS(ctx context.Context, id string) (SiteBrowseHostResultV1, error) {
	var out SiteBrowseHostResultV1
	err := hostBusinessCall(ctx, s.Host, SitesRSSCapability, SiteHostRefreshV1{ID: id}, &out)
	if err == nil && (out.SchemaVersion != 1 || out.Items == nil) {
		err = errors.New("invalid site RSS response")
	}
	return out, err
}
