package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	"light-media/internal/plugins"
	"light-media/internal/plugins/artifacts"
)

func originalPluginDockerMode() bool {
	_, err := os.Stat("/.dockerenv")
	return err == nil
}

func (a *App) pluginCodeBackupRoot() string { return filepath.Join(a.Dir, "plugin-code-backup") }

// Lifecycle entrypoints do not expose database backup or restoration APIs.
// Start calls restoration before initializing plugin objects; Close holds the
// original operation lock while backing up before stopping those objects.
func (a *App) restoreOriginalPluginCode(ctx context.Context) error {
	if !originalPluginDockerMode() {
		return nil
	}
	return a.restoreOriginalPluginCodeFiles(ctx)
}

func (a *App) backupOriginalPluginCode(ctx context.Context) error {
	if !originalPluginDockerMode() {
		return nil
	}
	return a.backupOriginalPluginCodeFiles(ctx)
}

func (a *App) originalPluginBackupValidator(ctx context.Context, id string) (func(plugins.Manifest, string) error, error) {
	in, err := a.pluginStore.Installation(ctx, id)
	if err != nil {
		return nil, err
	}
	return func(m plugins.Manifest, contract string) error {
		if m.PluginID != in.PluginID || m.Version != in.Version {
			return errors.New("backup code does not match persisted instance identity/version")
		}
		return validatePluginArtifactContract(m, contract)
	}, nil
}

func (a *App) backupOriginalPluginCodeFiles(ctx context.Context) error {
	a.pluginImplementationMu.Lock()
	defer a.pluginImplementationMu.Unlock()
	entries, err := os.ReadDir(a.pluginPackageRoot())
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var failures []error
	for _, entry := range entries {
		if !pluginStorageID.MatchString(entry.Name()) {
			continue
		}
		if err = ctx.Err(); err != nil {
			return errors.Join(append(failures, err)...)
		}
		validate, readErr := a.originalPluginBackupValidator(ctx, entry.Name())
		if readErr == nil {
			readErr = artifacts.BackupInstalled(a.pluginPackageRoot(), a.pluginCodeBackupRoot(), entry.Name(), false, validate)
		}
		if readErr != nil {
			failures = append(failures, readErr)
		}
	}
	return errors.Join(failures...)
}

func (a *App) restoreOriginalPluginCodeFiles(ctx context.Context) error {
	a.pluginImplementationMu.Lock()
	defer a.pluginImplementationMu.Unlock()
	entries, err := os.ReadDir(a.pluginCodeBackupRoot())
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var failures []error
	for _, entry := range entries {
		if !pluginStorageID.MatchString(entry.Name()) {
			continue
		}
		if err = ctx.Err(); err != nil {
			return errors.Join(append(failures, err)...)
		}
		// Existing persistent code is authoritative and must not be replaced by
		// an older shutdown snapshot. Its normal resolver still validates bytes.
		if _, statErr := os.Lstat(filepath.Join(a.pluginPackageRoot(), entry.Name())); statErr == nil {
			continue
		} else if !os.IsNotExist(statErr) {
			failures = append(failures, statErr)
			continue
		}
		validate, readErr := a.originalPluginBackupValidator(ctx, entry.Name())
		if readErr == nil {
			_, readErr = artifacts.RestoreMissingInstalled(a.pluginPackageRoot(), a.pluginCodeBackupRoot(), entry.Name(), validate)
		}
		if readErr != nil {
			failures = append(failures, readErr)
		}
	}
	// SystemChain.restore_plugins consumes the startup snapshot even when no
	// image reset occurred, and after isolated per-plugin restore failures.
	// Runtime code is persistent here: existing packages remain authoritative;
	// only missing copies need restoration before manager initialization.
	if err := os.RemoveAll(a.pluginCodeBackupRoot()); err != nil {
		failures = append(failures, err)
	}
	return errors.Join(failures...)
}
