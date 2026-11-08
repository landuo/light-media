package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDirectoryOverwriteUnknownPolicyChecksOtherVersions(t *testing.T) {
	for _, candidate := range []bool{true, false} {
		t.Run(map[bool]string{true: "matching-version", false: "empty-target"}[candidate], func(t *testing.T) {
			a, source, target := importTestApp(t)
			importFixture(t, a, "movie")
			r := overwriteRule(a, source, target, "<nil>", false)
			r.Renaming = importFlag(false)
			a.config.Directories = []DirectoryRule{r}
			next := filepath.Join(source, "New.Movie.2024.mkv")
			importWrite(t, next, []byte("new source"))
			prior := filepath.Join(target, "Old.Movie.2024.mkv")
			if candidate {
				importWrite(t, prior, []byte("old version"))
			}
			p := overwritePlan(t, a, next)
			j := overwriteStart(t, a, p)
			e := a.runImport(context.Background(), j, p.ID, nil)
			if candidate {
				if e == nil {
					t.Fatal("unmigrated policy was treated as None while latest could remove another version")
				}
				if _, e = os.Stat(p.Files[0].Target); !errors.Is(e, os.ErrNotExist) {
					t.Fatal("unknown policy published new material", e)
				}
				if b, e := os.ReadFile(prior); e != nil || string(b) != "old version" {
					t.Fatal("unknown changed old version", e)
				}
			} else if e != nil {
				t.Fatal("all four modes agree on empty directory", e)
			}
		})
	}
}

func TestDirectoryOverwriteUnknownPolicyCanReuseExistingHardlink(t *testing.T) {
	for _, sameFile := range []bool{true, false} {
		t.Run(fmt.Sprintf("same-file-%t", sameFile), func(t *testing.T) {
			a, source, target := importTestApp(t)
			movie := importFixture(t, a, "movie")
			overwriteRule(a, source, target, "<nil>", false)
			ctx := context.Background()
			input := filepath.Join(source, "Original.Movie.2024.2160p.mkv")
			content := []byte("same bytes can still be separate files")
			importWrite(t, input, content)
			item := historyUpsertSubmit(t, a, manualOrganizeRequest{Paths: []string{input}, MediaType: "movie",
				TMDBID: movie.ID, Mode: "hardlink", Scraping: importFlag(false)}, false)
			p, _ := historyUpsertAttempt(t, a, item.JobID)
			canonical := p.Files[0].Target
			existing := strings.TrimSuffix(canonical, filepath.Ext(canonical)) + " - 2160p" + filepath.Ext(canonical)
			if err := os.MkdirAll(filepath.Dir(existing), 0700); err != nil {
				t.Fatal(err)
			}
			if sameFile {
				if err := os.Link(input, existing); err != nil {
					t.Fatal(err)
				}
			} else {
				importWrite(t, existing, content)
			}
			job, err := a.claim(ctx)
			if err != nil || job.ID != item.JobID {
				t.Fatalf("claim submitted job: %v %v", job.ID, err)
			}
			err = a.safeRun(ctx, job)
			if !sameFile {
				if fileCode(err) != "REVIEW_OVERWRITE_POLICY" {
					t.Fatalf("a separate file should still require an overwrite choice: %v", err)
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				if _, err := a.finishJob(ctx, job, "succeeded", "", "", 0); err != nil {
					t.Fatal(err)
				}
				latest := historyUpsertOnly(t, a, "status=success")
				if latest.Target != existing || latest.Source != input {
					t.Fatalf("history did not point to the existing link: %+v", latest)
				}
				var asset Asset
				if err := a.assetAtPath(ctx, existing, &asset); err != nil || asset.State != "external" || asset.SourcePath != input {
					t.Fatalf("existing link was not registered as an external asset: %+v %v", asset, err)
				}
				var owned managedFile
				if err := a.Store.Get(ctx, "managed_file", tokenHash(existing), &owned); !errors.Is(err, sql.ErrNoRows) {
					t.Fatalf("the preexisting link must not be claimed as a newly written file: %v", err)
				}
			}
			if _, err := os.Stat(canonical); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("an extra video was created: %v", err)
			}
		})
	}
}
func TestDirectoryOverwriteActualSubtitleReplacement(t *testing.T) {
	a, source, target := importTestApp(t)
	importFixture(t, a, "movie")
	overwriteRule(a, source, target, "always", true)
	first := filepath.Join(source, "First.Movie.2024.mkv")
	second := filepath.Join(source, "Second.Movie.2024.mkv")
	importWrite(t, first, []byte("original video"))
	oldSub := strings.TrimSuffix(first, ".mkv") + ".zh.srt"
	importWrite(t, oldSub, []byte("original subtitle"))
	p := overwritePlan(t, a, first)
	j := overwriteStart(t, a, p)
	if e := a.runImport(context.Background(), j, p.ID, nil); e != nil {
		t.Fatal(e)
	}
	importWrite(t, second, []byte("new actual video larger"))
	newSub := strings.TrimSuffix(second, ".mkv") + ".zh.srt"
	importWrite(t, newSub, []byte("new actual subtitle"))
	p = overwritePlan(t, a, second)
	j = overwriteStart(t, a, p)
	if e := a.runImport(context.Background(), j, p.ID, nil); e != nil {
		t.Fatal(e)
	}
	targetSub := strings.TrimSuffix(p.Files[0].Target, ".mkv") + ".zh.srt"
	if b, e := os.ReadFile(targetSub); e != nil || string(b) != "new actual subtitle" {
		t.Fatal("subtitle not replaced", e)
	}
	src, e := os.Stat(newSub)
	if e != nil {
		t.Fatal(e)
	}
	dst, e := os.Stat(targetSub)
	if e != nil || !os.SameFile(src, dst) {
		t.Fatal("source link mode not applied to extra file", e)
	}
	var asset Asset
	if e = a.assetAtPath(context.Background(), p.Files[0].Target, &asset); e != nil {
		t.Fatal(e)
	}
	var owned managedFile
	if e = a.Store.Get(context.Background(), "managed_file", tokenHash(targetSub), &owned); e != nil || owned.AssetID != asset.ID {
		t.Fatal("new actual subtitle ownership", owned, e)
	}
	var meta importMetadata
	if e = a.Store.Get(context.Background(), "import_meta", p.ID, &meta); e != nil {
		t.Fatal(e)
	}
	for i := range meta.Sidecars[p.Files[0].ID] {
		key := fmt.Sprintf("%s/%s/side-%d", p.ID, p.Files[0].ID, i)
		op, e := a.readDirectoryOverwriteOperation(context.Background(), key)
		if e != nil || op.State != "complete" || !op.Decision.Overwrite || op.Decision.EventRequired {
			t.Fatal("not real original Extra overwrite", op, e)
		}
	}
}

func TestDirectoryOverwriteSubtitleSQLFailureColdRecovery(t *testing.T) {
	ctx := context.Background()
	a, source, target := importTestApp(t)
	importFixture(t, a, "movie")
	overwriteRule(a, source, target, "always", true)
	if e := a.saveSettings(ctx, a.Settings()); e != nil {
		t.Fatal(e)
	}
	first := filepath.Join(source, "First.Movie.2024.mkv")
	second := filepath.Join(source, "Second.Movie.2024.mkv")
	importWrite(t, first, []byte("old video"))
	importWrite(t, strings.TrimSuffix(first, ".mkv")+".zh.srt", []byte("old subtitle"))
	p := overwritePlan(t, a, first)
	j := overwriteStart(t, a, p)
	if e := a.runImport(ctx, j, p.ID, nil); e != nil {
		t.Fatal(e)
	}
	var prior Asset
	if e := a.assetAtPath(ctx, p.Files[0].Target, &prior); e != nil {
		t.Fatal(e)
	}
	importWrite(t, second, []byte("new actual video"))
	importWrite(t, strings.TrimSuffix(second, ".mkv")+".zh.srt", []byte("new actual subtitle"))
	p = overwritePlan(t, a, second)
	j = overwriteStart(t, a, p)
	if _, e := a.Store.DB.Exec(`CREATE TRIGGER owned_subtitle_register_failure BEFORE INSERT ON records WHEN NEW.kind='asset' BEGIN SELECT RAISE(ABORT,'owned subtitle transaction failure'); END`); e != nil {
		t.Fatal(e)
	}
	if e := a.runImport(ctx, j, p.ID, nil); e == nil {
		t.Fatal("actual registration SQL failure ignored")
	}
	paths := []string{p.Files[0].Target, strings.TrimSuffix(p.Files[0].Target, ".mkv") + ".zh.srt"}
	keys := []string{p.ID + "/" + p.Files[0].ID, p.ID + "/" + p.Files[0].ID + "/side-0"}
	stamps := make([]importSnapshot, len(paths))
	for i, path := range paths {
		var e error
		stamps[i], e = snapshotPath(path)
		if e != nil {
			t.Fatal("actual published file absent", e)
		}
		op, e := a.readDirectoryOverwriteOperation(ctx, keys[i])
		if e != nil || op.State != "published" {
			t.Fatal("actual video/extra publication fact lost", op, e)
		}
	}
	var unchanged Asset
	if e := a.Store.Get(ctx, "asset", prior.ID, &unchanged); e != nil || unchanged.State != "present" {
		t.Fatal("failed transaction changed original ownership", unchanged, e)
	}
	dir := a.Dir
	if e := a.Close(); e != nil {
		t.Fatal(e)
	}
	cold, e := New(Options{DataDir: dir})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { cold.Close() })
	if _, e = cold.Store.DB.Exec(`DROP TRIGGER owned_subtitle_register_failure`); e != nil {
		t.Fatal(e)
	}
	// New recovers interrupted running jobs to queued. Claim the same durable
	// import job again instead of executing with the pre-crash generation.
	j = overwriteStart(t, cold, p)
	if e = cold.runImport(ctx, j, p.ID, nil); e != nil {
		t.Fatal("cold actual registration recovery", e)
	}
	var current Asset
	if e = cold.assetAtPath(ctx, p.Files[0].Target, &current); e != nil || current.ID == prior.ID {
		t.Fatal("cold new asset not registered", current, e)
	}
	for i, path := range paths {
		stamp, e := snapshotPath(path)
		if e != nil || stamp != stamps[i] {
			t.Fatal("cold replay republished real material", path, e)
		}
		op, e := cold.readDirectoryOverwriteOperation(ctx, keys[i])
		if e != nil || op.State != "complete" {
			t.Fatal("cold overwrite cleanup incomplete", op, e)
		}
		var owned managedFile
		if e = cold.Store.Get(ctx, "managed_file", tokenHash(path), &owned); e != nil || owned.AssetID != current.ID || owned.Snapshot != stamp {
			t.Fatal("cold material ownership not atomic", owned, e)
		}
	}
}
func TestDirectoryOverwriteNativeDefaultNoneAndUnmigratedAreDistinct(t *testing.T) {
	a, source, target := importTestApp(t)
	if _, e := a.saveManagedDirectory(context.Background(), DirectoryRule{Name: "new native", SourcePath: source, LibraryPath: target}); e != nil {
		t.Fatal(e)
	}
	rules := a.Settings().Directories
	if len(rules) != 1 || !rules[0].OverwriteModeKnown || rules[0].OverwriteMode != nil {
		t.Fatal("new native source None was lost", rules)
	}
	legacy := rules[0]
	legacy.OverwriteModeKnown = false
	if _, e := a.saveManagedDirectory(context.Background(), legacy); e != nil {
		t.Fatal(e)
	}
	if a.Settings().Directories[0].OverwriteModeKnown {
		t.Fatal("existing unmigrated directory was auto-defaulted")
	}
}
