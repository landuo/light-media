package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"

	"light-media/internal/plugins/builtin"
)

type reviewedSystemProcessSnapshot struct {
	Version         int               `json:"version"`
	SourceContainer string            `json:"source_container"`
	SourceSHA       string            `json:"source_sha256"`
	Config          string            `json:"config"`
	Dashboard       map[string]string `json:"dashboard"`
	InstalledCount  int               `json:"installed_count"`
	KVCount         int               `json:"kv_count"`
	FileCount       int               `json:"file_count"`
	SymlinkCount    int               `json:"symlink_count"`
	SourceUserCount int               `json:"source_user_count"`
}

var systemProcessDashboardSource = map[string]string{
	"Dashboard": "enabled", "DashboardOrder": "order", "DashboardGridLayout": "desktop", "DashboardGridLayoutTablet": "tablet", "DashboardGridLayoutMobile": "mobile",
}
var systemProcessReviewedHashes = map[string]string{
	"config":                    "5ca2d51d987c6dc6a876173c0c8d721bc11081df05411d7ca650f0a3d781f163",
	"Dashboard":                 "e7dfa1a8999d9f96844001f22ababe1fb67983d5f88a30b5c4562d2b765b7a77",
	"DashboardOrder":            "741d90384840ecb1d6fabaaf14c307c507cca2ed81226b9fa70056dd89452f25",
	"DashboardGridLayout":       "e1fbb481e14579f9382c8ceb810bf4ff55f750ac4781b25ee96b91c0ae2e8efc",
	"DashboardGridLayoutTablet": "ba35853a1d42ebdd2c3d5f9dd66e99c7f65d7b77d92ceff62bb719b6946f085b",
	"DashboardGridLayoutMobile": "54732d377bab629aa98d35b4666a4b8fe8ccc9b865e3ea95d97aaa6760be3cf1",
}

func systemProcessMigrationInvalid() error {
	return errors.New("invalid_reviewed_system_process_snapshot")
}

func validateReviewedSystemProcess(raw []byte) (reviewedSystemProcessSnapshot, error) {
	var in reviewedSystemProcessSnapshot
	if len(raw) > 1<<20 || sourceSettingsJSON(raw, &in) != nil {
		return in, systemProcessMigrationInvalid()
	}
	if _, e := sourceSettingsObject(raw, "version", "source_container", "source_sha256", "config", "dashboard", "installed_count", "kv_count", "file_count", "symlink_count", "source_user_count"); e != nil {
		return in, systemProcessMigrationInvalid()
	}
	if in.Version != 1 || in.SourceContainer != "cb82e3f550881334ef3814426016d08d909bccd88d3297522756ba31654d129a" || in.SourceSHA != "d0c8c6f1e034f46ce8b7d3c1bcd0cddc0e2d6455b2a6b35daa4b8002cd39f6ac" || in.InstalledCount != 1 || in.KVCount != 0 || in.FileCount != 0 || in.SymlinkCount != 0 || in.SourceUserCount != 1 || len(in.Dashboard) != 5 || sourceSettingsDigest([]byte(in.Config)) != systemProcessReviewedHashes["config"] {
		return in, systemProcessMigrationInvalid()
	}
	for key := range systemProcessDashboardSource {
		value, ok := in.Dashboard[key]
		if !ok || sourceSettingsDigest([]byte(value)) != systemProcessReviewedHashes[key] {
			return in, systemProcessMigrationInvalid()
		}
	}
	if _, e := systemProcessSnapshotImport(in, ""); e != nil {
		return in, e
	}
	return in, nil
}

// ValidateReviewedSystemProcessInput examines only the selected stdin payload.
// No target, worker, network connection or original application is opened.
func ValidateReviewedSystemProcessInput(raw []byte) error {
	_, e := validateReviewedSystemProcess(raw)
	return e
}

func systemProcessSnapshotImport(in reviewedSystemProcessSnapshot, runID string) (PluginImport, error) {
	manifest := builtin.SystemProcessManifest()
	raw, _ := json.Marshal(manifest)
	values := map[string]json.RawMessage{}
	for source, target := range systemProcessDashboardSource {
		value, exists := in.Dashboard[source]
		if !exists {
			return PluginImport{}, systemProcessMigrationInvalid()
		}
		values[target] = json.RawMessage(value)
	}
	installation := PluginInstallation{InstanceID: "SystemProcess", PluginID: "SystemProcess", Version: "1.1", ManifestSchemaVersion: 1, Manifest: raw}
	if !validSystemProcessDashboardImport(installation, values) {
		return PluginImport{}, systemProcessMigrationInvalid()
	}
	var fields map[string]json.RawMessage
	if sourceSettingsJSON([]byte(in.Config), &fields) != nil || fields == nil {
		return PluginImport{}, systemProcessMigrationInvalid()
	}
	return PluginImport{RunID: runID, Installation: installation, Config: json.RawMessage(in.Config), systemProcessDashboard: values}, nil
}

func validSystemProcessDashboardImport(in PluginInstallation, values map[string]json.RawMessage) bool {
	if in.PluginID != "SystemProcess" || in.InstanceID != "SystemProcess" || in.Version != "1.1" || in.ManifestSchemaVersion != 1 || len(values) != 5 {
		return false
	}
	want, _ := json.Marshal(builtin.SystemProcessManifest())
	var gotManifest, wantManifest any
	if json.Unmarshal(in.Manifest, &gotManifest) != nil || json.Unmarshal(want, &wantManifest) != nil {
		return false
	}
	gotCanonical, _ := json.Marshal(gotManifest)
	wantCanonical, _ := json.Marshal(wantManifest)
	if !bytes.Equal(gotCanonical, wantCanonical) {
		return false
	}
	for _, key := range systemProcessDashboardSource {
		raw := bytes.TrimSpace(values[key])
		var v any
		if len(raw) == 0 || sourceSettingsJSON(raw, &v) != nil {
			return false
		}
		if key == "order" {
			if raw[0] != '[' {
				return false
			}
		} else if raw[0] != '{' {
			return false
		}
	}
	return true
}

// ImportReviewedSystemProcess imports config, all five source user preferences
// and their receipt in the same transaction. The source files/KV are absent.
// Replaying a receipt never overwrites subsequent settings or creates a run.
func (p *PluginStore) ImportReviewedSystemProcess(ctx context.Context, runID string, raw []byte) (PluginMigrationReceipt, error) {
	in, e := validateReviewedSystemProcess(raw)
	if e != nil {
		return PluginMigrationReceipt{}, e
	}
	value, e := systemProcessSnapshotImport(in, runID)
	if e != nil {
		return PluginMigrationReceipt{}, e
	}
	return p.Import(ctx, value)
}
