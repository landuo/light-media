package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"
)

const sourceSiteMessageKind = "source_site_message_settings"
const sourceSiteMessageMode = "selected_installed_cold_site_message_not_live_cache"

type sourceSiteMessageAudit struct {
	Writes    *int `json:"denied_writes"`
	Network   *int `json:"denied_network"`
	Processes *int `json:"denied_processes"`
}
type sourceSiteMessageSettings struct {
	Version         int                    `json:"version"`
	Mode            string                 `json:"mode"`
	Known           *bool                  `json:"known"`
	Value           *bool                  `json:"site_message"`
	Normalized      []string               `json:"normalization_fields"`
	SourceHashes    map[string]string      `json:"source_hashes"`
	EnvPath         string                 `json:"env_path"`
	EnvSHA          string                 `json:"env_sha256"`
	CapturesEqual   *bool                  `json:"captures_equal"`
	ConfigUnchanged *bool                  `json:"original_config_unchanged"`
	AppImports      *int                   `json:"original_app_imports"`
	Constructors    *int                   `json:"original_settings_constructors"`
	Audit           sourceSiteMessageAudit `json:"audit"`
}

type SourceSiteMessageImportResult struct {
	State      string `json:"state"`
	SourceMode string `json:"source_mode"`
	Records    int    `json:"records"`
	Writes     int    `json:"writes"`
	Jobs       int    `json:"jobs_queued"`
	Calls      int    `json:"external_calls"`
}

func invalidSourceSiteMessage() error { return errors.New("invalid_reviewed_site_message_settings") }
func validateSourceSiteMessage(raw []byte) (sourceSiteMessageSettings, []byte, error) {
	var in sourceSiteMessageSettings
	yes := func(p *bool) bool { return p != nil && *p }
	zero := func(p *int) bool { return p != nil && *p == 0 }
	if len(raw) == 0 || len(raw) > 64<<10 || sourceSettingsJSON(raw, &in) != nil || in.Version != 1 || in.Mode != sourceSiteMessageMode || !yes(in.Known) || in.Value == nil || !yes(in.CapturesEqual) || !yes(in.ConfigUnchanged) || !zero(in.AppImports) || !zero(in.Constructors) || !zero(in.Audit.Writes) || !zero(in.Audit.Network) || !zero(in.Audit.Processes) || in.EnvPath != "/config/app.env" || !pluginStorageDigest.MatchString(in.EnvSHA) || in.Normalized == nil {
		return in, nil, invalidSourceSiteMessage()
	}
	want := map[string]string{"/app/app/core/config.py": reviewedSourceSettingsHashes["/app/app/core/config.py"], "/app/app/utils/system.py": reviewedSourceSettingsHashes["/app/app/utils/system.py"]}
	if !reflect.DeepEqual(in.SourceHashes, want) {
		return in, nil, invalidSourceSiteMessage()
	}
	fields, e := sourceSettingsObject(raw, "version", "mode", "known", "site_message", "normalization_fields", "source_hashes", "env_path", "env_sha256", "captures_equal", "original_config_unchanged", "original_app_imports", "original_settings_constructors", "audit")
	if e != nil {
		return in, nil, invalidSourceSiteMessage()
	}
	if _, e = sourceSettingsObject(fields["audit"], "denied_writes", "denied_network", "denied_processes"); e != nil {
		return in, nil, invalidSourceSiteMessage()
	}
	seen := map[string]bool{}
	for _, n := range in.Normalized {
		if seen[n] || n != "CONFIG_DIR" && n != "SITE_MESSAGE" {
			return in, nil, invalidSourceSiteMessage()
		}
		seen[n] = true
	}
	b, e := json.Marshal(in)
	if e != nil {
		return in, nil, invalidSourceSiteMessage()
	}
	return in, b, nil
}

func ValidateReviewedSiteMessageSettingsInput(raw []byte) (SourceSiteMessageImportResult, error) {
	_, _, e := validateSourceSiteMessage(raw)
	if e != nil {
		return SourceSiteMessageImportResult{}, e
	}
	return SourceSiteMessageImportResult{State: "validated", SourceMode: sourceSiteMessageMode, Records: 1}, nil
}

// This is an offline addition to the existing source settings receipt. It never
// overwrites native preferences, the three prior source domains, or their receipt.
func ImportReviewedSiteMessageSettings(ctx context.Context, dir string, raw []byte, apply bool) (SourceSiteMessageImportResult, error) {
	in, canonical, e := validateSourceSiteMessage(raw)
	if e != nil {
		return SourceSiteMessageImportResult{}, e
	}
	target, e := openSourceSettingsTarget(ctx, dir, apply)
	if e != nil {
		return SourceSiteMessageImportResult{}, e
	}
	defer target.close()
	p, tx := target.p, target.tx
	if e = sourceSiteMessageParent(ctx, p, tx, in); e != nil {
		return SourceSiteMessageImportResult{}, e
	}
	result := SourceSiteMessageImportResult{State: "ready", SourceMode: sourceSiteMessageMode, Records: 1}
	var old []byte
	e = tx.QueryRowContext(ctx, `SELECT payload FROM records WHERE kind=? AND id='main'`, sourceSiteMessageKind).Scan(&old)
	if e == nil {
		value, e := p.unseal(sourceSiteMessageKind, "system", "main", old)
		if e != nil {
			return result, errors.New("source_site_message_existing_unconfirmed")
		}
		_, existing, e := validateSourceSiteMessage(value)
		if e != nil || !bytes.Equal(existing, canonical) {
			return result, errors.New("source_site_message_existing_conflict")
		}
		result.State = "already_imported"
		return result, nil
	}
	if !errors.Is(e, sql.ErrNoRows) {
		return result, errors.New("source_site_message_target_read_failed")
	}
	if !apply {
		return result, nil
	}
	sealed, e := p.seal(sourceSiteMessageKind, "system", "main", canonical)
	if e != nil {
		return result, errors.New("source_site_message_encryption_failed")
	}
	if _, e = tx.ExecContext(ctx, `INSERT INTO records(kind,id,payload,updated_at) VALUES(?,'main',?,?)`, sourceSiteMessageKind, sealed, now()); e != nil {
		return result, errors.New("source_site_message_transaction_failed")
	}
	if tx.Commit() != nil {
		return result, errors.New("source_site_message_commit_unconfirmed")
	}
	result.State, result.Writes = "imported", 1
	return result, nil
}

// Link to the exact env snapshot already used by the prior selected-settings
// import. A partial/different import cannot silently supply this global flag.
func sourceSiteMessageParent(ctx context.Context, p *PluginStore, tx *sql.Tx, in sourceSiteMessageSettings) error {
	var sealed []byte
	if e := tx.QueryRowContext(ctx, `SELECT payload FROM records WHERE kind='source_settings_migration' AND id='main'`).Scan(&sealed); e != nil {
		return errors.New("source_site_message_parent_settings_required")
	}
	b, e := p.unseal("source_settings_migration", "system", "main", sealed)
	if e != nil {
		return errors.New("source_site_message_parent_settings_unconfirmed")
	}
	var parent sourceSettingsReceipt
	if sourceSettingsJSON(b, &parent) != nil || parent.Version != 1 || parent.Records != 3 || parent.Source.Mode != "selected_installed_cold_settings" || parent.Source.EnvPath != in.EnvPath || parent.Source.EnvFileSHA256 != in.EnvSHA || parent.ImportedAt == "" || !pluginStorageDigest.MatchString(parent.Digest) {
		return errors.New("source_site_message_parent_settings_mismatch")
	}
	for path, sha := range in.SourceHashes {
		if parent.Source.SourceHashes[path] != sha {
			return errors.New("source_site_message_parent_settings_mismatch")
		}
	}
	// Verify the complete parent transaction, not merely a surviving receipt.
	settings := sourceSettingsInput{Version: 1, Source: parent.Source}
	for name, value := range map[string]any{"source_statistics_settings": &settings.Statistics, "source_network_settings": &settings.Network, "source_runtime_settings": &settings.Runtime} {
		var body []byte
		if tx.QueryRowContext(ctx, `SELECT payload FROM records WHERE kind=? AND id='main'`, name).Scan(&body) != nil {
			return errors.New("source_site_message_parent_settings_required")
		}
		plain, e := p.unseal(name, "system", "main", body)
		if e != nil || sourceSettingsJSON(plain, value) != nil {
			return errors.New("source_site_message_parent_settings_unconfirmed")
		}
	}
	encoded, e := json.Marshal(settings)
	if e != nil {
		return errors.New("source_site_message_parent_settings_unconfirmed")
	}
	_, canonical, e := validateSourceSettings(encoded)
	if e != nil || sourceSettingsDigest(canonical) != parent.Digest {
		return errors.New("source_site_message_parent_settings_mismatch")
	}

	return nil
}

type siteMessagePolicy struct {
	enabled     bool
	sourceProof string
}

func (a *App) sourceSiteMessagePolicy(ctx context.Context, c Settings) (siteMessagePolicy, error) {
	policy := siteMessagePolicy{enabled: siteMessagesEnabled(c)}
	tx, e := a.Store.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if e != nil {
		return policy, e
	}
	defer tx.Rollback()
	var sealed []byte
	e = tx.QueryRowContext(ctx, `SELECT payload FROM records WHERE kind=? AND id='main'`, sourceSiteMessageKind).Scan(&sealed)
	if errors.Is(e, sql.ErrNoRows) {
		return policy, nil
	}
	if e != nil {
		return policy, errors.New("source_site_message_record_read_failed")
	}
	b, e := a.pluginStore.unseal(sourceSiteMessageKind, "system", "main", sealed)
	if e != nil {
		return policy, errors.New("source_site_message_record_unconfirmed")
	}
	in, canonical, e := validateSourceSiteMessage(b)
	if e != nil {
		return policy, errors.New("source_site_message_record_unconfirmed")
	}
	if e = sourceSiteMessageParent(ctx, a.pluginStore, tx, in); e != nil {
		return policy, e
	}
	policy.sourceProof = sourceSettingsDigest(canonical)
	if c.ReadSiteMessages == nil {
		policy.enabled = *in.Value
	}
	return policy, nil
}
