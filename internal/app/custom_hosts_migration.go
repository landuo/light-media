package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"light-media/internal/plugins/builtin"
)

type reviewedCustomHostsSnapshot struct {
	Version         int    `json:"version"`
	SourceContainer string `json:"source_container"`
	SourceSHA       string `json:"source_sha256"`
	Config          string `json:"config"`
	InstalledCount  int    `json:"installed_count"`
	KVCount         int    `json:"kv_count"`
	FileCount       int    `json:"file_count"`
	SymlinkCount    int    `json:"symlink_count"`
}

const reviewedCustomHostsConfigSHA = "e304545981b55e031afcdbedd617cd778b15d76224f6b8d63128df8f7aa09463"

func invalidReviewedCustomHosts() error {
	return errors.New("invalid_reviewed_custom_hosts_snapshot")
}
func validateReviewedCustomHosts(raw []byte) (reviewedCustomHostsSnapshot, error) {
	var in reviewedCustomHostsSnapshot
	if len(raw) > 1<<20 || sourceSettingsJSON(raw, &in) != nil {
		return in, invalidReviewedCustomHosts()
	}
	if _, e := sourceSettingsObject(raw, "version", "source_container", "source_sha256", "config", "installed_count", "kv_count", "file_count", "symlink_count"); e != nil {
		return in, invalidReviewedCustomHosts()
	}
	var fields map[string]json.RawMessage
	if sourceSettingsJSON(raw, &fields) != nil {
		return in, invalidReviewedCustomHosts()
	}
	for _, value := range fields {
		if bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return in, invalidReviewedCustomHosts()
		}
	}
	if in.Version != 1 || in.SourceContainer != "cb82e3f550881334ef3814426016d08d909bccd88d3297522756ba31654d129a" || in.SourceSHA != "0d6561baa5fdeddac87039718e10648c6df2deed52bbe6397f807cf3862560ca" || in.InstalledCount != 1 || in.KVCount != 0 || in.FileCount != 0 || in.SymlinkCount != 0 || sourceSettingsDigest([]byte(in.Config)) != reviewedCustomHostsConfigSHA {
		return in, invalidReviewedCustomHosts()
	}
	return in, nil
}

// ValidateReviewedCustomHostsInput opens no target, worker or network connection.
func ValidateReviewedCustomHostsInput(raw []byte) error {
	_, e := validateReviewedCustomHosts(raw)
	return e
}
func customHostsSnapshotImport(in reviewedCustomHostsSnapshot, runID string) PluginImport {
	manifest := builtin.CustomHostsManifest()
	raw, _ := json.Marshal(manifest)
	return PluginImport{RunID: runID, Installation: PluginInstallation{InstanceID: "CustomHosts", PluginID: "CustomHosts", Version: "1.2.1", ManifestSchemaVersion: 1, Manifest: raw}, Config: json.RawMessage(in.Config), reviewedCustomHosts: true}
}
func validReviewedCustomHostsImport(in PluginInstallation, config json.RawMessage, data map[string]json.RawMessage, absent bool) bool {
	if in.InstanceID != "CustomHosts" || in.PluginID != "CustomHosts" || in.Version != "1.2.1" || in.ManifestSchemaVersion != 1 || absent || len(data) != 0 || sourceSettingsDigest(config) != reviewedCustomHostsConfigSHA {
		return false
	}
	want, _ := json.Marshal(builtin.CustomHostsManifest())
	var gotValue, wantValue any
	if json.Unmarshal(in.Manifest, &gotValue) != nil || json.Unmarshal(want, &wantValue) != nil {
		return false
	}
	got, _ := json.Marshal(gotValue)
	expected, _ := json.Marshal(wantValue)
	return bytes.Equal(got, expected)
}

// ImportReviewedCustomHosts preserves the reviewed source enable flag without
// executing a worker. Startup subsequently applies it only to this application
// container. Identical receipt replay preserves later user edits.
func (p *PluginStore) ImportReviewedCustomHosts(ctx context.Context, runID string, raw []byte) (PluginMigrationReceipt, error) {
	in, e := validateReviewedCustomHosts(raw)
	if e != nil {
		return PluginMigrationReceipt{}, e
	}
	return p.Import(ctx, customHostsSnapshotImport(in, runID))
}
