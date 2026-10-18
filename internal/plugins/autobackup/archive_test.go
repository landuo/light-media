package autobackup

import (
	"archive/zip"
	"context"
	"encoding/base64"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

type fakeDump struct{ failure bool }

func (f fakeDump) Dump(_ context.Context, p string) error {
	if f.failure {
		return &DumpError{"dump_failed"}
	}
	return os.WriteFile(p, []byte("-- owned PostgreSQL dump\n"), 0644)
}
func TestInstalledArchiveOracleOnOwnedFilesystem(t *testing.T) {
	raw, e := os.ReadFile(originalFixture("reference/archive-oracle.json"))
	if e != nil {
		t.Fatal(e)
	}
	var fixture struct {
		Cases []struct {
			Kind      string            `json:"kind"`
			Result    any               `json:"result"`
			Error     any               `json:"error"`
			Files     map[string]string `json:"zip_files"`
			Modes     map[string]uint32 `json:"modes"`
			Remaining []string          `json:"remaining"`
		} `json:"cases"`
	}
	decode(t, raw, &fixture)
	if len(fixture.Cases) != 9 {
		t.Fatal(len(fixture.Cases))
	}
	dbBytes, e := base64.StdEncoding.DecodeString(fixture.Cases[0].Files["user.db"])
	if e != nil {
		t.Fatal(e)
	}
	for _, c := range fixture.Cases {
		t.Run(c.Kind, func(t *testing.T) {
			dir := t.TempDir()
			config, dest := filepath.Join(dir, "config"), filepath.Join(dir, "backups")
			if e := os.MkdirAll(filepath.Join(config, "cookies/nested"), 0755); e != nil {
				t.Fatal(e)
			}
			if e := os.Mkdir(dest, 0755); e != nil {
				t.Fatal(e)
			}
			inputs := map[string][]byte{"category.yaml": []byte("movie: owned\n"), "app.env": []byte("OWNED_SYNTHETIC=1\n"), "cookies/nested/site.json": []byte(`{"owned":true}`), "not-backed-up.txt": []byte("excluded"), "user.db": dbBytes, "user.db-wal": []byte("owned-wal"), "user.db-shm": []byte("owned-shm")}
			for name, b := range inputs {
				if strings.HasPrefix(c.Kind, "sqlite_db_directory") && strings.HasPrefix(name, "user.db") {
					continue
				}
				if e := os.WriteFile(filepath.Join(config, name), b, 0644); e != nil {
					t.Fatal(e)
				}
			}
			switch c.Kind {
			case "sqlite_no_cookies":
				if e := os.RemoveAll(filepath.Join(config, "cookies")); e != nil {
					t.Fatal(e)
				}
			case "sqlite_missing_config":
				if e := os.RemoveAll(config); e != nil {
					t.Fatal(e)
				}
			case "sqlite_existing_cookies":
				if e := os.MkdirAll(filepath.Join(dest, "bk_20261018090203/cookies"), 0755); e != nil {
					t.Fatal(e)
				}
			case "sqlite_overwrite_zip":
				if e := os.WriteFile(filepath.Join(dest, "bk_20261018090203.zip"), []byte("old zip"), 0644); e != nil {
					t.Fatal(e)
				}
			case "sqlite_db_directory":
				if e := os.Mkdir(filepath.Join(config, "user.db-directory"), 0755); e != nil {
					t.Fatal(e)
				}
			}
			dbType := "other"
			if strings.HasPrefix(c.Kind, "sqlite") {
				dbType = "sqlite"
			} else if strings.HasPrefix(c.Kind, "postgresql") {
				dbType = "postgresql"
			}
			a, e := NewArchive(ArchiveOptions{ConfigDir: config, WriteRoots: []string{dest}, DatabaseType: dbType, Now: func() time.Time { return time.Date(2026, 10, 18, 9, 2, 3, 0, time.UTC) }, Postgres: fakeDump{c.Kind == "postgresql_failure"}})
			if e != nil {
				t.Fatal(e)
			}
			result, e := a.BackupFile(context.Background(), dest)
			if e != nil {
				t.Fatal(e)
			}
			var got any
			if result != nil {
				got = strings.ReplaceAll(*result, dir, "<ROOT>")
			}
			if canonical(got) != canonical(c.Result) {
				t.Fatalf("return got%s want%s", canonical(got), canonical(c.Result))
			}
			files := map[string]string{}
			modes := map[string]uint32{}
			if result != nil {
				z, e := zip.OpenReader(*result)
				if e != nil {
					t.Fatal(e)
				}
				defer z.Close()
				for _, f := range z.File {
					r, e := f.Open()
					if e != nil {
						t.Fatal(e)
					}
					b, e := io.ReadAll(r)
					r.Close()
					if e != nil {
						t.Fatal(e)
					}
					files[f.Name] = base64.StdEncoding.EncodeToString(b)
					modes[f.Name] = uint32(f.Mode().Perm())
				}
			}
			if canonical(files) != canonical(c.Files) || canonical(modes) != canonical(c.Modes) {
				t.Errorf("zip files/modes got%s/%s want%s/%s", canonical(files), canonical(modes), canonical(c.Files), canonical(c.Modes))
			}
			remaining := []string{}
			if e := filepath.WalkDir(dest, func(p string, d os.DirEntry, e error) error {
				if e != nil {
					return e
				}
				if p == dest {
					return nil
				}
				rel, _ := filepath.Rel(dest, p)
				rel = filepath.ToSlash(rel)
				if d.IsDir() {
					rel += "/"
				}
				remaining = append(remaining, rel)
				return nil
			}); e != nil {
				t.Fatal(e)
			}
			sort.Strings(remaining)
			if canonical(remaining) != canonical(c.Remaining) {
				t.Errorf("leftover source prefix got%s want%s", canonical(remaining), canonical(c.Remaining))
			}
		})
	}
}
func TestNativeArchiveGrantsRejectSymlinkEscapeWithoutReadingOrWriting(t *testing.T) {
	root := t.TempDir()
	config := filepath.Join(root, "config")
	dest := filepath.Join(root, "backups")
	outside := t.TempDir()
	for _, p := range []string{config, dest} {
		if e := os.Mkdir(p, 0755); e != nil {
			t.Fatal(e)
		}
	}
	if e := os.WriteFile(filepath.Join(outside, "owned-only"), []byte("ungranted-owned-sentinel"), 0600); e != nil {
		t.Fatal(e)
	}
	if e := os.Symlink(outside, filepath.Join(config, "cookies")); e != nil {
		t.Fatal(e)
	}
	a, e := NewArchive(ArchiveOptions{ConfigDir: config, WriteRoots: []string{dest}})
	if e != nil {
		t.Fatal(e)
	}
	if result, e := a.BackupFile(context.Background(), dest); e == nil || result != nil {
		t.Fatal("escaped read grant", result, e)
	}
	matches, _ := filepath.Glob(filepath.Join(dest, "*.zip"))
	if len(matches) != 0 {
		t.Fatal("created ungranted archive")
	}
	a, e = NewArchive(ArchiveOptions{ConfigDir: config, WriteRoots: []string{dest}, ReadRoots: []string{root, outside}})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = a.BackupFile(context.Background(), outside); e == nil {
		t.Fatal("escaped write grant")
	}
}

func TestActualCoreNativeArchiveAndOldestRetention(t *testing.T) {
	root := t.TempDir()
	config, dest := filepath.Join(root, "config"), filepath.Join(root, "backup")
	for _, p := range []string{config, dest} {
		if e := os.Mkdir(p, 0755); e != nil {
			t.Fatal(e)
		}
	}
	if e := os.WriteFile(filepath.Join(config, "app.env"), []byte("OWNED_SYNTHETIC=1"), 0600); e != nil {
		t.Fatal(e)
	}
	oldFile, oldDir, newerFile := filepath.Join(dest, "bk-old-file"), filepath.Join(dest, "bk-old-directory"), filepath.Join(dest, "bk-newer-file")
	if e := os.WriteFile(oldFile, []byte("old owned"), 0600); e != nil {
		t.Fatal(e)
	}
	time.Sleep(10 * time.Millisecond)
	if e := os.Mkdir(oldDir, 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(oldDir, "owned-child"), []byte("old"), 0600); e != nil {
		t.Fatal(e)
	}
	time.Sleep(10 * time.Millisecond)
	if e := os.WriteFile(newerFile, []byte("newer owned"), 0600); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(dest, "keep.txt"), []byte("unrelated owned"), 0600); e != nil {
		t.Fatal(e)
	}
	a, e := NewArchive(ArchiveOptions{ConfigDir: config, WriteRoots: []string{dest}, DatabaseType: "sqlite", Now: func() time.Time { return time.Date(2026, 10, 18, 9, 2, 3, 0, time.UTC) }})
	if e != nil {
		t.Fatal(e)
	}
	f := Filesystem{[]string{dest}}
	notices := []any{}
	p := New(Dependencies{DataPath: func() string { return dest }, BackupFile: a.BackupFile, ListBackups: f.ListBackups, RemoveFile: f.RemoveFile, RemoveTree: f.RemoveTree, PostMessage: func(_ context.Context, n Object) error { notices = append(notices, n); return nil }})
	if e = p.Init(context.Background(), Object{"cnt": 2, "notify": true}); e != nil {
		t.Fatal(e)
	}
	r, e := p.Backup(context.Background())
	if e != nil || !r.Success {
		t.Fatal(r, e)
	}
	for _, old := range []string{oldFile, oldDir} {
		if _, e = os.Stat(old); !os.IsNotExist(e) {
			t.Fatal("old source backup survived", old, e)
		}
	}
	for _, current := range []string{newerFile, filepath.Join(dest, "bk_20261018090203.zip"), filepath.Join(dest, "keep.txt"), filepath.Join(config, "app.env")} {
		if _, e = os.Stat(current); e != nil {
			t.Fatal("unrelated/current resource lost", current, e)
		}
	}
	if len(notices) != 1 || !strings.Contains(canonical(notices[0]), "清理备份数量 2") {
		t.Fatal("original retention notice changed", notices)
	}
	if r, e = p.Backup(context.Background()); e != nil || !r.Success {
		t.Fatal(r, e)
	}
	entries, e := f.ListBackups(context.Background(), dest)
	if e != nil || len(entries) != 2 {
		t.Fatal("same-second original zip overwrite changed", len(entries), e)
	}
}
