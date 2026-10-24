package app

import (
	"context"
	"encoding/json"
	"light-media/internal/plugins"
	"testing"
)

func installCloneSource(t *testing.T, p *PluginStore, m plugins.Manifest) {
	t.Helper()
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if err = p.Install(context.Background(), PluginInstallation{InstanceID: m.PluginID, PluginID: m.PluginID, Version: m.Version, ManifestSchemaVersion: m.SchemaVersion, Manifest: raw, DesiredEnabled: true}); err != nil {
		t.Fatal(err)
	}
}
