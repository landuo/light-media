package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func ownedNativeSourceDirectories(t *testing.T) (*App, string, string, string, string) {
	t.Helper()
	a, source, target := importTestApp(t)
	importFixture(t, a, "movie")
	one, two := filepath.Join(source, "one"), filepath.Join(source, "two")
	other := filepath.Join(filepath.Dir(target), "other-library")
	for _, p := range []string{one, two, other} {
		if e := os.MkdirAll(p, 0750); e != nil {
			t.Fatal(e)
		}
	}
	a.config.Roots = append(a.config.Roots, Root{ID: "root2", Path: other, Kind: "mixed"})
	never, always := "never", "always"
	a.config.Directories = []DirectoryRule{
		{ID: "one", Name: "first high numeric priority", Priority: 99, Enabled: true, SourcePath: one, LibraryPath: target, RootID: "root", MediaType: "mixed", Mode: "hardlink", MonitorType: "monitor", OverwriteModeKnown: true, OverwriteMode: &never, Scraping: importFlag(false)},
		{ID: "two", Name: "second low numeric priority", Priority: 0, Enabled: true, SourcePath: two, LibraryPath: other, RootID: "root2", MediaType: "mixed", Mode: "copy", MonitorType: "monitor", OverwriteModeKnown: true, OverwriteMode: &always, Scraping: importFlag(false)},
	}
	return a, one, two, target, other
}

func TestActualDirectorySourceLargeSelectionUsesCompactPublicationReference(t *testing.T) {
	a, one, _, _, _ := ownedNativeSourceDirectories(t)
	ctx := context.Background()
	path := one
	for i := 0; i < 4; i++ {
		path = filepath.Join(path, strings.Repeat("x", 160))
	}
	input := filepath.Join(path, "Owned.2024.mkv")
	importWrite(t, input, []byte("one actual selected video"))
	// This is one actual selected file in a large native preview. Repeated
	// entries remain blocked duplicates; they are not claimed as 2,000 transfers.
	files := make([]ImportFileRequest, 2000)
	for i := range files {
		files[i].Path = input
	}
	p, e := a.previewImport(ctx, ImportRequest{MediaType: "movie", TMDBID: 123, RootID: "root", Mode: "hardlink", Renaming: importFlag(false), Scraping: importFlag(false), Files: files})
	if e != nil || len(p.Files) != 2000 || p.Files[0].Status != "ready" {
		t.Fatal("actual large preview", e)
	}
	var size int
	if e = a.Store.DB.QueryRow(`SELECT length(payload) FROM records WHERE kind='native_directory_selection' AND id=?`, p.ID).Scan(&size); e != nil || size <= 1<<20 {
		t.Fatal("source selection did not cross old operation boundary", size, e)
	}
	j, e := a.executeImport(ctx, p.ID, []string{p.Files[0].ID})
	if e != nil {
		t.Fatal(e)
	}
	j = qualityRunningJob(t, a, j)
	if e = a.runImport(ctx, j, p.ID, []string{p.Files[0].ID}); e != nil {
		t.Fatal("one actual transfer was rejected by duplicated selector size", e)
	}
	op, e := a.readDirectoryOverwriteOperation(ctx, p.ID+"/"+p.Files[0].ID)
	raw, _ := json.Marshal(op)
	if e != nil || len(raw) > 16<<10 || op.Policy.Selection == nil || op.Policy.Selection.Plan != p.ID {
		t.Fatal("publication did not use compact authenticated reference", len(raw), e)
	}
	t.Logf("actual selection ciphertext=%d bytes, compact published operation=%d bytes; exactly one ready file selected", size, len(raw))
	src, _ := snapshotPath(input)
	dst, e := snapshotPath(p.Files[0].Target)
	if e != nil || !sameInode(src, dst) {
		t.Fatal("real selected publication missing", e)
	}
}

func TestActualDirectorySourceManualDestinationOverridesSourceMatching(t *testing.T) {
	a, one, _, _, other := ownedNativeSourceDirectories(t)
	ctx := context.Background()
	input := filepath.Join(one, "Owned.2024.mkv")
	importWrite(t, input, []byte("real selected source"))
	target := filepath.Join(other, filepath.Base(input))
	importWrite(t, target, []byte("prior target"))
	request := ImportRequest{MediaType: "movie", TMDBID: 123, TargetPath: other, Mode: "hardlink", Renaming: importFlag(false), Scraping: importFlag(false), LibraryTypeFolder: importFlag(false), Files: []ImportFileRequest{{Path: input}}}
	p, e := a.previewImport(ctx, request)
	if e != nil || len(p.Files) != 1 || p.Files[0].Status != "ready" {
		t.Fatal(p, e)
	}
	var meta importMetadata
	if e = a.Store.Get(ctx, "import_meta", p.ID, &meta); e != nil {
		t.Fatal(e)
	}
	var auth nativeDirectoryAuthorization
	if e = a.readQualitySealed(ctx, a.Store.DB, "native_directory_selection", p.ID, &auth); e != nil {
		t.Fatal(e)
	}
	if meta.Overwrite == nil || meta.Overwrite.DirectoryID != "two" || meta.Overwrite.Selection == nil || auth.Selection == nil || auth.Selection.Query.SourcePath != nil || auth.Selection.Query.DestPath == nil || *auth.Selection.Query.DestPath != other || p.Files[0].Method != "hardlink" || p.RootID != "root2" {
		t.Fatal("manual source/destination or independent method changed", p, meta)
	}
	j := overwriteStart(t, a, p)
	if e = a.runImport(ctx, j, p.ID, nil); e != nil {
		t.Fatal("real manual destination rule overwrite", e)
	}
	src, _ := snapshotPath(input)
	dst, e := snapshotPath(target)
	if e != nil || !sameInode(src, dst) {
		t.Fatal("explicit hardlink was coupled to selected rule copy mode", e)
	}
}

func TestActualDirectorySourceCohortOrderAndSameDiskObservations(t *testing.T) {
	a, one, _, _, _ := ownedNativeSourceDirectories(t)
	ctx := context.Background()
	input := filepath.Join(one, "Owned.2024.mkv")
	outside := filepath.Join(filepath.Dir(one), "outside", "Owned.2024.mkv")
	importWrite(t, input, []byte("inside"))
	importWrite(t, outside, []byte("outside"))
	request := ImportRequest{MediaType: "movie", TMDBID: 123, Files: []ImportFileRequest{{Path: input}}}
	proof, r, e := a.selectNativeImportDirectory(ctx, request)
	if e != nil || r == nil || r.ID != "one" || len(proof.Disks) != 1 || !proof.Disks[0].SourceExists || !proof.Disks[0].TargetExists || proof.Disks[0].SourceDevice != proof.Disks[0].TargetDevice {
		t.Fatal("actual source cohort/disk observation", r, proof, e)
	}
	request.Files[0].Path = outside
	fallback, r, e := a.selectNativeImportDirectory(ctx, request)
	if e != nil || r == nil || r.ID != "one" || fallback.Method != "get_dir" {
		t.Fatal("fallback sorted by priority instead of source configuration order", r, e)
	}
	a.config.Directories[0].MediaType = "unrecognized-source-type"
	request.Files[0].Path = input
	if proof, r, e = a.selectNativeImportDirectory(ctx, request); e != nil || proof == nil || proof.DirectoryID != "" || r != nil {
		t.Fatal("unknown original type became wildcard", r, e)
	}
	a.config.Directories[0].MediaType = "tv"
	request.Files[0].Path = input
	if proof, r, e = a.selectNativeImportDirectory(ctx, request); e != nil || proof == nil || proof.DirectoryID != "" || r != nil {
		t.Fatal("filtered source cohort incorrectly fell back to unrelated rule", r, e)
	}
	a.config.Directories[0].MediaType = "mixed"
	a.config.Directories[0], a.config.Directories[1] = a.config.Directories[1], a.config.Directories[0]
	if e = a.guardNativeDirectorySelection(fallback); e == nil {
		t.Fatal("configuration reorder retained stale selection authorization")
	}
}

func TestActualDirectorySourceNoMatchStillFreezesSourceInputs(t *testing.T) {
	a, one, _, target, _ := ownedNativeSourceDirectories(t)
	a.config.Directories[0].Enabled = false
	a.config.Directories[0].MonitorType = "none"
	input := filepath.Join(one, "Owned.2024.mkv")
	importWrite(t, input, []byte("actual"))
	ctx := context.Background()
	p, e := a.previewImport(ctx, ImportRequest{MediaType: "movie", TMDBID: 123, TargetPath: target, Mode: "hardlink", Renaming: importFlag(false), Scraping: importFlag(false), Files: []ImportFileRequest{{Path: input}}})
	if e != nil {
		t.Fatal(e)
	}
	var meta importMetadata
	if e = a.Store.Get(ctx, "import_meta", p.ID, &meta); e != nil || meta.Overwrite == nil || meta.Overwrite.Selection == nil || meta.Overwrite.Selection.DirectoryID != "" {
		t.Fatal("source no-match became unknown/no provenance", meta, e)
	}
	a.config.Directories[0].Enabled = true
	a.config.Directories[0].MonitorType = "monitor"
	j, e := a.executeImport(ctx, p.ID, nil)
	if e == nil {
		j = qualityRunningJob(t, a, j)
		e = a.runImport(ctx, j, p.ID, nil)
	}
	if pluginErrorCode(e) != "REVIEW_DIRECTORY_CHANGED" {
		t.Fatal("new matching rule did not invalidate no-match decision", e)
	}
}

func TestActualDirectorySourceUnsortedRuleDoesNotGrantManualOverwrite(t *testing.T) {
	a, one, _, target, _ := ownedNativeSourceDirectories(t)
	a.config.Directories[0].MonitorType = "none"
	a.config.Directories[0].Enabled = false
	mode := "always"
	a.config.Directories[0].OverwriteMode = &mode
	input := filepath.Join(one, "Owned.2024.mkv")
	importWrite(t, input, []byte("source"))
	importWrite(t, filepath.Join(target, filepath.Base(input)), []byte("old target"))
	p, e := a.previewImport(context.Background(), ImportRequest{MediaType: "movie", TMDBID: 123, RootID: "root", Mode: "hardlink", Renaming: importFlag(false), Scraping: importFlag(false), Files: []ImportFileRequest{{Path: input}}})
	if e != nil || len(p.Files) != 1 || p.Files[0].Status != "ready" {
		t.Fatal(p, e)
	}
	var meta importMetadata
	if e = a.Store.Get(context.Background(), "import_meta", p.ID, &meta); e != nil || meta.Overwrite == nil || meta.Overwrite.DirectoryID != "" || meta.Overwrite.Policy.Mode == nil || *meta.Overwrite.Policy.Mode != "never" {
		t.Fatal("original unsorted exclusion/custom never changed", meta, e)
	}
	j := overwriteStart(t, a, p)
	if e = a.runImport(context.Background(), j, p.ID, nil); pluginErrorCode(e) != "TRANSFER_OVERWRITE_DENIED" {
		t.Fatal("unsorted rule granted original overwrite", e)
	}
	if b, e := os.ReadFile(filepath.Join(target, filepath.Base(input))); e != nil || string(b) != "old target" {
		t.Fatal("denied manual changed target", e)
	}
}

func TestActualDirectorySourceSealedSelectionPreservesLargePriority(t *testing.T) {
	a, one, _, _, _ := ownedNativeSourceDirectories(t)
	a.config.Directories[0].Priority = 9007199254740993
	input := filepath.Join(one, "Owned.2024.mkv")
	importWrite(t, input, []byte("actual"))
	ctx := context.Background()
	p, _, e := a.selectNativeImportDirectory(ctx, ImportRequest{MediaType: "movie", TMDBID: 123, Files: []ImportFileRequest{{Path: input}}})
	if e != nil {
		t.Fatal(e)
	}
	if e = a.Store.Put(ctx, "owned_selection", "main", p); e != nil {
		t.Fatal(e)
	}
	var cold nativeDirectorySelection
	if e = a.Store.Get(ctx, "owned_selection", "main", &cold); e != nil {
		t.Fatal(e)
	}
	if e = a.guardNativeDirectorySelection(&cold); e != nil || cold.Rule.Values["priority"] != json.Number("9007199254740993") {
		t.Fatal("persisted native source integer changed", cold.Rule.Values["priority"], e)
	}
}

func TestActualDirectorySourceWatcherOwnsPrivateTargetDirectory(t *testing.T) {
	a, one, _, _, _ := ownedNativeSourceDirectories(t)
	input := filepath.Join(one, "Owned.2024.mkv")
	importWrite(t, input, []byte("actual"))
	ctx := context.Background()
	r, e := a.directoryFileIdentity(ctx, a.config.Directories[0], importScanFile{Path: input, InferredMedia: &importInferredMedia{Type: "movie", ID: 123}})
	if e != nil {
		t.Fatal(e)
	}
	p, rule, e := a.selectNativeImportDirectory(ctx, r)
	if e != nil || p == nil || p.Method != "target_directory" || rule == nil || rule.ID != "one" {
		t.Fatal("actual watcher lost explicit target_directory", p, rule, e)
	}
	var wire ImportRequest
	if e = json.Unmarshal([]byte(`{"directoryRule":{"ID":"two"},"directorySelection":{"DirectoryID":"two"}}`), &wire); e != nil {
		t.Fatal(e)
	}
	if wire.directoryRule != nil || wire.directorySelection != nil {
		t.Fatal("wire JSON fabricated original target_directory authority")
	}
}

func TestActualDirectorySourceAuthorizationRejectsPolicyAndMissingProofBeforeMutation(t *testing.T) {
	for _, mode := range []string{"delete_auth", "plaintext_auth", "policy", "remove_selection", "remove_policy", "method", "reorder"} {
		t.Run(mode, func(t *testing.T) {
			a, one, _, _, target := ownedNativeSourceDirectories(t)
			ctx := context.Background()
			input := filepath.Join(one, "Owned.2024.mkv")
			importWrite(t, input, []byte("new source"))
			output := filepath.Join(target, filepath.Base(input))
			importWrite(t, output, []byte("original target unchanged"))
			before, _ := snapshotPath(output)
			p, e := a.previewImport(ctx, ImportRequest{MediaType: "movie", TMDBID: 123, TargetPath: target, Mode: "hardlink", Renaming: importFlag(false), Scraping: importFlag(false), Files: []ImportFileRequest{{Path: input}}})
			if e != nil || p.DirectorySourceVersion != 2 {
				t.Fatal(p, e)
			}
			var meta importMetadata
			if e = a.Store.Get(ctx, "import_meta", p.ID, &meta); e != nil {
				t.Fatal(e)
			}
			switch mode {
			case "delete_auth":
				_, e = a.Store.DB.Exec(`DELETE FROM records WHERE kind='native_directory_selection' AND id=?`, p.ID)
			case "plaintext_auth":
				e = a.Store.Put(ctx, "native_directory_selection", p.ID, map[string]any{"Version": 1, "Plan": p.ID})
			case "policy":
				v := "never"
				meta.Overwrite.Policy.Mode = &v
			case "remove_selection":
				meta.Overwrite.Selection = nil
			case "remove_policy":
				meta.Overwrite = nil
			case "method":
				meta.Methods[p.Files[0].ID] = "copy"
			case "reorder":
				a.config.Directories[0], a.config.Directories[1] = a.config.Directories[1], a.config.Directories[0]
			}
			if e != nil {
				t.Fatal(e)
			}
			if mode == "policy" || mode == "remove_selection" || mode == "remove_policy" || mode == "method" {
				if e = a.Store.Put(ctx, "import_meta", p.ID, meta); e != nil {
					t.Fatal(e)
				}
			}
			j, e := a.executeImport(ctx, p.ID, nil)
			if e == nil {
				j = qualityRunningJob(t, a, j)
				e = a.runImport(ctx, j, p.ID, nil)
			}
			if e == nil {
				t.Fatal("altered ordinary selection authorized mutation")
			}
			after, se := snapshotPath(output)
			if se != nil || before != after {
				t.Fatal("rejected source selection changed target", se)
			}
			if b, se := os.ReadFile(output); se != nil || string(b) != "original target unchanged" {
				t.Fatal("rejected selection changed bytes", se)
			}
			var assets int
			if e = a.Store.DB.QueryRow(`SELECT count(*) FROM records WHERE kind='asset'`).Scan(&assets); e != nil || assets != 0 {
				t.Fatal("rejected source selection registered asset", assets, e)
			}
		})
	}
}

func TestActualDirectorySourceDoesNotInventUnrelatedCategoryDependency(t *testing.T) {
	a, one, _, _, _ := ownedNativeSourceDirectories(t)
	a.config.Directories[1].MediaType = "tv"
	a.config.Directories[1].MediaCategory = "unrelated TV category"
	input := filepath.Join(one, "Owned.2024.mkv")
	importWrite(t, input, []byte("actual"))
	ctx := context.Background()
	p, r, e := a.selectNativeImportDirectory(ctx, ImportRequest{MediaType: "movie", TMDBID: 123, Files: []ImportFileRequest{{Path: input}}})
	if e != nil || p == nil || r == nil || r.ID != "one" || p.Query.Media.CategoryKnown {
		t.Fatal("source never read unrelated category", r, e)
	}
}

func TestActualDirectorySourceChangedSameDiskEvidenceStopsPendingPublication(t *testing.T) {
	a, one, _, target, _ := ownedNativeSourceDirectories(t)
	ctx := context.Background()
	input := filepath.Join(filepath.Dir(one), "outside", "Owned.2024.mkv")
	importWrite(t, input, []byte("actual source"))
	p, e := a.previewImport(ctx, ImportRequest{MediaType: "movie", TMDBID: 123, Mode: "hardlink", Renaming: importFlag(false), Scraping: importFlag(false), Files: []ImportFileRequest{{Path: input}}})
	if e != nil || p.RootID != "root" {
		t.Fatal(p, e)
	}
	if e = os.Rename(one, one+"-moved-after-selection"); e != nil {
		t.Fatal(e)
	}
	j, e := a.executeImport(ctx, p.ID, nil)
	if e == nil {
		j = qualityRunningJob(t, a, j)
		e = a.runImport(ctx, j, p.ID, nil)
	}
	if pluginErrorCode(e) != "REVIEW_DIRECTORY_CHANGED" {
		t.Fatal("changed actual source disk selection still authorized", e)
	}
	if _, e = os.Lstat(filepath.Join(target, filepath.Base(input))); !os.IsNotExist(e) {
		t.Fatal("stale same-disk source choice published", e)
	}
}
