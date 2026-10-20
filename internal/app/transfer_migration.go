package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	transfer "light-media/internal/plugins/torrenttransfer"
)

const reviewedTransferProtocol = "reviewed-original-downloader-name/v1"
const reviewedTransferMappingKind = "plugin_original_transfer_references"
const reviewedTransferReferencesSHA = "65c0c47b87517d03c43124db54f62d55897daa79aa285e393b8b3359b5e683f2"
const reviewedTransferSourceNameSHA = "22d1fb414b6d62b69391bb42b9b94314a5b2fa77a7e8abd507cf3152ed4fa38e"
const reviewedTransferTargetNameSHA = "7817bb812e82168bd48fe1ea6783078d42be37e8db9bdaafdac5c45804aca64f"

type reviewedTransferProfile struct {
	SourceSHA, ConfigSHA, DataSHA string
	Rows                          int
}

func reviewedTransferProfileFor(id string) (reviewedTransferProfile, bool) {
	switch id {
	case "TorrentTransfer":
		return reviewedTransferProfile{"e5f513a57cf42ee8778744ba30cf669f93a5a3e6c7589ca0b2e3b9c483651bd5", "50b5ec8157db0974ef6a1f57214341ee9962a479454550b224086b188c07af9e", "00a23b1f182b7c1c8bb0077751ca34a8ff246cc26be77749245bc9a8108d9088", 422}, true
	case "TorrentTransfermusic":
		return reviewedTransferProfile{"4d271e68aa3fc994c1d50cd850d4b1e5699c9f7900eaa106082b9f13efe4c5bb", "08bd158b96b2b4339992ff9e5f3e86cedeb0f83ecc4942c209ec6ae9173e576a", "34f31c30ead3deba2a5c27117712245f20ca8029b3a6d29dbb46e0c82a9ff47c", 36}, true
	}
	return reviewedTransferProfile{}, false
}

// This pinned offline input has no HTTP entry point. Private source material is
// supplied over stdin and only the normal AEAD plugin records are persisted.
type reviewedTransferSnapshot struct {
	Version         int                      `json:"version"`
	SourceContainer string                   `json:"source_container"`
	PluginID        string                   `json:"plugin_id"`
	SourceSHA       string                   `json:"source_sha256"`
	InstalledCount  int                      `json:"installed_count"`
	Config          string                   `json:"config"`
	Data            map[string]string        `json:"data"`
	Downloaders     []reviewedIYUUDownloader `json:"downloaders"`
	FileCount       int                      `json:"file_count"`
	SymlinkCount    int                      `json:"symlink_count"`
}
type reviewedTransferDownloader struct {
	Name, Kind, NativeID string
}
type reviewedTransferBinding struct {
	Version                                                      int
	PluginID, SourceFingerprint, ConfigSHA, DataSHA, SettingsSHA string
	Generation                                                   int64
	Downloaders                                                  []reviewedTransferDownloader
}
type reviewedTransferProof struct {
	Binding                 reviewedTransferBinding
	Migration, ImportDigest string
}

func invalidReviewedTransfer() error {
	return bad("TORRENT_TRANSFER_REFERENCE_INVALID", "转移做种来源或引用无法校验，请核对迁移收据")
}
func reviewedTransferReferences(ds []reviewedIYUUDownloader) (string, error) {
	type ref struct {
		Name     string `json:"name"`
		Kind     string `json:"kind"`
		Endpoint string `json:"endpoint"`
		Username string `json:"username"`
	}
	if len(ds) != 2 {
		return "", invalidReviewedTransfer()
	}
	rows := []ref{}
	for i, d := range ds {
		kind := "qbittorrent"
		nameSHA := reviewedTransferSourceNameSHA
		if i == 1 {
			kind = "transmission"
			nameSHA = reviewedTransferTargetNameSHA
		}
		endpoint, e := reviewedIYUUDownloaderEndpoint(kind, d.Host, true)
		if e != nil || d.Kind != kind || !d.Enabled || tokenHash(d.Name) != nameSHA {
			return "", invalidReviewedTransfer()
		}
		rows = append(rows, ref{d.Name, kind, endpoint, d.Username})
	}
	return tokenHash(string(reviewedIYUUJSON(rows))), nil
}
func validateReviewedTransfer(raw []byte) (reviewedTransferSnapshot, map[string]json.RawMessage, error) {
	var in reviewedTransferSnapshot
	if len(raw) > pluginImportLimit || sourceSettingsJSON(raw, &in) != nil {
		return in, nil, invalidReviewedTransfer()
	}
	fields, e := sourceSettingsObject(raw, "version", "source_container", "plugin_id", "source_sha256", "installed_count", "config", "data", "downloaders", "file_count", "symlink_count")
	if e != nil {
		return in, nil, invalidReviewedTransfer()
	}
	for _, value := range fields {
		if bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return in, nil, invalidReviewedTransfer()
		}
	}
	profile, ok := reviewedTransferProfileFor(in.PluginID)
	if !ok || in.Version != 1 || in.SourceContainer != "cb82e3f550881334ef3814426016d08d909bccd88d3297522756ba31654d129a" || in.SourceSHA != profile.SourceSHA || in.InstalledCount != 1 || in.FileCount != 0 || in.SymlinkCount != 0 || tokenHash(in.Config) != profile.ConfigSHA || len(in.Data) != profile.Rows {
		return in, nil, invalidReviewedTransfer()
	}
	refs, e := reviewedTransferReferences(in.Downloaders)
	if e != nil || refs != reviewedTransferReferencesSHA {
		return in, nil, invalidReviewedTransfer()
	}
	var cfg struct {
		From string `json:"fromdownloader"`
		To   string `json:"todownloader"`
	}
	if json.Unmarshal([]byte(in.Config), &cfg) != nil || cfg.From != in.Downloaders[0].Name || cfg.To != in.Downloaders[1].Name {
		return in, nil, invalidReviewedTransfer()
	}
	data := map[string]json.RawMessage{}
	for key, value := range in.Data {
		if !validPluginKey(key) || !json.Valid([]byte(value)) {
			return in, nil, invalidReviewedTransfer()
		}
		data[key] = json.RawMessage(value)
	}
	if reviewedIYUUDataDigest(data) != profile.DataSHA {
		return in, nil, invalidReviewedTransfer()
	}
	return in, data, nil
}
func ValidateReviewedTorrentTransferInput(raw []byte) error {
	_, _, e := validateReviewedTransfer(raw)
	return e
}
func ReviewedTorrentTransferFingerprint(raw []byte) (string, error) {
	in, _, e := validateReviewedTransfer(raw)
	if e != nil {
		return "", e
	}
	return tokenHash(in.PluginID + "/1.10.3/reviewed-source/v1/" + string(reviewedIYUUJSON(in))), nil
}
func validReviewedTransferBinding(b reviewedTransferBinding) bool {
	profile, ok := reviewedTransferProfileFor(b.PluginID)
	if !ok || b.Version != 1 || b.Generation != 1 || b.ConfigSHA != profile.ConfigSHA || b.DataSHA != profile.DataSHA || !pluginStorageDigest.MatchString(b.SourceFingerprint) || !pluginStorageDigest.MatchString(b.SettingsSHA) || len(b.Downloaders) != 2 || b.Downloaders[0].NativeID == b.Downloaders[1].NativeID {
		return false
	}
	for i, d := range b.Downloaders {
		kind, name := "qbittorrent", reviewedTransferSourceNameSHA
		if i == 1 {
			kind, name = "transmission", reviewedTransferTargetNameSHA
		}
		if d.Kind != kind || tokenHash(d.Name) != name || d.NativeID == "" {
			return false
		}
	}
	return true
}
func (p *PluginStore) readReviewedTransferProof(ctx context.Context, q pluginRemoveLinkJournalReader, instance string) (reviewedTransferProof, error) {
	var proof reviewedTransferProof
	var sealed []byte
	e := q.QueryRowContext(ctx, `SELECT payload FROM records WHERE kind=? AND id=?`, reviewedTransferMappingKind, instance).Scan(&sealed)
	if e != nil {
		return proof, e
	}
	raw, e := p.unseal(reviewedTransferMappingKind, instance, "main", sealed)
	if e != nil || cleanupJournalStrict(raw, &proof) != nil || proof.Binding.PluginID != instance || !validReviewedTransferBinding(proof.Binding) || !pluginStorageID.MatchString(proof.Migration) || !pluginStorageDigest.MatchString(proof.ImportDigest) {
		return proof, invalidReviewedTransfer()
	}
	var digest, state string
	if q.QueryRowContext(ctx, `SELECT r.digest,m.state FROM plugin_migration_receipts r JOIN plugin_migration_runs m ON m.id=r.run_id AND m.source_fingerprint=r.source_fingerprint AND m.source_plugin_id=r.source_plugin_id AND m.instance_id=r.instance_id WHERE r.run_id=? AND r.source_fingerprint=? AND r.source_plugin_id=? AND r.instance_id=?`, proof.Migration, proof.Binding.SourceFingerprint, instance, instance).Scan(&digest, &state) != nil || digest != proof.ImportDigest || state != "imported" {
		return proof, invalidReviewedTransfer()
	}
	return proof, nil
}
func (p *PluginStore) prepareReviewedTransferBinding(ctx context.Context, in reviewedTransferSnapshot, fingerprint string) (reviewedTransferBinding, error) {
	tx, e := p.store.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if e != nil {
		return reviewedTransferBinding{}, e
	}
	defer tx.Rollback()
	proof, e := p.readReviewedTransferProof(ctx, tx, in.PluginID)
	if e == nil {
		if proof.Binding.SourceFingerprint != fingerprint {
			return proof.Binding, invalidReviewedTransfer()
		}
		if _, _, e = p.transferRootReadTx(ctx, tx, in.PluginID, proof.Binding.Generation); e != nil {
			return proof.Binding, e
		}
		return proof.Binding, tx.Commit()
	}
	if !errors.Is(e, sql.ErrNoRows) {
		return proof.Binding, e
	}
	var existing int
	if e = tx.QueryRowContext(ctx, `SELECT (SELECT COUNT(*) FROM plugin_installations WHERE instance_id=?)+(SELECT COUNT(*) FROM plugin_migration_receipts WHERE instance_id=?)`, in.PluginID, in.PluginID).Scan(&existing); e != nil || existing != 0 {
		return proof.Binding, invalidReviewedTransfer()
	}
	settings, sha, e := p.reviewedIYUUSettings(ctx, tx)
	if e != nil {
		return proof.Binding, e
	}
	profile, _ := reviewedTransferProfileFor(in.PluginID)
	b := reviewedTransferBinding{Version: 1, PluginID: in.PluginID, SourceFingerprint: fingerprint, ConfigSHA: profile.ConfigSHA, DataSHA: profile.DataSHA, SettingsSHA: sha, Generation: 1}
	for _, source := range in.Downloaders {
		endpoint, e := reviewedIYUUDownloaderEndpoint(source.Kind, source.Host, true)
		if e != nil {
			return b, e
		}
		matches := []DownloaderConfig{}
		for _, current := range settings.Downloaders {
			value, e := reviewedIYUUDownloaderEndpoint(source.Kind, current.BaseURL, false)
			if e == nil && reviewedIYUUKind(current.Kind) == source.Kind && value == endpoint && current.Username == source.Username && current.Password == source.Password && downloaderEnabled(current) {
				matches = append(matches, current)
			}
		}
		if len(matches) != 1 {
			return b, invalidReviewedTransfer()
		}
		b.Downloaders = append(b.Downloaders, reviewedTransferDownloader{source.Name, source.Kind, matches[0].ID})
	}
	if !validReviewedTransferBinding(b) {
		return b, invalidReviewedTransfer()
	}
	return b, tx.Commit()
}
func validReviewedTransferImport(in PluginImport, installation PluginInstallation) bool {
	b := in.reviewedTransfer
	if b == nil || !validReviewedTransferBinding(*b) || installation.InstanceID != b.PluginID || installation.PluginID != b.PluginID || installation.Version != "1.10.3" || installation.ManifestSchemaVersion != 1 || in.SourceConfigAbsent || tokenHash(string(in.Config)) != b.ConfigSHA || reviewedIYUUDataDigest(in.Data) != b.DataSHA {
		return false
	}
	want, e := pluginJSON(reviewedIYUUJSON(transfer.Manifest(b.PluginID)), true)
	got, ge := pluginJSON(installation.Manifest, true)
	return e == nil && ge == nil && bytes.Equal(got, want)
}
func (p *PluginStore) reviewedTransferTargetTx(ctx context.Context, tx *sql.Tx, b reviewedTransferBinding) error {
	_, sha, e := p.reviewedIYUUSettings(ctx, tx)
	if e != nil || sha != b.SettingsSHA {
		return invalidReviewedTransfer()
	}
	return nil
}
func (p *PluginStore) commitReviewedTransferReferencesTx(ctx context.Context, tx *sql.Tx, b reviewedTransferBinding, migration, digest string) error {
	if e := p.reviewedTransferTargetTx(ctx, tx, b); e != nil {
		return e
	}
	root, prior, e := p.transferRootReadTx(ctx, tx, b.PluginID, b.Generation)
	if e != nil || root.ReferenceProtocol != "moviepilot-reference-unclaimed/v1" || root.ReferenceMigration != migration || len(root.Runs) != 0 {
		return invalidReviewedTransfer()
	}
	sealed, e := p.seal(reviewedTransferMappingKind, b.PluginID, "main", reviewedIYUUJSON(reviewedTransferProof{b, migration, digest}))
	if e != nil {
		return e
	}
	if _, e = tx.ExecContext(ctx, `INSERT INTO records(kind,id,payload,updated_at) VALUES(?,?,?,?)`, reviewedTransferMappingKind, b.PluginID, sealed, now()); e != nil {
		return e
	}
	root.ReferenceProtocol = reviewedTransferProtocol
	root.Sequence++
	return p.transferSaveRootTx(ctx, tx, root, prior)
}
func (p *PluginStore) ImportReviewedTorrentTransfer(ctx context.Context, runID string, raw []byte) (PluginMigrationReceipt, error) {
	in, data, e := validateReviewedTransfer(raw)
	if e != nil {
		return PluginMigrationReceipt{}, e
	}
	fingerprint, e := ReviewedTorrentTransferFingerprint(raw)
	if e != nil {
		return PluginMigrationReceipt{}, e
	}
	b, e := p.prepareReviewedTransferBinding(ctx, in, fingerprint)
	if e != nil {
		return PluginMigrationReceipt{}, e
	}
	return p.Import(ctx, PluginImport{RunID: runID, Installation: PluginInstallation{InstanceID: in.PluginID, PluginID: in.PluginID, Version: "1.10.3", ManifestSchemaVersion: 1, Manifest: reviewedIYUUJSON(transfer.Manifest(in.PluginID))}, Config: json.RawMessage(in.Config), Data: data, reviewedTransfer: &b})
}
func ValidateReviewedTorrentTransferTarget(ctx context.Context, db *sql.DB, key, raw []byte) error {
	if db == nil || len(key) != 32 {
		return invalidReviewedTransfer()
	}
	in, _, e := validateReviewedTransfer(raw)
	if e != nil {
		return e
	}
	fingerprint, e := ReviewedTorrentTransferFingerprint(raw)
	if e != nil {
		return e
	}
	p := &PluginStore{store: &Store{DB: db, key: key}}
	_, e = p.prepareReviewedTransferBinding(ctx, in, fingerprint)
	return e
}
