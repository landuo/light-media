package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"light-media/internal/plugins/builtin"
)

// The original manager may explicitly load a class whose code remains on disk
// even though it is absent from UserInstalledPlugins. The existing Go invocation
// uses the exact in-memory init call binding rather than modifying that list.
func originalInitializationMatches(ctx context.Context, instance, plugin, version string, generation int64, action string) bool {
	call, ok := ctx.Value(originalInitializingObjectKey{}).(*originalInitializationCall)
	if !ok || call == nil || call.object == nil || action != "init" {
		return false
	}
	in := call.object.installation
	return in.InstanceID == instance && in.PluginID == plugin && in.Version == version && in.Generation == generation
}

func (p *PluginStore) originalInitializationRunBound(run PluginRun) bool {
	value, ok := p.originalInitializingRuns.Load(run.ID)
	if !ok {
		return false
	}
	call, ok := value.(*originalInitializationCall)
	if !ok || call == nil || call.object == nil || run.Action != "init" {
		return false
	}
	in := call.object.installation
	return in.InstanceID == run.InstanceID && in.Version == run.Version && in.Generation == run.Generation
}

// A compiled primary plugin is the Go counterpart of an on-disk original
// package. Its encrypted host namespace is not the UserInstalledPlugins list.
// Create storage only after an explicit load, with installed=false in the same
// transaction. No default config or business initialization is fabricated here.
func (a *App) originalDirectoryInstallation(ctx context.Context, instance string) (PluginInstallation, error) {
	in, err := a.pluginStore.Installation(ctx, instance)
	if !errors.Is(err, sql.ErrNoRows) {
		if err != nil {
			return in, err
		}
		present, e := a.originalCodeAliasPresent(ctx, in)
		if e != nil {
			return in, e
		}
		if !present {
			return in, sql.ErrNoRows
		}
		return in, nil
	}
	manifest, _, exists := builtin.Lookup(instance)
	if !exists || manifest.PluginID != instance {
		return in, sql.ErrNoRows
	}
	raw, err := json.Marshal(manifest)
	if err != nil {
		return in, err
	}
	in, err = validatePluginInstallation(PluginInstallation{InstanceID: instance, PluginID: manifest.PluginID, Version: manifest.Version, ManifestSchemaVersion: manifest.SchemaVersion, Manifest: raw})
	if err != nil {
		return in, err
	}
	tx, err := a.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		return in, err
	}
	defer tx.Rollback()
	if err = a.pluginStore.installTx(ctx, tx, in); err != nil {
		return in, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE plugin_installations SET installed=0,desired_enabled=1 WHERE instance_id=?`, instance); err != nil {
		return in, err
	}
	if err = tx.Commit(); err != nil {
		return in, err
	}
	return a.pluginStore.Installation(ctx, instance)
}

// originalPluginClass corresponds to _plugins, not _running_plugins. isClone
// is copied from the authenticated generated class metadata at import time.
type originalPluginClass struct {
	installation PluginInstallation
	isClone      bool
}

const originalPluginAliasKind = "plugin-original-code-alias-v1"

type originalPluginCodeAlias struct {
	Instance string `json:"instance"`
	Plugin   string `json:"plugin"`
	Present  bool   `json:"present"`
}

// A descriptor is the compiled-language equivalent of a copied Python package
// directory. It contains no config, history, installed-list or running state.
func (a *App) writeOriginalCodeAlias(ctx context.Context, in PluginInstallation, present bool) error {
	raw, err := json.Marshal(originalPluginCodeAlias{in.InstanceID, in.PluginID, present})
	if err != nil {
		return err
	}
	sealed, err := a.pluginStore.seal(originalPluginAliasKind, in.InstanceID, "main", raw)
	if err != nil {
		return err
	}
	_, err = a.Store.DB.ExecContext(ctx, `INSERT INTO records(kind,id,payload,updated_at) VALUES(?,?,?,?) ON CONFLICT(kind,id) DO UPDATE SET payload=excluded.payload,updated_at=excluded.updated_at`, originalPluginAliasKind, in.InstanceID, sealed, now())
	return err
}
func (a *App) originalCodeAliasPresent(ctx context.Context, in PluginInstallation) (bool, error) {
	if in.InstanceID == in.PluginID {
		return true, nil
	}
	meta, err := a.pluginStore.instanceMetadata(ctx, a.Store.DB, in.InstanceID)
	if err != nil {
		return false, err
	}
	if meta.SourceInstanceID == "" {
		return false, nil
	}
	var sealed []byte
	err = a.Store.DB.QueryRowContext(ctx, `SELECT payload FROM records WHERE kind=? AND id=?`, originalPluginAliasKind, in.InstanceID).Scan(&sealed)
	if errors.Is(err, sql.ErrNoRows) {
		if !in.Installed {
			return false, nil
		}
		return true, a.writeOriginalCodeAlias(ctx, in, true)
	}
	if err != nil {
		return false, err
	}
	raw, err := a.pluginStore.unseal(originalPluginAliasKind, in.InstanceID, "main", sealed)
	if err != nil {
		return false, err
	}
	var alias originalPluginCodeAlias
	if json.Unmarshal(raw, &alias) != nil || alias.Instance != in.InstanceID || alias.Plugin != in.PluginID {
		return false, bad("PLUGIN_INSTANCE_METADATA_INVALID", "插件代码别名无法读取")
	}
	return alias.Present, nil
}
