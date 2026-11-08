package app

import (
	"context"
	"crypto/hmac"
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"
	"sort"
	"strconv"
	"strings"

	"light-media/internal/plugins"
	"light-media/internal/sourceindexer"
)

// This is the finite persisted Site subset actually consumed by the installed
// AutoSignIn. SQLAlchemy has already normalized these native column types;
// CustomSites never passes through this type or this importer.
type reviewedNativeSiteFields struct {
	ID       int64   `json:"id"`
	Name     string  `json:"name"`
	Domain   string  `json:"domain"`
	URL      string  `json:"url"`
	Priority *int64  `json:"pri"`
	Cookie   *string `json:"cookie"`
	UA       *string `json:"ua"`
	APIKey   *string `json:"apikey"`
	Token    *string `json:"token"`
	Proxy    *int64  `json:"proxy"`
	Render   *int64  `json:"render"`
	Public   *int64  `json:"public"`
	Enabled  *bool   `json:"is_active"`
	Timeout  *int64  `json:"timeout"`
}
type reviewedNativeSiteEntry struct {
	ImportRefID string                   `json:"import_ref_id"`
	Fields      reviewedNativeSiteFields `json:"fields"`
}
type reviewedNativeSitesInput struct {
	Version           int                       `json:"version"`
	Mode              string                    `json:"mode"`
	SourceFingerprint string                    `json:"source_fingerprint"`
	SourceHashes      map[string]string         `json:"source_hashes"`
	Sites             []reviewedNativeSiteEntry `json:"sites"`
}
type nativeSourceSiteSnapshot struct {
	Version           int                      `json:"version"`
	SourceFingerprint string                   `json:"source_fingerprint"`
	Fields            reviewedNativeSiteFields `json:"fields"`
	DerivedFields     []string                 `json:"derived_fields,omitempty"`
}
type nativeSourceSiteReceipt struct {
	Version           int                  `json:"version"`
	State             string               `json:"state"`
	SourceFingerprint string               `json:"source_fingerprint"`
	SnapshotID        string               `json:"snapshot_id"`
	SnapshotDigest    string               `json:"snapshot_digest"`
	Ref               importedSiteIdentity `json:"ref"`
	BoundConfig       string               `json:"bound_config"`
	CanonicalOrigin   string               `json:"canonical_origin"`
}

const reviewedNativeSitesMode = "fixed8a_native_site_consumed_subset"

var reviewedNativeSitesHashes = map[string]string{
	"/app/app/db/models/site.py":     "5cda6b9eb37de3cc25c66abfd41832341ea5270bc73971a24425bd00ef63c8e4",
	"/app/app/schemas/site.py":       "aa759e3f1348863b109acdecb765c87a14822a14fbf63bab3fc93584d51bfaa6",
	"/app/app/api/endpoints/site.py": "c352a148fd418f7eb6c91f490d8db4319c91c6ac2d68b920b35b525d1d8372a8",
	"/app/app/db/site_oper.py":       "08e202eb3fc97c7fe5d67d7253867334ada9bdd1747eb23b2d3cbe26da7f0cf0",
	"/app/app/db/__init__.py":        "6da490f32d0de67839249b4e318fecbf4c877ec5acde4eef08634738c4cfafc9",
	"/app/app/utils/string.py":       "b21856bf4b309d20fe9d5a68538fbb0ddc7a2c23bbde424e5700caf57587297a",
}

func invalidReviewedNativeSites() error { return errors.New("invalid_reviewed_native_site_subset") }
func validateReviewedNativeSites(raw []byte) (reviewedNativeSitesInput, error) {
	var in reviewedNativeSitesInput
	if len(raw) == 0 || len(raw) > 4<<20 || sourceSettingsJSON(raw, &in) != nil || in.Version != 1 || in.Mode != reviewedNativeSitesMode || !pluginStorageDigest.MatchString(in.SourceFingerprint) || !reflect.DeepEqual(in.SourceHashes, reviewedNativeSitesHashes) || len(in.Sites) == 0 || len(in.Sites) > 128 {
		return in, invalidReviewedNativeSites()
	}
	fields, e := sourceSettingsObject(raw, "version", "mode", "source_fingerprint", "source_hashes", "sites")
	if e != nil {
		return in, invalidReviewedNativeSites()
	}
	var entries []json.RawMessage
	if json.Unmarshal(fields["sites"], &entries) != nil {
		return in, invalidReviewedNativeSites()
	}
	seen := map[int64]bool{}
	refs := map[string]bool{}
	for i, entry := range entries {
		object, e := sourceSettingsObject(entry, "import_ref_id", "fields")
		if e != nil {
			return in, invalidReviewedNativeSites()
		}
		if _, e = sourceSettingsObject(object["fields"], "id", "name", "domain", "url", "pri", "cookie", "ua", "apikey", "token", "proxy", "render", "public", "is_active", "timeout"); e != nil {
			return in, invalidReviewedNativeSites()
		}
		f := in.Sites[i].Fields
		r := in.Sites[i].ImportRefID
		// Native name/url are NOT NULL in the real model. encoding/json would turn
		// null strings into empty strings, so reject that loss before decoding them.
		var m map[string]json.RawMessage
		_ = json.Unmarshal(object["fields"], &m)
		if string(m["name"]) == "null" || string(m["url"]) == "null" || string(m["domain"]) == "null" || f.ID < 1 || seen[f.ID] || refs[r] || !strings.HasPrefix(r, "sir_") || !pluginStorageDigest.MatchString(strings.TrimPrefix(r, "sir_")) {
			return in, invalidReviewedNativeSites()
		}
		seen[f.ID], refs[r] = true, true
		if _, _, _, e = siteImportOrigin(f.URL); e != nil || f.Domain != sourceindexer.Domain(f.URL) {
			return in, invalidReviewedNativeSites()
		}
		if invalidSiteImportText(f.Name, 64<<10) {
			return in, invalidReviewedNativeSites()
		}
		for _, v := range []*string{f.Cookie, f.UA, f.APIKey, f.Token} {
			if v != nil && invalidSiteImportText(*v, 64<<10) {
				return in, invalidReviewedNativeSites()
			}
		}
	}
	return in, nil
}

// The normal management projection is intentionally bounded and unchanged.
// Raw NULL/integer flags, priority, timeout, name and URL live only in the
// sealed source snapshot; this projection never claims those are source values.
func reviewedNativeSiteUI(f reviewedNativeSiteFields, current SiteConfig) (SiteConfig, error) {
	kind, origin, _, e := siteImportOrigin(f.URL)
	if e != nil {
		return current, invalidReviewedNativeSites()
	}
	out := current
	out.Kind, out.BaseURL, out.Domain = kind, origin, f.Domain
	out.Name = strings.TrimSpace(f.Name)
	if len([]rune(out.Name)) > 64 {
		return current, invalidReviewedNativeSites()
	}
	str := func(p *string) string {
		if p == nil {
			return ""
		}
		return *p
	}
	flag := func(p *int64) bool { return p != nil && *p != 0 }
	bounded := func(p *int64, max int64) int {
		if p == nil || *p < 0 || *p > max {
			return 0
		}
		return int(*p)
	}
	out.Cookie, out.UserAgent, out.APIKey, out.Token = str(f.Cookie), str(f.UA), str(f.APIKey), str(f.Token)
	out.Proxy, out.Render, out.Public = flag(f.Proxy), flag(f.Render), flag(f.Public)
	out.Enabled = f.Enabled != nil && *f.Enabled
	out.Priority, out.Timeout = bounded(f.Priority, 10000), bounded(f.Timeout, 120)
	if normalizeManagedSite(&out) != nil {
		return current, invalidReviewedNativeSites()
	}
	return out, nil
}

// map values keep int64/None instead of going through float64 or typed bools.
func (f reviewedNativeSiteFields) row() map[string]any {
	integer := func(p *int64) any {
		if p == nil {
			return nil
		}
		return *p
	}
	str := func(p *string) any {
		if p == nil {
			return nil
		}
		return *p
	}
	var active any
	if f.Enabled != nil {
		active = *f.Enabled
	}
	return map[string]any{"id": f.ID, "name": f.Name, "domain": f.Domain, "url": f.URL, "pri": integer(f.Priority), "cookie": str(f.Cookie), "ua": str(f.UA), "apikey": str(f.APIKey), "token": str(f.Token), "proxy": integer(f.Proxy), "render": integer(f.Render), "public": integer(f.Public), "is_active": active, "timeout": integer(f.Timeout)}
}
func nativeSourceSiteError() error {
	return bad("SOURCE_SITE_PROVENANCE_CONFLICT", "原站点绑定记录未通过核对")
}
func (s *Store) nativeSourceBoundConfig(site SiteConfig) string {
	return s.siteIdentityMAC("source-native-config", site)
}
func (s *Store) readNativeSourceRef(ctx context.Context, q siteIdentityReader, id string) (importedSiteIdentity, error) {
	var v importedSiteIdentity
	var sealed []byte
	var siteID string
	var nativeID int64
	if q.QueryRowContext(ctx, `SELECT site_id,native_source_id,sealed FROM native_site_import_refs WHERE ref_id=?`, id).Scan(&siteID, &nativeID, &sealed) != nil || s.Unseal(sealed, &v) != nil || v.Version != 1 || v.RefID != id || v.SiteID != siteID || v.NativeSourceID != nativeID || v.Status != "unclaimed" || v.OriginalSourceID < 1 {
		return v, nativeSourceSiteError()
	}
	want := "sir_" + s.siteIdentityMAC("import-ref", []any{v.DocumentFingerprint, v.Ordinal, v.OriginalSourceID, v.NativeSourceID, v.CanonicalOrigin})
	if !hmac.Equal([]byte(want), []byte(id)) {
		return v, nativeSourceSiteError()
	}
	return v, nil
}
func (s *Store) readNativeSourceReceipts(ctx context.Context, q interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}, siteID string) ([]nativeSourceSiteReceipt, error) {
	rows, e := q.QueryContext(ctx, `SELECT source_fingerprint,original_source_id,native_source_id,site_id,sealed FROM native_site_migration_receipts WHERE site_id=?`, siteID)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []nativeSourceSiteReceipt{}
	for rows.Next() {
		var fingerprint, id string
		var original, native int64
		var sealed []byte
		var v nativeSourceSiteReceipt
		if rows.Scan(&fingerprint, &original, &native, &id, &sealed) != nil || s.Unseal(sealed, &v) != nil || v.Version != 1 || (v.State != "active" && v.State != "retired") || v.SourceFingerprint != fingerprint || v.Ref.SiteID != id || v.Ref.OriginalSourceID != original || v.Ref.NativeSourceID != native || !pluginStorageDigest.MatchString(v.BoundConfig) || !strings.HasPrefix(v.SnapshotID, "sns_") || !pluginStorageDigest.MatchString(v.SnapshotDigest) {
			return nil, nativeSourceSiteError()
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func (s *Store) nativeSourceSiteFields(ctx context.Context, site SiteConfig, identity nativeSiteIdentity) (*reviewedNativeSiteFields, int64, string, error) {
	receipts, e := s.readNativeSourceReceipts(ctx, s.DB, site.ID)
	if e != nil {
		return nil, 0, "", e
	}
	sourceID := identity.SourceID
	var active *nativeSourceSiteReceipt
	for i := range receipts {
		r := &receipts[i]
		// Retirement removes raw field authority, never the original stable ID.
		// A name/URL edit cannot remap an existing original plugin selection.
		if r.Ref.NativeSourceID != identity.SourceID || r.Ref.SiteID != site.ID || identity.State != "active" || i > 0 && r.Ref.OriginalSourceID != sourceID {
			return nil, 0, "", nativeSourceSiteError()
		}
		ref, e := s.readNativeSourceRef(ctx, s.DB, r.Ref.RefID)
		if e != nil || ref != r.Ref {
			return nil, 0, "", nativeSourceSiteError()
		}
		sourceID = r.Ref.OriginalSourceID
		if r.State == "active" {
			if active != nil {
				return nil, 0, "", nativeSourceSiteError()
			}
			active = r
		}
	}
	if active == nil {
		return nil, sourceID, "", nil
	}
	r := *active
	if !hmac.Equal([]byte(r.BoundConfig), []byte(s.nativeSourceBoundConfig(site))) || r.CanonicalOrigin != identity.CanonicalOrigin {
		return nil, 0, "", nativeSourceSiteError()
	}
	var sealed []byte
	var fingerprint, digest string
	var snapshot nativeSourceSiteSnapshot
	if s.DB.QueryRowContext(ctx, `SELECT source_fingerprint,snapshot_digest,sealed FROM native_site_source_snapshots WHERE snapshot_id=?`, r.SnapshotID).Scan(&fingerprint, &digest, &sealed) != nil || s.Unseal(sealed, &snapshot) != nil || snapshot.Version != 1 || fingerprint != r.SourceFingerprint || digest != r.SnapshotDigest || snapshot.Fields.ID != sourceID || s.siteIdentityMAC("source-native-fingerprint", snapshot.SourceFingerprint) != fingerprint || s.siteIdentityMAC("source-native-snapshot", snapshot) != digest || r.SnapshotID != "sns_"+s.siteIdentityMAC("source-native-snapshot-id", []string{fingerprint, digest}) {
		return nil, 0, "", nativeSourceSiteError()
	}
	return &snapshot.Fields, sourceID, s.siteIdentityMAC("source-native-proof", r), nil
}

type nativeSourceSiteImportsKey struct{}

// Source projection is an explicit request opt-in. Ordinary sites.read keeps
// its existing wire shape and typed ordering even after provenance is imported.
func (a *App) originalSourceConfiguredRows(ctx context.Context, in plugins.SiteHostRequestV1) (plugins.SiteHostResultV1, error) {
	out := plugins.SiteHostResultV1{SchemaVersion: 1, Items: []plugins.SiteHostRowV1{}}
	rows, e := a.originalSourceSiteRows(ctx)
	if e != nil {
		return out, e
	}
	if in.OrderByPriority {
		sortNativeSourceRows(rows)
	}
	for _, r := range rows {
		if in.ID != "" && in.ID != r.site.ID || in.ActiveOnly && !sourceNativeActive(r.fields["is_active"]) {
			continue
		}
		origin, _ := nativeConfiguredSiteOrigin(r.site)
		v := plugins.SiteHostRowV1{ID: r.site.ID, SourceID: r.sourceID, Name: r.site.Name, Kind: r.site.Kind, Domain: originalBrushInventoryDomain(r.site, origin), URL: origin, Priority: r.site.Priority, Enabled: r.site.Enabled, Public: r.site.Public, Proxy: r.site.Proxy, Render: r.site.Render}
		if r.provenance != "" {
			safe := map[string]any{}
			for _, k := range []string{"id", "name", "domain", "url", "pri", "is_active", "public", "proxy", "render"} {
				safe[k] = r.fields[k]
			}
			v.SourceFields = sourceSiteJSON(safe)
		}
		out.Items = append(out.Items, v)
	}
	return out, nil
}
func sourceNativeActive(value any) bool { v, ok := value.(bool); return ok && v }

// Only int64/null native priority is admitted; local typed priority is int.
func nativeSourcePriority(v any) (int64, bool) {
	switch n := v.(type) {
	case nil:
		return 0, false
	case int:
		return int64(n), true
	case int64:
		return n, true
	}
	return 0, false
}
func sortNativeSourceRows(rows []originalSourceSiteRow) {
	sort.SliceStable(rows, func(i, j int) bool {
		a, an := nativeSourcePriority(rows[i].fields["pri"])
		b, bn := nativeSourcePriority(rows[j].fields["pri"])
		if an != bn {
			return !an
		}
		if a != b {
			return a < b
		}
		return rows[i].sourceID < rows[j].sourceID
	})
}

type ReviewedNativeSitesImportResult struct {
	State         string `json:"state"`
	Sites         int    `json:"sites"`
	Writes        int    `json:"writes"`
	Jobs          int    `json:"jobs_queued"`
	ExternalCalls int    `json:"external_calls"`
}

func ValidateReviewedNativeSitesInput(raw []byte) (ReviewedNativeSitesImportResult, error) {
	in, e := validateReviewedNativeSites(raw)
	if e != nil {
		return ReviewedNativeSitesImportResult{}, e
	}
	return ReviewedNativeSitesImportResult{State: "validated", Sites: len(in.Sites)}, nil
}

// Attaches only a reviewed finite original DB capture to already imported,
// exactly matching native sites. It cannot create an identity, guess lost raw
// fields, alter normal settings or import another source's configuration.
func ImportReviewedNativeSites(ctx context.Context, dir string, raw []byte, apply bool) (ReviewedNativeSitesImportResult, error) {
	in, e := validateReviewedNativeSites(raw)
	if e != nil {
		return ReviewedNativeSitesImportResult{}, e
	}
	target, e := openSourceSettingsTarget(ctx, dir, apply)
	if e != nil {
		return ReviewedNativeSitesImportResult{}, e
	}
	defer target.close()
	s, tx := target.p.store, target.tx
	fail := func() (ReviewedNativeSitesImportResult, error) {
		return ReviewedNativeSitesImportResult{}, errors.New("source_native_site_binding_unconfirmed")
	}
	var sealed []byte
	var config Settings
	if tx.QueryRowContext(ctx, `SELECT payload FROM records WHERE kind='config' AND id='main'`).Scan(&sealed) != nil || s.Unseal(sealed, &config) != nil {
		return fail()
	}
	fingerprint := s.siteIdentityMAC("source-native-fingerprint", in.SourceFingerprint)
	type staged struct {
		snapshot nativeSourceSiteSnapshot
		receipt  nativeSourceSiteReceipt
		exists   bool
	}
	stages := []staged{}
	boundSites := map[string]bool{}
	for _, entry := range in.Sites {
		ref, e := s.readNativeSourceRef(ctx, tx, entry.ImportRefID)
		if e != nil || ref.OriginalSourceID != entry.Fields.ID || boundSites[ref.SiteID] {
			return fail()
		}
		if err := s.nativeSourceOriginalBindingTx(ctx, tx, ref); err != nil {
			return fail()
		}
		boundSites[ref.SiteID] = true
		native, e := s.readNativeSiteIdentity(ctx, tx, "site_id", ref.SiteID)
		if e != nil || native.State != "active" || native.SourceID != ref.NativeSourceID || native.CanonicalOrigin != ref.CanonicalOrigin {
			return fail()
		}
		var current *SiteConfig
		for i := range config.Sites {
			if config.Sites[i].ID == ref.SiteID {
				current = &config.Sites[i]
				break
			}
		}
		if current == nil {
			return fail()
		}
		_, e = reviewedNativeSiteUI(entry.Fields, *current)
		if e != nil || !nativeSourceProjectionMatches(entry.Fields, *current) {
			return fail()
		}
		snapshot := nativeSourceSiteSnapshot{Version: 1, SourceFingerprint: in.SourceFingerprint, Fields: entry.Fields}
		digest := s.siteIdentityMAC("source-native-snapshot", snapshot)
		id := "sns_" + s.siteIdentityMAC("source-native-snapshot-id", []string{fingerprint, digest})
		receipt := nativeSourceSiteReceipt{Version: 1, State: "active", SourceFingerprint: fingerprint, SnapshotID: id, SnapshotDigest: digest, Ref: ref, BoundConfig: s.nativeSourceBoundConfig(*current), CanonicalOrigin: ref.CanonicalOrigin}
		existing, e := s.readNativeSourceReceipts(ctx, tx, ref.SiteID)
		if e != nil {
			return fail()
		}
		found := false
		for _, old := range existing {
			if old.Ref.OriginalSourceID != ref.OriginalSourceID || old.Ref.NativeSourceID != ref.NativeSourceID {
				return fail()
			}
			if old.State == "active" {
				if old != receipt {
					return fail()
				}
				found = true
			} else if old.SourceFingerprint == fingerprint && old.Ref.OriginalSourceID == ref.OriginalSourceID {
				return fail()
			}
		}
		if found {
			var old []byte
			var f, d string
			var value nativeSourceSiteSnapshot
			if tx.QueryRowContext(ctx, `SELECT source_fingerprint,snapshot_digest,sealed FROM native_site_source_snapshots WHERE snapshot_id=?`, id).Scan(&f, &d, &old) != nil || s.Unseal(old, &value) != nil || f != fingerprint || d != digest || !reflect.DeepEqual(value, snapshot) {
				return fail()
			}
		}
		stages = append(stages, staged{snapshot, receipt, found})
	}
	// Original ids must stay unique across the complete source-facing inventory,
	// including locally created rows that have no admitted source provenance.
	ids := map[int64]bool{}
	for _, site := range config.Sites {
		identity, e := s.readNativeSiteIdentity(ctx, tx, "site_id", site.ID)
		if e != nil {
			return fail()
		}
		id := identity.SourceID
		for _, st := range stages {
			if st.receipt.Ref.SiteID == site.ID {
				id = st.receipt.Ref.OriginalSourceID
			}
		}
		if !boundSites[site.ID] {
			existing, e := s.readNativeSourceReceipts(ctx, tx, site.ID)
			if e != nil {
				return fail()
			}
			for i, r := range existing {
				if i > 0 && id != r.Ref.OriginalSourceID {
					return fail()
				}
				id = r.Ref.OriginalSourceID
			}
		}
		if ids[id] {
			return fail()
		}
		ids[id] = true
	}
	result := ReviewedNativeSitesImportResult{State: "ready", Sites: len(stages)}
	pending := 0
	for _, v := range stages {
		if !v.exists {
			pending++
		}
	}
	if pending == 0 {
		result.State = "already_imported"
		return result, nil
	}
	if !apply {
		return result, nil
	}
	for _, v := range stages {
		if v.exists {
			continue
		}
		r := v.receipt
		snapshot, e := s.Seal(v.snapshot)
		if e != nil {
			return fail()
		}
		receipt, e := s.Seal(r)
		if e != nil {
			return fail()
		}
		if _, e = tx.ExecContext(ctx, `INSERT INTO native_site_source_snapshots(snapshot_id,source_fingerprint,snapshot_digest,sealed) VALUES(?,?,?,?)`, r.SnapshotID, fingerprint, r.SnapshotDigest, snapshot); e != nil {
			return fail()
		}
		if _, e = tx.ExecContext(ctx, `INSERT INTO native_site_migration_receipts(source_fingerprint,original_source_id,native_source_id,site_id,sealed) VALUES(?,?,?,?,?)`, fingerprint, r.Ref.OriginalSourceID, r.Ref.NativeSourceID, r.Ref.SiteID, receipt); e != nil {
			return fail()
		}
	}
	// Reserve the admitted original namespace in SQLite's existing native ID
	// allocator. Permanent native tombstones must never be reused or collide
	// with an admitted original ID when a later site is created.
	var reserved int64
	for id := range ids {
		if id > reserved {
			reserved = id
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE sqlite_sequence SET seq=? WHERE name='native_site_identities' AND seq<?`, reserved, reserved); err != nil {
		return fail()
	}
	if tx.Commit() != nil {
		return ReviewedNativeSitesImportResult{}, errors.New("source_native_site_commit_unconfirmed")
	}
	result.State, result.Writes = "imported", 2*pending
	return result, nil
}

// Public numeric identity is shared by source/ordinary site resources and
// ordinary events. A retired raw snapshot keeps its original stable mapping.
// Callers holding a transaction pass that same reader rather than Store.DB.
func (s *Store) nativeSourcePublicSiteID(ctx context.Context, q interface {
	siteIdentityReader
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}, identity nativeSiteIdentity) (int64, error) {
	rows, err := s.readNativeSourceReceipts(ctx, q, identity.SiteID)
	if err != nil {
		return 0, err
	}
	sourceID := identity.SourceID
	for i, row := range rows {
		if row.Ref.NativeSourceID != identity.SourceID || row.Ref.SiteID != identity.SiteID || i > 0 && row.Ref.OriginalSourceID != sourceID {
			return 0, nativeSourceSiteError()
		}
		ref, err := s.readNativeSourceRef(ctx, q, row.Ref.RefID)
		if err != nil || ref != row.Ref {
			return 0, nativeSourceSiteError()
		}
		sourceID = row.Ref.OriginalSourceID
	}
	return sourceID, nil
}
func (s *Store) nativeSourceEventSiteID(ctx context.Context, identity nativeSiteIdentity) (json.Number, error) {
	id, err := s.nativeSourcePublicSiteID(ctx, s.DB, identity)
	if err != nil {
		return "", err
	}
	return json.Number(strconv.FormatInt(id, 10)), nil
}

// Resolve the same public ID accepted by the fixed ordinary core consumers.
// Native ID lookup alone may target a different imported original identity.
func (s *Store) nativeSiteByPublicSourceID(ctx context.Context, id int64) (nativeSiteIdentity, error) {
	if id < 1 {
		return nativeSiteIdentity{}, sql.ErrNoRows
	}
	candidates := map[string]bool{}
	local, err := s.nativeSiteByID(ctx, id)
	if err == nil {
		candidates[local.SiteID] = true
	} else if !errors.Is(err, sql.ErrNoRows) {
		return nativeSiteIdentity{}, err
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT DISTINCT site_id FROM native_site_migration_receipts WHERE original_source_id=?`, id)
	if err != nil {
		return nativeSiteIdentity{}, err
	}
	for rows.Next() {
		var site string
		if err = rows.Scan(&site); err != nil {
			rows.Close()
			return nativeSiteIdentity{}, err
		}
		candidates[site] = true
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nativeSiteIdentity{}, err
	}
	var result nativeSiteIdentity
	found := false
	for site := range candidates {
		identity, err := s.nativeSiteByAppID(ctx, site)
		if err != nil {
			return nativeSiteIdentity{}, err
		}
		public, err := s.nativeSourcePublicSiteID(ctx, s.DB, identity)
		if err != nil {
			return nativeSiteIdentity{}, err
		}
		if public != id {
			continue
		}
		if found {
			return nativeSiteIdentity{}, nativeSourceSiteError()
		}
		result = identity
		found = true
	}
	if !found {
		return nativeSiteIdentity{}, sql.ErrNoRows
	}
	return result, nil
}

// An original ID cannot be rebound to a new native object, including after
// deletion. Its authenticated retired mapping is still used by ordinary
// historical events; accepting another target would make that ID ambiguous.
func (s *Store) nativeSourceOriginalBindingTx(ctx context.Context, tx *sql.Tx, ref importedSiteIdentity) error {
	rows, err := tx.QueryContext(ctx, `SELECT DISTINCT site_id FROM native_site_migration_receipts WHERE original_source_id=?`, ref.OriginalSourceID)
	if err != nil {
		return err
	}
	sites := []string{}
	for rows.Next() {
		var site string
		if err = rows.Scan(&site); err != nil {
			rows.Close()
			return err
		}
		sites = append(sites, site)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, site := range sites {
		identity, err := s.readNativeSiteIdentity(ctx, tx, "site_id", site)
		if err != nil {
			return err
		}
		receipts, err := s.readNativeSourceReceipts(ctx, tx, site)
		if err != nil {
			return err
		}
		for _, row := range receipts {
			if row.Ref.OriginalSourceID != ref.OriginalSourceID {
				continue
			}
			original, err := s.readNativeSourceRef(ctx, tx, row.Ref.RefID)
			if err != nil || original != row.Ref || row.Ref.NativeSourceID != identity.SourceID || row.Ref.SiteID != identity.SiteID || row.Ref.NativeSourceID != ref.NativeSourceID || row.Ref.SiteID != ref.SiteID {
				return nativeSourceSiteError()
			}
		}
	}
	return nil
}
