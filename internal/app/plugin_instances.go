package app

// Instance management belongs to the host, not individual plugin workers.
// Presentation versions never select executable code. Exact-ID market updates
// may replace a copied clone implementation; other identity changes are rejected.
// Clones get independent namespaces and actual SDK objects.
import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"light-media/internal/plugins"
	"light-media/internal/plugins/artifacts"

	"modernc.org/sqlite"
)

const pluginInstanceSchema = `CREATE TABLE IF NOT EXISTS plugin_instance_metadata(
 instance_id TEXT PRIMARY KEY,sealed BLOB NOT NULL,
 FOREIGN KEY(instance_id) REFERENCES plugin_installations(instance_id));`

// The fixed frontend's alnum<=20 rule does not constrain the backend API.
// The complete generated ID is checked separately against a safe Linux path.
type pluginCloneSuffixIdentifier struct{}

func (pluginCloneSuffixIdentifier) MatchString(value string) bool {
	return value != "" && plugins.ValidInstanceID("P"+value)
}

var pluginCloneSuffix pluginCloneSuffixIdentifier

type PluginCloneRequest struct {
	Suffix      string `json:"suffix"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Version     string `json:"version"`
	Icon        string `json:"icon"`
}

type PluginInstanceMetadata struct {
	SourceInstanceID string `json:"source_instance_id"`
	Name             string `json:"name"`
	Description      string `json:"description"`
	DisplayVersion   string `json:"display_version"`
	Icon             string `json:"icon"`
}

type pluginInstanceMarker struct {
	Purpose          string `json:"purpose"`
	InstanceID       string `json:"instance_id"`
	SourceInstanceID string `json:"source_instance_id"`
}

func (p *PluginStore) instanceMetadata(ctx context.Context, q pluginOrganizationReader, instance string) (PluginInstanceMetadata, error) {
	var out PluginInstanceMetadata
	var manifestSealed, sealed []byte
	err := q.QueryRowContext(ctx, `SELECT i.manifest,m.sealed FROM plugin_installations i LEFT JOIN plugin_instance_metadata m ON m.instance_id=i.instance_id WHERE i.instance_id=?`, instance).Scan(&manifestSealed, &sealed)
	if err != nil {
		return out, err
	}
	manifest, err := p.unseal("manifest", instance, "main", manifestSealed)
	if err != nil {
		return out, err
	}
	var envelope struct {
		Marker *pluginInstanceMarker `json:"host_instance"`
	}
	if json.Unmarshal(manifest, &envelope) != nil {
		return out, bad("PLUGIN_INSTANCE_METADATA_INVALID", "插件实例信息无法校验")
	}
	if envelope.Marker == nil && sealed == nil {
		return out, nil
	}
	if envelope.Marker == nil || sealed == nil || envelope.Marker.Purpose != "instance-clone/v1" || envelope.Marker.InstanceID != instance || !pluginStorageID.MatchString(envelope.Marker.SourceInstanceID) || envelope.Marker.SourceInstanceID == instance {
		return out, bad("PLUGIN_INSTANCE_METADATA_INVALID", "插件分身来源或显示信息缺失")
	}
	raw, err := p.unseal("instance_metadata", instance, "main", sealed)
	if err != nil {
		return out, err
	}
	if json.Unmarshal(raw, &out) != nil || out.SourceInstanceID != envelope.Marker.SourceInstanceID {
		return out, bad("PLUGIN_INSTANCE_METADATA_INVALID", "插件实例信息无法校验")
	}
	return out, nil
}

func validatePluginCloneRequest(in PluginCloneRequest) error {
	if !pluginCloneSuffix.MatchString(in.Suffix) {
		return bad("INVALID_PLUGIN_CLONE", "分身后缀须非空且不能包含路径分隔符、控制符或不安全路径字符")
	}
	// Clone metadata is literal display text under the explicit field decision.
	// Executable identity, artifact paths and manifest validation remain separate.
	return nil
}

// The actual manager resolved the source class and copied its code before this
// namespace write. Installed membership is not a clone lifecycle gate.
func (p *PluginStore) cloneOriginalClass(ctx context.Context, source string, request PluginCloneRequest) (PluginInstallation, error) {
	var out PluginInstallation
	if !pluginStorageID.MatchString(source) {
		return out, bad("INVALID_PLUGIN_CLONE", "来源实例编号无效")
	}
	if err := validatePluginCloneRequest(request); err != nil {
		return out, err
	}
	target := source + plugins.OriginalPluginLower(request.Suffix)
	if !pluginStorageID.MatchString(target) {
		return out, bad("INVALID_PLUGIN_CLONE", "分身编号超过长度限制")
	}
	tx, err := p.store.DB.BeginTx(ctx, nil)
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	var sealed []byte
	if err = tx.QueryRowContext(ctx, `SELECT instance_id,plugin_id,version,manifest_schema,manifest,desired_enabled,installed,generation FROM plugin_installations WHERE instance_id=?`, source).Scan(&out.InstanceID, &out.PluginID, &out.Version, &out.ManifestSchemaVersion, &sealed, &out.DesiredEnabled, &out.Installed, &out.Generation); err != nil {
		return out, err
	}
	out.Manifest, err = p.unseal("manifest", source, "main", sealed)
	if err != nil {
		return out, err
	}
	var storedManifest plugins.Manifest
	if json.Unmarshal(out.Manifest, &storedManifest) != nil || storedManifest.Validate() != nil || storedManifest.PluginID != out.PluginID || storedManifest.Version != out.Version {
		return out, bad("PLUGIN_INSTANCE_METADATA_INVALID", "来源实例与已认证实现元数据不一致")
	}
	manifest := storedManifest

	raw := json.RawMessage(`{}`)
	err = tx.QueryRowContext(ctx, `SELECT sealed FROM plugin_values WHERE instance_id=? AND kind='config' AND item_key='main'`, source).Scan(&sealed)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return out, err
	}
	if err == nil {
		raw, err = p.unseal("config", source, "main", sealed)
		if err != nil {
			return out, err
		}
	}
	if cached, ok := ctx.Value(originalCloneConfigKey{}).(originalCloneConfiguration); ok && cached.source == source {
		raw = cached.raw
	}
	var config map[string]json.RawMessage
	if originalBaseJSONTruthy(raw) && json.Unmarshal(raw, &config) != nil {
		return out, bad("INVALID_PLUGIN_CONFIG", "来源插件配置不是对象")
	}
	// MoviePilot 8a83efe core/plugin.py clone_plugin: only a nonempty
	// source dictionary is copied. Both enable spellings are written, even
	// when absent in the source. Every other value is preserved verbatim.
	copyConfig := len(config) != 0
	if copyConfig {
		config["enable"] = json.RawMessage(`false`)
		config["enabled"] = json.RawMessage(`false`)
	}
	raw, err = json.Marshal(config)
	if err != nil {
		return out, err
	}
	if len(raw) > pluginValueLimit {
		return out, bad("INVALID_PLUGIN_VALUE", "分身配置超过大小上限")
	}
	sourceMeta, err := p.instanceMetadata(ctx, tx, source)
	if err != nil {
		return out, err
	}
	if sourceMeta.SourceInstanceID != "" {
		manifest.Name = sourceMeta.Name
		manifest.Icon = sourceMeta.Icon
		manifest.Description = sourceMeta.Description
	}
	metadata := PluginInstanceMetadata{SourceInstanceID: source, Name: request.Name, Description: request.Description, DisplayVersion: request.Version, Icon: request.Icon}
	if metadata.Name == "" {
		metadata.Name = manifest.Name
	}
	if metadata.Description == "" {
		metadata.Description = manifest.Description
	}
	if strings.TrimFunc(metadata.Icon, originalPluginVersionSpace) == "" {
		metadata.Icon = manifest.Icon
	}
	if metadata.DisplayVersion == "" {
		metadata.DisplayVersion = sourceMeta.DisplayVersion
		if metadata.DisplayVersion == "" {
			metadata.DisplayVersion = out.Version
		}
	}
	var manifestFields map[string]json.RawMessage
	if err = json.Unmarshal(out.Manifest, &manifestFields); err != nil {
		return out, err
	}
	marker, err := json.Marshal(pluginInstanceMarker{Purpose: "instance-clone/v1", InstanceID: target, SourceInstanceID: source})
	if err != nil {
		return out, err
	}
	manifestFields["host_instance"] = marker
	out.Manifest, err = json.Marshal(manifestFields)
	if err != nil {
		return out, err
	}
	out.InstanceID = target
	out.DesiredEnabled = true
	out.Generation = 1
	out.Installed = true
	if err = p.rebindOriginalDeletedCloneTx(ctx, tx, source, out); err != nil {
		return out, err
	}
	if err = p.installTx(ctx, tx, out); err != nil {
		return out, err
	}
	if copyConfig {
		configSealed, sealErr := p.seal("config", target, "main", raw)
		if sealErr != nil {
			return out, sealErr
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO plugin_values(instance_id,kind,item_key,revision,sealed,updated_at) VALUES(?,'config','main',1,?,?) ON CONFLICT(instance_id,kind,item_key) DO UPDATE SET revision=plugin_values.revision+1,sealed=excluded.sealed,updated_at=excluded.updated_at`, target, configSealed, now()); err != nil {
			return out, err
		}
	}
	raw, err = json.Marshal(metadata)
	if err != nil {
		return out, err
	}
	sealed, err = p.seal("instance_metadata", target, "main", raw)
	if err != nil {
		return out, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO plugin_instance_metadata(instance_id,sealed) VALUES(?,?) ON CONFLICT(instance_id) DO UPDATE SET sealed=excluded.sealed`, target, sealed); err != nil {
		return out, err
	}
	// Read the actual class metadata written by this transaction, not an old lifecycle marker.
	out, err = p.managerInstallation(ctx, tx, target)
	if err != nil {
		return out, err
	}
	if err = tx.Commit(); err != nil {
		return out, err
	}
	return out, nil
}

// An exact-ID market update can replace a clone's original implementation ID.
// Once that clone has actually been uninstalled and its code alias removed,
// creating the same clone again must bind the newly copied source package.
// The ordinary Install API still cannot change an existing identity.
func (p *PluginStore) rebindOriginalDeletedCloneTx(ctx context.Context, tx *sql.Tx, source string, next PluginInstallation) error {
	var previous string
	var installed bool
	err := tx.QueryRowContext(ctx, `SELECT plugin_id,installed FROM plugin_installations WHERE instance_id=?`, next.InstanceID).Scan(&previous, &installed)
	if errors.Is(err, sql.ErrNoRows) || err == nil && previous == next.PluginID {
		return nil
	}
	if err != nil {
		return err
	}
	conflict := bad("PLUGIN_INSTANCE_CONFLICT", "插件实例已属于其他插件")
	if installed || previous != next.InstanceID {
		return conflict
	}
	meta, err := p.instanceMetadata(ctx, tx, next.InstanceID)
	if err != nil {
		return err
	}
	if meta.SourceInstanceID != source {
		return conflict
	}
	var sealed []byte
	if err = tx.QueryRowContext(ctx, `SELECT payload FROM records WHERE kind=? AND id=?`, originalPluginAliasKind, next.InstanceID).Scan(&sealed); err != nil {
		return conflict
	}
	raw, err := p.unseal(originalPluginAliasKind, next.InstanceID, "main", sealed)
	if err != nil {
		return err
	}
	var alias originalPluginCodeAlias
	if json.Unmarshal(raw, &alias) != nil || alias.Instance != next.InstanceID || alias.Plugin != previous || alias.Present {
		return conflict
	}
	_, err = tx.ExecContext(ctx, `UPDATE plugin_installations SET plugin_id=? WHERE instance_id=? AND plugin_id=? AND installed=0`, next.PluginID, next.InstanceID, previous)
	return err
}

func (p *PluginStore) cloneOrganizationMemberTx(ctx context.Context, tx *sql.Tx, source, target string) error {
	_, err := p.cloneOrganizationMemberStateTx(ctx, tx, source, target)
	return err
}

func (p *PluginStore) cloneOrganizationMemberStateTx(ctx context.Context, tx *sql.Tx, source, target string) (*PluginOrganization, error) {
	return p.cloneOrganizationMemberCacheTx(ctx, tx, source, target, false, nil)
}

// sourceCache is enabled only by the original HTTP clone folder phase. The
// existing private native Tx helper retains its commit-owned cache behavior.
func (p *PluginStore) cloneOrganizationMemberCacheTx(ctx context.Context, tx *sql.Tx, source, target string, sourceCache bool, info func(event, message string)) (*PluginOrganization, error) {
	org, err := p.organization(ctx, tx)
	if err != nil {
		return nil, err
	}
	org = p.withOriginalFolders(org)
	for _, name := range originalFolderKeys(org) {
		value := org.Folders[name]
		var members []json.RawMessage
		var e error
		found, already, appendable := false, false, true
		if sourceCache {
			membership, err := readOriginalCloneFolderMembership(value, source, target)
			if err != nil {
				return nil, err
			}
			members = membership.members
			found, already, appendable = membership.source, membership.target, membership.appendable
		} else {
			members, e = pluginFolderRawMembers(value)
			if e != nil {
				return nil, e
			}
			for _, member := range members {
				found = found || originalFolderMemberIs(member, source)
				already = already || originalFolderMemberIs(member, target)
			}
		}
		if !found {
			continue
		}
		if name == "" {
			if sourceCache && info != nil {
				info("clone_folder_independent", "原插件 "+source+" 不在任何文件夹中，分身插件 "+target+" 将保持独立")
			}
			return nil, nil
		}
		if already {
			if sourceCache {
				if err := ctx.Err(); err != nil {
					return nil, err
				}
				var row int
				lookupErr := tx.QueryRowContext(ctx, `SELECT 1 FROM plugin_organization WHERE owner_id=1`).Scan(&row)
				if errors.Is(lookupErr, sql.ErrNoRows) {
					// A missing host row needs write preflight first. Source
					// set creates it even if the warm value is already equal.
					return p.saveOriginalCloneFolderTx(ctx, tx, org)
				}
				if lookupErr != nil {
					return nil, lookupErr
				}
				// Source still calls synchronous set for a predeclared ID:
				// publish a deepcopy, but equal cached value does not write SQL.
				p.publishOriginalFolders(org, false)
				if _, err := originalCloneFolderRowPresent(ctx, tx); err != nil {
					return nil, err
				}
			}
			return nil, nil
		}
		if !appendable {
			return nil, &originalCloneFolderValueError{cause: bad("INVALID_PLUGIN_FOLDER", "selected folder membership has no append operation")}
		}
		encodedTarget, _ := json.Marshal(target)
		members = append(members, encodedTarget)
		list, e := json.Marshal(members)
		if e != nil {
			return nil, e
		}
		if bytes.TrimSpace(value)[0] == '[' {
			org.Folders[name] = list
		} else {
			var object map[string]json.RawMessage
			if e = json.Unmarshal(value, &object); e != nil {
				return nil, e
			}
			object["plugins"] = list
			org.Folders[name], e = json.Marshal(object)
			if e != nil {
				return nil, e
			}
		}
		if sourceCache {
			if info != nil {
				info("clone_folder_added", "已将分身插件 "+target+" 添加到文件夹 '"+name+"' 中")
			}
			return p.saveOriginalCloneFolderTx(ctx, tx, org)
		}
		saved, err := p.saveOrganizationTx(ctx, tx, org)
		return &saved, err
	}
	if sourceCache && info != nil {
		info("clone_folder_independent", "原插件 "+source+" 不在任何文件夹中，分身插件 "+target+" 将保持独立")
	}
	return nil, nil
}

// This source-only projection handles the JSON values already accepted by the
// ordinary folder API. It does not change the native parser or uninstall path.
type originalCloneFolderMembership struct {
	members    []json.RawMessage
	source     bool
	target     bool
	appendable bool
}

func readOriginalCloneFolderMembership(raw json.RawMessage, source, target string) (originalCloneFolderMembership, error) {
	var out originalCloneFolderMembership
	value := bytes.TrimSpace(raw)
	if len(value) == 0 {
		return out, bad("INVALID_PLUGIN_FOLDER", "invalid stored folder JSON")
	}
	if value[0] == '{' {
		var object map[string]json.RawMessage
		if err := json.Unmarshal(value, &object); err != nil {
			return out, err
		}
		var present bool
		value, present = object["plugins"]
		if !present {
			return out, nil
		}
		value = bytes.TrimSpace(value)
	} else if value[0] != '[' {
		// Source ignores outer scalars; only dict-with-plugins or list enters
		// membership. Invalid authenticated JSON is not a source shape error.
		if !json.Valid(value) {
			return out, bad("INVALID_PLUGIN_FOLDER", "invalid stored folder JSON")
		}
		return out, nil
	}
	if len(value) == 0 {
		return out, bad("INVALID_PLUGIN_FOLDER", "invalid stored folder JSON")
	}
	switch value[0] {
	case '[':
		if err := json.Unmarshal(value, &out.members); err != nil {
			return out, err
		}
		out.appendable = true
		for _, member := range out.members {
			out.source = out.source || originalFolderMemberIs(member, source)
			out.target = out.target || originalFolderMemberIs(member, target)
		}
	case '"':
		var text string
		if err := json.Unmarshal(value, &text); err != nil {
			return out, err
		}
		out.source, out.target = strings.Contains(text, source), strings.Contains(text, target)
	case '{':
		var object map[string]json.RawMessage
		if err := json.Unmarshal(value, &object); err != nil {
			return out, err
		}
		_, out.source = object[source]
		_, out.target = object[target]
	default:
		if !json.Valid(value) {
			return out, bad("INVALID_PLUGIN_FOLDER", "invalid stored folder JSON")
		}
		// null, boolean and number are not iterable in the original helper.
		return out, &originalCloneFolderValueError{cause: bad("INVALID_PLUGIN_FOLDER", "folder membership is not iterable")}
	}
	return out, nil
}

// Keep native validation/CAS/encryption/context preflight before publication.
// Only the final SQL write follows source synchronous cache-before-SQL ordering;
// the shared saveOrganizationTx and unrelated native organization API are intact.
func (p *PluginStore) saveOriginalCloneFolderTx(ctx context.Context, tx *sql.Tx, in PluginOrganization) (*PluginOrganization, error) {
	if err := validatePluginOrganization(in); err != nil {
		return nil, err
	}
	current, err := p.organization(ctx, tx)
	if err != nil {
		return nil, err
	}
	if current.Revision != in.Revision {
		return nil, bad("PLUGIN_REVISION_CONFLICT", "插件文件夹或顺序已在其他页面更改，请刷新后重试")
	}
	in.Revision++
	in.folderKeys = originalFolderKeys(in)
	raw, err := pluginJSON(struct {
		PluginOrganization
		OrderPresent bool     `json:"order_present"`
		FolderKeys   []string `json:"folder_keys"`
	}{in, in.orderPresent, in.folderKeys}, true)
	if err != nil {
		return nil, err
	}
	sealed, err := p.seal("organization", "admin", "main", raw)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	p.publishOriginalFolders(in, false)
	// This is the source set/get_by_key stage, distinct from the host's earlier
	// CAS/authentication read. An existing unchanged value skips only the write.
	if _, err = originalCloneFolderRowPresent(ctx, tx); err != nil {
		return nil, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO plugin_organization(owner_id,revision,sealed,updated_at) VALUES(1,?,?,?) ON CONFLICT(owner_id) DO UPDATE SET revision=excluded.revision,sealed=excluded.sealed,updated_at=excluded.updated_at`, in.Revision, sealed, now())
	if err != nil {
		return nil, &originalCloneFolderSQLError{cause: err}
	}
	return &in, nil
}

func (a *App) pluginCloneHTTP(w http.ResponseWriter, r *http.Request, source string) (any, error) {
	var request PluginCloneRequest
	if err := decodePluginBody(w, r, &request); err != nil {
		return map[string]any{"success": false, "message": "创建插件分身失败：" + err.Error()}, nil
	}
	// The original FastAPI endpoint is synchronous (def); client disconnect
	// does not cancel the already accepted method sequence.
	ctx := context.WithoutCancel(r.Context())
	a.pluginOriginalOperationMu.Lock()
	defer a.pluginOriginalOperationMu.Unlock()
	if !a.admitOriginalBackground() {
		return nil, &APIError{"APP_STOPPING", "服务正在停止", http.StatusServiceUnavailable}
	}
	defer a.wg.Done()
	if _, exists := a.pluginOriginalClasses.Load(source); !exists {
		return map[string]any{"success": false, "message": "原插件 " + source + " 不存在"}, nil
	}
	if e := validatePluginCloneRequest(request); e != nil {
		return map[string]any{"success": false, "message": e.Error()}, nil
	}
	target := source + plugins.OriginalPluginLower(request.Suffix)
	if !pluginStorageID.MatchString(target) {
		return map[string]any{"success": false, "message": "完整分身标识不是安全的Linux目录名，或超过255个UTF-8字节"}, nil
	}
	if retained, e := a.pluginStore.Installation(ctx, target); e == nil {
		present, e := a.originalCodeAliasPresent(ctx, retained)
		if e != nil {
			return map[string]any{"success": false, "message": "创建插件分身失败：" + e.Error()}, nil
		}
		if present {
			return map[string]any{"success": false, "message": "分身插件 " + target + " 已存在"}, nil
		}
	} else if !errors.Is(e, sql.ErrNoRows) {
		return map[string]any{"success": false, "message": "创建插件分身失败：" + e.Error()}, nil
	}
	sourceInstallation, lookupErr := a.pluginStore.Installation(ctx, source)
	if lookupErr != nil {
		return nil, lookupErr
	}
	implementation, lookupErr := a.resolvePluginImplementation(ctx, sourceInstallation)
	if lookupErr != nil {
		return nil, lookupErr
	}
	if implementation.External {
		if _, copyErr := artifacts.CloneInstalled(a.pluginPackageRoot(), source, target, func(m plugins.Manifest, c string) error {
			if m.PluginID != sourceInstallation.PluginID || m.Version != sourceInstallation.Version {
				return bad("PLUGIN_IMPLEMENTATION_MISMATCH", "来源代码版本不匹配")
			}
			return validatePluginArtifactContract(m, c)
		}); copyErr != nil {
			return map[string]any{"success": false, "message": copyErr.Error()}, nil
		}
	}
	if implementation.External {
		if _, deriveErr := artifacts.DeriveInstalledFrontend(ctx, a.pluginPackageRoot(), target, func(m plugins.Manifest, c string) error {
			if m.PluginID != sourceInstallation.PluginID || m.Version != sourceInstallation.Version {
				return bad("PLUGIN_IMPLEMENTATION_MISMATCH", "来源代码版本不匹配")
			}
			return validatePluginArtifactContract(m, c)
		}); deriveErr != nil {
			cleanupErr := os.RemoveAll(filepath.Join(a.pluginPackageRoot(), target))
			return map[string]any{"success": false, "message": errors.Join(deriveErr, cleanupErr).Error()}, nil
		}
	}

	cached, _, cacheErr := a.readOriginalBaseConfigForInit(ctx, source)
	if cacheErr != nil {
		return map[string]any{"success": false, "message": cacheErr.Error()}, nil
	}
	ctx = context.WithValue(ctx, originalCloneConfigKey{}, originalCloneConfiguration{source: source, raw: cached})
	in, err := a.pluginStore.cloneOriginalClass(ctx, source, request)
	if err != nil {
		return map[string]any{"success": false, "message": "创建插件分身失败：" + err.Error()}, nil
	}
	if err = a.writeOriginalCodeAlias(ctx, in, true); err != nil {
		return map[string]any{"success": false, "message": "创建插件分身失败：" + err.Error()}, nil
	}
	// Port the real initializer calls at the HTTP boundary, not trace hooks.
	// The enclosing stop/reload-event/register operations are implemented in
	// the original lifecycle adapter; do not use the removed lazy-write path.
	if err = a.initializeOriginalCloneCalls(ctx, in.InstanceID); err != nil {
		return map[string]any{"success": false, "message": "创建插件分身失败：" + err.Error()}, nil
	}
	if err = a.addOriginalCloneToFolder(ctx, source, in.InstanceID); err != nil {
		message, ordinary := originalCloneFolderFailure(ctx, err)
		if !ordinary {
			return map[string]any{"success": false, "message": "创建插件分身失败：" + err.Error()}, nil
		}
		// The source catches only this folder stage after clone/reload succeeded.
		// Diagnostics are classified and fixed; no SQL or private field values.
		a.log("ERROR", "plugins", "clone_folder_failed", message, "", "")
	}
	return map[string]any{"success": true, "message": "插件分身创建成功", "created": true, "instance_id": in.InstanceID, "plugin_id": in.PluginID, "source_instance_id": source}, nil
}

// These markers are created only by the original clone folder operation.
// An unrelated APIError/SQLite error must not turn an earlier failure into success.
type originalCloneFolderValueError struct{ cause error }

func (e *originalCloneFolderValueError) Error() string { return "invalid clone folder value" }
func (e *originalCloneFolderValueError) Unwrap() error { return e.cause }

type originalCloneFolderSQLError struct{ cause error }

func (e *originalCloneFolderSQLError) Error() string { return "clone folder SQL operation failed" }
func (e *originalCloneFolderSQLError) Unwrap() error { return e.cause }

func originalCloneFolderRowPresent(ctx context.Context, tx *sql.Tx) (bool, error) {
	var present int
	err := tx.QueryRowContext(ctx, `SELECT 1 FROM plugin_organization WHERE owner_id=1`).Scan(&present)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, &originalCloneFolderSQLError{cause: err}
	}
	return true, nil
}

func originalCloneFolderFailure(ctx context.Context, err error) (string, bool) {
	if ctx.Err() != nil || err == nil {
		return "", false
	}
	// Do not classify a joined ordinary/platform failure by finding just one
	// matching descendant. Only the direct private stage marker is accepted.
	switch failure := err.(type) {
	case *originalCloneFolderValueError:
		value, ok := failure.cause.(*APIError)
		if ok && value.Code == "INVALID_PLUGIN_FOLDER" {
			return "处理插件文件夹时出错：文件夹成员格式无效", true
		}
	case *originalCloneFolderSQLError:
		// Only concrete errors returned by the owned SQLite source-stage
		// lookup/write/commit are ordinary source storage exceptions. This is
		// not a generic permission/IO/tool-policy or joined-error exemption.
		value, ok := failure.cause.(*sqlite.Error)
		if ok {
			return "处理插件文件夹时出错：SQLite 错误（代码" + strconv.Itoa(value.Code()) + "）", true
		}
	}
	return "", false
}

// Return only after rollback and operation-lock release, before the caller
// classifies/logs an ordinary folder exception or reports the clone result.
func (a *App) addOriginalCloneToFolder(ctx context.Context, source, target string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	a.pluginStore.folders.operation.Lock()
	defer a.pluginStore.folders.operation.Unlock()
	tx, err := a.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	folderState, err := a.pluginStore.cloneOrganizationMemberCacheTx(ctx, tx, source, target, true, func(event, message string) {
		// a.log synchronously queues sanitized data only; it does not query
		// Store.DB or wait for the asynchronous writer while this Tx is held.
		a.log("INFO", "plugins", event, message, "", "")
	})
	if err != nil {
		return err
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return &originalCloneFolderSQLError{cause: err}
	}
	if folderState != nil {
		a.pluginStore.publishOriginalFolders(*folderState, true)
	}
	return nil
}

func (p *PluginStore) presentInstance(ctx context.Context, in PluginInstallation, manifest plugins.Manifest) (pluginCard, error) {
	card := pluginCard{Manifest: manifest, InstanceID: in.InstanceID, Installed: true}
	metadata, err := p.instanceMetadata(ctx, p.store.DB, in.InstanceID)
	if err != nil {
		return card, err
	}
	if metadata.SourceInstanceID != "" {
		card.Name = metadata.Name
		card.Description = metadata.Description
		card.Icon = metadata.Icon
		card.DisplayVersion = metadata.DisplayVersion
		card.SourceInstanceID = metadata.SourceInstanceID
	}
	return card, nil
}

// Existing plugin-ID references are compatibility references, not a request to
// pick every instance of that implementation. Explicitly registered clones are
// never silently selected as a replacement provider, even if the source has
// been uninstalled. Legacy unidentified duplicates retain their ambiguity error.
func (p *PluginStore) defaultPluginReferenceCandidate(ctx context.Context, q pluginOrganizationReader, instance string) (bool, error) {
	metadata, err := p.instanceMetadata(ctx, q, instance)
	return metadata.SourceInstanceID == "", err
}

type originalCloneConfigKey struct{}
type originalCloneConfiguration struct {
	source string
	raw    json.RawMessage
}
