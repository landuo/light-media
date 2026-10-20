package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sort"

	sr "light-media/internal/plugins/seedrescue"
)

const reviewedSeedSourceSHA = "e379ec20d09fbd3bc461b1b8f51ec508d0270a63a841763cc965c39adb98d375"
const reviewedSeedConfigSHA = "62a36d3f29fc1c72b83a413ab301b16a5fe63777f41a685fbcd099b060d53756"
const reviewedSeedDataSHA = "43d65b2b130947660d40b895d624248e2c6bafe8a1501a84a3cae41107b091b7"
const reviewedSeedReferencesSHA = "378dd4c931abe633743027ed67938c9ba9c3408010b0a3bb94292e97a722d282"
const reviewedSeedDownloaderNameSHA = "22d1fb414b6d62b69391bb42b9b94314a5b2fa77a7e8abd507cf3152ed4fa38e"
const reviewedSeedProtocol = "seed-rescue-legacy-numeric/v1"
const reviewedSeedProtocolPrefix = "reviewed-moviepilot-seed-rescue/v1:"
const reviewedSeedMappingKind = "plugin_original_seed_rescue_references"

// Private, reviewed offline wire format. No HTTP handler accepts this contract.
type reviewedSeedSnapshot struct {
	Version         int                      `json:"version"`
	SourceContainer string                   `json:"source_container"`
	SourceSHA       string                   `json:"source_sha256"`
	InstalledCount  int                      `json:"installed_count"`
	Config          string                   `json:"config"`
	Data            map[string]string        `json:"data"`
	Sites           []reviewedIYUUSite       `json:"sites"`
	Downloaders     []reviewedIYUUDownloader `json:"downloaders"`
	ReservedCeiling int64                    `json:"reserved_ceiling"`
	FileCount       int                      `json:"file_count"`
	SymlinkCount    int                      `json:"symlink_count"`
}
type reviewedSeedBinding struct {
	Version                                                           int
	Generation, ReservedCeiling                                       int64
	SourceFingerprint, ConfigSHA, DataSHA, ReferencesSHA, SettingsSHA string
	Sites                                                             []reviewedIYUUBoundSite
	DownloaderName, DownloaderID                                      string
}
type reviewedSeedProof struct {
	Binding                 reviewedSeedBinding
	Migration, ImportDigest string
}

func invalidReviewedSeed() error {
	return bad("SEED_RESCUE_REFERENCE_INVALID", "稀缺保种引用来源无法校验，请核对迁移收据")
}
func validateReviewedSeed(raw []byte) (reviewedSeedSnapshot, map[string]json.RawMessage, error) {
	var in reviewedSeedSnapshot
	if len(raw) > pluginImportLimit || sourceSettingsJSON(raw, &in) != nil {
		return in, nil, invalidReviewedSeed()
	}
	fields, e := sourceSettingsObject(raw, "version", "source_container", "source_sha256", "installed_count", "config", "data", "sites", "downloaders", "reserved_ceiling", "file_count", "symlink_count")
	if e != nil {
		return in, nil, invalidReviewedSeed()
	}
	for _, v := range fields {
		if bytes.Equal(bytes.TrimSpace(v), []byte("null")) {
			return in, nil, invalidReviewedSeed()
		}
	}
	if in.Version != 1 || in.SourceContainer != "cb82e3f550881334ef3814426016d08d909bccd88d3297522756ba31654d129a" || in.SourceSHA != reviewedSeedSourceSHA || in.InstalledCount != 1 || in.FileCount != 0 || in.SymlinkCount != 0 || in.ReservedCeiling != 18 || tokenHash(in.Config) != reviewedSeedConfigSHA || len(in.Data) != 3 || len(in.Sites) != 17 || len(in.Downloaders) != 1 || in.Downloaders[0].Kind != "qbittorrent" || tokenHash(in.Downloaders[0].Name) != reviewedSeedDownloaderNameSHA {
		return in, nil, invalidReviewedSeed()
	}
	data := map[string]json.RawMessage{}
	for k, v := range in.Data {
		if !seedRescueDataKey(k) || !json.Valid([]byte(v)) {
			return in, nil, invalidReviewedSeed()
		}
		data[k] = json.RawMessage(v)
	}
	entries, refs, e := reviewedIYUUSourceEntries(reviewedIYUUSnapshot{Sites: in.Sites, Downloaders: in.Downloaders})
	if e != nil || siteIdentitySourceDigest(entries) != reviewedIYUUSitesSHA || refs != reviewedSeedReferencesSHA || reviewedIYUUDataDigest(data) != reviewedSeedDataSHA {
		return in, nil, invalidReviewedSeed()
	}
	for _, s := range entries {
		if s.OriginalSourceID > in.ReservedCeiling {
			return in, nil, invalidReviewedSeed()
		}
	}
	return in, data, nil
}
func ValidateReviewedSeedRescueInput(raw []byte) error {
	_, _, e := validateReviewedSeed(raw)
	return e
}
func ReviewedSeedRescueFingerprint(raw []byte) (string, error) {
	in, _, e := validateReviewedSeed(raw)
	if e != nil {
		return "", e
	}
	return tokenHash("SeedRescue/1.6.3/reviewed-source/v1/" + string(reviewedIYUUJSON(in))), nil
}
func validReviewedSeedBinding(b reviewedSeedBinding) bool {
	if b.Version != 1 || b.Generation != 1 || b.ReservedCeiling != 18 || b.ConfigSHA != reviewedSeedConfigSHA || b.DataSHA != reviewedSeedDataSHA || b.ReferencesSHA != reviewedSeedReferencesSHA || !pluginStorageDigest.MatchString(b.SourceFingerprint) || !pluginStorageDigest.MatchString(b.SettingsSHA) || len(b.Sites) != 17 || tokenHash(b.DownloaderName) != reviewedSeedDownloaderNameSHA || b.DownloaderID == "" {
		return false
	}
	entries := []siteIdentitySourceEntry{}
	ids := map[int64]bool{}
	appIDs := map[string]bool{}
	for _, s := range b.Sites {
		if s.OriginalID > b.ReservedCeiling || s.NativeID < 1 || s.NativeID > seedRescueMaximumAlias-b.ReservedCeiling || s.SiteID == "" || ids[s.NativeID] || appIDs[s.SiteID] {
			return false
		}
		ids[s.NativeID], appIDs[s.SiteID] = true, true
		entries = append(entries, siteIdentitySourceEntry{s.OriginalID, s.Origin})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].OriginalSourceID < entries[j].OriginalSourceID })
	return validateSiteIdentitySourceEntries(entries) == nil && siteIdentitySourceDigest(entries) == reviewedIYUUSitesSHA
}
func (p *PluginStore) readReviewedSeedProof(ctx context.Context, q pluginRemoveLinkJournalReader) (reviewedSeedProof, error) {
	var out reviewedSeedProof
	var sealed []byte
	e := q.QueryRowContext(ctx, `SELECT payload FROM records WHERE kind=? AND id='SeedRescue'`, reviewedSeedMappingKind).Scan(&sealed)
	if e != nil {
		return out, e
	}
	raw, e := p.unseal(reviewedSeedMappingKind, "SeedRescue", "main", sealed)
	if e != nil || cleanupJournalStrict(raw, &out) != nil || !validReviewedSeedBinding(out.Binding) || !pluginStorageID.MatchString(out.Migration) || !pluginStorageDigest.MatchString(out.ImportDigest) {
		return out, invalidReviewedSeed()
	}
	var digest, state string
	b := out.Binding
	if q.QueryRowContext(ctx, `SELECT r.digest,m.state FROM plugin_migration_receipts r JOIN plugin_migration_runs m ON m.id=r.run_id AND m.source_fingerprint=r.source_fingerprint AND m.source_plugin_id=r.source_plugin_id AND m.instance_id=r.instance_id WHERE r.run_id=? AND r.source_fingerprint=? AND r.source_plugin_id='SeedRescue' AND r.instance_id='SeedRescue'`, out.Migration, b.SourceFingerprint).Scan(&digest, &state) != nil || digest != out.ImportDigest || state != "imported" {
		return out, invalidReviewedSeed()
	}
	for _, s := range b.Sites {
		var native int64
		var appID string
		var receipt siteIdentityMigrationReceipt
		if q.QueryRowContext(ctx, `SELECT native_source_id,site_id,sealed FROM native_site_migration_receipts WHERE source_fingerprint=? AND original_source_id=?`, b.SourceFingerprint, s.OriginalID).Scan(&native, &appID, &sealed) != nil || p.store.Unseal(sealed, &receipt) != nil || native != s.NativeID || appID != s.SiteID || receipt.SourceFingerprint != b.SourceFingerprint || receipt.OriginalSourceID != s.OriginalID || receipt.NativeSourceID != native || receipt.SiteID != appID || receipt.CanonicalOrigin != s.Origin || p.store.verifySiteIdentityMigrationReceipt(ctx, q, receipt) != nil {
			return out, invalidReviewedSeed()
		}
	}
	return out, nil
}
func (p *PluginStore) validateReviewedSeedProtocol(ctx context.Context, q pluginRemoveLinkJournalReader, in PluginInstallation, source string) error {
	proof, e := p.readReviewedSeedProof(ctx, q)
	if e != nil || in.InstanceID != "SeedRescue" || in.PluginID != sr.PluginID || in.Version != sr.Version || in.Generation < proof.Binding.Generation || source != reviewedSeedProtocolPrefix+tokenHash(string(reviewedIYUUJSON(proof.Binding))) {
		return invalidReviewedSeed()
	}
	return nil
}
func (p *PluginStore) prepareReviewedSeedBinding(ctx context.Context, in reviewedSeedSnapshot, fingerprint string) (reviewedSeedBinding, error) {
	tx, e := p.store.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if e != nil {
		return reviewedSeedBinding{}, e
	}
	defer tx.Rollback()
	proof, e := p.readReviewedSeedProof(ctx, tx)
	if e == nil {
		b := proof.Binding
		if b.SourceFingerprint != fingerprint {
			return b, invalidReviewedSeed()
		}
		_, e = p.readSiteReferenceProtocol(ctx, tx, PluginInstallation{InstanceID: "SeedRescue", PluginID: sr.PluginID, Version: sr.Version, Generation: b.Generation})
		if e != nil {
			return b, e
		}
		return b, tx.Commit()
	}
	if !errors.Is(e, sql.ErrNoRows) {
		return proof.Binding, e
	}
	var existing int
	if e = tx.QueryRowContext(ctx, `SELECT (SELECT COUNT(*) FROM plugin_installations WHERE instance_id='SeedRescue')+(SELECT COUNT(*) FROM plugin_migration_receipts WHERE instance_id='SeedRescue')`).Scan(&existing); e != nil || existing != 0 {
		return proof.Binding, invalidReviewedSeed()
	}
	settings, sha, e := p.reviewedIYUUSettings(ctx, tx)
	if e != nil {
		return proof.Binding, e
	}
	b := reviewedSeedBinding{Version: 1, Generation: 1, ReservedCeiling: in.ReservedCeiling, SourceFingerprint: fingerprint, ConfigSHA: reviewedSeedConfigSHA, DataSHA: reviewedSeedDataSHA, ReferencesSHA: reviewedSeedReferencesSHA, SettingsSHA: sha, DownloaderName: in.Downloaders[0].Name}
	for _, source := range in.Sites {
		_, origin, _, e := siteImportOrigin(source.URL)
		if e != nil {
			return b, e
		}
		matches := []nativeSiteIdentity{}
		for _, site := range settings.Sites {
			current, e := nativeConfiguredSiteOrigin(site)
			if e != nil {
				return b, e
			}
			if current != origin {
				continue
			}
			identity, e := p.store.readNativeSiteIdentity(ctx, tx, "site_id", site.ID)
			if e != nil || identity.State != "active" || identity.CanonicalOrigin != origin || !site.Enabled || site.Public {
				return b, siteIdentityError()
			}
			matches = append(matches, identity)
		}
		if len(matches) != 1 {
			return b, invalidReviewedSeed()
		}
		s := matches[0]
		b.Sites = append(b.Sites, reviewedIYUUBoundSite{source.OriginalID, s.SourceID, s.SiteID, origin})
	}
	source := in.Downloaders[0]
	endpoint, e := reviewedIYUUDownloaderEndpoint(source.Kind, source.Host, true)
	if e != nil {
		return b, e
	}
	matches := []DownloaderConfig{}
	for _, d := range settings.Downloaders {
		current, e := reviewedIYUUDownloaderEndpoint("qbittorrent", d.BaseURL, false)
		if e == nil && reviewedIYUUKind(d.Kind) == "qbittorrent" && current == endpoint && d.Username == source.Username && d.Password == source.Password && downloaderEnabled(d) {
			matches = append(matches, d)
		}
	}
	if len(matches) != 1 {
		return b, invalidReviewedSeed()
	}
	b.DownloaderID = matches[0].ID
	if !validReviewedSeedBinding(b) {
		return b, invalidReviewedSeed()
	}
	return b, tx.Commit()
}
func validReviewedSeedImport(in PluginImport, installation PluginInstallation) bool {
	if in.reviewedSeed == nil || !validReviewedSeedBinding(*in.reviewedSeed) || installation.InstanceID != "SeedRescue" || installation.PluginID != sr.PluginID || installation.Version != sr.Version || installation.ManifestSchemaVersion != 1 || in.SourceConfigAbsent || tokenHash(string(in.Config)) != reviewedSeedConfigSHA || reviewedIYUUDataDigest(in.Data) != reviewedSeedDataSHA {
		return false
	}
	expected, _ := json.Marshal(sr.NativeManifest())
	got, e := pluginJSON(installation.Manifest, true)
	want, we := pluginJSON(json.RawMessage(expected), true)
	return e == nil && we == nil && bytes.Equal(got, want)
}
func (p *PluginStore) reviewedSeedTargetTx(ctx context.Context, tx *sql.Tx, b reviewedSeedBinding) (Settings, error) {
	s, sha, e := p.reviewedIYUUSettings(ctx, tx)
	if e != nil || sha != b.SettingsSHA {
		return s, invalidReviewedSeed()
	}
	for _, site := range b.Sites {
		current, e := p.store.readNativeSiteIdentity(ctx, tx, "site_id", site.SiteID)
		if e != nil || current.SourceID != site.NativeID || current.State != "active" || current.CanonicalOrigin != site.Origin {
			return s, siteIdentityError()
		}
	}
	return s, nil
}
func (p *PluginStore) commitReviewedSeedReferencesTx(ctx context.Context, tx *sql.Tx, b reviewedSeedBinding, migration, digest string) error {
	settings, e := p.reviewedSeedTargetTx(ctx, tx, b)
	if e != nil {
		return e
	}
	var generation int64
	var sealed []byte
	var protocol siteReferenceProtocol
	if tx.QueryRowContext(ctx, `SELECT generation FROM plugin_installations WHERE instance_id='SeedRescue'`).Scan(&generation) != nil || generation != b.Generation || tx.QueryRowContext(ctx, `SELECT sealed FROM native_site_reference_protocols WHERE instance_id='SeedRescue' AND generation=?`, generation).Scan(&sealed) != nil || p.store.Unseal(sealed, &protocol) != nil || protocol.Protocol != originalUnclaimedSiteReferenceProtocol || protocol.Source != "moviepilot-import/v1:"+migration {
		return invalidReviewedSeed()
	}
	entries := []siteIdentitySourceEntry{}
	for _, site := range b.Sites {
		entries = append(entries, siteIdentitySourceEntry{site.OriginalID, site.Origin})
	}
	snapshot, e := p.store.registerSiteIdentitySourceSnapshotTx(ctx, tx, b.SourceFingerprint, entries)
	if e != nil {
		return e
	}
	for _, site := range b.Sites {
		receipt, e := p.store.commitSiteIdentityMigrationTx(ctx, tx, settings, snapshot.SnapshotID, site.OriginalID, site.SiteID)
		if e != nil {
			return e
		}
		if receipt.NativeSourceID != site.NativeID {
			return siteIdentityError()
		}
	}
	sealed, e = p.seal(reviewedSeedMappingKind, "SeedRescue", "main", reviewedIYUUJSON(reviewedSeedProof{b, migration, digest}))
	if e != nil {
		return e
	}
	if _, e = tx.ExecContext(ctx, `INSERT INTO records(kind,id,payload,updated_at) VALUES(?,'SeedRescue',?,?)`, reviewedSeedMappingKind, sealed, now()); e != nil {
		return e
	}
	protocol.Protocol = reviewedSeedProtocol
	protocol.Source = reviewedSeedProtocolPrefix + tokenHash(string(reviewedIYUUJSON(b)))
	sealed, e = p.store.Seal(protocol)
	if e != nil {
		return e
	}
	result, e := tx.ExecContext(ctx, `UPDATE native_site_reference_protocols SET sealed=? WHERE instance_id='SeedRescue' AND generation=?`, sealed, generation)
	if e != nil {
		return e
	}
	n, e := result.RowsAffected()
	if e != nil {
		return e
	}
	if n != 1 {
		return invalidReviewedSeed()
	}
	return nil
}

// Preserve all source config/KV bytes and one-shot flags as encrypted data.
// Import neither starts a worker nor grants permission for automatic execution.
func (p *PluginStore) ImportReviewedSeedRescue(ctx context.Context, runID string, raw []byte) (PluginMigrationReceipt, error) {
	in, data, e := validateReviewedSeed(raw)
	if e != nil {
		return PluginMigrationReceipt{}, e
	}
	fingerprint, e := ReviewedSeedRescueFingerprint(raw)
	if e != nil {
		return PluginMigrationReceipt{}, e
	}
	b, e := p.prepareReviewedSeedBinding(ctx, in, fingerprint)
	if e != nil {
		return PluginMigrationReceipt{}, e
	}
	manifest, _ := json.Marshal(sr.NativeManifest())
	return p.Import(ctx, PluginImport{RunID: runID, Installation: PluginInstallation{InstanceID: "SeedRescue", PluginID: sr.PluginID, Version: sr.Version, ManifestSchemaVersion: 1, Manifest: manifest}, Config: json.RawMessage(in.Config), Data: data, reviewedSeed: &b})
}
func ValidateReviewedSeedRescueTarget(ctx context.Context, db *sql.DB, key, raw []byte) error {
	if db == nil || len(key) != 32 {
		return invalidReviewedSeed()
	}
	in, _, e := validateReviewedSeed(raw)
	if e != nil {
		return e
	}
	fingerprint, e := ReviewedSeedRescueFingerprint(raw)
	if e != nil {
		return e
	}
	p := &PluginStore{store: &Store{DB: db, key: key}}
	_, e = p.prepareReviewedSeedBinding(ctx, in, fingerprint)
	return e
}
