package app

// Shared _PluginBase storage adapter. Contract grants and active-call admission
// are supplied by the manager, never reconstructed from artifact payloads.
import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"math"
	"math/big"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"

	"golang.org/x/sys/unix"
	"light-media/internal/plugins"
)

// The manager must add pluginOriginalBase originalBaseState to App. Its zero
// value is ready for use. A cache entry containing null differs from no entry.
type originalBaseState struct {
	mu     sync.Mutex
	config map[string]originalBaseConfigValue
}

type originalBaseConfigValue struct {
	value    json.RawMessage
	present  bool                          // True even for an explicitly cached null; false for a miss.
	observed originalBaseConfigFingerprint // Actual DB state, not cached-value equality.
}

type originalBaseCallScope struct {
	object         *originalPluginInstance
	phase          string
	installation   PluginInstallation
	logCall        originalPluginLogCall
	initialization *originalInitializationCall
	iyuuMaterial   *originalIYUUMTeamMaterialGrant
	fnosRefresh    *originalFNOSRefreshEvent
	localBackup    *atomic.Bool
	brush          *originalBrushSourceCall
	cleanSeed      *originalCleanSeedCall
	seedRescue     *originalSeedRescueCall
	cloudflare     *originalCloudflareCall
	customHosts    *originalCustomHostsCall
	// validate must reject expired/replaced calls, not merely installed objects.
	validate func(context.Context) error
	// The host-owned recognized contract defines permitted namespace access;
	// a plugin payload cannot select or expand that contract.
}

type originalBaseRequest struct {
	PluginID *string         `json:"plugin_id"`
	Key      *string         `json:"key"`
	Config   json.RawMessage `json:"config"`
	Value    json.RawMessage `json:"value"`
}

type originalBaseDataRow struct {
	ID       int64           `json:"id"`
	PluginID string          `json:"plugin_id"`
	Key      string          `json:"key"`
	Value    json.RawMessage `json:"value"`
}

func (a *App) pluginOriginalBaseHost(ctx context.Context, scope originalBaseCallScope, identity plugins.Identity, method string, raw json.RawMessage) (json.RawMessage, error) {
	if scope.validate == nil || !plugins.IsBaseMethod(method) {
		return nil, bad("PLUGIN_CAPABILITY_DENIED", "插件公共方法未获授权")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := scope.validate(ctx); err != nil {
		return nil, err
	}
	in := scope.installation
	if identity.PluginID != in.PluginID || identity.InstanceID != in.InstanceID || identity.Version != in.Version {
		return nil, bad("PLUGIN_IDENTITY_MISMATCH", "插件执行身份不匹配")
	}
	ctx = withOriginalPluginLogContext(ctx, scope)
	if method == plugins.BaseGetInstanceMetadata {
		return a.originalBaseInstanceMetadata(ctx, scope, raw)
	}
	if method == plugins.BaseConfigSaveOrigin {
		call := scope.initialization
		if call == nil || !call.active.Load() || scope.phase != "init" || call.object != scope.object {
			return nil, bad("PLUGIN_CAPABILITY_DENIED", "配置保存来源只在初始化期间可读")
		}
		if err := originalResourceDecode(raw, &struct{}{}); err != nil {
			return nil, err
		}
		return json.Marshal(call.configurationSave)
	}
	var request originalBaseRequest
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(&request); err != nil {
		return nil, bad("INVALID_PLUGIN_VALUE", "插件公共方法参数无效")
	}
	if err := d.Decode(new(any)); !errors.Is(err, io.EOF) {
		return nil, bad("INVALID_PLUGIN_VALUE", "插件公共方法参数无效")
	}
	target := in.InstanceID
	if request.PluginID != nil && *request.PluginID != "" {
		target = *request.PluginID
	}
	if in.PluginID == "MergeSiteSwitch" && target != in.InstanceID {
		return nil, bad("PLUGIN_CAPABILITY_DENIED", "聚合站点开关只能通过专用站点字段接口访问其他插件")
	}
	if in.PluginID == "SeedRescue" && target != in.InstanceID {
		return nil, bad("PLUGIN_CAPABILITY_DENIED", "稀缺保种只能访问当前实例的数据")
	}
	if in.PluginID == "CloudflareSpeedTest" && target != in.InstanceID {
		return nil, bad("PLUGIN_CAPABILITY_DENIED", "Cloudflare IP优选只能通过受限接口更新 CustomHosts")
	}
	if originalCustomHostsInstallation(in) && target != in.InstanceID {
		return nil, bad("PLUGIN_CAPABILITY_DENIED", "自定义Hosts只能访问当前实例的配置")
	}
	if target == "" || len(target) > 4096 || strings.ContainsRune(target, 0) {
		return nil, bad("INVALID_PLUGIN_VALUE", "插件数据命名空间无效")
	}
	// Serialize this cache/storage operation, then revalidate after waiting. The
	// caller's manager owns lifecycle synchronization; this lock is not admission.
	a.pluginMu.Lock()
	defer a.pluginMu.Unlock()
	state := &a.pluginOriginalBase
	state.mu.Lock()
	defer state.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := scope.validate(ctx); err != nil {
		return nil, err
	}
	switch method {
	case plugins.BaseGetConfig:
		value, err := a.originalBaseConfigLocked(ctx, target)
		if call := scope.customHosts; err == nil && call != nil && call.init == nil && len(call.config) == 0 {
			call.config = copyOriginalBaseJSON(value)
			call.fingerprint = state.config[target].observed
		}
		return value, err
	case plugins.BaseUpdateConfig:
		if originalCustomHostsInstallation(in) {
			return a.originalCustomHostsSaveLocked(ctx, scope, target, request.Config)
		}
		return a.originalBaseUpdateConfigLocked(ctx, target, request.Config)
	case plugins.BaseGetDataPath:
		path, err := a.originalBaseDataPath(ctx, target)
		if err != nil {
			return nil, err
		}
		return json.Marshal(path)
	case plugins.BaseGetData:
		return a.pluginStore.originalBaseGetData(ctx, target, request.Key)
	case plugins.BaseSaveData:
		if in.PluginID == "SeedRescue" && in.InstanceID != in.PluginID {
			current, err := a.pluginStore.Installation(ctx, target)
			if errors.Is(err, sql.ErrNoRows) || err == nil && !current.Installed {
				// A retained scan can reach its stop checkpoint after the
				// clone's configuration and history have been removed.
				return json.RawMessage("null"), nil
			}
			if err != nil {
				return nil, err
			}
		}
		if request.Key == nil {
			return nil, bad("INVALID_PLUGIN_VALUE", "save_data 缺少 key")
		}
		if len(request.Value) == 0 {
			return nil, bad("INVALID_PLUGIN_VALUE", "save_data 缺少 value")
		}
		if err := a.pluginStore.originalBaseSaveData(ctx, target, *request.Key, request.Value); err != nil {
			return nil, err
		}
		return json.RawMessage("null"), nil
	case plugins.BaseDelData:
		// _PluginBase requires a key argument, but explicitly null is valid and
		// selects PluginDataOper's all-data deletion branch, as does the empty string.
		var fields map[string]json.RawMessage
		_ = json.Unmarshal(raw, &fields)
		if _, exists := fields["key"]; !exists {
			return nil, bad("INVALID_PLUGIN_VALUE", "del_data 缺少 key")
		}
		if err := a.pluginStore.originalBaseDeleteData(ctx, target, request.Key); err != nil {
			return nil, err
		}
		return json.RawMessage("null"), nil
	}
	return nil, bad("PLUGIN_CAPABILITY_DENIED", "插件公共方法未实现")
}

// Metadata is owned by the authenticated manager instance, never by a caller
// supplied target or a replacement executable's display claims. The worker's
// pinned implementation identity stays separate from the clone's presentation.
func (a *App) originalBaseInstanceMetadata(ctx context.Context, scope originalBaseCallScope, raw json.RawMessage) (json.RawMessage, error) {
	var request struct{}
	if err := originalResourceDecode(raw, &request); err != nil {
		return nil, err
	}
	object := scope.object
	if object == nil || object.app != a || object.installation.InstanceID != scope.installation.InstanceID {
		return nil, bad("PLUGIN_CAPABILITY_DENIED", "插件显示信息未绑定当前实例")
	}
	a.pluginMu.Lock()
	defer a.pluginMu.Unlock()
	if err := scope.validate(ctx); err != nil {
		return nil, err
	}
	in := scope.installation
	metadata, err := a.pluginStore.instanceMetadata(ctx, a.Store.DB, in.InstanceID)
	if err != nil {
		return nil, err
	}
	result := plugins.InstanceMetadata{
		InstanceID: in.InstanceID, PluginID: in.PluginID, Version: in.Version,
		Name: object.manifest.Name, Description: object.manifest.Description,
		DisplayVersion: in.Version, Icon: object.manifest.Icon,
	}
	if metadata.SourceInstanceID != "" {
		result.SourceInstanceID = metadata.SourceInstanceID
		result.Name = metadata.Name
		result.Description = metadata.Description
		result.DisplayVersion = metadata.DisplayVersion
		result.Icon = metadata.Icon
	}
	return json.Marshal(result)
}

func copyOriginalBaseJSON(raw json.RawMessage) json.RawMessage {
	return append(json.RawMessage(nil), raw...)
}

// Call after manager configuration save/reset, never merely on reload. Manager
// writers must synchronize this invalidation with their real storage mutation.
func (a *App) invalidateOriginalBaseConfig(target string) {
	a.pluginOriginalBase.mu.Lock()
	defer a.pluginOriginalBase.mu.Unlock()
	delete(a.pluginOriginalBase.config, target)
}

// The observed row is separate from Python's cache: a successful falsy set
// deletes a row but retains the cache, and a failed set retains its new cache.
// An unrelated native writer changes the observation and must refresh the cache.
type originalBaseConfigFingerprint struct {
	present      bool
	revision     int64
	updatedAt    string
	valueLimit   int
	sealedDigest [sha256.Size]byte
}

type originalBaseConfigReader interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func (p *PluginStore) readOriginalBaseConfigRow(ctx context.Context, q originalBaseConfigReader, target string) (originalBaseConfigFingerprint, []byte, error) {
	var observed originalBaseConfigFingerprint
	var sealed []byte
	var revision sql.NullInt64
	var pluginID string
	var hasCloneMetadata bool
	err := q.QueryRowContext(ctx, `SELECT v.revision,v.sealed,COALESCE(v.updated_at,''),COALESCE(i.plugin_id,''),m.instance_id IS NOT NULL
		FROM (SELECT ? AS instance_id) AS target
		LEFT JOIN plugin_installations AS i ON i.instance_id=target.instance_id
		LEFT JOIN plugin_instance_metadata AS m ON m.instance_id=target.instance_id
		LEFT JOIN plugin_values AS v ON v.instance_id=target.instance_id AND v.kind='config' AND v.item_key='main'`, target).
		Scan(&revision, &sealed, &observed.updatedAt, &pluginID, &hasCloneMetadata)
	if err != nil {
		return originalBaseConfigFingerprint{}, nil, err
	}
	// A clone's own ID or copied implementation can change after a market
	// update. Check every sealed source link before using the ancestor's bound.
	for instance := target; hasCloneMetadata; {
		metadata, err := p.instanceMetadata(ctx, q, instance)
		if err != nil {
			return originalBaseConfigFingerprint{}, nil, err
		}
		parent := metadata.SourceInstanceID
		if parent == "" || len(parent) >= len(instance) || !strings.HasPrefix(instance, parent) {
			return originalBaseConfigFingerprint{}, nil, bad("PLUGIN_INSTANCE_METADATA_INVALID", "插件分身来源与实例标识不一致")
		}
		err = q.QueryRowContext(ctx, `SELECT i.plugin_id,m.instance_id IS NOT NULL
			FROM plugin_installations AS i LEFT JOIN plugin_instance_metadata AS m ON m.instance_id=i.instance_id
			WHERE i.instance_id=?`, parent).Scan(&pluginID, &hasCloneMetadata)
		if errors.Is(err, sql.ErrNoRows) {
			pluginID = ""
			break
		}
		if err != nil {
			return originalBaseConfigFingerprint{}, nil, err
		}
		instance = parent
	}
	observed.valueLimit = originalPluginConfigValueLimit(pluginID)
	observed.present, observed.revision = revision.Valid, revision.Int64
	if observed.present {
		observed.sealedDigest = sha256.Sum256(sealed)
	}
	return observed, sealed, nil
}

func (a *App) originalBaseObserveConfigLocked(target string, observed originalBaseConfigFingerprint, sealed []byte) (json.RawMessage, error) {
	if cached, ok := a.pluginOriginalBase.config[target]; ok && cached.observed == observed {
		return copyOriginalBaseJSON(cached.value), nil
	}
	raw := json.RawMessage("null")
	if observed.present {
		var err error
		raw, err = a.pluginStore.unsealBound("config", target, "main", sealed, observed.valueLimit)
		if err != nil {
			return nil, err
		}
	}
	if a.pluginOriginalBase.config == nil {
		a.pluginOriginalBase.config = make(map[string]originalBaseConfigValue)
	}
	a.pluginOriginalBase.config[target] = originalBaseConfigValue{value: copyOriginalBaseJSON(raw), present: observed.present, observed: observed}
	return copyOriginalBaseJSON(raw), nil
}

func (a *App) originalBaseConfigLocked(ctx context.Context, target string) (json.RawMessage, error) {
	observed, sealed, err := a.pluginStore.readOriginalBaseConfigRow(ctx, a.pluginStore.store.DB, target)
	if err != nil {
		return nil, err
	}
	return a.originalBaseObserveConfigLocked(target, observed, sealed)
}

// Initializers and reload must use the same SystemConfig cache as get_config.
// A cached {} after source-compatible row deletion is still present, not None.
// This is a read helper, not an execution grant or config-row creator.
func (a *App) readOriginalBaseConfigForInit(ctx context.Context, target string) (json.RawMessage, bool, error) {
	a.pluginOriginalBase.mu.Lock()
	defer a.pluginOriginalBase.mu.Unlock()
	raw, err := a.originalBaseConfigLocked(ctx, target)
	if err != nil {
		return nil, false, err
	}
	return raw, a.pluginOriginalBase.config[target].present, nil
}

func (a *App) originalBaseUpdateConfigLocked(ctx context.Context, target string, raw json.RawMessage) (json.RawMessage, error) {
	// Python annotations do not constrain this forwarding method at runtime.
	// Preserve all JSON values, including falsy values, separately from omission.
	if len(raw) == 0 {
		return nil, bad("INVALID_PLUGIN_VALUE", "update_config 缺少 config")
	}
	p := a.pluginStore
	tx, err := p.store.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	observed, sealed, err := p.readOriginalBaseConfigRow(ctx, tx, target)
	if err != nil {
		return nil, err
	}
	checked, err := pluginJSONLimit(raw, false, observed.valueLimit)
	if err != nil {
		return nil, err
	}
	old, err := a.originalBaseObserveConfigLocked(target, observed, sealed)
	if err != nil {
		return nil, err
	}
	revision, present := observed.revision, observed.present
	// SystemConfigOper.set updates its cache before attempting the database
	// mutation, including when that mutation fails. The active SDK callback
	// scope is authenticated before this storage method is entered.
	a.pluginOriginalBase.config[target] = originalBaseConfigValue{value: copyOriginalBaseJSON(checked), present: true, observed: observed}
	if present && originalBaseJSONEqual(old, checked) {
		return json.RawMessage("null"), nil
	}
	if present && !originalBaseJSONTruthy(checked) {
		_, err = tx.ExecContext(ctx, `DELETE FROM plugin_values WHERE instance_id=? AND kind='config' AND item_key='main'`, target)
	} else {
		sealed, err = p.seal("config", target, "main", checked)
		if err == nil {
			_, err = tx.ExecContext(ctx, `INSERT INTO plugin_values(instance_id,kind,item_key,revision,sealed,updated_at) VALUES(?,'config','main',?,?,?) ON CONFLICT(instance_id,kind,item_key) DO UPDATE SET revision=excluded.revision,sealed=excluded.sealed,updated_at=excluded.updated_at`, target, revision+1, sealed, now())
		}
	}
	if err != nil {
		return nil, err
	}
	// Observe our actual post-mutation row inside this transaction, before
	// commit. A later namespace write must not be mistaken for our cached value.
	after, _, err := p.readOriginalBaseConfigRow(ctx, tx, target)
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	cached := a.pluginOriginalBase.config[target]
	cached.observed = after
	a.pluginOriginalBase.config[target] = cached
	return json.RawMessage("true"), nil
}

func originalBaseJSONTruthy(raw json.RawMessage) bool {
	var v any
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	if d.Decode(&v) != nil {
		return false
	}
	switch x := v.(type) {
	case nil:
		return false
	case bool:
		return x
	case string:
		return x != ""
	case []any:
		return len(x) != 0
	case map[string]any:
		return len(x) != 0
	case json.Number:
		n, inf, ok := originalBaseNumber(x)
		return ok && (inf != 0 || n.Sign() != 0)
	}
	return true
}

// Python compares booleans and numbers by numeric value (True == 1), including
// inside lists/dicts. Decoder.UseNumber avoids float rounding of large integers.
func originalBaseJSONEqual(a, b json.RawMessage) bool {
	decode := func(raw json.RawMessage) (any, bool) {
		var v any
		d := json.NewDecoder(bytes.NewReader(raw))
		d.UseNumber()
		err := d.Decode(&v)
		return v, err == nil
	}
	av, ao := decode(a)
	bv, bo := decode(b)
	return ao && bo && originalBaseValueEqual(av, bv)
}
func originalBaseNumber(v any) (*big.Rat, int, bool) {
	switch x := v.(type) {
	case bool:
		if x {
			return big.NewRat(1, 1), 0, true
		}
		return big.NewRat(0, 1), 0, true
	case json.Number:
		text := string(x)
		if strings.ContainsAny(text, ".eE") {
			f, err := strconv.ParseFloat(text, 64)
			if math.IsInf(f, 1) {
				return nil, 1, true
			}
			if math.IsInf(f, -1) {
				return nil, -1, true
			}
			if err != nil {
				return nil, 0, false
			}
			return new(big.Rat).SetFloat64(f), 0, true
		}
		n, ok := new(big.Rat).SetString(text)
		return n, 0, ok
	}
	return nil, 0, false
}

func originalBaseValueEqual(a, b any) bool {
	if an, ai, ok := originalBaseNumber(a); ok {
		bn, bi, yes := originalBaseNumber(b)
		if !yes {
			return false
		}
		if ai != 0 || bi != 0 {
			return ai != 0 && ai == bi
		}
		return an.Cmp(bn) == 0
	}
	switch av := a.(type) {
	case nil:
		return b == nil
	case string:
		bv, ok := b.(string)
		return ok && av == bv
	case []any:
		bv, ok := b.([]any)
		if !ok || len(av) != len(bv) {
			return false
		}
		for i := range av {
			if !originalBaseValueEqual(av[i], bv[i]) {
				return false
			}
		}
		return true
	case map[string]any:
		bv, ok := b.(map[string]any)
		if !ok || len(av) != len(bv) {
			return false
		}
		for k, v := range av {
			other, found := bv[k]
			if !found || !originalBaseValueEqual(v, other) {
				return false
			}
		}
		return true
	}
	return false
}

func (p *PluginStore) originalBaseGetData(ctx context.Context, target string, key *string) (json.RawMessage, error) {
	if key != nil && *key != "" {
		value, err := p.Data(ctx, target, *key)
		if errors.Is(err, sql.ErrNoRows) {
			return json.RawMessage("null"), nil
		}
		return value.Value, err
	}
	rows, err := p.store.DB.QueryContext(ctx, `SELECT rowid,item_key,sealed FROM plugin_values WHERE instance_id=? AND kind='data'`, target)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]originalBaseDataRow, 0)
	for rows.Next() {
		row := originalBaseDataRow{PluginID: target}
		var sealed []byte
		if err = rows.Scan(&row.ID, &row.Key, &sealed); err != nil {
			return nil, err
		}
		row.Value, err = p.unseal("data", target, row.Key, sealed)
		if err != nil {
			return nil, err
		}
		result = append(result, row)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	return json.Marshal(result)
}

func (p *PluginStore) originalBaseSaveData(ctx context.Context, target, key string, raw json.RawMessage) error {
	if len(key) > 256 || strings.ContainsAny(key, "\x00\r\n") {
		return bad("INVALID_PLUGIN_VALUE", "插件数据键无效")
	}
	checked, err := pluginJSONLimit(raw, false, pluginDataValueLimit)
	if err != nil {
		return err
	}
	tx, err := p.store.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var revision int64
	var previous []byte
	err = tx.QueryRowContext(ctx, `SELECT revision,sealed FROM plugin_values WHERE instance_id=? AND kind='data' AND item_key=?`, target, key).Scan(&revision, &previous)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if err == nil {
		if _, err = p.unseal("data", target, key, previous); err != nil {
			return err
		}
	}
	sealed, err := p.seal("data", target, key, checked)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO plugin_values(instance_id,kind,item_key,revision,sealed,updated_at) VALUES(?,'data',?,?,?,?) ON CONFLICT(instance_id,kind,item_key) DO UPDATE SET revision=excluded.revision,sealed=excluded.sealed,updated_at=excluded.updated_at`, target, key, revision+1, sealed, now())
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (p *PluginStore) originalBaseDeleteData(ctx context.Context, target string, key *string) error {
	tx, err := p.store.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// This is ordinary original data deletion, NOT a reset/generation transition.
	if key != nil && *key != "" {
		_, err = tx.ExecContext(ctx, `DELETE FROM plugin_values WHERE instance_id=? AND kind='data' AND item_key=?`, target, *key)
	} else {
		_, err = tx.ExecContext(ctx, `DELETE FROM plugin_values WHERE instance_id=? AND kind='data'`, target)
	}
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (a *App) originalBaseDataPath(ctx context.Context, target string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	// One namespace component, not a path supplied by a plugin. Even authorized
	// cross-namespace targeting does not authorize path traversal or symlinks.
	if !pluginStorageID.MatchString(target) || target == "." || target == ".." {
		return "", bad("INVALID_PLUGIN_VALUE", "插件数据目录名称无效")
	}
	ensure := true
	// Descriptor-relative Unix calls are deliberate: os.Root.OpenFile follows symlinks
	// within its root even when O_NOFOLLOW is supplied. Here each single named
	// component must itself be a directory, never any symlink (including sibling).
	rootFD, err := originalBaseOpenDirectory(-1, a.Dir)
	if err != nil {
		return "", err
	}
	defer syscall.Close(rootFD)
	dataFD, err := originalBaseEnsureDirectory(rootFD, "plugin-data", ensure)
	if err != nil {
		return "", err
	}
	defer syscall.Close(dataFD)
	targetFD, err := originalBaseEnsureDirectory(dataFD, target, ensure)
	if err != nil {
		return "", err
	}
	defer syscall.Close(targetFD)
	return filepath.Join(a.Dir, "plugin-data", target), nil
}

// Existing directories retain their permissions; newly created directories
// receive 0700 subject to umask. No chmod, removal or arbitrary path is exposed.
func originalBaseEnsureDirectory(parent int, name string, ensure bool) (int, error) {
	if ensure {
		err := unix.Mkdirat(parent, name, 0700)
		if err != nil && !errors.Is(err, syscall.EEXIST) {
			return -1, err
		}
	}
	fd, err := originalBaseOpenDirectory(parent, name)
	return fd, err
}

func originalBaseOpenDirectory(parent int, name string) (int, error) {
	flags := syscall.O_RDONLY | syscall.O_DIRECTORY | syscall.O_NOFOLLOW | syscall.O_CLOEXEC
	for {
		var fd int
		var err error
		if parent < 0 {
			fd, err = syscall.Open(name, flags, 0)
		} else {
			fd, err = unix.Openat(parent, name, flags, 0)
		}
		if errors.Is(err, syscall.EINTR) {
			continue
		}
		return fd, err
	}
}
