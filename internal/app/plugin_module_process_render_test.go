package app

import (
	"database/sql"
	"path/filepath"
	"testing"
)

// A real failed config transaction on this gate's owned fixture. It cannot
// reject another plugin instance, app setting, or existing user database.
func setOwnedRenderConfigurationFailure(t *testing.T, dir string, fail bool) {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(dir, "light-media.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, operation := range []string{"INSERT", "UPDATE"} {
		name := "owned_render_config_failure_" + operation
		query := "DROP TRIGGER IF EXISTS " + name
		if fail {
			query = "CREATE TRIGGER IF NOT EXISTS " + name + " BEFORE " + operation + " ON plugin_values WHEN NEW.instance_id='OwnedRenderFixture' AND NEW.kind='config' BEGIN SELECT RAISE(ABORT,'owned render config rejection'); END"
		}
		if _, err = db.Exec(query); err != nil {
			t.Fatal("owned config failure control", err)
		}
	}
}
