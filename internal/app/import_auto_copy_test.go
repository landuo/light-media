package app

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func TestActualQualityAutoEXDEVResealsReplacementAndScrapes(t *testing.T) {
	qualityActualMovieWorkflow(t, "auto_exdev")
}

func ownedAutoPlan(t *testing.T, a *App, source, mode string) ImportPlan {
	t.Helper()
	p, e := a.previewImport(context.Background(), ImportRequest{MediaType: "movie", TMDBID: 123, RootID: "root", Mode: mode, Files: []ImportFileRequest{{Path: source}}, Scraping: importFlag(false)})
	if e != nil || len(p.Files) != 1 || p.Files[0].Status != "ready" || p.Files[0].Method != "hardlink" {
		t.Fatal("owned actual preview", p, e)
	}
	return p
}

func TestActualAutoEXDEVCopyAndColdSQLRecovery(t *testing.T) {
	for _, boundary := range []string{"normal", "asset_sql_cold", "transition_sql", "commit_ack_lost"} {
		t.Run(boundary, func(t *testing.T) {
			a, source, target := importTestApp(t)
			ctx := context.Background()
			importFixture(t, a, "movie")
			overwriteRule(a, source, target, "always", true)
			old := filepath.Join(source, "Owned.2024.1080p.mkv")
			input := filepath.Join(source, "Owned.2024.2160p.mkv")
			importWrite(t, old, []byte("old owned bytes"))
			first := overwritePlan(t, a, old)
			oldJob := overwriteStart(t, a, first)
			if e := a.runImport(ctx, oldJob, first.ID, nil); e != nil {
				t.Fatal(e)
			}
			if _, e := a.Store.DB.Exec(`UPDATE jobs SET state='succeeded' WHERE id=?`, oldJob.ID); e != nil {
				t.Fatal(e)
			}
			importWrite(t, input, []byte("new owned copied bytes retained in source"))
			importWrite(t, filepath.Join(source, "Owned.2024.2160p.zh.srt"), []byte("new subtitle"))
			p := ownedAutoPlan(t, a, input, "auto")
			j := overwriteStart(t, a, p)
			calls := 0
			a.importSourceLink = func(ofd int, old string, nfd int, next string, flags int) error {
				calls++
				if old == filepath.Base(input) {
					return unix.EXDEV
				}
				return unix.Linkat(ofd, old, nfd, next, flags)
			}
			if boundary == "commit_ack_lost" {
				a.importAutoCommit = func(tx *sql.Tx) error {
					if e := tx.Commit(); e != nil {
						return e
					}
					return errors.New("owned actual COMMIT completed but acknowledgement lost")
				}
			}
			if boundary == "asset_sql_cold" {
				if _, e := a.Store.DB.Exec(`CREATE TRIGGER owned_auto_asset_failure BEFORE INSERT ON records WHEN NEW.kind='asset' BEGIN SELECT RAISE(ABORT,'owned auto asset SQL'); END`); e != nil {
					t.Fatal(e)
				}
			}
			if boundary == "transition_sql" {
				if _, e := a.Store.DB.Exec(`CREATE TRIGGER owned_auto_transition_failure BEFORE INSERT ON records WHEN NEW.kind='import_auto_copy' BEGIN SELECT RAISE(ABORT,'owned auto transition SQL'); END`); e != nil {
					t.Fatal(e)
				}
			}
			e := a.runImport(ctx, j, p.ID, nil)
			if boundary == "transition_sql" {
				if e == nil {
					t.Fatal("transition SQL failure ignored")
				}
				var unchanged ImportPlan
				var meta importMetadata
				if a.Store.Get(ctx, "import_plan", p.ID, &unchanged) != nil || unchanged.Files[0].Method != "hardlink" || a.Store.Get(ctx, "import_meta", p.ID, &meta) != nil || meta.Methods[p.Files[0].ID] != "hardlink" {
					t.Fatal("partial method transition committed")
				}
				op, oe := a.readDirectoryOverwriteOperation(ctx, p.ID+"/"+p.Files[0].ID)
				if oe != nil || op.Method != "hardlink" {
					t.Fatal(op, oe)
				}
				if b, oe := os.ReadFile(first.Files[0].Target); oe != nil || string(b) != "old owned bytes" {
					t.Fatal("failed transition changed old target", oe)
				}
				if _, e = a.Store.DB.Exec(`DROP TRIGGER owned_auto_transition_failure`); e != nil {
					t.Fatal(e)
				}
				e = a.runImport(ctx, j, p.ID, nil)
			}
			var published importSnapshot
			if boundary == "asset_sql_cold" {
				if e == nil {
					t.Fatal("actual asset SQL boundary ignored")
				}
				published, e = snapshotPath(p.Files[0].Target)
				if e != nil {
					t.Fatal(e)
				}
				if _, e = a.Store.DB.Exec(`DROP TRIGGER owned_auto_asset_failure`); e != nil {
					t.Fatal(e)
				}
				config, dir := a.Settings(), a.Dir
				if e = a.Close(); e != nil {
					t.Fatal(e)
				}
				a, e = New(Options{DataDir: dir})
				if e != nil {
					t.Fatal(e)
				}
				defer a.Close()
				a.config = config
				j, e = a.claim(ctx)
				if e != nil || j.ID == "" || j.Kind != "import" {
					t.Fatal("cold actual durable job not reclaimed", j, e)
				}
				a.importSourceLink = func(int, string, int, string, int) error {
					t.Error("cold known copy repeated source Linkat")
					return errors.New("no new link")
				}
				e = a.runImport(ctx, j, p.ID, nil)
			}
			if e != nil {
				t.Fatal("actual automatic copy", e)
			}
			var actual ImportPlan
			var meta importMetadata
			if a.Store.Get(ctx, "import_plan", p.ID, &actual) != nil || actual.Files[0].Method != "copy" || a.Store.Get(ctx, "import_meta", p.ID, &meta) != nil || meta.Methods[p.Files[0].ID] != "copy" {
				t.Fatal("plan and metadata did not share actual method")
			}
			op, e := a.readDirectoryOverwriteOperation(ctx, p.ID+"/"+p.Files[0].ID)
			if e != nil || op.Method != "copy" || op.State != "complete" {
				t.Fatal("sealed actual operation disagrees", op, e)
			}
			var proof importAutoCopyReceipt
			if e = a.readQualitySealed(ctx, a.Store.DB, "import_auto_copy", op.ID, &proof); e != nil || proof.BeforeInputSHA == proof.AfterInputSHA || proof.SourceReceipt.State != "planned" {
				t.Fatal("actual failed Linkat evidence missing", proof, e)
			}
			src, _ := snapshotPath(input)
			dst, e := snapshotPath(actual.Files[0].Target)
			if e != nil || sameInode(src, dst) || src.Size != dst.Size || published.Inode != 0 && dst != published {
				t.Fatal("copy inode/known publication incorrect", src, dst, e)
			}
			for path, want := range map[string]string{input: "new owned copied bytes retained in source", old: "old owned bytes", actual.Files[0].Target: "new owned copied bytes retained in source"} {
				b, e := os.ReadFile(path)
				if e != nil || string(b) != want {
					t.Fatal("real source/target bytes", path, e)
				}
			}
			if calls != 1 && boundary != "transition_sql" || boundary == "transition_sql" && calls != 2 {
				t.Fatal("unexpected source Linkat count", calls)
			}
		})
	}
}

func TestActualAutoTransitionLostACKCancellationAndUnknownKeepDirection(t *testing.T) {
	for _, boundary := range []string{"committed_then_cancelled", "rolled_back_unknown"} {
		t.Run(boundary, func(t *testing.T) {
			a, source, target := importTestApp(t)
			ctx := context.Background()
			importFixture(t, a, "movie")
			overwriteRule(a, source, target, "always", true)
			input := filepath.Join(source, "Owned.2024.mkv")
			importWrite(t, input, []byte("actual source retained"))
			p := ownedAutoPlan(t, a, input, "auto")
			j := overwriteStart(t, a, p)
			a.importSourceLink = func(int, string, int, string, int) error { return unix.EXDEV }
			a.importAutoCommit = func(tx *sql.Tx) error {
				if boundary == "rolled_back_unknown" {
					_ = tx.Rollback()
					return errors.New("owned commit not confirmed")
				}
				if e := tx.Commit(); e != nil {
					return e
				}
				if _, e := a.Store.DB.Exec(`UPDATE jobs SET state='cancelled' WHERE id=?`, j.ID); e != nil {
					return e
				}
				return errors.New("owned known COMMIT ACK lost after cancellation")
			}
			e := a.runImport(ctx, j, p.ID, nil)
			if e == nil {
				t.Fatal("scope/unknown failure claimed complete")
			}
			var persisted ImportPlan
			if e = a.Store.Get(ctx, "import_plan", p.ID, &persisted); e != nil {
				t.Fatal(e)
			}
			want := "copy"
			if boundary == "rolled_back_unknown" {
				want = "hardlink"
			}
			if persisted.Files[0].Method != want {
				t.Fatal("transaction direction was overwritten", persisted.Files[0].Method, want)
			}
			if _, e = os.Lstat(p.Files[0].Target); !errors.Is(e, os.ErrNotExist) {
				t.Fatal("cancel/unknown authorized new copy", e)
			}
		})
	}
}

func TestActualEXDEVDoesNotChangeExplicitHardlink(t *testing.T) {
	a, source, target := importTestApp(t)
	importFixture(t, a, "movie")
	overwriteRule(a, source, target, "always", true)
	input := filepath.Join(source, "Owned.2024.mkv")
	importWrite(t, input, []byte("owned hardlink source"))
	p := ownedAutoPlan(t, a, input, "hardlink")
	j := overwriteStart(t, a, p)
	a.importSourceLink = func(int, string, int, string, int) error { return unix.EXDEV }
	if e := a.runImport(context.Background(), j, p.ID, nil); pluginErrorCode(e) != "IMPORT_LINK_CROSS_DEVICE" {
		t.Fatal("explicit hardlink changed", e)
	}
	if _, e := os.Lstat(p.Files[0].Target); !errors.Is(e, os.ErrNotExist) {
		t.Fatal("explicit hardlink copied", e)
	}
	var p2 ImportPlan
	if e := a.Store.Get(context.Background(), "import_plan", p.ID, &p2); e != nil || p2.Files[0].Method != "hardlink" {
		t.Fatal(p2, e)
	}
}

func TestActualAutoEXDEVGrantRejectsChangedScopeAndReplay(t *testing.T) {
	for _, boundary := range []string{"cancelled", "source_changed", "rule_changed", "wrong_job", "foreign_app", "replay"} {
		t.Run(boundary, func(t *testing.T) {
			a, source, target := importTestApp(t)
			importFixture(t, a, "movie")
			overwriteRule(a, source, target, "always", true)
			ctx := context.Background()
			input := filepath.Join(source, "Owned.2024.mkv")
			importWrite(t, input, []byte("actual owned source"))
			p := ownedAutoPlan(t, a, input, "auto")
			j := overwriteStart(t, a, p)
			if e := a.Store.Get(ctx, "import_plan", p.ID, &p); e != nil {
				t.Fatal(e)
			}
			var meta importMetadata
			if e := a.Store.Get(ctx, "import_meta", p.ID, &meta); e != nil {
				t.Fatal(e)
			}
			key := p.ID + "/" + p.Files[0].ID
			a.importSourceLink = func(int, string, int, string, int) error { return unix.EXDEV }
			_, e := a.publishDirectoryOverwrite(ctx, key, input, p.Files[0].Target, p.RootID, "hardlink", planSnapshot(p.Files[0]), *meta.Overwrite, func() error { return nil })
			var grant *importEXDEVFailure
			if !errors.As(e, &grant) {
				t.Fatal("no actual failed Linkat grant", e)
			}
			switch boundary {
			case "cancelled":
				if _, e = a.Store.DB.Exec(`UPDATE jobs SET state='cancelled' WHERE id=?`, j.ID); e != nil {
					t.Fatal(e)
				}
			case "source_changed":
				importWrite(t, input, []byte("changed after actual Linkat"))
			case "rule_changed":
				a.config.Directories[0].Name = "edited after actual Linkat"
			case "wrong_job":
				j = importClaim(t, a, "asset_check", "not-the-import")
			case "foreign_app":
				other, _, _ := importTestApp(t)
				grant.app = other
			}
			e = a.adoptImportAutoCopy(ctx, j, &p, 0, &meta, nil, grant, func() error { return nil })
			if boundary == "replay" {
				if e != nil {
					t.Fatal("actual one-use grant failed", e)
				}
				if e = a.adoptImportAutoCopy(ctx, j, &p, 0, &meta, nil, grant, func() error { return nil }); e == nil {
					t.Fatal("same source failure capability replayed")
				}
				if e = a.runImport(ctx, j, p.ID, nil); e != nil {
					t.Fatal("authenticated transition did not resume actual copy", e)
				}
			} else {
				if e == nil {
					t.Fatal("changed scope authorized copy")
				}
				var persisted ImportPlan
				if e = a.Store.Get(ctx, "import_plan", p.ID, &persisted); e != nil || persisted.Files[0].Method != "hardlink" {
					t.Fatal("rejected transition changed plan", e)
				}
				if _, e = os.Lstat(p.Files[0].Target); !errors.Is(e, os.ErrNotExist) {
					t.Fatal("rejected transition published bytes", e)
				}
			}
		})
	}
}

func TestPublicCrossDeviceErrorCannotAuthorizeAutoCopy(t *testing.T) {
	a, source, target := importTestApp(t)
	importFixture(t, a, "movie")
	overwriteRule(a, source, target, "always", true)
	input := filepath.Join(source, "Owned.2024.mkv")
	importWrite(t, input, []byte("public error only"))
	p := ownedAutoPlan(t, a, input, "auto")
	j := overwriteStart(t, a, p)
	a.importSourceLink = func(int, string, int, string, int) error {
		return bad("IMPORT_LINK_CROSS_DEVICE", "fabricated external error text")
	}
	if e := a.runImport(context.Background(), j, p.ID, nil); pluginErrorCode(e) != "IMPORT_LINK_FAILED" {
		t.Fatal("public error accepted as actual syscall capability", e)
	}
	if _, e := os.Lstat(p.Files[0].Target); !errors.Is(e, os.ErrNotExist) {
		t.Fatal("unproven auto copied", e)
	}
}
