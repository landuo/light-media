package app

// Instance management belongs to the host, not individual plugin workers.
// Implementation identity remains immutable: presentation versions never select
// executable code, and clones get fresh owner journals and no queued invocation.
import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"unicode"

	"light-media/internal/plugins"
	"light-media/internal/plugins/builtin"
)

const pluginInstanceSchema = `CREATE TABLE IF NOT EXISTS plugin_instance_metadata(
 instance_id TEXT PRIMARY KEY,sealed BLOB NOT NULL,
 FOREIGN KEY(instance_id) REFERENCES plugin_installations(instance_id));`

var pluginCloneSuffix = regexp.MustCompile(`^[A-Za-z0-9]{1,20}$`)

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
		return bad("INVALID_PLUGIN_CLONE", "分身后缀须为1至20位字母或数字")
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

// Clone creates an isolated namespace atomically. A previously removed clone
// may reuse its authenticated source-bound ID only after cleanup and quiescence;
// installation allocates a new generation, never reviving the old attempt.
func (p *PluginStore) Clone(ctx context.Context, source string, request PluginCloneRequest) (PluginInstallation, error) {
	return p.cloneWithOriginalClass(ctx, source, request, false)
}

// originalClass means the actual manager already resolved its source class and
// target compiled directory. The installed-list is not an original clone gate.
func (p *PluginStore) cloneWithOriginalClass(ctx context.Context, source string, request PluginCloneRequest, originalClass bool) (PluginInstallation, error) {
	if err := p.checkPluginFormWrite(ctx, source, pluginFormWriteLifecycle); err != nil {
		return PluginInstallation{}, err
	}
	var out PluginInstallation
	if !pluginStorageID.MatchString(source) {
		return out, bad("INVALID_PLUGIN_CLONE", "来源实例编号无效")
	}
	if err := validatePluginCloneRequest(request); err != nil {
		return out, err
	}
	target := source + strings.ToLower(request.Suffix)
	if !pluginStorageID.MatchString(target) {
		return out, bad("INVALID_PLUGIN_CLONE", "分身编号超过长度限制")
	}
	if _, _, exists := builtin.Lookup(target); exists {
		return out, bad("PLUGIN_CLONE_CONFLICT", "分身编号与内置插件编号冲突")
	}
	tx, err := p.store.DB.BeginTx(ctx, nil)
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	var exists int
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM plugin_installations WHERE instance_id=?`, target).Scan(&exists); err != nil {
		return out, err
	}
	var sealed []byte
	if err = tx.QueryRowContext(ctx, `SELECT instance_id,plugin_id,version,manifest_schema,manifest,desired_enabled,installed,generation FROM plugin_installations WHERE instance_id=?`, source).Scan(&out.InstanceID, &out.PluginID, &out.Version, &out.ManifestSchemaVersion, &sealed, &out.DesiredEnabled, &out.Installed, &out.Generation); err != nil {
		return out, err
	}
	if !out.Installed && !originalClass {
		return out, bad("PLUGIN_NOT_INSTALLED", "来源插件已卸载")
	}
	manifest, _, available := builtin.Lookup(out.PluginID)
	if !available || manifest.Version != out.Version {
		return out, bad("PLUGIN_IMPLEMENTATION_UNAVAILABLE", "来源插件的当前实现不可用，不能创建分身")
	}
	out.Manifest, err = p.unseal("manifest", source, "main", sealed)
	if err != nil {
		return out, err
	}
	var storedManifest plugins.Manifest
	if json.Unmarshal(out.Manifest, &storedManifest) != nil || storedManifest.Validate() != nil || storedManifest.PluginID != out.PluginID || storedManifest.Version != out.Version {
		return out, bad("PLUGIN_INSTANCE_METADATA_INVALID", "来源实例与已认证实现元数据不一致")
	}
	if !originalClass {
		if _, err = p.pluginUpdateReviewRead(ctx, tx, out); err != nil {
			return out, err
		}
		if exists != 0 {
			if err = p.requireReusableCloneTx(ctx, tx, target, source, out.PluginID); err != nil {
				return out, err
			}
		}
		// Never relabel imported/unclaimed references as native through a clone.
		if err = p.requirePluginExecutionHandoffTx(ctx, tx, source); err != nil {
			return out, err
		}
		if siteReferencePlugin(out.PluginID) {
			protocol, e := p.readSiteReferenceProtocol(ctx, tx, out)
			if e != nil {
				return out, e
			}
			if protocol.Protocol != sourceSiteReferenceProtocol || (protocol.Source != "host-install/v1" && protocol.Source != "reset-empty/v1") {
				return out, bad("PLUGIN_CLONE_REFERENCE_MIGRATION_REQUIRED", "来源实例仍绑定专用迁移引用，需先完成可复制引用的转换")
			}
		}
		if isTorrentTransfer(out.PluginID) {
			root, _, e := p.transferRootReadTx(ctx, tx, source, out.Generation)
			if e != nil {
				return out, e
			}
			if root.ReferenceProtocol != "native-downloader-name/v1" {
				return out, bad("PLUGIN_CLONE_REFERENCE_MIGRATION_REQUIRED", "来源实例仍绑定专用下载器迁移引用")
			}
		}

	}
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
	var config map[string]json.RawMessage
	if json.Unmarshal(raw, &config) != nil {
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
	if len(raw) > pluginValueLimit && !brushFlowSnapshotOwner(out.PluginID, out.Version) {
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
	if err = p.installTx(ctx, tx, out); err != nil {
		return out, err
	}
	if copyConfig {
		if _, err = p.saveValueTx(ctx, tx, target, "config", "main", 0, raw); err != nil {
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
	// Return the actual target manifest, including its fresh lifecycle evidence,
	// rather than the caller/source copy prepared before installTx.
	out, err = p.pluginLifecycleInstallation(ctx, tx, target)
	if err != nil {
		return out, err
	}
	if err = tx.Commit(); err != nil {
		return out, err
	}
	return out, nil
}

func (p *PluginStore) cloneOrganizationMemberTx(ctx context.Context, tx *sql.Tx, source, target string) error {
	if err := p.checkPluginFormWrite(ctx, "", pluginFormWriteLifecycle); err != nil {
		return err
	}
	org, err := p.organization(ctx, tx)
	if err != nil {
		return err
	}
	for name, value := range org.Folders {
		members, e := pluginFolderMembers(value)
		if e != nil {
			return e
		}
		found := false
		for _, member := range members {
			if member == source {
				found = true
				break
			}
		}
		if !found {
			continue
		}
		members = append(members, target)
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
	a.pluginLifecycleMu.Lock()
	defer a.pluginLifecycleMu.Unlock()
	if _, exists := a.pluginOriginalClasses.Load(source); !exists {
		return map[string]any{"success": false, "message": "原插件 " + source + " 不存在"}, nil
	}
	target := source + strings.ToLower(request.Suffix)
	if known, _, exists := builtin.Lookup(target); exists && known.PluginID == target {
		return map[string]any{"success": false, "message": "分身插件 " + target + " 已存在"}, nil
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
	in, err := a.pluginStore.cloneWithOriginalClass(ctx, source, request, true)
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
