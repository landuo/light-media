package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"light-media/internal/plugins"
	"light-media/internal/plugins/artifacts"
	"net/http"
	"reflect"
	"strconv"
	"strings"
)

func originalPluginInstallRoute(path, method string) bool {
	parts := strings.Split(path, "/")
	return method == http.MethodPost && path == "install" || method == http.MethodGet && len(parts) == 2 && parts[0] == "install" && pluginStorageID.MatchString(parts[1])
}

// The existing-code branch of the original install endpoint. Repository code
// acquisition is deliberately not faked by changing the stored version label.
// Only a verified standard package can be started; legacy native adapters are unavailable.
func (a *App) originalPluginInstallHTTP(w http.ResponseWriter, r *http.Request, path string) (any, error) {
	var input struct {
		PluginID       string `json:"plugin_id"`
		Repository     string `json:"repo_url"`
		ReleaseVersion string `json:"release_version"`
		Force          bool   `json:"force"`
	}
	if r.Method == http.MethodPost {
		if err := decodePluginBody(w, r, &input); err != nil {
			return nil, err
		}
	} else {
		input.PluginID = strings.Split(path, "/")[1]
		input.Repository = r.URL.Query().Get("repo_url")
		input.ReleaseVersion = r.URL.Query().Get("release_version")
		if raw := r.URL.Query().Get("force"); raw != "" {
			value, err := strconv.ParseBool(raw)
			if err != nil {
				return nil, bad("INVALID_PLUGIN_JSON", "force必须为布尔值")
			}
			input.Force = value
		}
	}
	if !pluginStorageID.MatchString(input.PluginID) {
		return nil, bad("INVALID_PLUGIN_ID", "插件标识无效")
	}
	ctx := context.WithoutCancel(r.Context())
	a.pluginOriginalOperationMu.Lock()
	defer a.pluginOriginalOperationMu.Unlock()
	ctx, releaseOperationOwner := a.bindOriginalOperationOwner(ctx)
	defer releaseOperationOwner()
	if !a.admitOriginalBackground() {
		return nil, &APIError{"APP_STOPPING", "服务正在停止", http.StatusServiceUnavailable}
	}
	defer a.wg.Done()
	_, hasClass := a.pluginOriginalClasses.Load(input.PluginID)
	if input.Force || !hasClass {
		if input.Repository == "" {
			return map[string]any{"success": false, "message": "没有传入仓库地址，无法正确安装插件，请检查配置"}, nil
		}
		if acquireErr := a.acquireOriginalImplementation(ctx, input.PluginID, input.Repository, input.ReleaseVersion, input.Force); acquireErr != nil {
			return map[string]any{"success": false, "message": "插件制品获取失败: " + acquireErr.Error()}, nil
		}
		a.reportOriginalPluginInstallation(ctx, input.PluginID, input.Repository)

		if _, reloadErr := a.reloadOriginalPlugin(ctx, input.PluginID); reloadErr != nil {
			return nil, reloadErr
		}
		a.registerOriginalPlugin(ctx, input.PluginID)
		return map[string]any{"success": true}, nil
	}
	if input.Repository != "" {
		client, clientErr := a.originalPluginHTTPClient(0, true)
		if clientErr != nil {
			return map[string]any{"success": false, "message": "插件仓库代理配置无效"}, nil
		}
		defer client.CloseIdleConnections()
		if _, inspectErr := artifacts.InspectCompatible(ctx, artifacts.Request{RepositoryURL: input.Repository, LocalRoots: a.Settings().PluginLocalRepoPaths, PluginID: input.PluginID, Validate: validatePluginArtifactContract}, client); inspectErr != nil {
			return map[string]any{"success": false, "message": "插件兼容性检查失败: " + inspectErr.Error()}, nil
		}
	}
	// release_version is not consumed by the original already-present-class,
	// non-force branch. Configuration is neither synthesized nor default-filled.
	a.reportOriginalPluginInstallation(ctx, input.PluginID, input.Repository)
	if err := a.setOriginalPluginInstalled(ctx, input.PluginID, true); err != nil {
		return nil, err
	}
	if _, err := a.reloadOriginalPlugin(ctx, input.PluginID); err != nil {
		return nil, err
	}
	a.registerOriginalPlugin(ctx, input.PluginID)
	return map[string]any{"success": true}, nil
}

// Only the owned code publication and its trusted version binding are serialized
// against active instance calls. Reload runs after releasing this lock.
func (a *App) acquireOriginalImplementation(ctx context.Context, id, repository, release string, force bool) error {
	_, err := a.acquireOriginalImplementationMode(ctx, id, repository, release, force, false, false)
	return err
}
func (a *App) acquireOriginalImplementationForLocalWatch(ctx context.Context, id, repository string) (bool, error) {
	return a.acquireOriginalImplementationMode(ctx, id, repository, "", false, a.pluginDeveloperMode && artifacts.IsLocalRepository(repository), true)
}
func (a *App) acquireOriginalImplementationMode(ctx context.Context, id, repository, release string, force, developerLocalWatch, localWatch bool) (bool, error) {
	a.pluginImplementationMu.Lock()
	defer a.pluginImplementationMu.Unlock()
	// PluginHelper.install routes local URLs to install_local before examining
	// release_version. A historical release query cannot change that candidate.
	if artifacts.IsLocalRepository(repository) {
		release = ""
	}

	previous, readErr := a.pluginStore.Installation(ctx, id)
	var cloneMeta PluginInstanceMetadata
	if readErr == nil {
		cloneMeta, readErr = a.pluginStore.instanceMetadata(ctx, a.Store.DB, id)
		if readErr != nil {
			return true, readErr
		}
		if previous.PluginID != id && cloneMeta.SourceInstanceID == "" {
			return true, bad("PLUGIN_INSTANCE_CONFLICT", "只有已校验来源的分身可以安装其精确标识制品")
		}
	} else if !errors.Is(readErr, sql.ErrNoRows) {
		return true, readErr
	}

	var beforeCode artifacts.Acquired
	var beforeErr error
	if localWatch {
		beforeCode, beforeErr = artifacts.ReadInstalled(a.pluginPackageRoot(), id, validatePluginArtifactContract)
	}
	client, err := a.originalPluginHTTPClient(0, true)
	if err != nil {
		return true, bad("INVALID_PROXY", "插件仓库代理配置无效")
	}
	defer client.CloseIdleConnections()
	acquired, err := artifacts.Acquire(ctx, artifacts.Request{PreserveUnchangedRuntime: localWatch, DeveloperLocalWatch: developerLocalWatch, Root: a.pluginPackageRoot(), RepositoryURL: repository, LocalRoots: a.Settings().PluginLocalRepoPaths, PluginID: id, ReleaseVersion: release, Force: force, RefreshBackup: originalPluginDockerMode(), BackupRoot: a.pluginCodeBackupRoot(), Validate: validatePluginArtifactContract}, client)
	if err != nil {
		return true, err
	}
	if acquired.Manifest.Frontend != nil {
		runtimePreserved, backupWarning := acquired.RuntimePreserved, acquired.BackupWarning
		acquired, err = artifacts.DeriveInstalledFrontend(ctx, a.pluginPackageRoot(), id, validatePluginArtifactContract)
		if err != nil {
			return true, err
		}
		acquired.RuntimePreserved, acquired.BackupWarning = runtimePreserved, backupWarning
	}
	if acquired.BackupWarning != "" {
		a.log("WARN", "plugins", "code_backup_failed", "插件代码已安装，但持久代码备份刷新失败", "", "")
	}
	raw, err := json.Marshal(acquired.Manifest)
	if err != nil {
		return true, err
	}
	if localWatch && acquired.RuntimePreserved && previous.Installed && beforeErr == nil && beforeCode.SHA256 == acquired.SHA256 && beforeCode.HostContract == acquired.HostContract {
		previousManifest, err := json.Marshal(beforeCode.Manifest)
		if err == nil && bytes.Equal(previousManifest, raw) {
			retain := true
			if value, exists := a.pluginRunningInstances.Load(id); exists {
				object := value.(*originalPluginInstance)
				retain = object.installation.Generation == previous.Generation && reflect.DeepEqual(object.manifest, acquired.Manifest)
				if retain {
					object.setFrontendPackageHash(acquired.PackageSHA256)
				}
			}
			// Fixed federated/static changes synchronize resources without replacing
			// the backend object. Verified executable + declaration identity is stable;
			// keep its DB generation, RPC authority, callbacks, config and data intact.
			if retain {
				return false, nil
			}
		}
	}
	in, err := validatePluginInstallation(PluginInstallation{InstanceID: id, PluginID: acquired.Manifest.PluginID, Version: acquired.Manifest.Version, ManifestSchemaVersion: acquired.Manifest.SchemaVersion, Manifest: raw, DesiredEnabled: true})
	if err != nil {
		return true, err
	}
	if cloneMeta.SourceInstanceID != "" {
		return true, a.installOriginalCloneArtifact(ctx, in, previous, cloneMeta)
	}
	return true, a.pluginStore.Install(ctx, in)
}

// A copied Go clone initially retains its source worker's business identity.
// Only an authenticated clone may transition to a release explicitly declaring
// its exact instance ID. Keep host-owned provenance and instance data; ordinary
// Store.Install still rejects cross-plugin takeover.
func (a *App) installOriginalCloneArtifact(ctx context.Context, in, previous PluginInstallation, metadata PluginInstanceMetadata) error {
	if in.InstanceID != previous.InstanceID || in.PluginID != in.InstanceID || metadata.SourceInstanceID == "" {
		return bad("PLUGIN_INSTANCE_CONFLICT", "分身制品必须声明精确的目标实例标识")
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(in.Manifest, &fields); err != nil {
		return err
	}
	marker, err := json.Marshal(pluginInstanceMarker{Purpose: "instance-clone/v1", InstanceID: in.InstanceID, SourceInstanceID: metadata.SourceInstanceID})
	if err != nil {
		return err
	}
	fields["host_instance"] = marker
	in.Manifest, err = json.Marshal(fields)
	if err != nil {
		return err
	}
	tx, err := a.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	actual, err := a.pluginStore.instanceMetadata(ctx, tx, in.InstanceID)
	if err != nil {
		return err
	}
	if actual.SourceInstanceID != metadata.SourceInstanceID {
		return bad("PLUGIN_INSTANCE_CONFLICT", "分身来源已发生变化")
	}
	result, err := tx.ExecContext(ctx, `UPDATE plugin_installations SET plugin_id=? WHERE instance_id=? AND plugin_id=? AND generation=?`, in.PluginID, in.InstanceID, previous.PluginID, previous.Generation)
	if err != nil {
		return err
	}
	if count, err := result.RowsAffected(); err != nil || count != 1 {
		return bad("PLUGIN_INSTANCE_CONFLICT", "分身安装记录已发生变化")
	}
	if err = a.pluginStore.installTx(ctx, tx, in); err != nil {
		return err
	}
	// A release for the exact clone ID replaces the authored class attributes,
	// just as replacing its source file does. Keep only host-owned provenance;
	// the executable identity and version remain bound to the verified package.
	var manifest plugins.Manifest
	if err = json.Unmarshal(in.Manifest, &manifest); err != nil {
		return err
	}
	metadata.Name, metadata.Description = manifest.Name, manifest.Description
	metadata.DisplayVersion, metadata.Icon = manifest.Version, manifest.Icon
	metadataRaw, err := json.Marshal(metadata)
	if err != nil {
		return err
	}
	metadataSealed, err := a.pluginStore.seal("instance_metadata", in.InstanceID, "main", metadataRaw)
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE plugin_instance_metadata SET sealed=? WHERE instance_id=?`, metadataSealed, in.InstanceID); err != nil {
		return err
	}
	aliasRaw, err := json.Marshal(originalPluginCodeAlias{Instance: in.InstanceID, Plugin: in.PluginID, Present: true})
	if err != nil {
		return err
	}
	aliasSealed, err := a.pluginStore.seal(originalPluginAliasKind, in.InstanceID, "main", aliasRaw)
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO records(kind,id,payload,updated_at) VALUES(?,?,?,?) ON CONFLICT(kind,id) DO UPDATE SET payload=excluded.payload,updated_at=excluded.updated_at`, originalPluginAliasKind, in.InstanceID, aliasSealed, now()); err != nil {
		return err
	}
	return tx.Commit()
}
