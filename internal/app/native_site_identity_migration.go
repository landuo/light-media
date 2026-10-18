package app

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
)

// These types are internal migration-host contracts, not public HTTP inputs.
// The independent fingerprint is supplied by an authenticated read-only source
// snapshot collector; an import file, display name or DNS suffix cannot claim it.
type siteIdentitySourceEntry struct {
	OriginalSourceID int64  `json:"original_source_id"`
	CanonicalOrigin  string `json:"canonical_origin"`
}
type siteIdentitySourceSnapshot struct {
	Version           int                       `json:"version"`
	SnapshotID        string                    `json:"snapshot_id"`
	SourceFingerprint string                    `json:"source_fingerprint"`
	SnapshotDigest    string                    `json:"snapshot_digest"`
	Entries           []siteIdentitySourceEntry `json:"entries"`
}
type siteIdentityMigrationReceipt struct {
	Version           int    `json:"version"`
	ReceiptID         string `json:"receipt_id"`
	SnapshotID        string `json:"snapshot_id"`
	SnapshotDigest    string `json:"snapshot_digest"`
	SourceFingerprint string `json:"source_fingerprint"`
	OriginalSourceID  int64  `json:"original_source_id"`
	NativeSourceID    int64  `json:"native_source_id"`
	SiteID            string `json:"site_id"`
	CanonicalOrigin   string `json:"canonical_origin"`
}

func validSiteIdentityFingerprint(v string) bool {
	b, err := hex.DecodeString(v)
	return err == nil && len(b) == 32 && hex.EncodeToString(b) == v
}
func siteIdentitySourceDigest(entries []siteIdentitySourceEntry) string {
	raw, _ := json.Marshal(entries)
	h := sha256.Sum256(raw)
	return hex.EncodeToString(h[:])
}
func validateSiteIdentitySourceEntries(entries []siteIdentitySourceEntry) error {
	if len(entries) == 0 || len(entries) > 128 {
		return siteIdentityError()
	}
	ids := map[int64]bool{}
	origins := map[string]bool{}
	for _, v := range entries {
		_, origin, _, err := siteImportOrigin(v.CanonicalOrigin)
		if err != nil || origin != v.CanonicalOrigin || origin == "" || v.OriginalSourceID < 1 || ids[v.OriginalSourceID] || origins[origin] {
			return siteIdentityError()
		}
		ids[v.OriginalSourceID] = true
		origins[origin] = true
	}
	return nil
}

// Explicit write-only preparation for a reviewed migration. Native inventory and
// all read APIs never call this function and never infer a source fingerprint.
func (a *App) registerSiteIdentitySourceSnapshot(ctx context.Context, fingerprint string, entries []siteIdentitySourceEntry) (siteIdentitySourceSnapshot, error) {
	var out siteIdentitySourceSnapshot
	if !validSiteIdentityFingerprint(fingerprint) {
		return out, siteIdentityError()
	}
	owned := append([]siteIdentitySourceEntry(nil), entries...)
	sort.Slice(owned, func(i, j int) bool { return owned[i].OriginalSourceID < owned[j].OriginalSourceID })
	if err := validateSiteIdentitySourceEntries(owned); err != nil {
		return out, err
	}
	out = siteIdentitySourceSnapshot{Version: 1, SourceFingerprint: fingerprint, SnapshotDigest: siteIdentitySourceDigest(owned), Entries: owned}
	out.SnapshotID = "sss_" + a.Store.siteIdentityMAC("source-snapshot", []string{fingerprint, out.SnapshotDigest})
	tx, err := a.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	var oldRaw []byte
	err = tx.QueryRowContext(ctx, `SELECT sealed FROM native_site_source_snapshots WHERE snapshot_id=?`, out.SnapshotID).Scan(&oldRaw)
	if err == nil {
		old, e := a.readSiteIdentitySourceSnapshot(ctx, tx, out.SnapshotID)
		if e != nil {
			return out, e
		}
		if old.SnapshotDigest != out.SnapshotDigest || old.SourceFingerprint != fingerprint {
			return out, siteIdentityError()
		}
		return old, tx.Commit()
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return out, err
	}
	sealed, err := a.Store.Seal(out)
	if err != nil {
		return out, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO native_site_source_snapshots VALUES(?,?,?,?)`, out.SnapshotID, fingerprint, out.SnapshotDigest, sealed); err != nil {
		return out, err
	}
	return out, tx.Commit()
}
func (a *App) readSiteIdentitySourceSnapshot(ctx context.Context, q siteIdentityReader, id string) (siteIdentitySourceSnapshot, error) {
	var out siteIdentitySourceSnapshot
	var fingerprint, digest string
	var sealed []byte
	if err := q.QueryRowContext(ctx, `SELECT source_fingerprint,snapshot_digest,sealed FROM native_site_source_snapshots WHERE snapshot_id=?`, id).Scan(&fingerprint, &digest, &sealed); err != nil {
		return out, err
	}
	if a.Store.Unseal(sealed, &out) != nil || out.Version != 1 || out.SnapshotID != id || out.SourceFingerprint != fingerprint || out.SnapshotDigest != digest || !validSiteIdentityFingerprint(fingerprint) || validateSiteIdentitySourceEntries(out.Entries) != nil || siteIdentitySourceDigest(out.Entries) != digest || id != "sss_"+a.Store.siteIdentityMAC("source-snapshot", []string{fingerprint, digest}) {
		return out, siteIdentityError()
	}
	return out, nil
}

// The host supplies an explicit local target; neither the name nor a loosely
// related domain selects it. This records reference identity only, never copies
// an original plugin config, onlyonce flag, KV value or credential.
func (a *App) commitSiteIdentityMigration(ctx context.Context, snapshotID string, originalID int64, targetAppID string) (siteIdentityMigrationReceipt, error) {
	var out siteIdentityMigrationReceipt
	a.settingsMu.Lock()
	defer a.settingsMu.Unlock()
	tx, err := a.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	snapshot, err := a.readSiteIdentitySourceSnapshot(ctx, tx, snapshotID)
	if err != nil {
		return out, err
	}
	var sourceOrigin string
	for _, v := range snapshot.Entries {
		if v.OriginalSourceID == originalID {
			sourceOrigin = v.CanonicalOrigin
			break
		}
	}
	if sourceOrigin == "" {
		return out, siteIdentityError()
	}
	local, err := a.Store.readNativeSiteIdentity(ctx, tx, "site_id", targetAppID)
	if err != nil {
		return out, err
	}
	if local.State != "active" || local.CanonicalOrigin != sourceOrigin {
		return out, siteIdentityError()
	}
	found := false
	for _, site := range a.Settings().Sites {
		if site.ID == targetAppID {
			origin, e := nativeConfiguredSiteOrigin(site)
			if e != nil || origin != sourceOrigin {
				return out, siteIdentityError()
			}
			found = true
			break
		}
	}
	if !found {
		return out, siteIdentityError()
	}
	out = siteIdentityMigrationReceipt{Version: 1, SnapshotID: snapshotID, SnapshotDigest: snapshot.SnapshotDigest, SourceFingerprint: snapshot.SourceFingerprint, OriginalSourceID: originalID, NativeSourceID: local.SourceID, SiteID: targetAppID, CanonicalOrigin: sourceOrigin}
	out.ReceiptID = "smr_" + a.Store.siteIdentityMAC("migration-receipt", out)
	var oldSealed []byte
	var oldNativeID int64
	var oldAppID string
	err = tx.QueryRowContext(ctx, `SELECT native_source_id,site_id,sealed FROM native_site_migration_receipts WHERE source_fingerprint=? AND original_source_id=?`, snapshot.SourceFingerprint, originalID).Scan(&oldNativeID, &oldAppID, &oldSealed)
	if err == nil {
		var old siteIdentityMigrationReceipt
		if a.Store.Unseal(oldSealed, &old) != nil || old.NativeSourceID != oldNativeID || old.SiteID != oldAppID || a.verifySiteIdentityMigrationReceipt(ctx, tx, old) != nil || old.SourceFingerprint != out.SourceFingerprint || old.OriginalSourceID != out.OriginalSourceID || old.NativeSourceID != out.NativeSourceID || old.SiteID != out.SiteID || old.CanonicalOrigin != out.CanonicalOrigin {
			return out, siteIdentityError()
		}
		// A newer observed inventory may add other sites. Existing exact identity
		// bindings retain their original immutable receipt, never a new claim.
		return old, tx.Commit()
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return out, err
	}
	sealed, err := a.Store.Seal(out)
	if err != nil {
		return out, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO native_site_migration_receipts VALUES(?,?,?,?,?)`, snapshot.SourceFingerprint, originalID, local.SourceID, local.SiteID, sealed); err != nil {
		return out, err
	}
	return out, tx.Commit()
}
func (a *App) migratedSiteIdentity(ctx context.Context, fingerprint string, originalID int64) (nativeSiteIdentity, error) {
	var out nativeSiteIdentity
	if !validSiteIdentityFingerprint(fingerprint) || originalID < 1 {
		return out, siteIdentityError()
	}
	tx, err := a.Store.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	var nativeID int64
	var appID string
	var sealed []byte
	if err = tx.QueryRowContext(ctx, `SELECT native_source_id,site_id,sealed FROM native_site_migration_receipts WHERE source_fingerprint=? AND original_source_id=?`, fingerprint, originalID).Scan(&nativeID, &appID, &sealed); err != nil {
		return out, err
	}
	var receipt siteIdentityMigrationReceipt
	if a.Store.Unseal(sealed, &receipt) != nil || receipt.Version != 1 || receipt.SourceFingerprint != fingerprint || receipt.OriginalSourceID != originalID || receipt.NativeSourceID != nativeID || receipt.SiteID != appID {
		return out, siteIdentityError()
	}
	if err := a.verifySiteIdentityMigrationReceipt(ctx, tx, receipt); err != nil {
		return out, err
	}
	out, err = a.Store.readNativeSiteIdentity(ctx, tx, "source_id", nativeID)
	if err != nil {
		return out, err
	}
	if out.SiteID != appID || out.State != "active" || out.CanonicalOrigin != receipt.CanonicalOrigin {
		return out, siteIdentityError()
	}
	return out, tx.Commit()
}

func (a *App) verifySiteIdentityMigrationReceipt(ctx context.Context, q siteIdentityReader, receipt siteIdentityMigrationReceipt) error {
	if receipt.Version != 1 || receipt.OriginalSourceID < 1 || receipt.NativeSourceID < 1 || receipt.SiteID == "" || receipt.CanonicalOrigin == "" {
		return siteIdentityError()
	}
	proof := receipt.ReceiptID
	receipt.ReceiptID = ""
	if proof != "smr_"+a.Store.siteIdentityMAC("migration-receipt", receipt) {
		return siteIdentityError()
	}
	snapshot, err := a.readSiteIdentitySourceSnapshot(ctx, q, receipt.SnapshotID)
	if err != nil || snapshot.SourceFingerprint != receipt.SourceFingerprint || snapshot.SnapshotDigest != receipt.SnapshotDigest {
		return siteIdentityError()
	}
	for _, entry := range snapshot.Entries {
		if entry.OriginalSourceID == receipt.OriginalSourceID && entry.CanonicalOrigin == receipt.CanonicalOrigin {
			return nil
		}
	}
	return siteIdentityError()
}
