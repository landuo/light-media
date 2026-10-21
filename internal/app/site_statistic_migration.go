package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strconv"
	"strings"

	"light-media/internal/plugins/builtin"
)

const reviewedStatisticSourceSHA = "763a88be85185a94082a2f131ed1c80d8306abe24b10ebe9ae95447cfe5da17d"
const reviewedStatisticConfigSHA = "2ec13ebdddbe2e5a7595cbd744552c016e45e989516a48f53be27f84e9de253f"
const reviewedStatisticCatalogSHA = "b27625bd4cdfbcc188683874968786eb4e1dc548fa4ffa1171ccfb9db3e331a1"
const reviewedStatisticAccountsSHA = "fac3972705d612955dcd77294a90dc920e35ec362b1021dd52bc3fadb4e3031e"
const reviewedStatisticProtocolPrefix = "reviewed-moviepilot-site-statistic/v1:"

type reviewedStatisticSite struct {
	OriginalID int64  `json:"original_source_id"`
	URL        string `json:"url"`
	Domain     string `json:"domain"`
}
type reviewedStatisticSnapshot struct {
	Version         int                     `json:"version"`
	SourceContainer string                  `json:"source_container"`
	SourceSHA       string                  `json:"source_sha256"`
	InstalledCount  int                     `json:"installed_count"`
	Config          string                  `json:"config"`
	Data            map[string]string       `json:"data"`
	Sites           []reviewedStatisticSite `json:"sites"`
	AccountRows     []string                `json:"account_rows"`
	LatestIDs       []int64                 `json:"latest_ids"`
	FileCount       int                     `json:"file_count"`
	SymlinkCount    int                     `json:"symlink_count"`
}
type reviewedStatisticImport struct {
	Binding sourceAccountBinding
	Rows    []string
}

func sourceAccountDataDigest(rows []string) string {
	h := sha256.New()
	for _, raw := range rows {
		h.Write([]byte(raw))
		h.Write([]byte{'\n'})
	}
	return hex.EncodeToString(h.Sum(nil))
}
func validateReviewedStatistic(raw []byte) (reviewedStatisticSnapshot, []sourceAccountHeader, error) {
	var in reviewedStatisticSnapshot
	if len(raw) > pluginImportLimit || sourceSettingsJSON(raw, &in) != nil {
		return in, nil, invalidSourceAccounts()
	}
	fields, e := sourceSettingsObject(raw, "version", "source_container", "source_sha256", "installed_count", "config", "data", "sites", "account_rows", "latest_ids", "file_count", "symlink_count")
	if e != nil {
		return in, nil, invalidSourceAccounts()
	}
	for _, value := range fields {
		if bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return in, nil, invalidSourceAccounts()
		}
	}
	if in.Version != 1 || in.SourceContainer != originalPluginContainerID || in.SourceSHA != reviewedStatisticSourceSHA || in.InstalledCount != 1 || tokenHash(in.Config) != reviewedStatisticConfigSHA || len(in.Data) != 0 || len(in.Sites) != 17 || len(in.AccountRows) != 2133 || in.FileCount != 0 || in.SymlinkCount != 0 || tokenHash(string(reviewedIYUUJSON(in.Sites))) != reviewedStatisticCatalogSHA || sourceAccountDataDigest(in.AccountRows) != reviewedStatisticAccountsSHA {
		return in, nil, invalidSourceAccounts()
	}
	headers := make([]sourceAccountHeader, 0, len(in.AccountRows))
	var previous int64
	for _, raw := range in.AccountRows {
		_, header, e := decodeSourceAccount([]byte(raw))
		if e != nil || header.ID <= previous {
			return in, nil, invalidSourceAccounts()
		}
		previous = header.ID
		headers = append(headers, header)
	}
	return in, headers, nil
}
func ValidateReviewedSiteStatisticInput(raw []byte) error {
	_, _, e := validateReviewedStatistic(raw)
	return e
}
func ReviewedSiteStatisticFingerprint(raw []byte) (string, error) {
	if e := ValidateReviewedSiteStatisticInput(raw); e != nil {
		return "", e
	}
	return reviewedStatisticFingerprint(), nil
}
func reviewedStatisticFingerprint() string {
	return tokenHash("SiteStatistic/1.9.7/reviewed-source/v1/" + reviewedStatisticSourceSHA + "/" + reviewedStatisticConfigSHA + "/" + reviewedStatisticCatalogSHA + "/" + reviewedStatisticAccountsSHA)
}
func validSourceAccountBinding(b sourceAccountBinding) bool {
	if b.Version != 1 || b.ConfigSHA != reviewedStatisticConfigSHA || b.DataSHA != reviewedStatisticAccountsSHA || b.CatalogSHA != reviewedStatisticCatalogSHA || b.SourceFingerprint != reviewedStatisticFingerprint() || !pluginStorageDigest.MatchString(b.SettingsSHA) || len(b.Sites) != 17 || len(b.Rows) != 2133 {
		return false
	}
	catalog, ids, domains := []reviewedStatisticSite{}, map[string]bool{}, map[string]bool{}
	var previous int64
	for _, site := range b.Sites {
		_, origin, _, e := siteImportOrigin(site.URL)
		if e != nil || origin != site.Origin || site.SiteID == "" || site.NativeID < 1 || site.OriginalID <= previous || site.Domain == "" || ids[site.SiteID] || domains[site.Domain] {
			return false
		}
		previous, ids[site.SiteID], domains[site.Domain] = site.OriginalID, true, true
		catalog = append(catalog, reviewedStatisticSite{site.OriginalID, site.URL, site.Domain})
	}
	if tokenHash(string(reviewedIYUUJSON(catalog))) != b.CatalogSHA {
		return false
	}
	previous = 0
	for _, h := range b.Rows {
		if h.ID <= previous || h.Domain == "" || h.Day == "" || h.Time == "" || !pluginStorageDigest.MatchString(h.SHA) {
			return false
		}
		previous = h.ID
	}
	return true
}

func (p *PluginStore) prepareReviewedStatistic(ctx context.Context, source reviewedStatisticSnapshot, headers []sourceAccountHeader, fingerprint string) (sourceAccountBinding, error) {
	tx, e := p.store.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if e != nil {
		return sourceAccountBinding{}, e
	}
	defer tx.Rollback()
	old, e := p.sourceAccountProofTx(ctx, tx)
	if e != nil {
		return sourceAccountBinding{}, e
	}
	if old != nil {
		if old.Binding.SourceFingerprint != fingerprint {
			return old.Binding, invalidSourceAccounts()
		}
		return old.Binding, sourceAccountHeadersTx(ctx, tx, old.Binding)
	}
	var existingRows int
	if tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM source_site_account_rows`).Scan(&existingRows) != nil || existingRows != 0 {
		return sourceAccountBinding{}, invalidSourceAccounts()
	}
	s, settingsSHA, e := p.reviewedIYUUSettings(ctx, tx)
	if e != nil {
		return sourceAccountBinding{}, e
	}
	b := sourceAccountBinding{Version: 1, SourceFingerprint: fingerprint, ConfigSHA: reviewedStatisticConfigSHA, DataSHA: reviewedStatisticAccountsSHA, CatalogSHA: reviewedStatisticCatalogSHA, SettingsSHA: settingsSHA, Rows: headers}
	for _, source := range source.Sites {
		_, origin, _, e := siteImportOrigin(source.URL)
		if e != nil {
			return b, e
		}
		matches := []nativeSiteIdentity{}
		for _, site := range s.Sites {
			current, e := nativeConfiguredSiteOrigin(site)
			if e != nil || current != origin {
				continue
			}
			identity, e := p.store.readNativeSiteIdentity(ctx, tx, "site_id", site.ID)
			if e != nil || identity.State != "active" || identity.CanonicalOrigin != origin {
				return b, siteIdentityError()
			}
			matches = append(matches, identity)
		}
		if len(matches) != 1 {
			return b, bad("SITE_ACCOUNT_REFERENCE_UNMATCHED", "原站点历史未能对应到唯一站点")
		}
		m := matches[0]
		b.Sites = append(b.Sites, sourceAccountSite{source.OriginalID, m.SourceID, m.SiteID, origin, source.Domain, source.URL})
	}
	if !validSourceAccountBinding(b) {
		return b, invalidSourceAccounts()
	}
	return b, nil
}
func validReviewedStatisticImport(in PluginImport, installation PluginInstallation) bool {
	if in.reviewedStatistic == nil || !validSourceAccountBinding(in.reviewedStatistic.Binding) || installation.InstanceID != "SiteStatistic" || installation.PluginID != "SiteStatistic" || installation.Version != "1.9.7" || in.SourceConfigAbsent || len(in.Data) != 0 || tokenHash(string(in.Config)) != reviewedStatisticConfigSHA || sourceAccountDataDigest(in.reviewedStatistic.Rows) != reviewedStatisticAccountsSHA || len(in.reviewedStatistic.Rows) != len(in.reviewedStatistic.Binding.Rows) {
		return false
	}
	for i, raw := range in.reviewedStatistic.Rows {
		_, h, e := decodeSourceAccount([]byte(raw))
		if e != nil || h != in.reviewedStatistic.Binding.Rows[i] {
			return false
		}
	}
	want, _ := pluginJSON(reviewedIYUUJSON(builtin.SiteStatisticManifest()), true)
	got, e := pluginJSON(installation.Manifest, true)
	return e == nil && bytes.Equal(want, got)
}
func (p *PluginStore) reviewedStatisticTargetTx(ctx context.Context, tx *sql.Tx, b sourceAccountBinding) error {
	s, sha, e := p.reviewedIYUUSettings(ctx, tx)
	if e != nil || sha != b.SettingsSHA {
		return bad("SITE_ACCOUNT_TARGET_CHANGED", "站点设置已变化，请重新核对历史迁移")
	}
	for _, bind := range b.Sites {
		identity, e := p.store.readNativeSiteIdentity(ctx, tx, "site_id", bind.SiteID)
		if e != nil || identity.SourceID != bind.NativeID || identity.State != "active" || identity.CanonicalOrigin != bind.Origin {
			return siteIdentityError()
		}
		matched := false
		for _, site := range s.Sites {
			origin, e := nativeConfiguredSiteOrigin(site)
			matched = matched || e == nil && site.ID == bind.SiteID && origin == bind.Origin
		}
		if !matched {
			return siteIdentityError()
		}
	}
	var count int
	if tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM source_site_account_rows`).Scan(&count) != nil || count != 0 {
		return invalidSourceAccounts()
	}
	return nil
}
func (p *PluginStore) commitReviewedStatisticTx(ctx context.Context, tx *sql.Tx, in reviewedStatisticImport, migration, digest string) error {
	b := in.Binding
	for i, raw := range in.Rows {
		h := b.Rows[i]
		sealed, e := p.seal(sourceAccountKind, b.SourceFingerprint, strconv.FormatInt(h.ID, 10), json.RawMessage(raw))
		if e != nil {
			return e
		}
		if _, e = tx.ExecContext(ctx, `INSERT INTO source_site_account_rows VALUES(?,?,?,?,?,?,?)`, b.SourceFingerprint, h.ID, h.Domain, h.Day, h.Time, h.Good, sealed); e != nil {
			return e
		}
	}
	proof := sourceAccountProof{Binding: b, Migration: migration, ImportDigest: digest}
	sealed, e := p.seal(sourceAccountKind, "SiteStatistic", "main", reviewedIYUUJSON(proof))
	if e != nil {
		return e
	}
	if _, e = tx.ExecContext(ctx, `INSERT INTO records(kind,id,payload,updated_at) VALUES(?,'main',?,?)`, sourceAccountKind, sealed, now()); e != nil {
		return e
	}
	var protocol siteReferenceProtocol
	if tx.QueryRowContext(ctx, `SELECT sealed FROM native_site_reference_protocols WHERE instance_id='SiteStatistic' AND generation=1`).Scan(&sealed) != nil || p.store.Unseal(sealed, &protocol) != nil || protocol.Protocol != originalUnclaimedSiteReferenceProtocol || protocol.Source != "moviepilot-import/v1:"+migration {
		return invalidSourceAccounts()
	}
	protocol.Protocol, protocol.Source = sourceSiteReferenceProtocol, reviewedStatisticProtocolPrefix+b.SourceFingerprint
	sealed, e = p.store.Seal(protocol)
	if e != nil {
		return e
	}
	_, e = tx.ExecContext(ctx, `UPDATE native_site_reference_protocols SET sealed=? WHERE instance_id='SiteStatistic' AND generation=1`, sealed)
	return e
}
func (p *PluginStore) ImportReviewedSiteStatistic(ctx context.Context, migration string, raw []byte) (PluginMigrationReceipt, error) {
	in, headers, e := validateReviewedStatistic(raw)
	if e != nil {
		return PluginMigrationReceipt{}, e
	}
	fp, e := ReviewedSiteStatisticFingerprint(raw)
	if e != nil {
		return PluginMigrationReceipt{}, e
	}
	b, e := p.prepareReviewedStatistic(ctx, in, headers, fp)
	if e != nil {
		return PluginMigrationReceipt{}, e
	}
	return p.Import(ctx, PluginImport{RunID: migration, Installation: PluginInstallation{InstanceID: "SiteStatistic", PluginID: "SiteStatistic", Version: "1.9.7", ManifestSchemaVersion: 1, Manifest: reviewedIYUUJSON(builtin.SiteStatisticManifest())}, Config: json.RawMessage(in.Config), Data: map[string]json.RawMessage{}, reviewedStatistic: &reviewedStatisticImport{Binding: b, Rows: in.AccountRows}})
}
func ValidateReviewedSiteStatisticTarget(ctx context.Context, db *sql.DB, key, raw []byte) error {
	if db == nil || len(key) != 32 {
		return invalidSourceAccounts()
	}
	in, headers, e := validateReviewedStatistic(raw)
	if e != nil {
		return e
	}
	fp, e := ReviewedSiteStatisticFingerprint(raw)
	if e != nil {
		return e
	}
	p := &PluginStore{store: &Store{DB: db, key: key}}
	_, e = p.prepareReviewedStatistic(ctx, in, headers, fp)
	return e
}

func (p *PluginStore) validateStatisticProtocol(ctx context.Context, tx pluginRemoveLinkJournalReader, in PluginInstallation, source string) error {
	proof, e := p.sourceAccountProofTx(ctx, tx)
	if e != nil || proof == nil || in.InstanceID != "SiteStatistic" || in.PluginID != "SiteStatistic" || in.Version != "1.9.7" || source != reviewedStatisticProtocolPrefix+proof.Binding.SourceFingerprint {
		return invalidSourceAccounts()
	}
	return nil
}

// The shared account archive outlives plugin reset/uninstall. Only retained
// source configuration inherits its execution namespace; an explicit reset
// writes the ordinary native protocol through the existing lifecycle.
func (p *PluginStore) inheritStatisticProtocolTx(ctx context.Context, tx *sql.Tx, in PluginInstallation, next siteReferenceProtocol) (siteReferenceProtocol, error) {
	var retained int
	if e := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM plugin_values WHERE instance_id=? AND kind='config'`, in.InstanceID).Scan(&retained); e != nil || retained == 0 {
		return next, e
	}
	var generation int64
	e := tx.QueryRowContext(ctx, `SELECT generation FROM native_site_reference_protocols WHERE instance_id=? AND generation<? ORDER BY generation DESC LIMIT 1`, in.InstanceID, in.Generation).Scan(&generation)
	if errors.Is(e, sql.ErrNoRows) {
		return next, nil
	}
	if e != nil {
		return next, e
	}
	previous := in
	previous.Generation = generation
	old, e := p.readSiteReferenceProtocol(ctx, tx, previous)
	if e != nil {
		return next, e
	}
	if strings.HasPrefix(old.Source, reviewedStatisticProtocolPrefix) {
		next.Source = old.Source
	}
	return next, nil
}
