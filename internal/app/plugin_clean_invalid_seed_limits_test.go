package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"testing"

	seedhost "light-media/internal/plugins/cleaninvalidseed/host"
)

func TestPluginCleanInvalidSeedPrivateJournalLimit(t *testing.T) {
	_, p, journal := cleanSeedJournalFixture(t)
	state := cleanSeedJournalState()
	// A single native snapshot can include every torrent/file from a selected
	// downloader. Its private row must survive above the public config limit.
	payload, err := json.Marshal(map[string]string{"inventory": strings.Repeat("synthetic-owned-inventory;", 60000)})
	if err != nil || len(payload) <= pluginValueLimit {
		t.Fatal("large owned fixture was not constructed", err)
	}
	effect := state.Effects["native-effect"]
	effect.NativeSnapshot = payload
	state.Effects[effect.ID] = effect
	if err = journal.Update(context.Background(), func(_ *sql.Tx, current *seedhost.State) error {
		*current = state
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	again, err := journal.Read(context.Background())
	if err != nil || !bytes.Equal(again.Effects[effect.ID].NativeSnapshot, payload) {
		t.Fatal("private native inventory could not be recovered", err)
	}
	for _, kind := range []string{"config", "unrecognized-private-kind"} {
		sealed, err := p.seal(kind, journal.instance, "owned", payload)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := p.unseal(kind, journal.instance, "owned", sealed); err == nil {
			t.Fatal("private journal allowance expanded another value kind", kind)
		}
	}
	if pluginCleanInvalidSeedJournalLimit != 32<<20 {
		t.Fatal("unreviewed private journal size policy")
	}
}
