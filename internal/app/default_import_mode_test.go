package app

import (
	"context"
	"testing"
)

func TestGlobalHardlinkDefaultKeepsExplicitDirectoryMode(t *testing.T) {
	a, source, target := importTestApp(t)
	c := a.Settings()
	c.AutoImportMode = "copy"
	c.Directories = []DirectoryRule{{
		ID: "explicit-copy", Name: "Explicit copy", SourcePath: source,
		LibraryPath: target, RootID: "root", MediaType: "mixed", Mode: "copy", MonitorType: "none",
	}}
	if err := a.saveSettings(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	saved := a.Settings()
	if saved.AutoImportMode != "hardlink" || saved.Directories[0].Mode != "copy" {
		t.Fatalf("global default changed an explicit directory choice: %q / %q", saved.AutoImportMode, saved.Directories[0].Mode)
	}
}

func TestLegacyGlobalModeLoadsAsHardlink(t *testing.T) {
	for _, oldMode := range []string{"", "copy", "auto"} {
		t.Run("legacy-"+oldMode, func(t *testing.T) {
			dir := t.TempDir()
			a, err := New(Options{DataDir: dir})
			if err != nil {
				t.Fatal(err)
			}
			if a.Settings().AutoImportMode != "hardlink" {
				t.Fatal("new installations must default to hardlink")
			}
			legacy := a.Settings()
			legacy.AutoImportMode = oldMode
			sealed, err := a.Store.Seal(legacy)
			if err == nil {
				_, err = a.Store.DB.Exec(`INSERT INTO records VALUES('config','main',?,?)`, sealed, now())
			}
			closeErr := a.Close()
			if err != nil || closeErr != nil {
				t.Fatal(err, closeErr)
			}
			loaded, err := New(Options{DataDir: dir})
			if err != nil {
				t.Fatal(err)
			}
			defer loaded.Close()
			if loaded.Settings().AutoImportMode != "hardlink" {
				t.Fatal("obsolete persisted global default remained active")
			}
		})
	}
}
