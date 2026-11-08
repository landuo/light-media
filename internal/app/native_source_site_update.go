package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/url"
	"reflect"
	"sort"
	"strings"

	"light-media/internal/sourceindexer"
)

// The management model already validates this legal domain alias. Source DB
// domain is instead derived from the retained source URL, as update_site does.
// Both representations stay bound by the complete native config MAC.
func nativeSourceProjectionMatches(f reviewedNativeSiteFields, current SiteConfig) bool {
	projected, e := reviewedNativeSiteUI(f, current)
	if e != nil {
		return false
	}
	checked := current
	if normalizeManagedSite(&checked) != nil || checked.Domain != current.Domain {
		return false
	}
	projected.Domain = current.Domain
	return reflect.DeepEqual(projected, current)
}
func nativeSourceRoot(raw string) string {
	u, e := url.Parse(strings.TrimSpace(raw))
	if e != nil {
		return ""
	}
	return u.Scheme + "://" + u.Host + "/"
}
func nativeSourceChangedFields(old, current reviewedNativeSiteFields) []string {
	a, b := old.row(), current.row()
	out := []string{}
	for key, value := range a {
		if !reflect.DeepEqual(value, b[key]) {
			out = append(out, key)
		}
	}
	sort.Strings(out)
	return out
}

// This is the existing complete native settings transition, not a replacement
// partial-JSON API. Unchanged UI projections retain their exact imported NULL,
// string, integer and URL literal; only actual native field changes overwrite.
func nativeSourceApplyChanged(f reviewedNativeSiteFields, old, current SiteConfig) reviewedNativeSiteFields {
	str := func(value string) *string { return &value }
	num := func(value int) *int64 { v := int64(value); return &v }
	flag := func(value bool) *int64 {
		v := int64(0)
		if value {
			v = 1
		}
		return &v
	}
	if old.Name != current.Name {
		f.Name = current.Name
	}
	if old.BaseURL != current.BaseURL {
		f.URL = nativeSourceRoot(current.BaseURL)
		f.Domain = sourceindexer.Domain(f.URL)
	}
	if old.Priority != current.Priority {
		f.Priority = num(current.Priority)
	}
	if old.Cookie != current.Cookie {
		f.Cookie = str(current.Cookie)
	}
	if old.UserAgent != current.UserAgent {
		f.UA = str(current.UserAgent)
	}
	if old.APIKey != current.APIKey {
		f.APIKey = str(current.APIKey)
	}
	if old.Token != current.Token {
		f.Token = str(current.Token)
	}
	if old.Proxy != current.Proxy {
		f.Proxy = flag(current.Proxy)
	}
	if old.Render != current.Render {
		f.Render = flag(current.Render)
	}
	if old.Public != current.Public {
		f.Public = flag(current.Public)
	}
	if old.Enabled != current.Enabled {
		v := current.Enabled
		f.Enabled = &v
	}
	if old.Timeout != current.Timeout {
		f.Timeout = num(current.Timeout)
	}
	return f
}

type nativeSourceSiteEditKey struct{}

// Raw-free native management gets only the two pre-normalization literals
// whose intentional edits can otherwise normalize to an unchanged typed value.
func nativeSourceApplyEdit(f reviewedNativeSiteFields, old, current SiteConfig, input SiteConfig) reviewedNativeSiteFields {
	// The original full update normalizes URL/domain on every save,
	// independently of which visible field changed. Priority-only updates do not.
	f.URL = nativeSourceRoot(f.URL)
	f.Domain = sourceindexer.Domain(f.URL)
	if input.Name != old.Name {
		candidate := f
		candidate.Name = input.Name
		if nativeSourceProjectionMatches(candidate, current) {
			f = candidate
		}
	}
	if input.BaseURL != old.BaseURL {
		candidate := f
		candidate.URL = nativeSourceRoot(input.BaseURL)
		candidate.Domain = sourceindexer.Domain(candidate.URL)
		if nativeSourceProjectionMatches(candidate, current) {
			f = candidate
		}
	}
	return f
}

// Only these known Site columns are interpreted. Unknown export extensions are
// never copied into the sealed raw subset. Existing native empty-secret
// preservation wins whenever a submitted raw field does not project to what
// management actually saved.
func nativeSourceApplyImport(f reviewedNativeSiteFields, current SiteConfig, raw json.RawMessage) (reviewedNativeSiteFields, error) {
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		return f, nativeSourceSiteError()
	}
	original, e := importedNumericSiteID(raw)
	if e != nil || original > 0 && original != f.ID {
		return f, nativeSourceSiteError()
	}
	pick := func(names ...string) (json.RawMessage, bool) {
		for _, name := range names {
			if v, ok := fields[name]; ok {
				return v, true
			}
		}
		return nil, false
	}
	optionalString := func(raw json.RawMessage) (*string, error) {
		if strings.TrimSpace(string(raw)) == "null" {
			return nil, nil
		}
		var v string
		if json.Unmarshal(raw, &v) != nil {
			return nil, nativeSourceSiteError()
		}
		return &v, nil
	}
	apply := func(candidate reviewedNativeSiteFields) {
		if nativeSourceProjectionMatches(candidate, current) {
			f = candidate
		}
	}
	if raw, ok := pick("name"); ok {
		v, e := optionalString(raw)
		if e != nil || v == nil {
			return f, nativeSourceSiteError()
		}
		candidate := f
		candidate.Name = *v
		apply(candidate)
	}
	if raw, ok := pick("base_url", "url"); ok {
		v, e := optionalString(raw)
		if e != nil || v == nil {
			return f, nativeSourceSiteError()
		}
		candidate := f
		candidate.URL = nativeSourceRoot(*v)
		candidate.Domain = sourceindexer.Domain(candidate.URL)
		apply(candidate)
	}
	for _, key := range []string{"cookie", "ua", "apikey", "token"} {
		names := []string{key}
		if key == "ua" {
			names = append(names, "user_agent")
		}
		if key == "apikey" {
			names = []string{"api_key", "apikey"}
		}
		raw, ok := pick(names...)
		if !ok {
			continue
		}
		v, e := optionalString(raw)
		if e != nil {
			return f, e
		}
		candidate := f
		switch key {
		case "cookie":
			candidate.Cookie = v
		case "ua":
			candidate.UA = v
		case "apikey":
			candidate.APIKey = v
		case "token":
			candidate.Token = v
		}
		apply(candidate)
	}
	for _, key := range []string{"pri", "timeout", "proxy", "render", "public"} {
		raw, ok := pick(key)
		if !ok {
			continue
		}
		var v *int64
		if strings.TrimSpace(string(raw)) != "null" {
			var number int
			if key == "pri" || key == "timeout" {
				var e error
				number, e = siteImportInt(raw)
				if e != nil {
					return f, nativeSourceSiteError()
				}
			} else {
				flag, e := siteImportBool(raw)
				if e != nil {
					return f, nativeSourceSiteError()
				}
				if flag {
					number = 1
				}
			}
			n := int64(number)
			v = &n
		}
		candidate := f
		switch key {
		case "pri":
			candidate.Priority = v
		case "timeout":
			candidate.Timeout = v
		case "proxy":
			candidate.Proxy = v
		case "render":
			candidate.Render = v
		case "public":
			candidate.Public = v
		}
		apply(candidate)
	}
	if raw, ok := pick("enabled", "is_active"); ok {
		var v *bool
		if strings.TrimSpace(string(raw)) != "null" {
			flag, e := siteImportBool(raw)
			if e != nil {
				return f, nativeSourceSiteError()
			}
			v = &flag
		}
		candidate := f
		candidate.Enabled = v
		apply(candidate)
	}
	return f, nil
}
func (s *Store) readNativeSourceSnapshotTx(ctx context.Context, tx *sql.Tx, r nativeSourceSiteReceipt) (nativeSourceSiteSnapshot, error) {
	var value nativeSourceSiteSnapshot
	var fingerprint, digest string
	var sealed []byte
	if tx.QueryRowContext(ctx, `SELECT source_fingerprint,snapshot_digest,sealed FROM native_site_source_snapshots WHERE snapshot_id=?`, r.SnapshotID).Scan(&fingerprint, &digest, &sealed) != nil || s.Unseal(sealed, &value) != nil || value.Version != 1 || value.Fields.ID != r.Ref.OriginalSourceID || fingerprint != r.SourceFingerprint || digest != r.SnapshotDigest || s.siteIdentityMAC("source-native-fingerprint", value.SourceFingerprint) != fingerprint || s.siteIdentityMAC("source-native-snapshot", value) != digest || r.SnapshotID != "sns_"+s.siteIdentityMAC("source-native-snapshot-id", []string{fingerprint, digest}) {
		return value, nativeSourceSiteError()
	}
	return value, nil
}
func (s *Store) writeNativeSourceSnapshotTx(ctx context.Context, tx *sql.Tx, value nativeSourceSiteSnapshot, r *nativeSourceSiteReceipt) error {
	r.SnapshotDigest = s.siteIdentityMAC("source-native-snapshot", value)
	r.SnapshotID = "sns_" + s.siteIdentityMAC("source-native-snapshot-id", []string{r.SourceFingerprint, r.SnapshotDigest})
	sealed, e := s.Seal(value)
	if e != nil {
		return e
	}
	if _, e = tx.ExecContext(ctx, `INSERT OR IGNORE INTO native_site_source_snapshots(snapshot_id,source_fingerprint,snapshot_digest,sealed) VALUES(?,?,?,?)`, r.SnapshotID, r.SourceFingerprint, r.SnapshotDigest, sealed); e != nil {
		return e
	}
	stored, e := s.readNativeSourceSnapshotTx(ctx, tx, *r)
	if e != nil || !reflect.DeepEqual(stored, value) {
		return nativeSourceSiteError()
	}
	return nil
}

// Names/URLs/credentials are updated within the existing settings transaction.
// Deletion retires authority; ordinary edits rebind the finite source subset,
// preserving every unchanged native column and the stable original ID.
func (a *App) reconcileNativeSourceSiteValuesTx(ctx context.Context, tx *sql.Tx, previous, next Settings) error {
	imports, _ := ctx.Value(nativeSourceSiteImportsKey{}).(map[string]json.RawMessage)
	edit, _ := ctx.Value(nativeSourceSiteEditKey{}).(*SiteConfig)
	current := map[string]SiteConfig{}
	for _, s := range next.Sites {
		current[s.ID] = s
	}
	for _, old := range previous.Sites {
		value, exists := current[old.ID]
		input, imported := imports[old.ID]
		hasEdit := edit != nil && edit.ID == old.ID
		if exists && !imported && !hasEdit && a.Store.nativeSourceBoundConfig(old) == a.Store.nativeSourceBoundConfig(value) {
			continue
		}
		rows, e := a.Store.readNativeSourceReceipts(ctx, tx, old.ID)
		if e != nil {
			return e
		}
		for _, r := range rows {
			if r.State != "active" {
				continue
			}
			if r.BoundConfig != a.Store.nativeSourceBoundConfig(old) {
				return nativeSourceSiteError()
			}
			ref, e := a.Store.readNativeSourceRef(ctx, tx, r.Ref.RefID)
			if e != nil || ref != r.Ref {
				return nativeSourceSiteError()
			}
			if !exists {
				r.State = "retired"
			} else {
				snapshot, e := a.Store.readNativeSourceSnapshotTx(ctx, tx, r)
				if e != nil {
					return e
				}
				fields := nativeSourceApplyChanged(snapshot.Fields, old, value)
				if hasEdit {
					fields = nativeSourceApplyEdit(fields, old, value, *edit)
				}
				if imported {
					fields, e = nativeSourceApplyImport(fields, value, input)
					if e != nil {
						return e
					}
				}
				if !nativeSourceProjectionMatches(fields, value) {
					return nativeSourceSiteError()
				}
				changed := nativeSourceChangedFields(snapshot.Fields, fields)
				seen := map[string]bool{}
				for _, key := range snapshot.DerivedFields {
					seen[key] = true
				}
				for _, key := range changed {
					seen[key] = true
				}
				snapshot.DerivedFields = nil
				for key := range seen {
					snapshot.DerivedFields = append(snapshot.DerivedFields, key)
				}
				sort.Strings(snapshot.DerivedFields)
				snapshot.Fields = fields
				origin, e := nativeConfiguredSiteOrigin(value)
				if e != nil {
					return e
				}
				r.CanonicalOrigin = origin
				r.BoundConfig = a.Store.nativeSourceBoundConfig(value)
				if e = a.Store.writeNativeSourceSnapshotTx(ctx, tx, snapshot, &r); e != nil {
					return e
				}
			}
			sealed, e := a.Store.Seal(r)
			if e != nil {
				return e
			}
			if _, e = tx.ExecContext(ctx, `UPDATE native_site_migration_receipts SET sealed=? WHERE source_fingerprint=? AND original_source_id=? AND site_id=?`, sealed, r.SourceFingerprint, r.Ref.OriginalSourceID, old.ID); e != nil {
				return e
			}
		}
	}
	return nil
}
