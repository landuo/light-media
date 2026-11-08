package plugins

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"unicode/utf8"
)

const (
	SitesSourceIndexerCapability    = "sites.source.indexer"
	SitesSourceIndexersCapability   = "sites.source.indexers"
	SitesSourceRefreshCapability    = "sites.source.refresh"
	SitesSourceRefreshAllCapability = "sites.source.refresh_all"
	SitesSourceAccountsCapability   = "sites.source.account.read"
)

type SiteSourceIndexerRequestV1 struct {
	Domain string `json:"domain"`
}

// SiteID authorizes only a current canonical configured site. Catalog-only
// templates carry nil identity and cannot authorize requests or credentials.
// Fields contains the consumed public source metadata, never credentials.
type SiteSourceIndexerV1 struct {
	SiteID *string         `json:"site_id"`
	Fields json.RawMessage `json:"fields"`
}
type SiteSourceIndexerResultV1 struct {
	SchemaVersion int                  `json:"schema_version"`
	Indexer       *SiteSourceIndexerV1 `json:"indexer"`
}
type SiteSourceIndexersResultV1 struct {
	SchemaVersion int                   `json:"schema_version"`
	Items         []SiteSourceIndexerV1 `json:"items"`
}

// Data is mandatory: the current source DTO (single), insertion-ordered name
// dictionary (all), or explicit null. It is never a substituted account-day row.
type SiteSourceRefreshResultV1 struct {
	SchemaVersion int             `json:"schema_version"`
	Data          json.RawMessage `json:"data"`
}

func siteSourceObject(raw json.RawMessage, nullable bool) bool {
	raw = bytes.TrimSpace(raw)
	return json.Valid(raw) && (len(raw) > 0 && raw[0] == '{' || nullable && bytes.Equal(raw, []byte("null")))
}
func siteSourceIndexerValid(row SiteSourceIndexerV1) bool {
	return siteSourceObject(row.Fields, false) && (row.SiteID == nil || *row.SiteID != "" && utf8.ValidString(*row.SiteID))
}
func (s Sites) SourceIndexer(ctx context.Context, domain string) (SiteSourceIndexerResultV1, error) {
	var out SiteSourceIndexerResultV1
	if !utf8.ValidString(domain) || len(domain) > MaxPayloadBytes {
		return out, errors.New("invalid site domain")
	}
	var wire struct {
		SchemaVersion int             `json:"schema_version"`
		Indexer       json.RawMessage `json:"indexer"`
	}
	err := hostBusinessCall(ctx, s.Host, SitesSourceIndexerCapability, SiteSourceIndexerRequestV1{domain}, &wire)
	if err == nil {
		if wire.SchemaVersion != 1 || !siteSourceObject(wire.Indexer, true) {
			return out, errors.New("invalid source indexer response")
		}
		out.SchemaVersion = wire.SchemaVersion
		if !bytes.Equal(bytes.TrimSpace(wire.Indexer), []byte("null")) {
			err = json.Unmarshal(wire.Indexer, &out.Indexer)
		}
	}
	if err == nil && (out.SchemaVersion != 1 || out.Indexer != nil && !siteSourceIndexerValid(*out.Indexer)) {
		err = errors.New("invalid source indexer response")
	}
	return out, err
}
func (s Sites) SourceIndexers(ctx context.Context) (SiteSourceIndexersResultV1, error) {
	var out SiteSourceIndexersResultV1
	err := hostBusinessCall(ctx, s.Host, SitesSourceIndexersCapability, struct{}{}, &out)
	if err == nil && (out.SchemaVersion != 1 || out.Items == nil) {
		err = errors.New("invalid source indexers response")
	}
	if err == nil {
		for _, row := range out.Items {
			if !siteSourceIndexerValid(row) {
				err = errors.New("invalid source indexer response")
				break
			}
		}
	}
	return out, err
}
func (s Sites) sourceRefresh(ctx context.Context, method string, request any) (SiteSourceRefreshResultV1, error) {
	var out SiteSourceRefreshResultV1
	err := hostBusinessCall(ctx, s.Host, method, request, &out)
	if err == nil && (out.SchemaVersion != 1 || !siteSourceObject(out.Data, true)) {
		err = errors.New("invalid current source userdata response")
	}
	return out, err
}
func (s Sites) SourceRefresh(ctx context.Context, id string) (SiteSourceRefreshResultV1, error) {
	if id == "" || !utf8.ValidString(id) || len(id) > MaxPayloadBytes {
		return SiteSourceRefreshResultV1{}, errors.New("explicit site required")
	}
	return s.sourceRefresh(ctx, SitesSourceRefreshCapability, SiteHostRefreshV1{id})
}
func (s Sites) SourceRefreshAll(ctx context.Context) (SiteSourceRefreshResultV1, error) {
	return s.sourceRefresh(ctx, SitesSourceRefreshAllCapability, struct{}{})
}

func (s Sites) SourceAccounts(ctx context.Context, request SiteAccountHostRequestV1) (SiteAccountHostResultV1, error) {
	var out SiteAccountHostResultV1
	err := hostBusinessCall(ctx, s.Host, SitesSourceAccountsCapability, request, &out)
	if err == nil && (out.SchemaVersion != 1 || out.Items == nil) {
		err = errors.New("invalid source site account response")
	}
	return out, err
}
