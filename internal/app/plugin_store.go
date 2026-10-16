package app

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
)

// PluginInstallation is prepared package metadata, not evidence of a running
// implementation. Protocol/package validation belongs to the plugin host.
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

// Values are available to trusted host code only; serializing a storage result
// must never accidentally disclose config, state, or encrypted bytes to an API.
type PluginStoredValue struct {
	Revision  int64           `json:"revision"`
	Value     json.RawMessage `json:"-"`
	UpdatedAt string          `json:"updated_at"`
}

type PluginMigrationRun struct {
	ID                string          `json:"id"`
	SourceFingerprint string          `json:"source_fingerprint"`
	SourcePluginID    string          `json:"source_plugin_id"`
	InstanceID        string          `json:"instance_id"`
	State             string          `json:"state"`
	ErrorCode         string          `json:"error_code,omitempty"`
	Details           json.RawMessage `json:"-"`
	CreatedAt         string          `json:"created_at"`
	UpdatedAt         string          `json:"updated_at"`
}

type PluginMigrationReceipt struct {
	RunID             string `json:"run_id"`
	SourceFingerprint string `json:"source_fingerprint"`
	SourcePluginID    string `json:"source_plugin_id"`
	InstanceID        string `json:"instance_id"`
	State             string `json:"state"`
	ImportedAt        string `json:"imported_at"`
	Digest            string `json:"-"`
}

type PluginImport struct {
	RunID        string
	Installation PluginInstallation
	Config       json.RawMessage
	Data         map[string]json.RawMessage
}

// PluginStore owns only its namespaced tables. It neither starts plugins nor
// registers schedules, events, HTTP routes, or external operations.
type PluginStore struct{ store *Store }

const pluginValueLimit = 1 << 20
const pluginDataValueLimit = 16 << 20
const pluginImportLimit = 64 << 20

var pluginStorageID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,95}$`)
var pluginStorageVersion = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.+\-]{0,63}$`)
var pluginStorageDigest = regexp.MustCompile(`^[a-f0-9]{64}$`)

// NewPluginStore applies the isolated, additive storage schema in one
// transaction. Core schema v1 remains unchanged; an unsupported future plugin
// schema is rejected instead of being silently treated as compatible.
func NewPluginStore(ctx context.Context, store *Store) (*PluginStore, error) {
	if store == nil || store.DB == nil || len(store.key) != 32 {
		return nil, errors.New("invalid plugin store")
	}
	tx, err := store.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS plugin_storage_schema(version INTEGER PRIMARY KEY CHECK(version>0))`); err != nil {
		return nil, err
	}
	var count, version int
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*),COALESCE(MAX(version),0) FROM plugin_storage_schema`).Scan(&count, &version); err != nil {
		return nil, err
	}
	if count > 1 || count == 1 && version != 1 {
		return nil, errors.New("unsupported plugin storage schema")
	}
	_, err = tx.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS plugin_installations(
 instance_id TEXT PRIMARY KEY,plugin_id TEXT NOT NULL,version TEXT NOT NULL,manifest_schema INTEGER NOT NULL,
 manifest BLOB NOT NULL,desired_enabled INTEGER NOT NULL DEFAULT 0,installed INTEGER NOT NULL DEFAULT 1,generation INTEGER NOT NULL DEFAULT 1,
 created_at TEXT NOT NULL,updated_at TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS plugin_values(
 instance_id TEXT NOT NULL,kind TEXT NOT NULL CHECK(kind IN ('config','data')),item_key TEXT NOT NULL,
 revision INTEGER NOT NULL CHECK(revision>0),sealed BLOB NOT NULL,updated_at TEXT NOT NULL,
 PRIMARY KEY(instance_id,kind,item_key),FOREIGN KEY(instance_id) REFERENCES plugin_installations(instance_id));
CREATE TABLE IF NOT EXISTS plugin_migration_runs(
 id TEXT PRIMARY KEY,source_fingerprint TEXT NOT NULL,source_plugin_id TEXT NOT NULL,instance_id TEXT NOT NULL,
 state TEXT NOT NULL CHECK(state IN ('planned','running','imported','failed')),error_code TEXT NOT NULL DEFAULT '',
 details BLOB NOT NULL,created_at TEXT NOT NULL,updated_at TEXT NOT NULL,
 UNIQUE(source_fingerprint,source_plugin_id,instance_id));
CREATE TABLE IF NOT EXISTS plugin_migration_receipts(
 source_fingerprint TEXT NOT NULL,source_plugin_id TEXT NOT NULL,instance_id TEXT NOT NULL,
 run_id TEXT NOT NULL UNIQUE,digest TEXT NOT NULL,imported_at TEXT NOT NULL,
 PRIMARY KEY(source_fingerprint,source_plugin_id,instance_id),
 FOREIGN KEY(run_id) REFERENCES plugin_migration_runs(id));
CREATE TABLE IF NOT EXISTS plugin_runs(
 id TEXT PRIMARY KEY,instance_id TEXT NOT NULL,version TEXT NOT NULL,generation INTEGER NOT NULL,
 config_revision INTEGER NOT NULL,action TEXT NOT NULL,state TEXT NOT NULL,
 input BLOB NOT NULL,result BLOB,error_code TEXT NOT NULL DEFAULT '',created_at TEXT NOT NULL,updated_at TEXT NOT NULL,
 FOREIGN KEY(instance_id) REFERENCES plugin_installations(instance_id));
CREATE INDEX IF NOT EXISTS plugin_runs_pending ON plugin_runs(state,created_at,id);
CREATE TABLE IF NOT EXISTS plugin_event_subscriptions(
 instance_id TEXT NOT NULL,handler_key TEXT NOT NULL,generation INTEGER NOT NULL,config_revision INTEGER NOT NULL,
 event_types TEXT NOT NULL,delay_ms INTEGER NOT NULL,active INTEGER NOT NULL,cursor INTEGER NOT NULL,
 PRIMARY KEY(instance_id,handler_key),FOREIGN KEY(instance_id) REFERENCES plugin_installations(instance_id));
CREATE TABLE IF NOT EXISTS plugin_event_batches(
 id TEXT PRIMARY KEY,instance_id TEXT NOT NULL,handler_key TEXT NOT NULL,generation INTEGER NOT NULL,
 config_revision INTEGER NOT NULL,due_at INTEGER NOT NULL,run_id TEXT UNIQUE,created_at TEXT NOT NULL,state TEXT NOT NULL DEFAULT 'collecting',item_count INTEGER NOT NULL DEFAULT 0,
 FOREIGN KEY(instance_id) REFERENCES plugin_installations(instance_id));
CREATE INDEX IF NOT EXISTS plugin_event_batches_due ON plugin_event_batches(run_id,due_at);
CREATE INDEX IF NOT EXISTS plugin_event_batches_owner ON plugin_event_batches(instance_id,handler_key,generation,config_revision,state,run_id);
CREATE TABLE IF NOT EXISTS plugin_event_deliveries(
 instance_id TEXT NOT NULL,handler_key TEXT NOT NULL,event_id TEXT NOT NULL,event_seq INTEGER NOT NULL,batch_id TEXT NOT NULL,
 PRIMARY KEY(instance_id,handler_key,event_id),FOREIGN KEY(batch_id) REFERENCES plugin_event_batches(id));
CREATE INDEX IF NOT EXISTS plugin_event_deliveries_batch ON plugin_event_deliveries(batch_id,event_seq);
INSERT OR IGNORE INTO plugin_storage_schema VALUES(1);`)
	if err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(ctx, domainEventSchema); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return &PluginStore{store: store}, nil
}

func pluginJSON(value any, object bool) (json.RawMessage, error) {
	return pluginJSONLimit(value, object, pluginValueLimit)
}

func pluginJSONLimit(value any, object bool, limit int) (json.RawMessage, error) {
	raw, err := json.Marshal(value)
	if err != nil || len(raw) > limit {
		return nil, bad("INVALID_PLUGIN_VALUE", "插件数据不是有效JSON或超过大小上限")
	}
	if object {
		var fields map[string]json.RawMessage
		if json.Unmarshal(raw, &fields) != nil || fields == nil {
			return nil, bad("INVALID_PLUGIN_CONFIG", "插件配置或清单必须是JSON对象")
		}
	}
	return raw, nil
}

func validPluginKey(key string) bool {
	return key != "" && len(key) <= 256 && !strings.ContainsAny(key, "\x00\r\n")
}

func (p *PluginStore) seal(kind, instanceID, key string, raw []byte) ([]byte, error) {
	block, err := aes.NewCipher(p.store.key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err = io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	aad, _ := json.Marshal([]string{"light-media/plugins/v1", kind, instanceID, key})
	return aead.Seal(nonce, nonce, raw, aad), nil
}

func (p *PluginStore) unseal(kind, instanceID, key string, sealed []byte) (json.RawMessage, error) {
	block, err := aes.NewCipher(p.store.key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	limit := pluginValueLimit
	if kind == "data" {
		limit = pluginDataValueLimit
	}
	if len(sealed) < aead.NonceSize()+aead.Overhead() || len(sealed) > limit+aead.NonceSize()+aead.Overhead() {
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
	err := tx.QueryRowContext(ctx, `SELECT plugin_id FROM plugin_installations WHERE instance_id=?`, in.InstanceID).Scan(&previousID)
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
ON CONFLICT(instance_id) DO UPDATE SET version=excluded.version,manifest_schema=excluded.manifest_schema,
 manifest=excluded.manifest,desired_enabled=excluded.desired_enabled,installed=1,generation=plugin_installations.generation+1,updated_at=excluded.updated_at`,
		in.InstanceID, in.PluginID, in.Version, in.ManifestSchemaVersion, sealed, in.DesiredEnabled, now(), now())
	return err
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

func (p *PluginStore) Config(ctx context.Context, instanceID string) (PluginStoredValue, error) {
	return p.readValue(ctx, instanceID, "config", "main")
}

func (p *PluginStore) Data(ctx context.Context, instanceID, key string) (PluginStoredValue, error) {
	return p.readValue(ctx, instanceID, "data", key)
}

// expectedRevision=0 creates a missing value; otherwise a stale writer is
// rejected. Namespace keys are data keys, never filesystem paths.
func (p *PluginStore) saveValueTx(ctx context.Context, tx *sql.Tx, instanceID, kind, key string, expectedRevision int64, raw json.RawMessage) (PluginStoredValue, error) {
	if !pluginStorageID.MatchString(instanceID) || !validPluginKey(key) || expectedRevision < 0 {
		return PluginStoredValue{}, bad("INVALID_PLUGIN_VALUE", "插件数据命名空间或修订无效")
	}
	var installed bool
	if err := tx.QueryRowContext(ctx, `SELECT installed FROM plugin_installations WHERE instance_id=?`, instanceID).Scan(&installed); err != nil {
		return PluginStoredValue{}, err
	}
	if !installed {
		return PluginStoredValue{}, bad("PLUGIN_NOT_INSTALLED", "插件已卸载，保留数据只读")
	}
	var revision int64
	err := tx.QueryRowContext(ctx, `SELECT revision FROM plugin_values WHERE instance_id=? AND kind=? AND item_key=?`, instanceID, kind, key).Scan(&revision)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return PluginStoredValue{}, err
	}
	if revision != expectedRevision {
		return PluginStoredValue{}, bad("PLUGIN_REVISION_CONFLICT", "插件数据已变化，请重新读取后保存")
	}
	sealed, err := p.seal(kind, instanceID, key, raw)
	if err != nil {
		return PluginStoredValue{}, err
	}
	value := PluginStoredValue{Revision: revision + 1, Value: append(json.RawMessage(nil), raw...), UpdatedAt: now()}
	_, err = tx.ExecContext(ctx, `INSERT INTO plugin_values VALUES(?,?,?,?,?,?) ON CONFLICT(instance_id,kind,item_key) DO UPDATE SET revision=excluded.revision,sealed=excluded.sealed,updated_at=excluded.updated_at`, instanceID, kind, key, value.Revision, sealed, value.UpdatedAt)
	return value, err
}

func (p *PluginStore) saveValue(ctx context.Context, instanceID, kind, key string, expectedRevision int64, value any) (PluginStoredValue, error) {
	limit := pluginValueLimit
	if kind == "data" {
		limit = pluginDataValueLimit
	}
	raw, err := pluginJSONLimit(value, kind == "config", limit)
	if err != nil {
		return PluginStoredValue{}, err
	}
	tx, err := p.store.DB.BeginTx(ctx, nil)
	if err != nil {
		return PluginStoredValue{}, err
	}
	defer tx.Rollback()
	stored, err := p.saveValueTx(ctx, tx, instanceID, kind, key, expectedRevision, raw)
	if err != nil {
		return PluginStoredValue{}, err
	}
	return stored, tx.Commit()
}

func (p *PluginStore) SaveConfig(ctx context.Context, instanceID string, expectedRevision int64, value any) (PluginStoredValue, error) {
	return p.saveValue(ctx, instanceID, "config", "main", expectedRevision, value)
}

func (p *PluginStore) SaveData(ctx context.Context, instanceID, key string, expectedRevision int64, value any) (PluginStoredValue, error) {
	return p.saveValue(ctx, instanceID, "data", key, expectedRevision, value)
}

// Uninstall keeps all config/data and migration receipts. Reset removes only
// this instance's config/KV; it does not delete its package or migration audit.
func (p *PluginStore) Uninstall(ctx context.Context, instanceID string) error {
	tx, err := p.store.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `UPDATE plugin_installations SET installed=0,desired_enabled=0,generation=generation+1,updated_at=? WHERE instance_id=?`, now(), instanceID)
	if err != nil {
		return err
	}
	if changed, _ := result.RowsAffected(); changed == 0 {
		return sql.ErrNoRows
	}
	if _, err := tx.ExecContext(ctx, `UPDATE plugin_event_subscriptions SET active=0 WHERE instance_id=?`, instanceID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE plugin_event_batches SET state='canceled' WHERE instance_id=? AND run_id IS NULL`, instanceID); err != nil {
		return err
	}
	return tx.Commit()
}

func (p *PluginStore) Reset(ctx context.Context, instanceID string) error {
	tx, err := p.store.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `UPDATE plugin_installations SET desired_enabled=0,generation=generation+1,updated_at=? WHERE instance_id=?`, now(), instanceID)
	if err != nil {
		return err
	}
	if changed, _ := result.RowsAffected(); changed == 0 {
		return sql.ErrNoRows
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM plugin_values WHERE instance_id=?`, instanceID); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE plugin_event_subscriptions SET active=0 WHERE instance_id=?`, instanceID); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE plugin_event_batches SET state='canceled' WHERE instance_id=? AND run_id IS NULL`, instanceID); err != nil {
		return err
	}
	return tx.Commit()
}

const pluginMigrationColumns = `id,source_fingerprint,source_plugin_id,instance_id,state,error_code,details,created_at,updated_at`

func (p *PluginStore) scanMigration(row interface{ Scan(...any) error }) (PluginMigrationRun, error) {
	var run PluginMigrationRun
	var sealed []byte
	err := row.Scan(&run.ID, &run.SourceFingerprint, &run.SourcePluginID, &run.InstanceID, &run.State, &run.ErrorCode, &sealed, &run.CreatedAt, &run.UpdatedAt)
	if err != nil {
		return run, err
	}
	run.Details, err = p.unseal("migration", run.InstanceID, run.ID, sealed)
	return run, err
}

func (p *PluginStore) Migration(ctx context.Context, runID string) (PluginMigrationRun, error) {
	return p.scanMigration(p.store.DB.QueryRowContext(ctx, `SELECT `+pluginMigrationColumns+` FROM plugin_migration_runs WHERE id=?`, runID))
}

func (p *PluginStore) BeginMigration(ctx context.Context, sourceFingerprint, sourcePluginID, instanceID string) (PluginMigrationRun, error) {
	if !pluginStorageDigest.MatchString(sourceFingerprint) || !pluginStorageID.MatchString(sourcePluginID) || !pluginStorageID.MatchString(instanceID) {
		return PluginMigrationRun{}, bad("INVALID_PLUGIN_MIGRATION", "插件迁移来源或实例无效")
	}
	run := PluginMigrationRun{ID: id("pm_"), SourceFingerprint: sourceFingerprint, SourcePluginID: sourcePluginID, InstanceID: instanceID, State: "planned", CreatedAt: now(), UpdatedAt: now()}
	sealed, err := p.seal("migration", instanceID, run.ID, []byte(`{}`))
	if err != nil {
		return PluginMigrationRun{}, err
	}
	_, err = p.store.DB.ExecContext(ctx, `INSERT INTO plugin_migration_runs VALUES(?,?,?,?,?,'',?,?,?) ON CONFLICT(source_fingerprint,source_plugin_id,instance_id) DO NOTHING`, run.ID, sourceFingerprint, sourcePluginID, instanceID, run.State, sealed, run.CreatedAt, run.UpdatedAt)
	if err != nil {
		return PluginMigrationRun{}, err
	}
	return p.scanMigration(p.store.DB.QueryRowContext(ctx, `SELECT `+pluginMigrationColumns+` FROM plugin_migration_runs WHERE source_fingerprint=? AND source_plugin_id=? AND instance_id=?`, sourceFingerprint, sourcePluginID, instanceID))
}

func (p *PluginStore) SetMigrationState(ctx context.Context, runID, state, errorCode string, details any) error {
	if state != "running" && state != "failed" || errorCode != "" && !pluginStorageID.MatchString(errorCode) || state != "failed" && errorCode != "" {
		return bad("INVALID_PLUGIN_MIGRATION", "插件迁移状态或错误代码无效")
	}
	raw, err := pluginJSON(details, false)
	if err != nil {
		return err
	}
	run, err := p.Migration(ctx, runID)
	if err != nil {
		return err
	}
	sealed, err := p.seal("migration", run.InstanceID, run.ID, raw)
	if err != nil {
		return err
	}
	result, err := p.store.DB.ExecContext(ctx, `UPDATE plugin_migration_runs SET state=?,error_code=?,details=?,updated_at=? WHERE id=? AND state IN ('planned','running','failed')`, state, errorCode, sealed, now(), runID)
	if err != nil {
		return err
	}
	if changed, _ := result.RowsAffected(); changed != 1 {
		return bad("PLUGIN_MIGRATION_TERMINAL", "已导入的插件迁移记录不能改写")
	}
	return nil
}

// Import commits the converted configuration, KV, installation and receipt
// atomically. "imported" means data imported, never runtime/function parity.
// Existing target instances are not overwritten. Replaying an identical import
// returns its receipt without reverting later user edits or running onlyonce.
func (p *PluginStore) Import(ctx context.Context, in PluginImport) (PluginMigrationReceipt, error) {
	installation, err := validatePluginInstallation(in.Installation)
	if err != nil {
		return PluginMigrationReceipt{}, err
	}
	installation.DesiredEnabled = false // Import never activates schedules/actions.
	config, err := pluginJSON(in.Config, true)
	if err != nil {
		return PluginMigrationReceipt{}, err
	}
	total := len(config) + len(installation.Manifest)
	if len(in.Data) > 4096 {
		return PluginMigrationReceipt{}, bad("INVALID_PLUGIN_MIGRATION", "插件迁移数据条目过多")
	}
	for key, value := range in.Data {
		if !validPluginKey(key) {
			return PluginMigrationReceipt{}, bad("INVALID_PLUGIN_VALUE", "插件数据键无效")
		}
		if _, err = pluginJSONLimit(value, false, pluginDataValueLimit); err != nil {
			return PluginMigrationReceipt{}, err
		}
		total += len(key) + len(value)
	}
	if total > pluginImportLimit {
		return PluginMigrationReceipt{}, bad("INVALID_PLUGIN_MIGRATION", "插件迁移数据超过大小上限")
	}
	// Include the private manifest explicitly; its public JSON projection omits it.
	digestInput, err := json.Marshal(struct {
		PluginID, Version string
		ManifestSchema    int
		Manifest, Config  json.RawMessage
		Data              map[string]json.RawMessage
	}{installation.PluginID, installation.Version, installation.ManifestSchemaVersion, installation.Manifest, config, in.Data})
	if err != nil {
		return PluginMigrationReceipt{}, err
	}
	sum := sha256.Sum256(digestInput)
	digest := hex.EncodeToString(sum[:])
	tx, err := p.store.DB.BeginTx(ctx, nil)
	if err != nil {
		return PluginMigrationReceipt{}, err
	}
	defer tx.Rollback()
	run, err := p.scanMigration(tx.QueryRowContext(ctx, `SELECT `+pluginMigrationColumns+` FROM plugin_migration_runs WHERE id=?`, in.RunID))
	if err != nil {
		return PluginMigrationReceipt{}, err
	}
	if run.InstanceID != installation.InstanceID {
		return PluginMigrationReceipt{}, bad("PLUGIN_MIGRATION_CONFLICT", "迁移目标与预览记录不符")
	}
	receipt := PluginMigrationReceipt{RunID: run.ID, SourceFingerprint: run.SourceFingerprint, SourcePluginID: run.SourcePluginID, InstanceID: run.InstanceID, State: "imported", Digest: digest}
	var previousDigest string
	err = tx.QueryRowContext(ctx, `SELECT digest,imported_at FROM plugin_migration_receipts WHERE source_fingerprint=? AND source_plugin_id=? AND instance_id=?`, run.SourceFingerprint, run.SourcePluginID, run.InstanceID).Scan(&previousDigest, &receipt.ImportedAt)
	if err == nil {
		if previousDigest != digest {
			return PluginMigrationReceipt{}, bad("PLUGIN_MIGRATION_CONFLICT", "相同来源迁移内容已变化，未覆盖现有插件")
		}
		return receipt, tx.Commit()
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return PluginMigrationReceipt{}, err
	}
	if run.State == "imported" {
		return PluginMigrationReceipt{}, bad("PLUGIN_DATA_CORRUPT", "迁移收据缺失，需核对后继续")
	}
	var existing int
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM plugin_installations WHERE instance_id=?`, installation.InstanceID).Scan(&existing); err != nil {
		return PluginMigrationReceipt{}, err
	}
	if existing != 0 {
		return PluginMigrationReceipt{}, bad("PLUGIN_IMPORT_TARGET_EXISTS", "目标插件已有配置或保留数据，迁移未覆盖")
	}
	if err = p.installTx(ctx, tx, installation); err != nil {
		return PluginMigrationReceipt{}, err
	}
	if _, err = p.saveValueTx(ctx, tx, installation.InstanceID, "config", "main", 0, config); err != nil {
		return PluginMigrationReceipt{}, err
	}
	for key, value := range in.Data {
		if _, err = p.saveValueTx(ctx, tx, installation.InstanceID, "data", key, 0, value); err != nil {
			return PluginMigrationReceipt{}, err
		}
	}
	receipt.ImportedAt = now()
	if _, err = tx.ExecContext(ctx, `INSERT INTO plugin_migration_receipts VALUES(?,?,?,?,?,?)`, run.SourceFingerprint, run.SourcePluginID, run.InstanceID, run.ID, digest, receipt.ImportedAt); err != nil {
		return PluginMigrationReceipt{}, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE plugin_migration_runs SET state='imported',error_code='',updated_at=? WHERE id=?`, receipt.ImportedAt, run.ID); err != nil {
		return PluginMigrationReceipt{}, err
	}
	if err = tx.Commit(); err != nil {
		return PluginMigrationReceipt{}, fmt.Errorf("plugin import commit: %w", err)
	}
	return receipt, nil
}
