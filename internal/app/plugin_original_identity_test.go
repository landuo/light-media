package app

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

func TestLoadedPluginDisplayNamesReadOnlyCloneMetadata(t *testing.T) {
	a, cookie, source := ownedModuleFixture(t, true)
	clone := ownedModuleClone(t, a, cookie, "display")
	reader := a.Store.reader()
	reader.SetMaxOpenConns(1)
	conn, err := reader.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	// The live original's name is known even when the reader is occupied.
	ctx, cancel := context.WithTimeout(t.Context(), 250*time.Millisecond)
	name, err := source.originalDisplayName(ctx)
	cancel()
	conn.Close()
	if err != nil || name != source.manifest.Name {
		t.Fatal(name, err)
	}
	id := clone.installation.InstanceID
	metadata, err := a.pluginStore.instanceMetadata(t.Context(), reader, id)
	if err != nil {
		t.Fatal(err)
	}
	for _, updated := range []string{source.manifest.Name, "Renamed twin"} {
		metadata.Name = updated
		raw, err := json.Marshal(metadata)
		if err != nil {
			t.Fatal(err)
		}
		sealed, err := a.pluginStore.seal("instance_metadata", id, "main", raw)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := a.Store.DB.Exec("UPDATE plugin_instance_metadata SET sealed=? WHERE instance_id=?", sealed, id); err != nil {
			t.Fatal(err)
		}
		if got, err := clone.originalDisplayName(t.Context()); err != nil || got != updated {
			t.Fatalf("clone returned %q after rename to %q: %v", got, updated, err)
		}
	}
}
