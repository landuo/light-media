package app

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
)

const nativeSiteIdentitySchema = `
CREATE TABLE IF NOT EXISTS native_site_identities(
 source_id INTEGER PRIMARY KEY AUTOINCREMENT CHECK(source_id>0),
 site_id TEXT NOT NULL UNIQUE,canonical_origin TEXT NOT NULL,
 state TEXT NOT NULL CHECK(state IN ('active','deleted')),
 created_at TEXT NOT NULL,deleted_at TEXT NOT NULL,proof TEXT NOT NULL);
CREATE UNIQUE INDEX IF NOT EXISTS native_site_active_origin ON native_site_identities(canonical_origin) WHERE state='active' AND canonical_origin<>'';
CREATE TABLE IF NOT EXISTS native_site_deletions(event_id TEXT PRIMARY KEY,site_id TEXT NOT NULL UNIQUE,source_id INTEGER NOT NULL UNIQUE,sealed BLOB NOT NULL);
CREATE TABLE IF NOT EXISTS native_site_import_refs(ref_id TEXT PRIMARY KEY,site_id TEXT NOT NULL,native_source_id INTEGER NOT NULL,sealed BLOB NOT NULL);
CREATE TABLE IF NOT EXISTS native_site_source_snapshots(snapshot_id TEXT PRIMARY KEY,source_fingerprint TEXT NOT NULL,snapshot_digest TEXT NOT NULL,sealed BLOB NOT NULL,UNIQUE(source_fingerprint,snapshot_digest));
CREATE TABLE IF NOT EXISTS native_site_migration_receipts(source_fingerprint TEXT NOT NULL,original_source_id INTEGER NOT NULL,native_source_id INTEGER NOT NULL,site_id TEXT NOT NULL,sealed BLOB NOT NULL,PRIMARY KEY(source_fingerprint,original_source_id),UNIQUE(source_fingerprint,native_source_id));`

type nativeSiteIdentity struct {
	SourceID        int64  `json:"source_id"`
	SiteID          string `json:"site_id"`
	CanonicalOrigin string `json:"canonical_origin"`
	State           string `json:"state"`
	CreatedAt       string `json:"created_at"`
	DeletedAt       string `json:"deleted_at"`
}
type siteIdentityReader interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func siteIdentityError() error {
	return bad("SITE_IDENTITY_CONFLICT", "站点身份记录不一致，未修改配置")
}
func (s *Store) siteIdentityMAC(domain string, v any) string {
	b, _ := json.Marshal(v)
	m := hmac.New(sha256.New, s.key)
	m.Write([]byte("light-media/native-site-identity/v1/" + domain + "\x00"))
	m.Write(b)
	return hex.EncodeToString(m.Sum(nil))
}
func (s *Store) readNativeSiteIdentity(ctx context.Context, q siteIdentityReader, by string, value any) (nativeSiteIdentity, error) {
	var out nativeSiteIdentity
	var proof string
	if by != "source_id" && by != "site_id" {
		return out, siteIdentityError()
	}
	err := q.QueryRowContext(ctx, `SELECT source_id,site_id,canonical_origin,state,created_at,deleted_at,proof FROM native_site_identities WHERE `+by+`=?`, value).Scan(&out.SourceID, &out.SiteID, &out.CanonicalOrigin, &out.State, &out.CreatedAt, &out.DeletedAt, &proof)
	if err != nil {
		return out, err
	}
	if out.SourceID < 1 || out.SiteID == "" || (out.State != "active" && out.State != "deleted") || out.CreatedAt == "" || (out.State == "active" && out.DeletedAt != "") || (out.State == "deleted" && out.DeletedAt == "") || !hmac.Equal([]byte(proof), []byte(s.siteIdentityMAC("mapping", out))) {
		return out, siteIdentityError()
	}
	return out, nil
}

// Lookups never allocate, revive, import, or claim a source configuration.
func (s *Store) nativeSiteByID(ctx context.Context, id int64) (nativeSiteIdentity, error) {
	if id < 1 {
		return nativeSiteIdentity{}, siteIdentityError()
	}
	return s.readNativeSiteIdentity(ctx, s.DB, "source_id", id)
}
func (s *Store) nativeSiteByAppID(ctx context.Context, id string) (nativeSiteIdentity, error) {
	if id == "" {
		return nativeSiteIdentity{}, siteIdentityError()
	}
	return s.readNativeSiteIdentity(ctx, s.DB, "site_id", id)
}
func nativeSiteCanonicalOrigin(raw string) (string, error) {
	if strings.TrimSpace(raw) == "" {
		return "", nil
	}
	if _, origin, _, err := siteImportOrigin(raw); err == nil {
		return origin, nil
	}
	// The existing management API permits explicit legacy adapters on loopback
	// for owned protocol fixtures. This exact origin stays in the native map;
	// source migration still requires siteImportOrigin's strict public boundary.
	u, err := url.Parse(raw)
	if err != nil || u.User != nil || u.Opaque != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || (u.Path != "" && u.Path != "/") || (u.Scheme != "http" && u.Scheme != "https") {
		return "", siteIdentityError()
	}
	h := strings.ToLower(u.Hostname())
	if h != "127.0.0.1" && h != "localhost" && h != "::1" {
		return "", siteIdentityError()
	}
	u.Host = strings.ToLower(u.Host)
	u.Path = ""
	u.RawPath = ""
	return u.String(), nil
}
func nativeConfiguredSiteOrigin(site SiteConfig) (string, error) {
	origin, err := nativeSiteCanonicalOrigin(site.BaseURL)
	if err != nil && !site.Enabled {
		return "", nil
	}
	return origin, err
}

func (s *Store) writeNativeSiteIdentityTx(ctx context.Context, tx *sql.Tx, v nativeSiteIdentity) error {
	result, err := tx.ExecContext(ctx, `UPDATE native_site_identities SET canonical_origin=?,state=?,created_at=?,deleted_at=?,proof=? WHERE source_id=? AND site_id=?`, v.CanonicalOrigin, v.State, v.CreatedAt, v.DeletedAt, s.siteIdentityMAC("mapping", v), v.SourceID, v.SiteID)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return siteIdentityError()
	}
	return nil
}
func (a *App) reconcileNativeSiteIdentitiesTx(ctx context.Context, tx *sql.Tx, previous, next Settings) error {
	old := map[string]bool{}
	for _, v := range previous.Sites {
		old[v.ID] = true
	}
	current := map[string]SiteConfig{}
	origins := map[string]bool{}
	for _, v := range next.Sites {
		if v.ID == "" {
			return siteIdentityError()
		}
		if _, ok := current[v.ID]; ok {
			return siteIdentityError()
		}
		origin, err := nativeConfiguredSiteOrigin(v)
		if err != nil {
			return err
		}
		if origin != "" && origins[origin] {
			return siteIdentityError()
		}
		origins[origin] = true
		current[v.ID] = v
	}
	rows, err := tx.QueryContext(ctx, `SELECT site_id FROM native_site_identities WHERE state='active' ORDER BY source_id`)
	if err != nil {
		return err
	}
	active := []string{}
	for rows.Next() {
		var v string
		if err = rows.Scan(&v); err != nil {
			rows.Close()
			return err
		}
		active = append(active, v)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, id := range active {
		v, err := a.Store.readNativeSiteIdentity(ctx, tx, "site_id", id)
		if err != nil {
			return err
		}
		if _, exists := current[id]; !exists {
			if !old[id] {
				return siteIdentityError()
			}
			v.State = "deleted"
			v.DeletedAt = now()
			if err = a.Store.writeNativeSiteIdentityTx(ctx, tx, v); err != nil {
				return err
			}
			if err = a.Store.recordNativeSiteDeletionTx(ctx, tx, v); err != nil {
				return err
			}
		}
	}
	keys := make([]string, 0, len(current))
	for id := range current {
		keys = append(keys, id)
	}
	sort.Strings(keys)
	for _, id := range keys {
		site := current[id]
		origin, _ := nativeConfiguredSiteOrigin(site)
		v, err := a.Store.readNativeSiteIdentity(ctx, tx, "site_id", id)
		if errors.Is(err, sql.ErrNoRows) {
			v = nativeSiteIdentity{SiteID: id, CanonicalOrigin: origin, State: "active", CreatedAt: now()}
			result, err := tx.ExecContext(ctx, `INSERT INTO native_site_identities(site_id,canonical_origin,state,created_at,deleted_at,proof) VALUES(?,?,'active',?,'','')`, id, origin, v.CreatedAt)
			if err != nil {
				return err
			}
			v.SourceID, err = result.LastInsertId()
			if err != nil {
				return err
			}
			if err = a.Store.writeNativeSiteIdentityTx(ctx, tx, v); err != nil {
				return err
			}
		} else if err != nil {
			return err
		} else {
			if v.State != "active" {
				return bad("SITE_ID_RETIRED", "该站点 ID 已删除，重新添加需使用新的站点 ID")
			}
			if v.CanonicalOrigin != origin {
				v.CanonicalOrigin = origin
				if err = a.Store.writeNativeSiteIdentityTx(ctx, tx, v); err != nil {
					return err
				}
			}
		}
	}
	// Management deletion's cache removal belongs to this same commit.
	for _, site := range previous.Sites {
		if _, ok := current[site.ID]; !ok {
			if _, err = tx.ExecContext(ctx, `DELETE FROM records WHERE (kind IN ('site_connection','site_stats_latest','site_cooldown','site_icon') AND id=?) OR (kind='site_stats_history' AND substr(id,1,?)=?)`, site.ID, len(site.ID)+1, site.ID+":"); err != nil {
				return err
			}
		}
	}
	return a.recordImportedSiteIdentitiesTx(ctx, tx)
}
func (a *App) bootstrapNativeSiteIdentities(ctx context.Context) error {
	tx, err := a.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// Explicit local schema upgrade: deterministic opaque-ID order, never a
	// read-time allocation and never a claim of an original plugin selection.
	if err = a.reconcileNativeSiteIdentitiesTx(ctx, tx, a.config, a.config); err != nil {
		return err
	}
	return tx.Commit()
}

type importedSiteIdentity struct {
	Version             int    `json:"version"`
	RefID               string `json:"ref_id"`
	DocumentFingerprint string `json:"document_fingerprint"`
	Ordinal             int    `json:"ordinal"`
	OriginalSourceID    int64  `json:"original_source_id"`
	NativeSourceID      int64  `json:"native_source_id"`
	SiteID              string `json:"site_id"`
	CanonicalOrigin     string `json:"canonical_origin"`
	Status              string `json:"status"`
}
type importedSiteIdentitiesKey struct{}

func importedNumericSiteID(raw json.RawMessage) (int64, error) {
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		return 0, siteIdentityError()
	}
	rawID, ok := fields["id"]
	if !ok || string(rawID) == "null" {
		return 0, nil
	}
	var n json.Number
	if err := json.Unmarshal(rawID, &n); err != nil {
		return 0, nil
	} // old native opaque strings are not source IDs
	id, err := strconv.ParseInt(string(n), 10, 64)
	if err != nil || id < 1 {
		return 0, bad("INVALID_SOURCE_SITE_ID", "原站点数字 ID 无效")
	}
	return id, nil
}
func (a *App) recordImportedSiteIdentitiesTx(ctx context.Context, tx *sql.Tx) error {
	refs, _ := ctx.Value(importedSiteIdentitiesKey{}).([]importedSiteIdentity)
	for _, v := range refs {
		native, err := a.Store.readNativeSiteIdentity(ctx, tx, "site_id", v.SiteID)
		if err != nil {
			return err
		}
		if native.State != "active" || native.CanonicalOrigin != v.CanonicalOrigin || v.OriginalSourceID < 1 {
			return siteIdentityError()
		}
		v.Version = 1
		v.NativeSourceID = native.SourceID
		v.Status = "unclaimed"
		v.RefID = "sir_" + a.Store.siteIdentityMAC("import-ref", []any{v.DocumentFingerprint, v.Ordinal, v.OriginalSourceID, v.NativeSourceID, v.CanonicalOrigin})
		var previous []byte
		err = tx.QueryRowContext(ctx, `SELECT sealed FROM native_site_import_refs WHERE ref_id=?`, v.RefID).Scan(&previous)
		if err == nil {
			var old importedSiteIdentity
			if a.Store.Unseal(previous, &old) != nil || old != v {
				return siteIdentityError()
			}
			continue
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		sealed, err := a.Store.Seal(v)
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO native_site_import_refs VALUES(?,?,?,?)`, v.RefID, v.SiteID, v.NativeSourceID, sealed); err != nil {
			return err
		}
	}
	return nil
}
func (a *App) nativeSiteInventory(ctx context.Context) ([]nativeSiteIdentity, error) {
	c := a.Settings()
	out := make([]nativeSiteIdentity, 0, len(c.Sites))
	for _, site := range c.Sites {
		v, err := a.Store.nativeSiteByAppID(ctx, site.ID)
		if err != nil {
			return nil, err
		}
		origin, e := nativeConfiguredSiteOrigin(site)
		if e != nil || v.State != "active" || v.CanonicalOrigin != origin {
			return nil, siteIdentityError()
		}
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].SourceID < out[j].SourceID })
	return out, nil
}
func (v nativeSiteIdentity) publicSourceID() json.Number { return json.Number(fmt.Sprint(v.SourceID)) }
