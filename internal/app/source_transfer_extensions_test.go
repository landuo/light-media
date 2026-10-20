package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"light-media/internal/fileextensions"
	"light-media/internal/transferextensions"
)

func ownedExtensionConfig(a *App, media, subtitle, audio []string) {
	a.config.RMTMediaExt, _ = json.Marshal(media)
	a.config.RMTSubExt, _ = json.Marshal(subtitle)
	a.config.RMTAudioExt, _ = json.Marshal(audio)
}
func TestActualTransferConstructorAndLiveHandlerAreSeparate(t *testing.T) {
	a, source, _ := importTestApp(t)
	ctx := context.Background()
	ownedExtensionConfig(a, []string{".old"}, []string{".subs"}, []string{".au"})
	initial, e := a.transferFileExtensions(ctx)
	if e != nil {
		t.Fatal(e)
	}
	ownedExtensionConfig(a, []string{".new"}, []string{".nextsubs"}, []string{})
	_, observed, e := a.observeTransferExtensions(ctx)
	if e != nil {
		t.Fatal(e)
	}
	if !reflect.DeepEqual(initial, observed.Constructor) || !reflect.DeepEqual(observed.Handler.Values(fileextensions.MediaKey), []string{".new"}) {
		t.Fatal("constructor accidentally refreshed", observed)
	}
	for _, name := range []string{"One.old", "Two.new"} {
		importWrite(t, filepath.Join(source, name), []byte("actual scan input"))
	}
	result, e := a.scanFiles(ctx, source, false)
	if e != nil {
		t.Fatal(e)
	}
	files := result.(map[string]any)["files"].([]importScanFile)
	if len(files) != 1 || filepath.Ext(files[0].Path) != ".old" {
		t.Fatal("scan did not use actual constructor", files)
	}
	if e = a.saveSettings(ctx, a.Settings()); e != nil {
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
	fresh, e := cold.transferFileExtensions(ctx)
	if e != nil || !reflect.DeepEqual(fresh.Values(fileextensions.MediaKey), []string{".new"}) {
		t.Fatal("new native lifecycle reused previous constructor", fresh, e)
	}
}
func TestActualTransferDefaultAndCustomMediaExtraPublish(t *testing.T) {
	for _, custom := range []bool{false, true} {
		t.Run(map[bool]string{false: "source-defaults", true: "native-custom"}[custom], func(t *testing.T) {
			a, one, _, _, _ := ownedNativeSourceDirectories(t)
			ctx := context.Background()
			ext, sub, audio := ".iso", ".srt", ".flac"
			if custom {
				ext, sub, audio = ".video", ".subs", ".au"
				ownedExtensionConfig(a, []string{ext}, []string{sub}, []string{audio})
			}
			input := filepath.Join(one, "Owned.2024"+ext)
			subtitle := filepath.Join(one, "Owned.2024.zh-cn"+sub)
			sound := filepath.Join(one, "Owned.2024"+audio)
			unrelated := filepath.Join(one, "Owned.2024.random"+sub)
			ignored := filepath.Join(one, "Owned.2024.zh.vtt")
			for p, body := range map[string]string{input: "actual media bytes", subtitle: "actual subtitle bytes", sound: "actual audio bytes", unrelated: "unrelated stem", ignored: "not in original default list"} {
				importWrite(t, p, []byte(body))
			}
			if !custom {
				importWrite(t, filepath.Join(one, "Other.strm"), []byte("stream"))
			}
			scanned, e := a.scanFiles(ctx, one, false)
			if e != nil {
				t.Fatal(e)
			}
			want := 1
			if !custom {
				want = 2
			}
			if got := len(scanned.(map[string]any)["files"].([]importScanFile)); got != want {
				t.Fatal("actual custom/default scan", got, want)
			}
			p, e := a.previewImport(ctx, ImportRequest{MediaType: "movie", TMDBID: 123, RootID: "root", Mode: "hardlink", Renaming: importFlag(false), Scraping: importFlag(false), Files: []ImportFileRequest{{Path: input}}})
			if e != nil || len(p.Files) != 1 || p.Files[0].Status != "ready" {
				t.Fatal("actual preview", p, e)
			}
			var meta importMetadata
			var auth nativeDirectoryAuthorization
			if e = a.Store.Get(ctx, "import_meta", p.ID, &meta); e != nil {
				t.Fatal(e)
			}
			if e = a.readQualitySealed(ctx, a.Store.DB, "native_directory_selection", p.ID, &auth); e != nil {
				t.Fatal(e)
			}
			if p.DirectorySourceVersion != 2 || auth.Version != 2 || auth.Extensions == nil || !validTransferExtensionObservation(auth.Extensions) || meta.ExtensionInputSHA != auth.Extensions.InputSHA || len(meta.Sidecars[p.Files[0].ID]) != 2 {
				t.Fatal("real classifier proof/association missing", auth, meta)
			}
			j := overwriteStart(t, a, p)
			if e = a.runImport(ctx, j, p.ID, nil); e != nil {
				t.Fatal("actual publication", e)
			}
			for _, side := range meta.Sidecars[p.Files[0].ID] {
				src, e := snapshotPath(side.Source)
				if e != nil {
					t.Fatal(e)
				}
				dst, e := snapshotPath(side.Target)
				if e != nil || !sameInode(src, dst) {
					t.Fatal("extra did not use actual hardlink", side, e)
				}
				var owned managedFile
				if e = a.Store.Get(ctx, "managed_file", tokenHash(side.Target), &owned); e != nil || owned.Kind != sidecarMaterialKind(side) {
					t.Fatal("actual audio/subtitle ownership", owned, e)
				}
			}
			if _, e = os.Stat(filepath.Join(filepath.Dir(p.Files[0].Target), filepath.Base(unrelated))); !errors.Is(e, os.ErrNotExist) {
				t.Fatal("unrelated extra was transferred", e)
			}
		})
	}
}
func TestActualTransferExtensionChangesRejectNewPublication(t *testing.T) {
	for _, tamper := range []bool{false, true} {
		t.Run(map[bool]string{false: "current-handler-changed", true: "auth-cipher-tampered"}[tamper], func(t *testing.T) {
			a, one, _, _, _ := ownedNativeSourceDirectories(t)
			ctx := context.Background()
			ownedExtensionConfig(a, []string{".video"}, []string{}, []string{})
			input := filepath.Join(one, "Owned.2024.video")
			importWrite(t, input, []byte("actual unchanged source"))
			p, e := a.previewImport(ctx, ImportRequest{MediaType: "movie", TMDBID: 123, RootID: "root", Mode: "hardlink", Renaming: importFlag(false), Scraping: importFlag(false), Files: []ImportFileRequest{{Path: input}}})
			if e != nil {
				t.Fatal(e)
			}
			if tamper {
				if _, e = a.Store.DB.Exec(`UPDATE records SET payload='{}' WHERE kind='native_directory_selection' AND id=?`, p.ID); e != nil {
					t.Fatal(e)
				}
			} else {
				a.config.RMTMediaExt = json.RawMessage(`[".different"]`)
			}
			j, e := a.executeImport(ctx, p.ID, nil)
			if e == nil {
				j = qualityRunningJob(t, a, j)
				e = a.runImport(ctx, j, p.ID, nil)
			}
			if e == nil {
				t.Fatal("changed extension authority accepted")
			}
			if _, e = os.Stat(p.Files[0].Target); !errors.Is(e, os.ErrNotExist) {
				t.Fatal("published under changed classifier", e)
			}
		})
	}
}
func TestActualTransferConstructorSnapshotCloneAndCanonicalColdProof(t *testing.T) {
	a, _, _ := importTestApp(t)
	ctx := context.Background()
	t.Setenv(fileextensions.MediaKey, " [\".video\"] ")
	_, p, e := a.observeTransferExtensions(ctx)
	if e != nil {
		t.Fatal(e)
	}
	raw, e := json.Marshal(p)
	if e != nil {
		t.Fatal(e)
	}
	var cold nativeTransferExtensionObservation
	if e = cleanupJournalStrict(raw, &cold); e != nil {
		t.Fatal(e)
	}
	if !validTransferExtensionObservation(&cold) || a.guardTransferExtensionObservation(ctx, &cold) != nil {
		t.Fatal("canonical cold proof lost observed environment", cold)
	}
	p.Constructor.Fields[fileextensions.MediaKey] = fileextensions.Field{}
	fresh, e := a.transferFileExtensions(ctx)
	if e != nil || !fresh.Valid() {
		t.Fatal("caller changed cached instance", e)
	}
	c, e := transferextensions.New(fresh)
	if e != nil {
		t.Fatal(e)
	}
	ok, e := c.Media(transferextensions.PathFile("Source.video"))
	if e != nil || !ok {
		t.Fatal(e)
	}
}

func TestActualTransferLatestUsesLiveConfiguredMediaCandidates(t *testing.T) {
	a, source, target := importTestApp(t)
	importFixture(t, a, "movie")
	ctx := context.Background()
	ownedExtensionConfig(a, []string{".video", ".mkv"}, []string{".subs"}, []string{".au"})
	if _, e := a.transferFileExtensions(ctx); e != nil {
		t.Fatal(e)
	}
	// The source constructor stays old. Handler latest candidates use current RMT.
	a.config.RMTMediaExt = json.RawMessage(`[".video"]`)
	r := overwriteRule(a, source, target, "latest", true)
	r.Renaming = importFlag(false)
	a.config.Directories = []DirectoryRule{r}
	input := filepath.Join(source, "New.Movie.2024.video")
	importWrite(t, input, []byte("actual custom video"))
	old := filepath.Join(target, "Old.Movie.2024.video")
	protected := filepath.Join(target, "Old.Movie.2024.mkv")
	extra := filepath.Join(target, "Old.Movie.2024.au")
	for _, path := range []string{old, protected, extra} {
		importWrite(t, path, []byte("old material"))
	}
	p, e := a.previewImport(ctx, ImportRequest{MediaType: "movie", TMDBID: 123, RootID: "root", Mode: "hardlink", Renaming: importFlag(false), Scraping: importFlag(false), Files: []ImportFileRequest{{Path: input}}})
	if e != nil || p.Files[0].Status != "ready" {
		t.Fatal(p, e)
	}
	j := overwriteStart(t, a, p)
	if e = a.runImport(ctx, j, p.ID, nil); e != nil {
		t.Fatal(e)
	}
	if _, e = os.Stat(old); !errors.Is(e, os.ErrNotExist) {
		t.Fatal("custom latest candidate not removed", e)
	}
	for _, path := range []string{protected, extra} {
		if b, e := os.ReadFile(path); e != nil || string(b) != "old material" {
			t.Fatal("latest used cached/default list instead of current source list", path, e)
		}
	}
	op, e := a.readDirectoryOverwriteOperation(ctx, p.ID+"/"+p.Files[0].ID)
	if e != nil || len(op.Targets) != 1 || op.Targets[0].Path != old {
		t.Fatal("not real exact custom candidate receipt", op, e)
	}
}

func TestActualTransferAudioSQLFailureColdRecovery(t *testing.T) {
	a, source, target := importTestApp(t)
	importFixture(t, a, "movie")
	ctx := context.Background()
	ownedExtensionConfig(a, []string{".video"}, []string{".subs"}, []string{".au"})
	overwriteRule(a, source, target, "always", true)
	if e := a.saveSettings(ctx, a.Settings()); e != nil {
		t.Fatal(e)
	}
	input := filepath.Join(source, "Owned.Movie.2024.video")
	audio := filepath.Join(source, "Owned.Movie.2024.au")
	importWrite(t, input, []byte("real cold media"))
	importWrite(t, audio, []byte("real cold audio"))
	p, e := a.previewImport(ctx, ImportRequest{MediaType: "movie", TMDBID: 123, RootID: "root", Mode: "hardlink", Renaming: importFlag(false), Scraping: importFlag(false), Files: []ImportFileRequest{{Path: input}}})
	if e != nil || p.Files[0].Status != "ready" {
		t.Fatal(p, e)
	}
	var meta importMetadata
	if e = a.Store.Get(ctx, "import_meta", p.ID, &meta); e != nil || len(meta.Sidecars[p.Files[0].ID]) != 1 {
		t.Fatal(meta, e)
	}
	j := overwriteStart(t, a, p)
	if _, e = a.Store.DB.Exec(`CREATE TRIGGER owned_audio_register_failure BEFORE INSERT ON records WHEN NEW.kind='asset' BEGIN SELECT RAISE(ABORT,'owned audio transaction failure'); END`); e != nil {
		t.Fatal(e)
	}
	if e = a.runImport(ctx, j, p.ID, nil); e == nil {
		t.Fatal("actual SQL failure ignored")
	}
	paths := []string{p.Files[0].Target, meta.Sidecars[p.Files[0].ID][0].Target}
	stamps := []importSnapshot{}
	for _, path := range paths {
		s, e := snapshotPath(path)
		if e != nil {
			t.Fatal("actual publication missing", path, e)
		}
		stamps = append(stamps, s)
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
	if _, e = cold.Store.DB.Exec(`DROP TRIGGER owned_audio_register_failure`); e != nil {
		t.Fatal(e)
	}
	publications := 0
	cold.importSourceLink = func(int, string, int, string, int) error {
		publications++
		return errors.New("known publication must not relink")
	}
	j = overwriteStart(t, cold, p)
	if e = cold.runImport(ctx, j, p.ID, nil); e != nil {
		t.Fatal("real cold registration", e)
	}
	if publications != 0 {
		t.Fatal("known media/audio publication repeated", publications)
	}
	for i, path := range paths {
		s, e := snapshotPath(path)
		if e != nil || s != stamps[i] {
			t.Fatal("known file replaced during recovery", path, e)
		}
	}
	var owned managedFile
	if e = cold.Store.Get(ctx, "managed_file", tokenHash(paths[1]), &owned); e != nil || owned.Kind != "audio" {
		t.Fatal("cold audio owner missing", owned, e)
	}
	// A known fact stays readable after a new live-handler config arrives. This
	// does not grant quarantine/cleanup/new publication under the stale snapshot.
	cold.config.RMTMediaExt = json.RawMessage(`[".different"]`)
	op, e := cold.readDirectoryOverwriteOperation(ctx, p.ID+"/"+p.Files[0].ID)
	if e != nil {
		t.Fatal(e)
	}
	if known, e := cold.knownDirectoryOverwritePublication(ctx, &op); e != nil || !known {
		t.Fatal("current config erased actual published fact", e)
	}
	if e = cold.guardDirectoryOverwriteExtensionPolicy(ctx, op); e == nil {
		t.Fatal("known fact became new mutation authority")
	}
}

func TestInstalledTransferExtraAssociation(t *testing.T) {
	var oracle struct {
		Tags  []string `json:"subtitle_stem_tags"`
		Cases []struct {
			Mains    []string
			Extra    string
			Expected []string
		} `json:"association_cases"`
	}
	raw, e := os.ReadFile("../transferextensions/testdata/source-oracle.json")
	if e != nil {
		t.Fatal(e)
	}
	if e = json.Unmarshal(raw, &oracle); e != nil {
		t.Fatal(e)
	}
	if len(oracle.Tags) != len(sourceSubtitleStemTags) {
		t.Fatal("source tags missing")
	}
	for _, tag := range oracle.Tags {
		if !sourceSubtitleStemTags[tag] {
			t.Fatal("tag", tag)
		}
	}
	in := fileextensions.Inputs{Namespace: "oracle", SourceID: "actual-association", EnvironmentKnown: true, ConfigurationKnown: true, Configuration: map[string]json.RawMessage{fileextensions.SubtitleKey: json.RawMessage(`[".srt",".ass",".ssa",".sup",".subs"]`), fileextensions.AudioKey: json.RawMessage(`[".flac",".au"]`)}}
	snapshot, e := fileextensions.Resolve(in)
	if e != nil {
		t.Fatal(e)
	}
	classifier, e := transferextensions.New(snapshot)
	if e != nil {
		t.Fatal(e)
	}
	for i, tc := range oracle.Cases {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			main, ok := sourceRelatedMainName(tc.Extra, tc.Mains, classifier)
			if len(tc.Expected) == 0 {
				if ok {
					t.Fatal("unexpected match", main)
				}
			} else if !ok || len(tc.Expected) != 2 || filepath.Base(tc.Expected[1]) != main {
				t.Fatal("source mismatch", main, ok, tc.Expected)
			}
		})
	}
}

func TestSubscriptionQualityActualCustomAudioUsesFrozenExtensionSnapshots(t *testing.T) {
	qualityActualMovieWorkflow(t, "custom_audio", func(a *App, _ Subscription) {
		ctx := context.Background()
		raw, e := a.Store.List(ctx, "asset", 20, 0)
		if e != nil {
			t.Fatal(e)
		}
		found := false
		for _, r := range raw {
			var asset Asset
			if e = json.Unmarshal(r, &asset); e != nil {
				t.Fatal(e)
			}
			if asset.IntentID == "" || asset.State != "present" {
				continue
			}
			found = true
			target := strings.TrimSuffix(asset.Path, filepath.Ext(asset.Path)) + ".audio"
			b, e := os.ReadFile(target)
			if e != nil || string(b) != "new actual custom audio" {
				t.Fatal("actual quality replacement audio", e)
			}
			var owned managedFile
			if e = a.Store.Get(ctx, "managed_file", tokenHash(target), &owned); e != nil || owned.AssetID != asset.ID || owned.Kind != "audio" {
				t.Fatal("quality audio owner", owned, e)
			}
			var intent Intent
			if e = a.Store.Get(ctx, "intent", asset.IntentID, &intent); e != nil {
				t.Fatal(e)
			}
			var auth nativeDirectoryAuthorization
			if e = a.readQualitySealed(ctx, a.Store.DB, "native_directory_selection", intent.ImportPlanID, &auth); e != nil || auth.Extensions == nil || auth.Version != 2 {
				t.Fatal("actual download import lacks dual extension receipt", e)
			}
		}
		if !found {
			t.Fatal("actual quality workflow did not produce replacement")
		}
	})
}
