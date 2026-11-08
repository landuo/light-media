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
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"light-media/internal/plugins"
	"light-media/internal/plugins/artifacts"
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
	for _, field := range []struct {
		value string
		limit int
	}{{in.Name, 256}, {in.Description, 4096}, {in.Version, 64}, {in.Icon, 2048}} {
		if len(field.value) > field.limit || strings.ContainsFunc(field.value, unicode.IsControl) {
			return bad("INVALID_PLUGIN_CLONE", "分身信息过长或包含控制字符")
		}
	}
	if in.Version != "" && !pluginStorageVersion.MatchString(in.Version) {
		return bad("INVALID_PLUGIN_CLONE", "分身显示版本无效")
	}
	if in.Icon != "" {
		u, err := url.Parse(in.Icon)
		if err != nil || u.User != nil || (u.Scheme != "" && u.Scheme != "https" && u.Scheme != "http") || (u.Scheme != "" && u.Host == "") || strings.HasPrefix(in.Icon, "//") || strings.Contains(in.Icon, "\\") {
			return bad("INVALID_PLUGIN_CLONE", "分身图标须使用安全的相对资源或HTTP/HTTPS地址")
		}
	}
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
	observed, configSealed, err := p.readOriginalBaseConfigRow(ctx, tx, source)
	if err != nil {
		return out, err
	}
	if observed.present {
		raw, err = p.unsealBound("config", source, "main", configSealed, observed.valueLimit)
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
	if len(raw) > observed.valueLimit {
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
	metadata := PluginInstanceMetadata{SourceInstanceID: source, Name: strings.TrimSpace(request.Name), Description: request.Description, DisplayVersion: request.Version, Icon: request.Icon}
	if metadata.Name == "" {
		metadata.Name = manifest.Name
	}
	if metadata.Description == "" {
		metadata.Description = manifest.Description
	}
	if metadata.Icon == "" {
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
	org, err := p.organization(ctx, tx)
	if err != nil {
		return err
	}
	for _, name := range originalFolderKeys(org) {
		value := org.Folders[name]
		members, e := pluginFolderRawMembers(value)
		if e != nil {
			return e
		}
		found := false
		for _, member := range members {
			if originalFolderMemberIs(member, source) {
				found = true
				break
			}
		}
		if !found {
			continue
		}
		if name == "" {
			return nil
		}
		for _, member := range members {
			if originalFolderMemberIs(member, target) {
				return nil
			}
		}
		encodedTarget, _ := json.Marshal(target)
		members = append(members, encodedTarget)
		list, e := json.Marshal(members)
		if e != nil {
			return e
		}
		if bytes.TrimSpace(value)[0] == '[' {
			org.Folders[name] = list
		} else {
			var object map[string]json.RawMessage
			if e = json.Unmarshal(value, &object); e != nil {
				return e
			}
			object["plugins"] = list
			org.Folders[name], e = json.Marshal(object)
			if e != nil {
				return e
			}
		}
		_, err = p.saveOrganizationTx(ctx, tx, org)
		return err
	}
	return nil
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
	ctx, releaseOperationOwner := a.bindOriginalOperationOwner(ctx)
	defer releaseOperationOwner()
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
	tx, err := a.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		return map[string]any{"success": false, "message": "创建插件分身失败：" + err.Error()}, nil
	}
	defer tx.Rollback()
	if err = a.pluginStore.cloneOrganizationMemberTx(ctx, tx, source, in.InstanceID); err != nil {
		return map[string]any{"success": false, "message": "创建插件分身失败：" + err.Error()}, nil
	}
	if err = tx.Commit(); err != nil {
		return map[string]any{"success": false, "message": "创建插件分身失败：" + err.Error()}, nil
	}
	return map[string]any{"success": true, "message": "插件分身创建成功", "created": true, "instance_id": in.InstanceID, "plugin_id": in.PluginID, "source_instance_id": source}, nil
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
