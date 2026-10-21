package app

// This offline, disabled-only importer validates a supplied snapshot contract.
// It does not attest that a caller's snapshot was collected from production.
import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"syscall"

	"light-media/internal/plugins/removelink"
	cleanuphost "light-media/internal/plugins/removelink/host"
)

const reviewedRemoveLinkSourceSHA = "1f9e719e16f5f72340aa50e942d6f7062390a4bddd82f5f3f47d58ef757dbda4"
const reviewedRemoveLinkMappingKind = "plugin_original_remove_link_references"

type reviewedRemoveLinkPath struct {
	Source string `json:"source_root"`
	Target string `json:"target_root"`
}
type reviewedRemoveLinkSnapshot struct {
	Version         int                        `json:"version"`
	SourceContainer string                     `json:"source_container"`
	PluginID        string                     `json:"plugin_id"`
	InstanceID      string                     `json:"instance_id"`
	SourceSHA       string                     `json:"source_sha256"`
	InstalledCount  int                        `json:"installed_count"`
	Config          string                     `json:"config"`
	Data            map[string]json.RawMessage `json:"data"`
	FileCount       int                        `json:"file_count"`
	SymlinkCount    int                        `json:"symlink_count"`
	Paths           []reviewedRemoveLinkPath   `json:"path_mappings"`
}
type reviewedRemoveLinkDirectory struct {
	Path  string
	Stamp cleanuphost.Stamp
}
type reviewedRemoveLinkBinding struct {
	Version                                                                int
	InstanceID, SourceFingerprint, SourceConfigSHA, ConfigSHA, SettingsSHA string
	Paths                                                                  []reviewedRemoveLinkPath
	Directories                                                            []reviewedRemoveLinkDirectory
}
type reviewedRemoveLinkProof struct {
	Evidence                string // supplied-snapshot-validated/v1, not independently observed deployment evidence.
	Binding                 reviewedRemoveLinkBinding
	Migration, ImportDigest string
}

func invalidReviewedRemoveLink() error {
	return bad("REMOVE_LINK_REFERENCE_INVALID", "联动删除来源或路径引用未通过核对，未导入或启用")
}
func reviewedRemoveLinkPathValid(s string) bool {
	return importPath(s) && s != "/" && filepath.Clean(s) == s && !strings.ContainsAny(s, ":\r\n")
}
func validateReviewedRemoveLink(raw []byte) (reviewedRemoveLinkSnapshot, json.RawMessage, []string, error) {
	var in reviewedRemoveLinkSnapshot
	fail := func() (reviewedRemoveLinkSnapshot, json.RawMessage, []string, error) {
		return in, nil, nil, invalidReviewedRemoveLink()
	}
	if len(raw) > pluginImportLimit || sourceSettingsJSON(raw, &in) != nil {
		return fail()
	}
	fields, e := sourceSettingsObject(raw, "version", "source_container", "plugin_id", "instance_id", "source_sha256", "installed_count", "config", "data", "file_count", "symlink_count", "path_mappings")
	if e != nil {
		return fail()
	}
	for _, v := range fields {
		if bytes.Equal(bytes.TrimSpace(v), []byte("null")) {
			return fail()
		}
	}
	if in.Version != 1 || !pluginStorageDigest.MatchString(in.SourceContainer) || in.PluginID != "RemoveLink" || !pluginStorageID.MatchString(in.InstanceID) || in.SourceSHA != reviewedRemoveLinkSourceSHA || in.InstalledCount != 1 || in.FileCount != 0 || in.SymlinkCount != 0 || len(in.Data) != 0 || len(in.Paths) > 256 {
		return fail()
	}
	var cfg map[string]json.RawMessage
	if sourceSettingsJSON([]byte(in.Config), &cfg) != nil || cfg == nil {
		return fail()
	}
	var enabled any
	if v, ok := cfg["enabled"]; ok && json.Unmarshal(v, &enabled) != nil {
		return fail()
	}
	// Original config.get("enabled") defaults to None, which is disabled.
	if removelink.Truth(enabled) {
		return fail()
	}
	for i, m := range in.Paths {
		if !reviewedRemoveLinkPathValid(m.Source) || !reviewedRemoveLinkPathValid(m.Target) {
			return fail()
		}
		for _, p := range in.Paths[:i] {
			// Reject both directory and textual-prefix ambiguity. STRM uses startsWith,
			// not a directory-boundary test, so /a and /ab cannot be remapped separately.
			if strings.HasPrefix(m.Source, p.Source) || strings.HasPrefix(p.Source, m.Source) || strings.HasPrefix(m.Target, p.Target) || strings.HasPrefix(p.Target, m.Target) {
				return fail()
			}
		}
	}
	dirs := []string{}
	used := make([]bool, len(in.Paths))
	mapPath := func(s string) (string, error) {
		if !reviewedRemoveLinkPathValid(s) {
			return "", invalidReviewedRemoveLink()
		}
		found := ""
		for i, m := range in.Paths {
			if within(m.Source, s) {
				if found != "" {
					return "", invalidReviewedRemoveLink()
				}
				found = m.Target + strings.TrimPrefix(s, m.Source)
				used[i] = true
			}
		}
		if found == "" || !reviewedRemoveLinkPathValid(found) {
			return "", invalidReviewedRemoveLink()
		}
		dirs = append(dirs, found)
		return found, nil
	}
	for _, field := range []string{"monitor_dirs", "exclude_dirs", "strm_path_mappings"} {
		v, ok := cfg[field]
		if !ok || bytes.Equal(bytes.TrimSpace(v), []byte("null")) {
			continue
		}
		var s string
		if json.Unmarshal(v, &s) != nil {
			return fail()
		}
		lines := strings.Split(s, "\n")
		for i, line := range lines {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			if field != "strm_path_mappings" {
				lines[i], e = mapPath(line)
			} else {
				parts := strings.SplitN(line, ":", 3)
				if len(parts) < 2 {
					return fail()
				}
				left, err := mapPath(strings.TrimSpace(parts[0]))
				if err != nil {
					return fail()
				}
				targetIndex := 1
				if len(parts) == 3 {
					if strings.TrimSpace(parts[1]) != "local" {
						return fail()
					}
					targetIndex = 2
				}
				right, err := mapPath(strings.TrimSpace(parts[targetIndex]))
				if err != nil {
					return fail()
				}
				if len(parts) == 2 {
					lines[i] = left + ":" + right
				} else {
					lines[i] = left + ":local:" + right
				}
			}
			if e != nil {
				return fail()
			}
		}
		cfg[field] = reviewedIYUUJSON(strings.Join(lines, "\n"))
	}
	for i, m := range in.Paths {
		if !used[i] {
			return fail()
		}
		dirs = append(dirs, m.Target)
	}
	mapped := reviewedIYUUJSON(cfg)
	c := removelink.NewConfig()
	if c.Apply(mapped) != nil {
		return fail()
	}
	return in, mapped, dirs, nil
}
func ValidateReviewedRemoveLinkInput(raw []byte) error {
	_, _, _, e := validateReviewedRemoveLink(raw)
	return e
}
func ReviewedRemoveLinkFingerprint(raw []byte) (string, error) {
	in, _, _, e := validateReviewedRemoveLink(raw)
	if e != nil {
		return "", e
	}
	return tokenHash("RemoveLink/2.17/disabled-reviewed-input/v1/" + string(reviewedIYUUJSON(in))), nil
}
func (p *PluginStore) reviewedRemoveLinkTargetTx(ctx context.Context, q pluginRemoveLinkJournalReader, b reviewedRemoveLinkBinding) error {
	settings, sha, e := p.reviewedIYUUSettings(ctx, q)
	if e != nil || sha != b.SettingsSHA {
		return invalidReviewedRemoveLink()
	}
	for _, d := range b.Directories {
		allowed := false
		for _, root := range settings.AllowedRoots {
			if reviewedRemoveLinkPathValid(root) && within(root, d.Path) {
				allowed = true
			}
		}
		for _, root := range settings.Roots {
			if reviewedRemoveLinkPathValid(root.Path) && within(root.Path, d.Path) {
				allowed = true
			}
		}
		if !allowed {
			return invalidReviewedRemoveLink()
		}
		stamp, e := reviewedRemoveLinkDirectoryStamp(d.Path)
		if e != nil || !cleanupEffectSameEntity(stamp, d.Stamp) {
			return invalidReviewedRemoveLink()
		}
	}
	return nil
}
func (p *PluginStore) prepareReviewedRemoveLink(ctx context.Context, in reviewedRemoveLinkSnapshot, mapped json.RawMessage, dirs []string, fp string) (reviewedRemoveLinkBinding, error) {
	tx, e := p.store.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if e != nil {
		return reviewedRemoveLinkBinding{}, e
	}
	defer tx.Rollback()
	b := reviewedRemoveLinkBinding{Version: 1, InstanceID: in.InstanceID, SourceFingerprint: fp, SourceConfigSHA: tokenHash(in.Config), ConfigSHA: tokenHash(string(mapped)), Paths: in.Paths}
	var sealed []byte
	e = tx.QueryRowContext(ctx, `SELECT payload FROM records WHERE kind=? AND id=?`, reviewedRemoveLinkMappingKind, in.InstanceID).Scan(&sealed)
	if e == nil {
		raw, err := p.unseal(reviewedRemoveLinkMappingKind, in.InstanceID, "main", sealed)
		var proof reviewedRemoveLinkProof
		if err != nil || cleanupJournalStrict(raw, &proof) != nil || proof.Evidence != "supplied-snapshot-validated/v1" || proof.Binding.InstanceID != in.InstanceID || proof.Binding.Version != 1 || proof.Binding.SourceFingerprint != fp || proof.Binding.ConfigSHA != b.ConfigSHA || proof.Binding.SourceConfigSHA != b.SourceConfigSHA {
			return b, invalidReviewedRemoveLink()
		}
		if err = p.reviewedRemoveLinkInstallationTx(ctx, tx, in.InstanceID); err != nil {
			return b, err
		}
		var digest, state string
		if tx.QueryRowContext(ctx, `SELECT r.digest,m.state FROM plugin_migration_receipts r JOIN plugin_migration_runs m ON m.id=r.run_id AND m.source_fingerprint=r.source_fingerprint AND m.source_plugin_id=r.source_plugin_id AND m.instance_id=r.instance_id WHERE r.run_id=? AND r.source_fingerprint=? AND r.source_plugin_id='RemoveLink' AND r.instance_id=?`, proof.Migration, fp, in.InstanceID).Scan(&digest, &state) != nil || digest != proof.ImportDigest || state != "imported" {
			return b, invalidReviewedRemoveLink()
		}
		if !bytes.Equal(reviewedIYUUJSON(proof.Binding.Paths), reviewedIYUUJSON(in.Paths)) || !pluginStorageDigest.MatchString(proof.Binding.SettingsSHA) {
			return b, invalidReviewedRemoveLink()
		}
		expected := []string{}
		seen := map[string]bool{}
		for _, dir := range dirs {
			if !seen[dir] {
				expected = append(expected, dir)
				seen[dir] = true
			}
		}
		if len(expected) != len(proof.Binding.Directories) {
			return b, invalidReviewedRemoveLink()
		}
		for i, dir := range expected {
			if proof.Binding.Directories[i].Path != dir {
				return b, invalidReviewedRemoveLink()
			}
		}
		// A repeat import never rebinds references or overwrites user edits.
		b = proof.Binding
	} else if !errors.Is(e, sql.ErrNoRows) {
		return b, e
	} else {
		var existing int
		if err := tx.QueryRowContext(ctx, `SELECT (SELECT COUNT(*) FROM plugin_installations WHERE instance_id=?)+(SELECT COUNT(*) FROM plugin_migration_receipts WHERE instance_id=?)`, in.InstanceID, in.InstanceID).Scan(&existing); err != nil || existing != 0 {
			return b, invalidReviewedRemoveLink()
		}
		_, b.SettingsSHA, e = p.reviewedIYUUSettings(ctx, tx)
		if e != nil {
			return b, e
		}
		seen := map[string]bool{}
		for _, dir := range dirs {
			if seen[dir] {
				continue
			}
			seen[dir] = true
			stamp, err := reviewedRemoveLinkDirectoryStamp(dir)
			if err != nil {
				return b, invalidReviewedRemoveLink()
			}
			b.Directories = append(b.Directories, reviewedRemoveLinkDirectory{dir, stamp})
		}
	}
	if e = p.reviewedRemoveLinkTargetTx(ctx, tx, b); e != nil {
		return b, e
	}
	return b, tx.Commit()
}
func validReviewedRemoveLinkImport(in PluginImport, i PluginInstallation) bool {
	b := in.reviewedRemoveLink
	if b == nil || b.Version != 1 || i.InstanceID != b.InstanceID || i.PluginID != "RemoveLink" || i.Version != "2.17" || i.ManifestSchemaVersion != 1 || in.SourceConfigAbsent || len(in.Data) != 0 || tokenHash(string(in.Config)) != b.ConfigSHA {
		return false
	}
	want, e := pluginJSON(reviewedIYUUJSON(removelink.Manifest()), true)
	got, err := pluginJSON(i.Manifest, true)
	return e == nil && err == nil && bytes.Equal(want, got)
}
func (p *PluginStore) commitReviewedRemoveLinkTx(ctx context.Context, tx *sql.Tx, b reviewedRemoveLinkBinding, run, digest string) error {
	if e := p.reviewedRemoveLinkTargetTx(ctx, tx, b); e != nil {
		return e
	}
	sealed, e := p.seal(reviewedRemoveLinkMappingKind, b.InstanceID, "main", reviewedIYUUJSON(reviewedRemoveLinkProof{"supplied-snapshot-validated/v1", b, run, digest}))
	if e != nil {
		return e
	}
	_, e = tx.ExecContext(ctx, `INSERT INTO records(kind,id,payload,updated_at) VALUES(?,?,?,?)`, reviewedRemoveLinkMappingKind, b.InstanceID, sealed, now())
	return e
}
func (p *PluginStore) ImportReviewedRemoveLink(ctx context.Context, runID string, raw []byte) (PluginMigrationReceipt, error) {
	in, mapped, dirs, e := validateReviewedRemoveLink(raw)
	if e != nil {
		return PluginMigrationReceipt{}, e
	}
	fp, e := ReviewedRemoveLinkFingerprint(raw)
	if e != nil {
		return PluginMigrationReceipt{}, e
	}
	b, e := p.prepareReviewedRemoveLink(ctx, in, mapped, dirs, fp)
	if e != nil {
		return PluginMigrationReceipt{}, e
	}
	return p.Import(ctx, PluginImport{RunID: runID, Installation: PluginInstallation{InstanceID: in.InstanceID, PluginID: "RemoveLink", Version: "2.17", ManifestSchemaVersion: 1, Manifest: reviewedIYUUJSON(removelink.Manifest())}, Config: mapped, Data: in.Data, reviewedRemoveLink: &b})
}
func ValidateReviewedRemoveLinkTarget(ctx context.Context, db *sql.DB, key, raw []byte) error {
	if db == nil || len(key) != 32 {
		return invalidReviewedRemoveLink()
	}
	in, mapped, dirs, e := validateReviewedRemoveLink(raw)
	if e != nil {
		return e
	}
	fp, e := ReviewedRemoveLinkFingerprint(raw)
	if e != nil {
		return e
	}
	p := &PluginStore{store: &Store{DB: db, key: key}}
	_, e = p.prepareReviewedRemoveLink(ctx, in, mapped, dirs, fp)
	return e
}

// Caller retains its verified exclusive offline lock and existing schema. This
// helper does not open/close a store, migrate schema, recover jobs, or start hosts.
func ImportReviewedRemoveLinkOwned(ctx context.Context, db *sql.DB, key, raw []byte) (PluginMigrationReceipt, error) {
	if e := ValidateReviewedRemoveLinkTarget(ctx, db, key, raw); e != nil {
		return PluginMigrationReceipt{}, e
	}
	in, _, _, _ := validateReviewedRemoveLink(raw)
	fp, _ := ReviewedRemoveLinkFingerprint(raw)
	p := &PluginStore{store: &Store{DB: db, key: key}}
	run, e := p.BeginMigration(ctx, fp, "RemoveLink", in.InstanceID)
	if e != nil {
		return PluginMigrationReceipt{}, e
	}
	return p.ImportReviewedRemoveLink(ctx, run.ID, raw)
}

func (p *PluginStore) verifyReviewedRemoveLinkReceiptTx(ctx context.Context, tx *sql.Tx, b reviewedRemoveLinkBinding, migration, digest string) error {
	var sealed []byte
	if tx.QueryRowContext(ctx, `SELECT payload FROM records WHERE kind=? AND id=?`, reviewedRemoveLinkMappingKind, b.InstanceID).Scan(&sealed) != nil {
		return invalidReviewedRemoveLink()
	}
	raw, e := p.unseal(reviewedRemoveLinkMappingKind, b.InstanceID, "main", sealed)
	var proof reviewedRemoveLinkProof
	if e != nil || cleanupJournalStrict(raw, &proof) != nil || proof.Evidence != "supplied-snapshot-validated/v1" || proof.Migration != migration || proof.ImportDigest != digest || !bytes.Equal(reviewedIYUUJSON(proof.Binding), reviewedIYUUJSON(b)) {
		return invalidReviewedRemoveLink()
	}
	return p.reviewedRemoveLinkInstallationTx(ctx, tx, b.InstanceID)
}
func (p *PluginStore) reviewedRemoveLinkInstallationTx(ctx context.Context, q pluginRemoveLinkJournalReader, instance string) error {
	var count int
	if q.QueryRowContext(ctx, `SELECT COUNT(*) FROM plugin_installations WHERE instance_id=? AND plugin_id='RemoveLink' AND version='2.17' AND generation=1 AND installed=1`, instance).Scan(&count) != nil || count != 1 {
		return invalidReviewedRemoveLink()
	}
	return nil
}

// Stamp the no-follow directory descriptor itself, never re-resolve its path
// after closing it. Only identity is compared; child changes are not authority.
func reviewedRemoveLinkDirectoryStamp(path string) (cleanuphost.Stamp, error) {
	f, e := importDir(path, false)
	if e != nil {
		return cleanuphost.Stamp{}, e
	}
	defer f.Close()
	info, e := f.Stat()
	if e != nil {
		return cleanuphost.Stamp{}, e
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.IsDir() {
		return cleanuphost.Stamp{}, invalidReviewedRemoveLink()
	}
	return cleanuphost.Stamp{Dev: uint64(st.Dev), Inode: uint64(st.Ino), Mode: uint32(info.Mode())}, nil
}
