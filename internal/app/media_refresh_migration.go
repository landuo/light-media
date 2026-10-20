package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"light-media/internal/fnos"
	"light-media/internal/plugins/builtin"
)

const reviewedMediaRefreshConfigSHA = "a9611ac9dcd35f975281a8adf02ca924a6f06410c6de53e5ba2644f48a7f1030"
const reviewedMediaRefreshMappedSHA = "25f28d91ef8c590fc30f1d232e6ee4f99a006d0bf5977e4e30b3df6e82eaaaa1"
const reviewedMediaRefreshNameSHA = "f879612a3abfb31c7c40e68b30c1ab87b3858fd7ed075059dc4129e911fa795b"
const reviewedMediaRefreshHostSHA = "eed6f3d4caca53506297f1a9d89aa2cd8b4744bc41495c0032bb8eac7222299a"
const reviewedMediaRefreshUsernameSHA = "8c6976e5b5410415bde908bd4dee15dfb167a9c873fc4bb8a81f6f2ab448a918"

type reviewedMediaRefreshReference struct {
	Name, Kind, Host, Username string
	Enabled                    bool
}

type reviewedMediaRefreshSnapshot struct {
	Version         int                            `json:"version"`
	SourceContainer string                         `json:"source_container"`
	SourceSHA       string                         `json:"source_sha256"`
	Config          string                         `json:"config"`
	InstalledCount  int                            `json:"installed_count"`
	KVCount         int                            `json:"kv_count"`
	FileCount       int                            `json:"file_count"`
	SymlinkCount    int                            `json:"symlink_count"`
	Reference       *reviewedMediaRefreshReference `json:"reference"`
}

// The receipt binds the reviewed source reference to the already configured
// native connection. It contains no host, account name or credential value.
type reviewedMediaRefreshBinding struct {
	Version                                                      int
	SourceConfigSHA, SourceNameSHA, SourceHostSHA, SourceUserSHA string
	Target                                                       string
}

func reviewedMediaRefreshBindingValue() reviewedMediaRefreshBinding {
	return reviewedMediaRefreshBinding{1, reviewedMediaRefreshConfigSHA, reviewedMediaRefreshNameSHA, reviewedMediaRefreshHostSHA, reviewedMediaRefreshUsernameSHA, "fnos"}
}

func invalidReviewedMediaRefresh() error {
	return errors.New("invalid_reviewed_media_refresh_snapshot")
}

func validateReviewedMediaRefresh(raw []byte) (reviewedMediaRefreshSnapshot, json.RawMessage, error) {
	var in reviewedMediaRefreshSnapshot
	if len(raw) > 1<<20 || sourceSettingsJSON(raw, &in) != nil {
		return in, nil, invalidReviewedMediaRefresh()
	}
	fields, e := sourceSettingsObject(raw, "version", "source_container", "source_sha256", "config", "installed_count", "kv_count", "file_count", "symlink_count", "reference")
	if e != nil {
		return in, nil, invalidReviewedMediaRefresh()
	}
	for _, value := range fields {
		if bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return in, nil, invalidReviewedMediaRefresh()
		}
	}
	if in.Version != 1 || in.SourceContainer != "cb82e3f550881334ef3814426016d08d909bccd88d3297522756ba31654d129a" || in.SourceSHA != "ea92f39a852aa0333cb625885e0fbf7b2a679f4f7449463bb6126afe6694a3fc" || in.InstalledCount != 1 || in.KVCount != 0 || in.FileCount != 0 || in.SymlinkCount != 0 || sourceSettingsDigest([]byte(in.Config)) != reviewedMediaRefreshConfigSHA || in.Reference == nil {
		return in, nil, invalidReviewedMediaRefresh()
	}
	r := in.Reference
	host, e := fnos.NormalizeHost(r.Host)
	if e != nil || !r.Enabled || r.Kind != "trimemedia" || sourceSettingsDigest([]byte(r.Name)) != reviewedMediaRefreshNameSHA || sourceSettingsDigest([]byte(host)) != reviewedMediaRefreshHostSHA || sourceSettingsDigest([]byte(r.Username)) != reviewedMediaRefreshUsernameSHA {
		return in, nil, invalidReviewedMediaRefresh()
	}
	var c builtin.MediaServerRefreshConfiguration
	if sourceSettingsJSON([]byte(in.Config), &c) != nil || !c.Enabled || len(c.MediaServers) != 1 || c.MediaServers[0] != r.Name {
		return in, nil, invalidReviewedMediaRefresh()
	}
	if _, e = c.Duration(); e != nil {
		return in, nil, invalidReviewedMediaRefresh()
	}
	c.MediaServers = []string{"fnos"}
	mapped, e := json.Marshal(map[string]any{"enabled": c.Enabled, "delay": c.Delay, "mediaservers": c.MediaServers})
	if e != nil {
		return in, nil, invalidReviewedMediaRefresh()
	}
	canonical, e := pluginJSON(json.RawMessage(mapped), true)
	if e != nil || sourceSettingsDigest(canonical) != reviewedMediaRefreshMappedSHA {
		return in, nil, invalidReviewedMediaRefresh()
	}
	return in, canonical, nil
}

// Validation alone opens no target database, worker or network connection.
func ValidateReviewedMediaRefreshInput(raw []byte) error {
	_, _, e := validateReviewedMediaRefresh(raw)
	return e
}

func validReviewedMediaRefreshImport(in PluginImport, installation PluginInstallation) bool {
	if in.reviewedMediaRefresh == nil || *in.reviewedMediaRefresh != reviewedMediaRefreshBindingValue() || installation.InstanceID != "MediaServerRefresh" || installation.PluginID != "MediaServerRefresh" || installation.Version != "1.3.3" || installation.ManifestSchemaVersion != 1 || in.SourceConfigAbsent || len(in.Data) != 0 {
		return false
	}
	config, e := pluginJSON(in.Config, true)
	if e != nil || sourceSettingsDigest(config) != reviewedMediaRefreshMappedSHA {
		return false
	}
	want, _ := json.Marshal(builtin.MediaServerRefreshManifest())
	got, e := pluginJSON(installation.Manifest, true)
	expected, we := pluginJSON(json.RawMessage(want), true)
	return e == nil && we == nil && bytes.Equal(got, expected)
}

func (p *PluginStore) reviewedMediaRefreshTargetTx(ctx context.Context, tx *sql.Tx, binding reviewedMediaRefreshBinding) error {
	var sealed []byte
	if tx.QueryRowContext(ctx, `SELECT payload FROM records WHERE kind='config' AND id='main'`).Scan(&sealed) != nil {
		return errors.New("reviewed_media_refresh_connection_missing")
	}
	if e := ValidateReviewedMediaRefreshConnection(sealed, p.store.key); e != nil {
		return e
	}
	value, e := p.seal("plugin_media_reference_mapping", "MediaServerRefresh", "main", mustCleanSeedJSON(binding))
	if e != nil {
		return e
	}
	_, e = tx.ExecContext(ctx, `INSERT INTO records(kind,id,payload,updated_at) VALUES('plugin_media_reference_mapping','MediaServerRefresh',?,?)`, value, now())
	return e
}

// This offline preflight decrypts only in memory and returns no configuration.
// It neither opens a Store nor changes the target during a CLI dry run.
func ValidateReviewedMediaRefreshConnection(sealed, key []byte) error {
	if len(key) != 32 {
		return errors.New("reviewed_media_refresh_connection_missing")
	}
	var settings Settings
	if (&Store{key: key}).Unseal(sealed, &settings) != nil {
		return errors.New("reviewed_media_refresh_connection_missing")
	}
	host, e := fnos.NormalizeHost(settings.FNOS.Host)
	if e != nil || !settings.FNOS.Enabled || settings.FNOS.Password == "" || sourceSettingsDigest([]byte(host)) != reviewedMediaRefreshHostSHA || sourceSettingsDigest([]byte(settings.FNOS.Username)) != reviewedMediaRefreshUsernameSHA {
		return errors.New("reviewed_media_refresh_connection_changed")
	}
	return nil
}

// The only change to source configuration is its explicitly verified reference.
// Import itself starts no worker and registers no events or refresh requests.
func (p *PluginStore) ImportReviewedMediaRefresh(ctx context.Context, runID string, raw []byte) (PluginMigrationReceipt, error) {
	_, mapped, e := validateReviewedMediaRefresh(raw)
	if e != nil {
		return PluginMigrationReceipt{}, e
	}
	manifest, _ := json.Marshal(builtin.MediaServerRefreshManifest())
	binding := reviewedMediaRefreshBindingValue()
	return p.Import(ctx, PluginImport{RunID: runID, Installation: PluginInstallation{InstanceID: "MediaServerRefresh", PluginID: "MediaServerRefresh", Version: "1.3.3", ManifestSchemaVersion: 1, Manifest: manifest}, Config: mapped, reviewedMediaRefresh: &binding})
}
