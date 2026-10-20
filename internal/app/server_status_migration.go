package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"light-media/internal/plugins/serverstatus"
)

type reviewedServerStatusSnapshot struct {
	Version         int    `json:"version"`
	SourceContainer string `json:"source_container"`
	SourceSHA       string `json:"source_sha256"`
	Config          string `json:"config"`
	InstalledCount  int    `json:"installed_count"`
	KVCount         int    `json:"kv_count"`
	FileCount       int    `json:"file_count"`
	SymlinkCount    int    `json:"symlink_count"`
}

const reviewedServerStatusConfigSHA = "230cc6899bbbe3afa7e15808f8cdf4badc2afdec8366fa6bc5a4ea41aa526851"

func invalidReviewedServerStatus() error {
	return errors.New("invalid_reviewed_server_status_snapshot")
}
func validateReviewedServerStatus(raw []byte) (reviewedServerStatusSnapshot, error) {
	var in reviewedServerStatusSnapshot
	if len(raw) > 1<<20 || sourceSettingsJSON(raw, &in) != nil {
		return in, invalidReviewedServerStatus()
	}
	if _, e := sourceSettingsObject(raw, "version", "source_container", "source_sha256", "config", "installed_count", "kv_count", "file_count", "symlink_count"); e != nil {
		return in, invalidReviewedServerStatus()
	}
	var fields map[string]json.RawMessage
	if sourceSettingsJSON(raw, &fields) != nil {
		return in, invalidReviewedServerStatus()
	}
	for _, value := range fields {
		if bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return in, invalidReviewedServerStatus()
		}
	}
	if in.Version != 1 || in.SourceContainer != "cb82e3f550881334ef3814426016d08d909bccd88d3297522756ba31654d129a" || in.SourceSHA != "63583e9706aeee31a181b2fefa4e80c793ae4f2b9a76c9efea5956f8b0043d90" || in.InstalledCount != 1 || in.KVCount != 0 || in.FileCount != 0 || in.SymlinkCount != 0 || sourceSettingsDigest([]byte(in.Config)) != reviewedServerStatusConfigSHA {
		return in, invalidReviewedServerStatus()
	}
	return in, nil
}

// ValidateReviewedServerStatusInput opens no target, worker or network connection.
func ValidateReviewedServerStatusInput(raw []byte) error {
	_, e := validateReviewedServerStatus(raw)
	return e
}
func serverStatusSnapshotImport(in reviewedServerStatusSnapshot, runID string) PluginImport {
	manifest := serverstatus.Manifest()
	raw, _ := json.Marshal(manifest)
	return PluginImport{RunID: runID, Installation: PluginInstallation{InstanceID: "MPServerStatus", PluginID: "MPServerStatus", Version: "1.4", ManifestSchemaVersion: 1, Manifest: raw}, Config: json.RawMessage(in.Config), reviewedServerStatus: true}
}
func validReviewedServerStatusImport(in PluginInstallation, config json.RawMessage, data map[string]json.RawMessage, absent bool) bool {
	if in.InstanceID != "MPServerStatus" || in.PluginID != "MPServerStatus" || in.Version != "1.4" || in.ManifestSchemaVersion != 1 || absent || len(data) != 0 || sourceSettingsDigest(config) != reviewedServerStatusConfigSHA {
		return false
	}
	want, _ := json.Marshal(serverstatus.Manifest())
	var gotValue, wantValue any
	if json.Unmarshal(in.Manifest, &gotValue) != nil || json.Unmarshal(want, &wantValue) != nil {
		return false
	}
	got, _ := json.Marshal(gotValue)
	expected, _ := json.Marshal(wantValue)
	return bytes.Equal(got, expected)
}

// ImportReviewedServerStatus preserves the reviewed source enable flag without
// queuing work. Existing dashboard preferences remain untouched; they were
// migrated with SystemProcess. Identical receipt replay preserves later edits.
func (p *PluginStore) ImportReviewedServerStatus(ctx context.Context, runID string, raw []byte) (PluginMigrationReceipt, error) {
	in, e := validateReviewedServerStatus(raw)
	if e != nil {
		return PluginMigrationReceipt{}, e
	}
	return p.Import(ctx, serverStatusSnapshotImport(in, runID))
}
