package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"light-media/internal/plugins/artifacts"
)

// The original manager may explicitly load a class whose code remains on disk
// even though it is absent from UserInstalledPlugins. The existing Go invocation
// uses the exact in-memory init call binding rather than modifying that list.

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
	acquired, readErr := artifacts.ReadInstalled(a.pluginPackageRoot(), instance, validatePluginArtifactContract)
	if readErr != nil {
		return in, readErr
	}
	manifest := acquired.Manifest
	if manifest.PluginID != instance {
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
	if err = a.pluginStore.updateOriginalInstalledOrderTx(ctx, tx, instance, false, true); err != nil {
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
	external     bool // verified implementation identity at class import, never catalog membership
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
	meta, err := a.pluginStore.instanceMetadata(ctx, a.Store.DB, in.InstanceID)
	if err != nil {
		return false, err
	}
	if meta.SourceInstanceID == "" {
		return in.InstanceID == in.PluginID, nil
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
