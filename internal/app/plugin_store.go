package app

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"database/sql"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
)

type PluginInstallation struct {
	InstanceID            string          `json:"instance_id"`
	PluginID              string          `json:"plugin_id"`
	Version               string          `json:"version"`
	ManifestSchemaVersion int             `json:"manifest_schema_version"`
	Manifest              json.RawMessage `json:"-"`
	DesiredEnabled        bool            `json:"desired_enabled"`
	Installed             bool            `json:"installed"`
	Generation            int64           `json:"generation"`
	CreatedAt             string          `json:"created_at"`
	UpdatedAt             string          `json:"updated_at"`
}

type PluginStoredValue struct {
	Revision  int64           `json:"revision"`
	Value     json.RawMessage `json:"-"`
	UpdatedAt string          `json:"updated_at"`
}

type PluginStore struct {
	store   *Store
	notices pluginNoticeRuntime
	folders originalPluginFolderCache
}

const pluginImportLimit = 64 << 20

var pluginStorageVersion = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.+\-]{0,63}$`)

// NewPluginStore initializes encrypted namespaces and ordinary core storage.
// Existing legacy tables remain readable historical data; no old executor,
// recovery engine, lifecycle journal or native owner is constructed here.
func NewPluginStore(ctx context.Context, store *Store) (*PluginStore, error) {
	if store == nil || store.DB == nil || len(store.key) != 32 {
		return nil, errors.New("invalid plugin store")
	}
	tx, err := store.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, legacyPluginArchiveSchema); err != nil {
		return nil, err
	}
	var count, version int
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*),COALESCE(MAX(version),0) FROM plugin_storage_schema`).Scan(&count, &version); err != nil {
		return nil, err
	}
	if count != 1 || version != 1 {
		return nil, errors.New("unsupported plugin storage schema")
	}
	if err = migrateOriginalBaseNamespaces(ctx, tx); err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(ctx, pluginOrganizationSchema+";"+pluginInstanceSchema+";"+domainEventSchema+";"+pluginNoticeSchema); err != nil {
		return nil, err
	}
	p := &PluginStore{store: store}
	if err = p.initNotificationTemplatesTx(ctx, tx); err != nil {
		return nil, err
	}
	if err = p.initNotificationChannelsTx(ctx, tx); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return p, nil
}

func pluginJSON(value any, object bool) (json.RawMessage, error) {
	return pluginJSONLimit(value, object, pluginValueLimit)
}

func validPluginKey(key string) bool {
	return key != "" && len(key) <= 256 && !strings.ContainsAny(key, "\x00\r\n")
}

func (p *PluginStore) unsealBound(kind, instanceID, key string, sealed []byte, bound int) (json.RawMessage, error) {
	block, err := aes.NewCipher(p.store.key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	limit := pluginValueLimit
	if kind == "native_directory_selection" || kind == "download_season_scope" || kind == "subscription_acquisition" || kind == "subscription_download_failure" || kind == "source_subscription_origin" || kind == "subscription_add" || kind == "subscription_stats" || kind == "subscription_completion_ordinary" || kind == "subscription_completion" || kind == "subscription_history" || kind == "source_chain_event" || kind == "source_subscribe_complete" || kind == "subscription_quality_download" || kind == "subscription_quality_import" || kind == "subscription_quality_swap" || kind == "subscription_quality_cleanup" || kind == "data" || kind == "core_words" || kind == "download_context" || kind == "download_added" || kind == "subscription_words" || kind == "run_result" || kind == "cleanup_journal" || kind == "scrape_journal" {
		limit = pluginDataValueLimit
	}
	if kind == "clean_seed_journal" || kind == "notice_journal" {
		limit = pluginCleanInvalidSeedJournalLimit
	}
	if bound >= 0 {
		limit = bound
	}
	if len(sealed) < aead.NonceSize()+aead.Overhead() || limit > 0 && len(sealed) > limit+aead.NonceSize()+aead.Overhead() {
		return nil, bad("PLUGIN_DATA_CORRUPT", "插件数据无法通过完整性校验")
	}
	aad, _ := json.Marshal([]string{"light-media/plugins/v1", kind, instanceID, key})
	raw, err := aead.Open(nil, sealed[:aead.NonceSize()], sealed[aead.NonceSize():], aad)
	if err != nil || !json.Valid(raw) {
		return nil, bad("PLUGIN_DATA_CORRUPT", "插件数据无法通过完整性校验")
	}
	return json.RawMessage(raw), nil
}

func validatePluginInstallation(in PluginInstallation) (PluginInstallation, error) {
	if !pluginStorageID.MatchString(in.InstanceID) || !pluginStorageID.MatchString(in.PluginID) || !pluginStorageVersion.MatchString(in.Version) || in.ManifestSchemaVersion < 1 || in.ManifestSchemaVersion > 65535 {
		return in, bad("INVALID_PLUGIN_INSTALLATION", "插件安装元数据无效")
	}
	raw, err := pluginJSON(in.Manifest, true)
	if err != nil {
		return in, err
	}
	in.Manifest = raw
	return in, nil
}

func (p *PluginStore) installTx(ctx context.Context, tx *sql.Tx, in PluginInstallation) error {
	var previousID string
	var previouslyInstalled bool
	err := tx.QueryRowContext(ctx, `SELECT plugin_id,installed FROM plugin_installations WHERE instance_id=?`, in.InstanceID).Scan(&previousID, &previouslyInstalled)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if err == nil && previousID != in.PluginID {
		return bad("PLUGIN_INSTANCE_CONFLICT", "插件实例已属于其他插件")
	}
	sealed, err := p.seal("manifest", in.InstanceID, "main", in.Manifest)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO plugin_installations(instance_id,plugin_id,version,manifest_schema,manifest,desired_enabled,installed,generation,created_at,updated_at) VALUES(?,?,?,?,?,?,1,1,?,?)
ON CONFLICT(instance_id) DO UPDATE SET version=excluded.version,manifest_schema=excluded.manifest_schema,manifest=excluded.manifest,desired_enabled=excluded.desired_enabled,installed=1,generation=plugin_installations.generation+1,updated_at=excluded.updated_at`, in.InstanceID, in.PluginID, in.Version, in.ManifestSchemaVersion, sealed, in.DesiredEnabled, now(), now())
	if err != nil {
		return err
	}
	return p.updateOriginalInstalledOrderTx(ctx, tx, in.InstanceID, true, previouslyInstalled)
}

func (p *PluginStore) Install(ctx context.Context, in PluginInstallation) error {
	in, err := validatePluginInstallation(in)
	if err != nil {
		return err
	}
	tx, err := p.store.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = p.installTx(ctx, tx, in); err != nil {
		return err
	}
	return tx.Commit()
}

func (p *PluginStore) Installation(ctx context.Context, instanceID string) (PluginInstallation, error) {
	var in PluginInstallation
	var sealed []byte
	err := p.store.DB.QueryRowContext(ctx, `SELECT instance_id,plugin_id,version,manifest_schema,manifest,desired_enabled,installed,generation,created_at,updated_at FROM plugin_installations WHERE instance_id=?`, instanceID).
		Scan(&in.InstanceID, &in.PluginID, &in.Version, &in.ManifestSchemaVersion, &sealed, &in.DesiredEnabled, &in.Installed, &in.Generation, &in.CreatedAt, &in.UpdatedAt)
	if err != nil {
		return in, err
	}
	in.Manifest, err = p.unseal("manifest", instanceID, "main", sealed)
	return in, err
}

func (p *PluginStore) readValue(ctx context.Context, instanceID, kind, key string) (PluginStoredValue, error) {
	var value PluginStoredValue
	var sealed []byte
	err := p.store.DB.QueryRowContext(ctx, `SELECT revision,sealed,updated_at FROM plugin_values WHERE instance_id=? AND kind=? AND item_key=?`, instanceID, kind, key).Scan(&value.Revision, &sealed, &value.UpdatedAt)
	if err != nil {
		return value, err
	}
	value.Value, err = p.unseal(kind, instanceID, key, sealed)
	return value, err
}

func (p *PluginStore) Data(ctx context.Context, instanceID, key string) (PluginStoredValue, error) {
	return p.readValue(ctx, instanceID, "data", key)
}
func (p *PluginStore) Config(ctx context.Context, instance string) (PluginStoredValue, error) {
	return p.readValue(ctx, instance, "config", "main")
}
func (p *PluginStore) unseal(kind, instance, key string, sealed []byte) (json.RawMessage, error) {
	return p.unsealBound(kind, instance, key, sealed, -1)
}
