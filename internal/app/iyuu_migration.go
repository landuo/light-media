package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/url"
	"sort"
	"strings"

	iyuu "light-media/internal/plugins/iyuuautoseed"
)

const reviewedIYUUSourceSHA = "a47d36432e87da046af025fc76281d99c714064a8fe05bdf8b11e83d2c683a3f"
const reviewedIYUUConfigSHA = "490bd44c3e18de1919fe758d023ba9dec381eb94f9637bd6e9e2d4a724702046"
const reviewedIYUUDataSHA = "e2fcfc3ca75d4084336683416ed7ff5d4c857ff4f6f449184cc6a8e265ba6623"
const reviewedIYUUSitesSHA = "0ae615c182f1d7b6518dc5999b83103ced7ee6d08e3c9208e7c46c621ace5c41"
const reviewedIYUUReferencesSHA = "c27b0670172349043d6a9dd003b4e33c878d66cfd348514b34f57e3b5e18aafb"
const reviewedIYUUProtocolPrefix = "reviewed-moviepilot-iyuu/v1:"
const reviewedIYUUMappingKind = "plugin_original_iyuu_references"

type reviewedIYUUSite struct {
	OriginalID int64  `json:"original_source_id"`
	URL        string `json:"url"`
}
type reviewedIYUUDownloader struct {
	Name     string `json:"name"`
	Kind     string `json:"kind"`
	Host     string `json:"host"`
	Username string `json:"username"`
	Password string `json:"password"`
	Enabled  bool   `json:"enabled"`
}
type reviewedIYUUSnapshot struct {
	Version         int                      `json:"version"`
	SourceContainer string                   `json:"source_container"`
	SourceSHA       string                   `json:"source_sha256"`
	InstalledCount  int                      `json:"installed_count"`
	Config          string                   `json:"config"`
	Data            map[string]string        `json:"data"`
	Sites           []reviewedIYUUSite       `json:"sites"`
	Downloaders     []reviewedIYUUDownloader `json:"downloaders"`
	FileCount       int                      `json:"file_count"`
	SymlinkCount    int                      `json:"symlink_count"`
}
type reviewedIYUUBoundSite struct {
	OriginalID, NativeID int64
	SiteID, Origin       string
}
type reviewedIYUUBoundDownloader struct {
	SourceNameSHA, NativeID string
}
type reviewedIYUUImportBinding struct {
	Version                                              int
	SourceFingerprint, ConfigSHA, DataSHA, ReferencesSHA string
	SettingsSHA, MappedConfigSHA                         string
	Generation                                           int64
	Sites                                                []reviewedIYUUBoundSite
	Downloaders                                          []reviewedIYUUBoundDownloader
}

// Reviewed wire hashes retain the declared struct-field order used by the
// independent collector. Runtime journal canonicalization sorts object keys.
func reviewedIYUUJSON(v any) json.RawMessage { raw, _ := json.Marshal(v); return raw }

func invalidReviewedIYUU() error { return errors.New("invalid_reviewed_iyuu_snapshot") }

// Match the effective daemon endpoint, including a configured RPC/proxy path.
// Only original no-scheme input receives MoviePilot's documented http prefix.
func reviewedIYUUDownloaderEndpoint(kind, raw string, original bool) (string, error) {
	if original && !strings.HasPrefix(raw, "http") {
		raw = "http://" + raw
	}
	u, e := url.Parse(raw)
	if e != nil || u.Hostname() == "" || u.User != nil || u.Opaque != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || (u.Scheme != "http" && u.Scheme != "https") || strings.ContainsAny(u.Path, "\x00\r\n\\") || strings.Contains(u.EscapedPath(), "%") {
		return "", invalidReviewedIYUU()
	}
	u.Host = strings.ToLower(u.Host)
	if u.Scheme == "http" && u.Port() == "80" || u.Scheme == "https" && u.Port() == "443" {
		u.Host = u.Hostname()
		if strings.Contains(u.Host, ":") {
			u.Host = "[" + u.Host + "]"
		}
	}
	u.Path = strings.TrimRight(u.Path, "/")
	if original && kind == "transmission" {
		// Original Transmission parses the URL path but does not pass it to
		// transmission_rpc.Client, which uses its default RPC endpoint.
		u.Path = "/transmission/rpc"
	}
	if kind == "transmission" && u.Path == "" {
		u.Path = "/transmission/rpc"
	}
	return u.String(), nil
}
func reviewedIYUUKind(kind string) string {
	if kind == "qb" {
		return "qbittorrent"
	}
	if kind == "tr" {
		return "transmission"
	}
	return kind
}

func reviewedIYUUDataDigest(data map[string]json.RawMessage) string {
	type proof struct {
		KeySHA   string `json:"key_sha256"`
		ValueSHA string `json:"value_sha256"`
		Bytes    int    `json:"bytes"`
	}
	rows := make([]proof, 0, len(data))
	for key, value := range data {
		rows = append(rows, proof{tokenHash(key), tokenHash(string(value)), len(value)})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].KeySHA < rows[j].KeySHA })
	return tokenHash(string(reviewedIYUUJSON(rows)))
}

func reviewedIYUUSourceEntries(in reviewedIYUUSnapshot) ([]siteIdentitySourceEntry, string, error) {
	entries := make([]siteIdentitySourceEntry, 0, len(in.Sites))
	for _, site := range in.Sites {
		_, origin, _, e := siteImportOrigin(site.URL)
		if e != nil {
			return nil, "", invalidReviewedIYUU()
		}
		entries = append(entries, siteIdentitySourceEntry{site.OriginalID, origin})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].OriginalSourceID < entries[j].OriginalSourceID })
	if validateSiteIdentitySourceEntries(entries) != nil {
		return nil, "", invalidReviewedIYUU()
	}
	type downloaderIdentity struct {
		Name     string `json:"name"`
		Kind     string `json:"kind"`
		Endpoint string `json:"endpoint"`
		Username string `json:"username"`
	}
	downloaders := make([]downloaderIdentity, 0, len(in.Downloaders))
	for _, d := range in.Downloaders {
		endpoint, e := reviewedIYUUDownloaderEndpoint(d.Kind, d.Host, true)
		if e != nil || !d.Enabled || d.Name == "" || (d.Kind != "qbittorrent" && d.Kind != "transmission") {
			return nil, "", invalidReviewedIYUU()
		}
		downloaders = append(downloaders, downloaderIdentity{d.Name, d.Kind, endpoint, d.Username})
	}
	sort.Slice(downloaders, func(i, j int) bool { return downloaders[i].Name < downloaders[j].Name })
	value := struct {
		Sites       []siteIdentitySourceEntry `json:"sites"`
		Downloaders []downloaderIdentity      `json:"downloaders"`
	}{entries, downloaders}
	return entries, tokenHash(string(reviewedIYUUJSON(value))), nil
}

func validateReviewedIYUU(raw []byte) (reviewedIYUUSnapshot, map[string]json.RawMessage, error) {
	var in reviewedIYUUSnapshot
	if len(raw) > pluginImportLimit || sourceSettingsJSON(raw, &in) != nil {
		return in, nil, invalidReviewedIYUU()
	}
	fields, e := sourceSettingsObject(raw, "version", "source_container", "source_sha256", "installed_count", "config", "data", "sites", "downloaders", "file_count", "symlink_count")
	if e != nil {
		return in, nil, invalidReviewedIYUU()
	}
	for _, v := range fields {
		if bytes.Equal(bytes.TrimSpace(v), []byte("null")) {
			return in, nil, invalidReviewedIYUU()
		}
	}
	if in.Version != 1 || in.SourceContainer != "cb82e3f550881334ef3814426016d08d909bccd88d3297522756ba31654d129a" || in.SourceSHA != reviewedIYUUSourceSHA || in.InstalledCount != 1 || in.FileCount != 0 || in.SymlinkCount != 0 || tokenHash(in.Config) != reviewedIYUUConfigSHA || len(in.Data) != 329 || len(in.Sites) != 17 || len(in.Downloaders) != 2 {
		return in, nil, invalidReviewedIYUU()
	}
	data := make(map[string]json.RawMessage, len(in.Data))
	for k, v := range in.Data {
		if !validPluginKey(k) || !json.Valid([]byte(v)) {
			return in, nil, invalidReviewedIYUU()
		}
		data[k] = json.RawMessage(v)
	}
	entries, refs, e := reviewedIYUUSourceEntries(in)
	if e != nil || siteIdentitySourceDigest(entries) != reviewedIYUUSitesSHA || refs != reviewedIYUUReferencesSHA || reviewedIYUUDataDigest(data) != reviewedIYUUDataSHA {
		return in, nil, invalidReviewedIYUU()
	}
	var cfg struct {
		Sites       []int64  `json:"sites"`
		Downloaders []string `json:"downloaders"`
	}
	if json.Unmarshal([]byte(in.Config), &cfg) != nil || len(cfg.Sites) != len(in.Sites) || len(cfg.Downloaders) != len(in.Downloaders) {
		return in, nil, invalidReviewedIYUU()
	}
	for i, v := range cfg.Sites {
		if v != in.Sites[i].OriginalID {
			return in, nil, invalidReviewedIYUU()
		}
	}
	for i, v := range cfg.Downloaders {
		if v != in.Downloaders[i].Name {
			return in, nil, invalidReviewedIYUU()
		}
	}
	return in, data, nil
}

func ValidateReviewedIYUUAutoSeedInput(raw []byte) error {
	_, _, e := validateReviewedIYUU(raw)
	return e
}
func ReviewedIYUUAutoSeedFingerprint(raw []byte) (string, error) {
	var in reviewedIYUUSnapshot
	if _, _, e := validateReviewedIYUU(raw); e != nil {
		return "", e
	}
	if json.Unmarshal(raw, &in) != nil {
		return "", invalidReviewedIYUU()
	}
	return tokenHash("IYUUAutoSeed/2.19/reviewed-source/v1/" + string(reviewedIYUUJSON(in))), nil
}

func (p *PluginStore) readReviewedIYUUBinding(ctx context.Context, q pluginRemoveLinkJournalReader) (reviewedIYUUImportBinding, error) {
	var out reviewedIYUUImportBinding
	var sealed []byte
	if e := q.QueryRowContext(ctx, `SELECT payload FROM records WHERE kind=? AND id='IYUUAutoSeed'`, reviewedIYUUMappingKind).Scan(&sealed); e != nil {
		return out, e
	}
	raw, e := p.unseal(reviewedIYUUMappingKind, "IYUUAutoSeed", "main", sealed)
	if e != nil || cleanupJournalStrict(raw, &out) != nil || !validReviewedIYUUBinding(out) {
		return out, invalidReviewedIYUU()
	}
	return out, nil
}
func validReviewedIYUUBinding(b reviewedIYUUImportBinding) bool {
	if b.Version != 1 || b.Generation != 1 || b.ConfigSHA != reviewedIYUUConfigSHA || b.DataSHA != reviewedIYUUDataSHA || b.ReferencesSHA != reviewedIYUUReferencesSHA || !pluginStorageDigest.MatchString(b.SourceFingerprint) || !pluginStorageDigest.MatchString(b.SettingsSHA) || !pluginStorageDigest.MatchString(b.MappedConfigSHA) || len(b.Sites) != 17 || len(b.Downloaders) != 2 {
		return false
	}
	entries := make([]siteIdentitySourceEntry, 0, len(b.Sites))
	native := map[int64]bool{}
	ids := map[string]bool{}
	for _, s := range b.Sites {
		if s.NativeID < 1 || s.SiteID == "" || native[s.NativeID] || ids[s.SiteID] {
			return false
		}
		native[s.NativeID], ids[s.SiteID] = true, true
		entries = append(entries, siteIdentitySourceEntry{s.OriginalID, s.Origin})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].OriginalSourceID < entries[j].OriginalSourceID })
	if validateSiteIdentitySourceEntries(entries) != nil || siteIdentitySourceDigest(entries) != reviewedIYUUSitesSHA {
		return false
	}
	for _, d := range b.Downloaders {
		if d.NativeID == "" || !pluginStorageDigest.MatchString(d.SourceNameSHA) {
			return false
		}
	}
	return b.Downloaders[0].NativeID != b.Downloaders[1].NativeID && b.Downloaders[0].SourceNameSHA != b.Downloaders[1].SourceNameSHA
}

func reviewedIYUUMappedConfig(source string, b reviewedIYUUImportBinding) (json.RawMessage, error) {
	var fields map[string]json.RawMessage
	var ids []int64
	if json.Unmarshal([]byte(source), &fields) != nil || fields == nil || json.Unmarshal(fields["sites"], &ids) != nil || len(ids) != len(b.Sites) {
		return nil, invalidReviewedIYUU()
	}
	for i, id := range ids {
		if id != b.Sites[i].OriginalID {
			return nil, invalidReviewedIYUU()
		}
		ids[i] = b.Sites[i].NativeID
	}
	fields["sites"] = reviewedIYUUJSON(ids)
	return pluginJSON(fields, true)
}
func (p *PluginStore) reviewedIYUUSettings(ctx context.Context, q pluginRemoveLinkJournalReader) (Settings, string, error) {
	var s Settings
	var sealed []byte
	if q.QueryRowContext(ctx, `SELECT payload FROM records WHERE kind='config' AND id='main'`).Scan(&sealed) != nil || p.store.Unseal(sealed, &s) != nil {
		return s, "", errors.New("reviewed_iyuu_target_settings_missing")
	}
	return s, tokenHash(string(reviewedIYUUJSON(s))), nil
}
func (p *PluginStore) prepareReviewedIYUUBinding(ctx context.Context, in reviewedIYUUSnapshot, fingerprint string) (reviewedIYUUImportBinding, error) {
	tx, e := p.store.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if e != nil {
		return reviewedIYUUImportBinding{}, e
	}
	defer tx.Rollback()
	old, e := p.readReviewedIYUUBinding(ctx, tx)
	if e == nil {
		if old.SourceFingerprint != fingerprint {
			return old, invalidReviewedIYUU()
		}
		historical := PluginInstallation{InstanceID: "IYUUAutoSeed", PluginID: "IYUUAutoSeed", Version: "2.19", Generation: old.Generation}
		if _, e = p.readSiteReferenceProtocol(ctx, tx, historical); e != nil {
			return old, e
		}
		return old, tx.Commit()
	}
	if !errors.Is(e, sql.ErrNoRows) {
		return old, e
	}
	s, settingsSHA, e := p.reviewedIYUUSettings(ctx, tx)
	if e != nil {
		return old, e
	}
	b := reviewedIYUUImportBinding{Version: 1, Generation: 1, SourceFingerprint: fingerprint, ConfigSHA: reviewedIYUUConfigSHA, DataSHA: reviewedIYUUDataSHA, ReferencesSHA: reviewedIYUUReferencesSHA, SettingsSHA: settingsSHA}
	for _, source := range in.Sites {
		_, origin, _, e := siteImportOrigin(source.URL)
		if e != nil {
			return b, e
		}
		matches := []nativeSiteIdentity{}
		for _, site := range s.Sites {
			configured, e := nativeConfiguredSiteOrigin(site)
			if e != nil {
				return b, e
			}
			if configured != origin {
				continue
			}
			id, e := p.store.readNativeSiteIdentity(ctx, tx, "site_id", site.ID)
			if e != nil || id.State != "active" || id.CanonicalOrigin != origin || !site.Enabled || site.Public {
				return b, siteIdentityError()
			}
			matches = append(matches, id)
		}
		if len(matches) != 1 {
			return b, errors.New("reviewed_iyuu_site_reference_unmatched")
		}
		id := matches[0]
		b.Sites = append(b.Sites, reviewedIYUUBoundSite{source.OriginalID, id.SourceID, id.SiteID, origin})
	}
	for _, source := range in.Downloaders {
		endpoint, e := reviewedIYUUDownloaderEndpoint(source.Kind, source.Host, true)
		if e != nil {
			return b, e
		}
		matches := []DownloaderConfig{}
		for _, d := range s.Downloaders {
			if reviewedIYUUKind(d.Kind) != source.Kind {
				continue
			}
			nativeEndpoint, e := reviewedIYUUDownloaderEndpoint(source.Kind, d.BaseURL, false)
			if e == nil && nativeEndpoint == endpoint && d.Name == source.Name && d.Username == source.Username && d.Password == source.Password && downloaderEnabled(d) {
				matches = append(matches, d)
			}
		}
		if len(matches) != 1 {
			return b, errors.New("reviewed_iyuu_downloader_reference_unmatched")
		}
		b.Downloaders = append(b.Downloaders, reviewedIYUUBoundDownloader{tokenHash(source.Name), matches[0].ID})
	}
	mapped, e := reviewedIYUUMappedConfig(in.Config, b)
	if e != nil {
		return b, e
	}
	b.MappedConfigSHA = tokenHash(string(mapped))
	if !validReviewedIYUUBinding(b) {
		return b, invalidReviewedIYUU()
	}
	return b, tx.Commit()
}

func validReviewedIYUUImport(in PluginImport, installation PluginInstallation) bool {
	if in.reviewedIYUU == nil || !validReviewedIYUUBinding(*in.reviewedIYUU) || installation.InstanceID != "IYUUAutoSeed" || installation.PluginID != "IYUUAutoSeed" || installation.Version != "2.19" || installation.ManifestSchemaVersion != 1 || in.SourceConfigAbsent || len(in.Data) != 329 {
		return false
	}
	b := *in.reviewedIYUU
	if reviewedIYUUDataDigest(in.Data) != reviewedIYUUDataSHA || tokenHash(string(in.Config)) != b.MappedConfigSHA {
		return false
	}
	want, _ := json.Marshal(iyuu.Manifest())
	got, e := pluginJSON(installation.Manifest, true)
	expected, we := pluginJSON(json.RawMessage(want), true)
	return e == nil && we == nil && bytes.Equal(got, expected)
}

func (p *PluginStore) reviewedIYUUTargetTx(ctx context.Context, tx *sql.Tx, b reviewedIYUUImportBinding) (Settings, error) {
	s, sha, e := p.reviewedIYUUSettings(ctx, tx)
	if e != nil || sha != b.SettingsSHA {
		return s, errors.New("reviewed_iyuu_target_settings_changed")
	}
	for _, site := range b.Sites {
		id, e := p.store.readNativeSiteIdentity(ctx, tx, "site_id", site.SiteID)
		if e != nil || id.SourceID != site.NativeID || id.State != "active" || id.CanonicalOrigin != site.Origin {
			return s, siteIdentityError()
		}
	}
	return s, nil
}

func (p *PluginStore) commitReviewedIYUUReferencesTx(ctx context.Context, tx *sql.Tx, b reviewedIYUUImportBinding, migration string) error {
	s, e := p.reviewedIYUUTargetTx(ctx, tx, b)
	if e != nil {
		return e
	}
	var generation int64
	var sealed []byte
	var protocol siteReferenceProtocol
	if tx.QueryRowContext(ctx, `SELECT generation FROM plugin_installations WHERE instance_id='IYUUAutoSeed'`).Scan(&generation) != nil || generation != b.Generation || tx.QueryRowContext(ctx, `SELECT sealed FROM native_site_reference_protocols WHERE instance_id='IYUUAutoSeed' AND generation=?`, generation).Scan(&sealed) != nil || p.store.Unseal(sealed, &protocol) != nil || protocol.Protocol != originalUnclaimedSiteReferenceProtocol || protocol.Source != "moviepilot-import/v1:"+migration {
		return invalidReviewedIYUU()
	}
	entries := make([]siteIdentitySourceEntry, 0, len(b.Sites))
	for _, site := range b.Sites {
		entries = append(entries, siteIdentitySourceEntry{site.OriginalID, site.Origin})
	}
	snapshot, e := p.store.registerSiteIdentitySourceSnapshotTx(ctx, tx, b.SourceFingerprint, entries)
	if e != nil {
		return e
	}
	for _, site := range b.Sites {
		receipt, e := p.store.commitSiteIdentityMigrationTx(ctx, tx, s, snapshot.SnapshotID, site.OriginalID, site.SiteID)
		if e != nil {
			return e
		}
		if receipt.NativeSourceID != site.NativeID {
			return siteIdentityError()
		}
	}
	raw := reviewedIYUUJSON(b)
	sealed, e = p.seal(reviewedIYUUMappingKind, "IYUUAutoSeed", "main", raw)
	if e != nil {
		return e
	}
	if _, e = tx.ExecContext(ctx, `INSERT INTO records(kind,id,payload,updated_at) VALUES(?,'IYUUAutoSeed',?,?)`, reviewedIYUUMappingKind, sealed, now()); e != nil {
		return e
	}
	protocol.Protocol = sourceSiteReferenceProtocol
	protocol.Source = reviewedIYUUProtocolPrefix + tokenHash(string(raw))
	sealed, e = p.store.Seal(protocol)
	if e != nil {
		return e
	}
	result, e := tx.ExecContext(ctx, `UPDATE native_site_reference_protocols SET sealed=? WHERE instance_id='IYUUAutoSeed' AND generation=?`, sealed, generation)
	if e != nil {
		return e
	}
	n, e := result.RowsAffected()
	if e != nil {
		return e
	}
	if n != 1 {
		return invalidReviewedIYUU()
	}
	return nil
}

func (p *PluginStore) validateReviewedIYUUProtocol(ctx context.Context, q pluginRemoveLinkJournalReader, in PluginInstallation, source string) error {
	b, e := p.readReviewedIYUUBinding(ctx, q)
	if e != nil || in.InstanceID != "IYUUAutoSeed" || in.PluginID != "IYUUAutoSeed" || in.Version != "2.19" || in.Generation != b.Generation || source != reviewedIYUUProtocolPrefix+tokenHash(string(reviewedIYUUJSON(b))) {
		return invalidReviewedIYUU()
	}
	var digest, state string
	if q.QueryRowContext(ctx, `SELECT r.digest,m.state FROM plugin_migration_receipts r JOIN plugin_migration_runs m ON m.id=r.run_id AND m.source_fingerprint=r.source_fingerprint AND m.source_plugin_id=r.source_plugin_id AND m.instance_id=r.instance_id WHERE r.source_fingerprint=? AND r.source_plugin_id='IYUUAutoSeed' AND r.instance_id='IYUUAutoSeed'`, b.SourceFingerprint).Scan(&digest, &state) != nil || !pluginStorageDigest.MatchString(digest) || state != "imported" {
		return invalidReviewedIYUU()
	}
	for _, site := range b.Sites {
		var native int64
		var appID string
		var sealed []byte
		var receipt siteIdentityMigrationReceipt
		if q.QueryRowContext(ctx, `SELECT native_source_id,site_id,sealed FROM native_site_migration_receipts WHERE source_fingerprint=? AND original_source_id=?`, b.SourceFingerprint, site.OriginalID).Scan(&native, &appID, &sealed) != nil || p.store.Unseal(sealed, &receipt) != nil || native != site.NativeID || appID != site.SiteID || receipt.SourceFingerprint != b.SourceFingerprint || receipt.OriginalSourceID != site.OriginalID || receipt.NativeSourceID != native || receipt.SiteID != appID || receipt.CanonicalOrigin != site.Origin || p.store.verifySiteIdentityMigrationReceipt(ctx, q, receipt) != nil {
			return invalidReviewedIYUU()
		}
	}
	return nil
}

// Offline import only. Source one-shot flags remain data; no worker, schedule,
// downloader or site request is started, and activation remains disabled.
func (p *PluginStore) ImportReviewedIYUUAutoSeed(ctx context.Context, runID string, raw []byte) (PluginMigrationReceipt, error) {
	in, data, e := validateReviewedIYUU(raw)
	if e != nil {
		return PluginMigrationReceipt{}, e
	}
	fingerprint, e := ReviewedIYUUAutoSeedFingerprint(raw)
	if e != nil {
		return PluginMigrationReceipt{}, e
	}
	b, e := p.prepareReviewedIYUUBinding(ctx, in, fingerprint)
	if e != nil {
		return PluginMigrationReceipt{}, e
	}
	config, e := reviewedIYUUMappedConfig(in.Config, b)
	if e != nil {
		return PluginMigrationReceipt{}, e
	}
	manifest, _ := json.Marshal(iyuu.Manifest())
	return p.Import(ctx, PluginImport{RunID: runID, Installation: PluginInstallation{InstanceID: "IYUUAutoSeed", PluginID: "IYUUAutoSeed", Version: "2.19", ManifestSchemaVersion: 1, Manifest: manifest}, Config: config, Data: data, reviewedIYUU: &b})
}

// Dry-run validation against an existing read-only database. No schema setup,
// Store startup, configuration writes or native network requests are performed.
func ValidateReviewedIYUUAutoSeedTarget(ctx context.Context, db *sql.DB, key, raw []byte) error {
	if db == nil || len(key) != 32 {
		return errors.New("reviewed_iyuu_target_missing")
	}
	in, _, e := validateReviewedIYUU(raw)
	if e != nil {
		return e
	}
	fingerprint, e := ReviewedIYUUAutoSeedFingerprint(raw)
	if e != nil {
		return e
	}
	p := &PluginStore{store: &Store{DB: db, key: key}}
	_, e = p.prepareReviewedIYUUBinding(ctx, in, fingerprint)
	return e
}
