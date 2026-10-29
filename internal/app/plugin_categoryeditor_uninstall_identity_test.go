package app

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"light-media/internal/plugins/builtin"
)

func TestCategoryEditorLegacyAliasUninstallDoesNotDeletePackagePaths(t *testing.T) {
	// Synthetic negative control for the preserved compiled-alias branch, not
	// evidence of an adapted native business worker (none is loaded or run).
	a := newTestApp(t)
	ctx := context.Background()
	manifest, _, found := builtin.Lookup("CategoryEditor")
	if !found {
		t.Fatal("legacy catalog control missing")
	}
	raw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	in := PluginInstallation{InstanceID: "CategoryEditorlegacyalias", PluginID: "CategoryEditor", Version: manifest.Version, ManifestSchemaVersion: 1, Manifest: raw}
	if err = a.pluginStore.Install(ctx, in); err != nil {
		t.Fatal(err)
	}
	in, err = a.pluginStore.Installation(ctx, in.InstanceID)
	if err != nil {
		t.Fatal(err)
	}
	a.storeOriginalPluginClass(in.InstanceID, originalPluginClass{installation: in, isClone: true, external: false})
	for _, id := range []string{in.InstanceID, "CategoryEditor", "CategoryEditorother"} {
		path := filepath.Join(a.pluginPackageRoot(), id)
		if err = os.MkdirAll(path, 0700); err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(filepath.Join(path, "owned-marker"), []byte(id), 0600); err != nil {
			t.Fatal(err)
		}
	}
	request := httptest.NewRequest("DELETE", "/api/plugins/"+in.InstanceID, nil)
	if _, err = a.originalPluginUninstallHTTP(request, in.InstanceID); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{in.InstanceID, "CategoryEditor", "CategoryEditorother"} {
		got, readErr := os.ReadFile(filepath.Join(a.pluginPackageRoot(), id, "owned-marker"))
		if readErr != nil || string(got) != id {
			t.Fatalf("compiled alias changed package path %s: %q %v", id, got, readErr)
		}
	}
}
