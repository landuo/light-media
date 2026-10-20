package app

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"light-media/internal/catalog"
)

func overwriteRule(a *App, source, target, mode string, known bool) DirectoryRule {
	var value *string
	if mode != "<nil>" {
		value = &mode
	}
	r := DirectoryRule{ID: "overwrite-owned", Name: "合成目录", SourcePath: source, LibraryPath: target, RootID: "root", MediaType: "mixed", Mode: "hardlink", MonitorType: "monitor", Enabled: true, Renaming: importFlag(true), Scraping: importFlag(false), OverwriteMode: value, OverwriteModeKnown: known}
	a.config.Directories = []DirectoryRule{r}
	return r
}
func overwritePlan(t *testing.T, a *App, source string) ImportPlan {
	t.Helper()
	p, e := a.previewImport(context.Background(), ImportRequest{MediaType: "movie", TMDBID: 123, RootID: "root", Mode: "hardlink", Files: []ImportFileRequest{{Path: source}}, Scraping: importFlag(false)})
	if e != nil || len(p.Files) != 1 || p.Files[0].Status != "ready" {
		t.Fatalf("source preview=%+v err=%v", p, e)
	}
	return p
}
func overwriteStart(t *testing.T, a *App, p ImportPlan) Job {
	t.Helper()
	j, e := a.executeImport(context.Background(), p.ID, nil)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = a.Store.DB.Exec(`UPDATE jobs SET state='running',generation=generation+1 WHERE id=?`, j.ID); e != nil {
		t.Fatal(e)
	}
	j, e = scanJob(a.Store.DB.QueryRow(`SELECT `+jobColumns+` FROM jobs WHERE id=?`, j.ID))
	if e != nil {
		t.Fatal(e)
	}
	return j
}
func TestDirectoryOverwriteActualModesAndFileFacts(t *testing.T) {
	cases := []struct {
		name, mode         string
		known              bool
		old, new           string
		want               bool
		sameInode, oldTime bool
	}{
		{"always", "always", true, "old", "new larger version", true, false, false},
		{"size-larger", "size", true, "old", "new larger version", true, false, false},
		{"size-equal", "size", true, "old", "new", false, false, false},
		{"size-smaller", "size", true, "old version", "new", false, false, false},
		{"never", "never", true, "old", "new larger version", false, false, false},
		{"latest-older-mtime", "latest", true, "old", "new larger version", true, false, true},
		{"source-none", "<nil>", true, "old", "new larger version", false, false, false},
		{"unmigrated", "<nil>", false, "old", "new larger version", false, false, false},
		{"same-inode-always", "always", true, "old", "", true, true, false},
		{"same-inode-size", "size", true, "old", "", false, true, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a, source, target := importTestApp(t)
			importFixture(t, a, "movie")
			overwriteRule(a, source, target, c.mode, c.known)
			old := filepath.Join(source, "Original.Movie.2024.1080p.mkv")
			importWrite(t, old, []byte(c.old))
			first := overwritePlan(t, a, old)
			j := overwriteStart(t, a, first)
			if e := a.runImport(context.Background(), j, first.ID, nil); e != nil {
				t.Fatal("initial materialization", e)
			}
			var prior Asset
			if e := a.assetAtPath(context.Background(), first.Files[0].Target, &prior); e != nil {
				t.Fatal(e)
			}
			next := filepath.Join(source, "Original.Movie.2024.2160p.mkv")
			if c.sameInode {
				if e := os.Link(old, next); e != nil {
					t.Fatal(e)
				}
			} else {
				importWrite(t, next, []byte(c.new))
			}
			if c.oldTime {
				past := time.Unix(1000, 0)
				if e := os.Chtimes(next, past, past); e != nil {
					t.Fatal(e)
				}
			}
			p := overwritePlan(t, a, next)
			// Source preview never makes or applies an overwrite decision.
			if got, _ := os.ReadFile(p.Files[0].Target); string(got) != c.old {
				t.Fatal("preview changed target")
			}
			j = overwriteStart(t, a, p)
			e := a.runImport(context.Background(), j, p.ID, nil)
			if (e == nil) != c.want {
				t.Fatalf("want success=%t got %v", c.want, e)
			}
			expected := c.old
			if c.want && !c.sameInode {
				expected = c.new
			}
			if got, e := os.ReadFile(p.Files[0].Target); e != nil || string(got) != expected {
				t.Fatal("actual target", string(got), e)
			}
			if got, e := os.ReadFile(old); e != nil || string(got) != c.old {
				t.Fatal("old download source changed", e)
			}
			if _, e := os.Stat(next); e != nil {
				t.Fatal("current download source changed", e)
			}
			if c.want {
				var current Asset
				if e = a.assetAtPath(context.Background(), p.Files[0].Target, &current); e != nil || current.SourcePath != next || current.ID == prior.ID {
					t.Fatal("new asset not committed", current, e)
				}
				op, e := a.readDirectoryOverwriteOperation(context.Background(), p.ID+"/"+p.Files[0].ID)
				if e != nil || op.State != "complete" || len(op.Targets) != 1 {
					t.Fatal("real sealed overwrite", op, e)
				}
				if _, e = os.Lstat(filepath.Join(filepath.Dir(op.Targets[0].Path), op.Targets[0].Backup)); !errors.Is(e, os.ErrNotExist) {
					t.Fatal("old temporary version not finalized", e)
				}
			} else {
				var current Asset
				if e = a.Store.Get(context.Background(), "asset", prior.ID, &current); e != nil || current.State != "present" {
					t.Fatal("denial changed history", e)
				}
			}
		})
	}
}
func TestDirectoryOverwriteLatestActualVersionsAndParts(t *testing.T) {
	for _, mode := range []string{"latest", "always", "never", "size"} {
		t.Run(mode, func(t *testing.T) {
			a, source, target := importTestApp(t)
			identity := importFixture(t, a, "tv")
			r := overwriteRule(a, source, target, mode, true)
			r.Renaming = importFlag(false)
			a.config.Directories = []DirectoryRule{r}
			importCache(t, a, "season:123:1", catalog.Season{SeriesID: 123, SeasonNumber: 1, Episodes: []catalog.Episode{{ID: 1, SeasonNumber: 1, EpisodeNumber: 1}, {ID: 2, SeasonNumber: 1, EpisodeNumber: 2}}})
			names := []string{"Synthetic.Show.S01E01.CD1.720p.mkv", "Synthetic.Show.S01E01.CD2.720p.mkv", "Synthetic.Show.S01E02.CD1.mkv", "Synthetic.Show.S01E01-E02.mkv", "Synthetic.Show.S01E01.CD1.zh.srt", "Synthetic.Show.S01E01.CD1.nfo"}
			for _, n := range names {
				importWrite(t, filepath.Join(target, n), []byte("original version "+n))
			}
			input := filepath.Join(source, "Synthetic.Show.S01E01.CD1.2160p.mkv")
			importWrite(t, input, []byte("actual new version"))
			p, e := a.previewImport(context.Background(), ImportRequest{MediaType: identity.MediaType, TMDBID: identity.ID, RootID: "root", Mode: "hardlink", Files: []ImportFileRequest{{Path: input, Season: 1, Episodes: []int{1}}}, Scraping: importFlag(false)})
			if e != nil || p.Files[0].Status != "ready" {
				t.Fatal(p, e)
			}
			j := overwriteStart(t, a, p)
			if e = a.runImport(context.Background(), j, p.ID, nil); e != nil {
				t.Fatal(e)
			}
			for i, n := range names {
				_, e := os.Stat(filepath.Join(target, n))
				deleted := mode == "latest" && i == 0
				if deleted && !errors.Is(e, os.ErrNotExist) || !deleted && e != nil {
					t.Fatal("wrong latest victim", n, e)
				}
			}
			if got, e := os.ReadFile(p.Files[0].Target); e != nil || string(got) != "actual new version" {
				t.Fatal("new material", e)
			}
		})
	}
}
func TestDirectoryOverwriteKnownPublicationColdAndScopeWithdrawal(t *testing.T) {
	a, source, target := importTestApp(t)
	r := overwriteRule(a, source, target, "always", true)
	input := filepath.Join(source, "Movie.2024.mkv")
	output := filepath.Join(target, "Movie.2024.mkv")
	importWrite(t, input, []byte("actual new"))
	importWrite(t, output, []byte("old bytes"))
	stamp, _ := snapshotPath(input)
	key := "actual-known-overwrite"
	policy := directoryOverwritePolicy(r)
	op, e := a.readDirectoryOverwrite(context.Background(), key, input, output, "root", "hardlink", stamp, policy)
	if e != nil {
		t.Fatal(e)
	}
	if e = a.saveDirectoryOverwrite(context.Background(), op); e != nil {
		t.Fatal(e)
	}
	var before directoryOverwriteCipher
	if e = a.Store.Get(context.Background(), "directory_overwrite", key, &before); e != nil {
		t.Fatal(e)
	}
	receipt, e := a.publishDirectoryOverwrite(context.Background(), key, input, output, "root", "hardlink", stamp, policy, func() error { return nil })
	if e != nil {
		t.Fatal(e)
	}
	// Keep the actual filesystem/publish receipt, and restore only the previous
	// sealed queue state to model a lost acknowledgement of its final SQL write.
	if e = a.Store.Put(context.Background(), "directory_overwrite", key, before); e != nil {
		t.Fatal(e)
	}
	if e = a.saveSettings(context.Background(), a.Settings()); e != nil {
		t.Fatal(e)
	}
	dir := a.Dir
	if e = a.Close(); e != nil {
		t.Fatal(e)
	}
	cold, e := New(Options{DataDir: dir})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { cold.Close() })
	cold.config.Directories = nil
	called := false
	got, e := cold.publishDirectoryOverwrite(context.Background(), key, input, output, "root", "hardlink", stamp, policy, func() error { called = true; return errors.New("scope withdrawn") })
	if e != nil || called || got.StageSnapshot != receipt.StageSnapshot {
		t.Fatal("known publication was turned into a new action", called, got, e)
	}
	if got, e := os.ReadFile(output); e != nil || string(got) != "actual new" {
		t.Fatal("known target changed", e)
	}
}
func TestDirectoryOverwriteSQLFailureRetryAndTamper(t *testing.T) {
	a, source, target := importTestApp(t)
	importFixture(t, a, "movie")
	overwriteRule(a, source, target, "always", true)
	old := filepath.Join(source, "Old.Movie.2024.mkv")
	importWrite(t, old, []byte("old material"))
	p := overwritePlan(t, a, old)
	j := overwriteStart(t, a, p)
	if e := a.runImport(context.Background(), j, p.ID, nil); e != nil {
		t.Fatal(e)
	}
	next := filepath.Join(source, "Next.Movie.2024.mkv")
	importWrite(t, next, []byte("new material larger"))
	p = overwritePlan(t, a, next)
	j = overwriteStart(t, a, p)
	if _, e := a.Store.DB.Exec(`CREATE TRIGGER owned_overwrite_asset_failure BEFORE INSERT ON records WHEN NEW.kind='asset' BEGIN SELECT RAISE(ABORT,'owned asset transaction failure'); END`); e != nil {
		t.Fatal(e)
	}
	if e := a.runImport(context.Background(), j, p.ID, nil); e == nil {
		t.Fatal("asset SQL failure ignored")
	}
	op, e := a.readDirectoryOverwriteOperation(context.Background(), p.ID+"/"+p.Files[0].ID)
	if e != nil || op.State != "published" {
		t.Fatal("actual publication fact lost", op, e)
	}
	before, _ := snapshotPath(p.Files[0].Target)
	if _, e = a.Store.DB.Exec(`DROP TRIGGER owned_overwrite_asset_failure`); e != nil {
		t.Fatal(e)
	}
	if e = a.runImport(context.Background(), j, p.ID, nil); e != nil {
		t.Fatal("actual SQL recovery", e)
	}
	after, _ := snapshotPath(p.Files[0].Target)
	if before != after {
		t.Fatal("known file was republished")
	}
	// The operation is AEAD-bound to its key; SQL text cannot authorize another
	// target or fake a policy decision.
	var cipher directoryOverwriteCipher
	key := p.ID + "/" + p.Files[0].ID
	if e = a.Store.Get(context.Background(), "directory_overwrite", key, &cipher); e != nil {
		t.Fatal(e)
	}
	cipher.Sealed[len(cipher.Sealed)-1] ^= 1
	if e = a.Store.Put(context.Background(), "directory_overwrite", key, cipher); e != nil {
		t.Fatal(e)
	}
	if _, e = a.readDirectoryOverwriteOperation(context.Background(), key); e == nil {
		t.Fatal("tamper accepted")
	}
}
func TestDirectoryOverwriteActualUISettingsAndMissingMarker(t *testing.T) {
	a, source, target := importTestApp(t)
	cookie := setupTestAdmin(t, a)
	for _, mode := range []string{"never", "always", "size", "latest"} {
		body, _ := json.Marshal(map[string]any{"directories": []any{map[string]any{"name": "owned policy", "source_path": source, "library_path": target, "media_type": "mixed", "mode": "hardlink", "monitor_type": "none", "overwrite_mode": mode, "overwrite_mode_known": true}}})
		response := callTest(t, a.Handler(), "PUT", "/api/directories/manage", string(body), cookie, "")
		if response.Code != 200 {
			t.Fatal(response.Code, response.Body)
		}
		rules := a.Settings().Directories
		if len(rules) != 1 || !rules[0].OverwriteModeKnown || rules[0].OverwriteMode == nil || *rules[0].OverwriteMode != mode {
			t.Fatal(rules)
		}
		response = callTest(t, a.Handler(), "GET", "/api/directories/manage", "", cookie, "")
		if !strings.Contains(response.Body.String(), `"overwrite_mode":"`+mode+`"`) {
			t.Fatal("not returned to directory card", response.Body)
		}
	}
	old := DirectoryRule{ID: "legacy", Name: "legacy", SourcePath: source, LibraryPath: target, RootID: "root", MediaType: "mixed", Mode: "hardlink", MonitorType: "none"}
	policy := directoryOverwritePolicy(old)
	if policy.Policy.Known || policy.Policy.Mode != nil {
		t.Fatal("invented policy for old directory")
	}
	raw, _ := json.Marshal(old)
	if strings.Contains(string(raw), "overwrite_mode") {
		t.Fatal("legacy fingerprints changed", string(raw))
	}
}

func TestDirectoryOverwriteFailureBeforePublicationRestoresExactTarget(t *testing.T) {
	a, source, target := importTestApp(t)
	r := overwriteRule(a, source, target, "always", true)
	input := filepath.Join(source, "input.mkv")
	output := filepath.Join(target, "output.mkv")
	importWrite(t, input, []byte("source initial"))
	importWrite(t, output, []byte("old exact bytes"))
	locked, _ := snapshotPath(input)
	old, _ := snapshotPath(output)
	calls := 0
	_, e := a.publishDirectoryOverwrite(context.Background(), "owned-source-change", input, output, "root", "hardlink", locked, directoryOverwritePolicy(r), func() error {
		calls++
		if calls == 3 {
			importWrite(t, input, []byte("source externally changed"))
		}
		return nil
	})
	if e == nil {
		t.Fatal("source mutation accepted")
	}
	got, err := snapshotPath(output)
	if err != nil || got != old {
		t.Fatal("exact prior target not restored", got, err)
	}
	if b, _ := os.ReadFile(input); string(b) != "source externally changed" {
		t.Fatal("mutated download source was rewritten")
	}
}
func TestDirectoryOverwritePolicyAndUnknownPluginAreNotDefaulted(t *testing.T) {
	for _, change := range []string{"policy", "unknown-plugin", "target-symlink", "target-query-error"} {
		t.Run(change, func(t *testing.T) {
			a, source, target := importTestApp(t)
			importFixture(t, a, "movie")
			r := overwriteRule(a, source, target, "always", true)
			input := filepath.Join(source, "input.2024.mkv")
			importWrite(t, input, []byte("new"))
			p := overwritePlan(t, a, input)
			importWrite(t, p.Files[0].Target, []byte("original"))
			if change == "policy" {
				mode := "never"
				a.config.Directories[0].OverwriteMode = &mode
			}
			if change == "unknown-plugin" {
				installation := storedPluginFixture("UnknownOverwrite")
				installation.PluginID = "UnknownOverwrite"
				installation.Installed = true
				if e := a.pluginStore.Install(context.Background(), installation); e != nil {
					t.Fatal(e)
				}
			}
			if change == "target-symlink" {
				if e := os.Remove(p.Files[0].Target); e != nil {
					t.Fatal(e)
				}
				if e := os.Symlink(input, p.Files[0].Target); e != nil {
					t.Fatal(e)
				}
			}
			if change == "target-query-error" {
				if e := os.Remove(p.Files[0].Target); e != nil {
					t.Fatal(e)
				}
				if e := os.Mkdir(p.Files[0].Target, 0750); e != nil {
					t.Fatal(e)
				}
			}
			j := overwriteStart(t, a, p)
			if e := a.runImport(context.Background(), j, p.ID, nil); e == nil {
				t.Fatal("unknown or changed evidence became permission", r)
			}
			if b, _ := os.ReadFile(input); string(b) != "new" {
				t.Fatal("source changed")
			}
			if change == "policy" || change == "unknown-plugin" {
				if b, _ := os.ReadFile(p.Files[0].Target); string(b) != "original" {
					t.Fatal("existing target changed")
				}
			}
		})
	}
}

func TestDirectoryOverwriteKeepsUnmatchedManagedVersionsAndHistory(t *testing.T) {
	for _, mode := range []string{"always", "size", "never"} {
		t.Run(mode, func(t *testing.T) {
			a, source, target := importTestApp(t)
			importFixture(t, a, "movie")
			r := overwriteRule(a, source, target, mode, true)
			r.Renaming = importFlag(false)
			a.config.Directories = []DirectoryRule{r}
			old := filepath.Join(source, "Old.Version.2024.mkv")
			next := filepath.Join(source, "New.Version.2024.mkv")
			importWrite(t, old, []byte("original version"))
			importWrite(t, next, []byte("actual newer version"))
			first := overwritePlan(t, a, old)
			j := overwriteStart(t, a, first)
			if e := a.runImport(context.Background(), j, first.ID, nil); e != nil {
				t.Fatal(e)
			}
			var prior Asset
			if e := a.assetAtPath(context.Background(), first.Files[0].Target, &prior); e != nil {
				t.Fatal(e)
			}
			second := overwritePlan(t, a, next)
			j = overwriteStart(t, a, second)
			if e := a.runImport(context.Background(), j, second.ID, nil); e != nil {
				t.Fatal(e)
			}
			var retained Asset
			if e := a.Store.Get(context.Background(), "asset", prior.ID, &retained); e != nil || retained.State != "present" {
				t.Fatal("different filename history removed", retained, e)
			}
			if b, e := os.ReadFile(prior.Path); e != nil || string(b) != "original version" {
				t.Fatal("non-latest removed an unrelated version", e)
			}
			var oldOwner managedFile
			if e := a.Store.Get(context.Background(), "managed_file", tokenHash(prior.Path), &oldOwner); e != nil || oldOwner.AssetID != prior.ID {
				t.Fatal("retained ownership was changed", e)
			}
			var active string
			if e := a.Store.DB.QueryRow(`SELECT asset_id FROM asset_slots WHERE media_key=?`, mediaKey(prior.Media)).Scan(&active); e != nil || active == prior.ID {
				t.Fatal("actual newly present inventory not registered", active, e)
			}
		})
	}
}
