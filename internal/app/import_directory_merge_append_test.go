package app

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"light-media/internal/catalog"
)

func appendSourceDirectory(t *testing.T, a *App, base, name string, contents map[string][]byte) string {
	t.Helper()
	source := filepath.Join(base, name)
	for relative, data := range contents {
		importWrite(t, filepath.Join(source, relative), data)
	}
	return source
}

func submitDirectoryImport(t *testing.T, a *App, source, mode string, tmdbID int64) (Job, ImportPlan, string) {
	t.Helper()
	request := manualOrganizeRequest{Paths: []string{source}, TargetPath: a.Settings().Roots[0].Path, Mode: mode, MediaType: "movie", TMDBID: tmdbID, Scraping: importFlag(false), Preview: true}
	preview, err := a.manualOrganize(t.Context(), request)
	if err != nil || preview.Failed != 0 || len(preview.Items) != 1 {
		t.Fatal("directory preview", preview, err)
	}
	request.Preview = false
	out, err := a.manualOrganize(t.Context(), request)
	if err != nil || out.Failed != 0 || out.Submitted != 1 || len(out.Items) != 1 {
		t.Fatal("directory submission", out, err)
	}
	var plan ImportPlan
	if err = a.Store.Get(t.Context(), "import_plan", out.Items[0].PlanID, &plan); err != nil {
		t.Fatal(err)
	}
	if _, err = a.Store.DB.Exec(`UPDATE jobs SET state='running',generation=generation+1 WHERE id=?`, out.Items[0].JobID); err != nil {
		t.Fatal(err)
	}
	job, err := scanJob(a.Store.DB.QueryRow(`SELECT `+jobColumns+` FROM jobs WHERE id=?`, out.Items[0].JobID))
	if err != nil {
		t.Fatal(err)
	}
	return job, plan, preview.Items[0].Target
}

func historyRowForSource(t *testing.T, a *App, source string) transferHistoryItem {
	t.Helper()
	out, err := a.transferHistory(t.Context(), httptest.NewRequest("GET", "/history/transfers?page=1&page_size=50", nil))
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(out)
	var page struct {
		Items []transferHistoryItem `json:"items"`
	}
	if err = json.Unmarshal(raw, &page); err != nil {
		t.Fatal(err)
	}
	for _, item := range page.Items {
		if item.Source == source {
			return item
		}
	}
	t.Fatal("history row missing for source", source)
	return transferHistoryItem{}
}

func TestDirectoryMergeAppendsIntoOwnedRootAndKeepsPriorMembers(t *testing.T) {
	for _, mode := range []string{"copy", "hardlink"} {
		t.Run(mode, func(t *testing.T) {
			testDirectoryMergeAppendMode(t, mode)
		})
	}
}

func testDirectoryMergeAppendMode(t *testing.T, mode string) {
	a, _, plan, job, fixture := prepareSharedDirectoryImport(t, "copy")
	if err := a.runImport(t.Context(), job, plan.ID, nil); err != nil {
		t.Fatal("initial owned publication", err)
	}
	if _, err := a.finishJob(t.Context(), job, "succeeded", "", "", 0); err != nil {
		t.Fatal("initial job finish", err)
	}
	if err := a.Store.Get(t.Context(), "import_plan", plan.ID, &plan); err != nil {
		t.Fatal(err)
	}
	var first Asset
	if err := a.Store.Get(t.Context(), "asset", plan.Files[0].AssetID, &first); err != nil {
		t.Fatal("initial asset", err)
	}
	if first.Path != fixture.Target || first.State != "present" {
		t.Fatal("initial asset record", first)
	}
	firstHistory := historyRowForSource(t, a, fixture.Source)
	if firstHistory.Target != fixture.Target || firstHistory.Status != "success" {
		t.Fatal("initial history", firstHistory)
	}

	// The same media directory receives another source's member. MoviePilot
	// simply publishes the planned member and keeps the entries already there.
	source := appendSourceDirectory(t, a, filepath.Dir(fixture.Source), "Owned.2024.Extras", map[string][]byte{"BDMV/STREAM/00002.m2ts": []byte("actual appended extra")})
	if err := os.Mkdir(filepath.Join(source, "CERTIFICATE"), 0750); err != nil {
		t.Fatal(err)
	}
	secondJob, secondPlan, target := submitDirectoryImport(t, a, source, mode, 123)
	if target != fixture.Target {
		t.Fatal("append target differs", target, fixture.Target)
	}
	if err := a.runImport(t.Context(), secondJob, secondPlan.ID, nil); err != nil {
		t.Fatal("append publication", err)
	}
	if _, err := a.finishJob(t.Context(), secondJob, "succeeded", "", "", 0); err != nil {
		t.Fatal("append job finish", err)
	}

	appended := filepath.Join(fixture.Target, "BDMV/STREAM/00002.m2ts")
	if got, err := os.ReadFile(appended); err != nil || string(got) != "actual appended extra" {
		t.Fatal("appended member", err)
	}
	for relative, data := range fixture.Contents {
		if got, err := os.ReadFile(filepath.Join(fixture.Target, relative)); err != nil || string(got) != string(data) {
			t.Fatal("prior member changed", relative, err)
		}
	}
	var receipt importReceipt
	if err := a.Store.Get(t.Context(), "import_receipt", secondPlan.ID+"/"+secondPlan.Files[0].ID, &receipt); err != nil {
		t.Fatal(err)
	}
	if receipt.AppendAssetID != first.ID || receipt.SharedDirectory == nil {
		t.Fatalf("append receipt %#v", receipt)
	}
	if _, ok := receipt.SharedDirectory.Members["BDMV/STREAM/00001.m2ts"]; ok {
		t.Fatal("append claimed a member it did not publish")
	}
	if member, ok := receipt.SharedDirectory.Members["BDMV/STREAM/00002.m2ts"]; !ok || member.Snapshot.Size != int64(len("actual appended extra")) {
		t.Fatalf("append members %#v", receipt.SharedDirectory.Members)
	}
	var owner managedFile
	if err := a.Store.Get(t.Context(), "managed_file", tokenHash(fixture.Target), &owner); err != nil || owner.AssetID != first.ID {
		t.Fatal("owned root replaced", owner, err)
	}
	var after Asset
	if err := a.Store.Get(t.Context(), "asset", first.ID, &after); err != nil || after.Path != fixture.Target {
		t.Fatal("reused asset missing", err)
	}
	var assets int
	if err := a.Store.DB.QueryRow(`SELECT COUNT(*) FROM library_asset WHERE json_extract(payload,'$.path')=?`, fixture.Target).Scan(&assets); err != nil || assets != 1 {
		t.Fatal("duplicate directory asset", assets, err)
	}
	secondHistory := historyRowForSource(t, a, source)
	if secondHistory.Target != fixture.Target || secondHistory.Status != "success" {
		t.Fatal("append history", secondHistory)
	}
	if secondHistory.ID == firstHistory.ID {
		t.Fatal("different sources must keep independent history records")
	}

	// Deleting the appended history removes only what that plan published.
	if _, err := a.deleteTransferHistory(t.Context(), secondHistory, historyDeleteRequest{Confirm: true, DeleteTarget: true, FileID: secondHistory.executionFileID()}); err != nil {
		t.Fatal("append history deletion", err)
	}
	if _, err := os.Stat(appended); !os.IsNotExist(err) {
		t.Fatal("appended member survived its own history deletion", err)
	}
	for relative := range fixture.Contents {
		if _, err := os.Stat(filepath.Join(fixture.Target, relative)); err != nil {
			t.Fatal("prior member removed with another history", relative, err)
		}
	}
	if kept := historyRowForSource(t, a, fixture.Source); kept.ID != firstHistory.ID || kept.Status != "success" {
		t.Fatal("prior history lost", kept)
	}
}

func TestDirectoryMergeAppendRejectsDifferentMediaIdentity(t *testing.T) {
	a, _, plan, job, fixture := prepareSharedDirectoryImport(t, "copy")
	if err := a.runImport(t.Context(), job, plan.ID, nil); err != nil {
		t.Fatal(err)
	}
	receipt := importReceipt{ID: "append-mismatch", Source: filepath.Join(filepath.Dir(fixture.Source), "Other.2024.BluRay"), Target: fixture.Target, State: "planned", MediaKey: "movie|999"}
	if err := a.beginDirectoryMerge(t.Context(), &receipt, PlanFile{Source: receipt.Source, Target: fixture.Target, Method: "copy"}); err == nil {
		t.Fatal("different media identity appended into an owned directory")
	}
	var owner managedFile
	if err := a.Store.Get(t.Context(), "managed_file", tokenHash(fixture.Target), &owner); err != nil {
		t.Fatal(err)
	}
	if receipt.AppendAssetID != "" || receipt.SharedDirectory != nil {
		t.Fatal("rejected append mutated ownership", receipt)
	}
}

// Native softlink publication into an already-owned root: each member keeps its
// own entry proof, and the root asset is reused. The plugin transfer module's
// whole-tree adoption stays closed, so this drives the native publisher.
func TestDirectoryMergeAppendSoftlinkReusesOwnedRoot(t *testing.T) {
	a, source, library := importTestApp(t)
	first := filepath.Join(source, "Show.2024.BluRay")
	for relative, data := range map[string]string{"BDMV/STREAM/00001.m2ts": "first stream", "BDMV/index.bdmv": "first index"} {
		importWrite(t, filepath.Join(first, relative), []byte(data))
	}
	target := filepath.Join(library, "Show (2024)")
	file := PlanFile{ID: "disc-one", Source: first, Target: target, Method: "copy"}
	stamp, _, err := snapshotOriginalDirectory(first)
	if err != nil {
		t.Fatal(err)
	}
	file.DirectoryDigest, file.Device, file.Inode, file.ModNS, file.Size = stamp.Directory, stamp.Device, stamp.Inode, stamp.ModNS, stamp.Size
	receipt, err := a.publishOriginalDirectory(t.Context(), "append-native/one", file, func() error { return nil }, "movie:123")
	if err != nil || receipt.State != "published" || len(receipt.DirectoryCompleted) != 2 {
		t.Fatalf("initial native directory %+v %v", receipt, err)
	}
	rootSnapshot, err := snapshotTransferPath(target)
	if err != nil {
		t.Fatal(err)
	}
	owner := managedFile{Path: target, RootID: "root", AssetID: "asset_first", MediaKey: "movie:123", Kind: "video", Snapshot: receipt.StageSnapshot, SourceSnapshot: planSnapshot(file), SharedDirectory: receipt.SharedDirectory}
	if err = a.Store.Put(t.Context(), "managed_file", tokenHash(target), owner); err != nil {
		t.Fatal(err)
	}
	asset := Asset{ID: "asset_first", Media: catalog.Media{ID: 123, MediaType: "movie", Title: "Show"}, RootID: "root", Path: target, SourcePath: first, State: "present", CreatedAt: now()}
	if err = a.Store.Put(t.Context(), "asset", asset.ID, asset); err != nil {
		t.Fatal(err)
	}
	if sharedDirectoryIdentity(rootSnapshot) != sharedDirectoryIdentity(owner.Snapshot) {
		t.Fatalf("root identity drift %v %v", rootSnapshot, owner.Snapshot)
	}

	second := filepath.Join(source, "Show.2024.BluRay.Extras")
	importWrite(t, filepath.Join(second, "BDMV/STREAM/00002.m2ts"), []byte("second stream"))
	next := PlanFile{ID: "disc-two", Source: second, Target: target, Method: "softlink"}
	nextStamp, _, err := snapshotOriginalDirectory(second)
	if err != nil {
		t.Fatal(err)
	}
	next.DirectoryDigest, next.Device, next.Inode, next.ModNS, next.Size = nextStamp.Directory, nextStamp.Device, nextStamp.Inode, nextStamp.ModNS, nextStamp.Size
	appended, err := a.publishOriginalDirectory(t.Context(), "append-native/two", next, func() error { return nil }, "movie:123")
	if err != nil || appended.State != "published" {
		t.Fatalf("native softlink append %+v %v", appended, err)
	}
	if appended.AppendAssetID != asset.ID || appended.SharedDirectory == nil {
		t.Fatalf("append receipt %+v", appended)
	}
	if _, claimed := appended.SharedDirectory.Members["BDMV/STREAM/00001.m2ts"]; claimed {
		t.Fatal("append claimed prior member")
	}
	member, ok := appended.SharedDirectory.Members["BDMV/STREAM/00002.m2ts"]
	if !ok || member.Softlink == nil || member.Softlink.Source != filepath.Join(second, "BDMV/STREAM/00002.m2ts") {
		t.Fatalf("append member %#v", member)
	}
	link := filepath.Join(target, "BDMV/STREAM/00002.m2ts")
	if data, err := os.ReadFile(link); err != nil || string(data) != "second stream" {
		t.Fatalf("actual appended link %v", err)
	}
	if info, err := os.Lstat(link); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("appended member is not a link %v", err)
	}
	if data, err := os.ReadFile(filepath.Join(target, "BDMV/STREAM/00001.m2ts")); err != nil || string(data) != "first stream" {
		t.Fatalf("prior member changed %v", err)
	}
}

// MoviePilot rewrites destination files during a directory transfer. A member
// can only be replaced while it still matches its registered snapshot, so a
// drifted file is refused instead of silently taken over.
func TestDirectoryMergeReplacesOwnedMemberWithinSameAsset(t *testing.T) {
	a, _, plan, job, fixture := prepareSharedDirectoryImport(t, "copy")
	if err := a.runImport(t.Context(), job, plan.ID, nil); err != nil {
		t.Fatal("initial owned publication", err)
	}
	if _, err := a.finishJob(t.Context(), job, "succeeded", "", "", 0); err != nil {
		t.Fatal(err)
	}
	if err := a.Store.Get(t.Context(), "import_plan", plan.ID, &plan); err != nil {
		t.Fatal(err)
	}
	firstAsset := plan.Files[0].AssetID
	updated := map[string][]byte{"BDMV/STREAM/00001.m2ts": []byte("replacement stream"), "BDMV/index.bdmv": []byte("replacement index")}
	source := appendSourceDirectory(t, a, filepath.Dir(fixture.Source), "Owned.2024.BluRay.v2", updated)
	if err := os.Mkdir(filepath.Join(source, "CERTIFICATE"), 0750); err != nil {
		t.Fatal(err)
	}
	secondJob, secondPlan, target := submitDirectoryImport(t, a, source, "copy", 123)
	if target != fixture.Target {
		t.Fatal("replacement target differs", target, fixture.Target)
	}
	if err := a.runImport(t.Context(), secondJob, secondPlan.ID, nil); err != nil {
		t.Fatal("same-asset member replacement", err)
	}
	if _, err := a.finishJob(t.Context(), secondJob, "succeeded", "", "", 0); err != nil {
		t.Fatal(err)
	}
	for relative, data := range updated {
		got, err := os.ReadFile(filepath.Join(fixture.Target, relative))
		if err != nil || string(got) != string(data) {
			t.Fatalf("replacement member %s %v", relative, err)
		}
	}
	var receipt importReceipt
	if err := a.Store.Get(t.Context(), "import_receipt", secondPlan.ID+"/"+secondPlan.Files[0].ID, &receipt); err != nil {
		t.Fatal(err)
	}
	if len(receipt.SharedDirectory.Members) != len(updated) {
		t.Fatalf("replacement members %#v", receipt.SharedDirectory.Members)
	}
	for relative := range updated {
		member, ok := receipt.SharedDirectory.Members[relative]
		if !ok || member.Snapshot.Size != int64(len(updated[relative])) {
			t.Fatalf("replacement snapshot %s %#v", relative, member)
		}
	}
	var rootOwner managedFile
	if err := a.Store.Get(t.Context(), "managed_file", tokenHash(fixture.Target), &rootOwner); err != nil || rootOwner.AssetID != firstAsset {
		t.Fatalf("root ownership changed %v %v", rootOwner, err)
	}
	var assets int
	if err := a.Store.DB.QueryRow(`SELECT COUNT(*) FROM library_asset WHERE json_extract(payload,'$.path')=?`, fixture.Target).Scan(&assets); err != nil || assets != 1 {
		t.Fatal("replacement duplicated the directory asset", assets, err)
	}
}

func TestDirectoryMergeRejectsOwnedMemberDriftAndForeignAsset(t *testing.T) {
	for _, scenario := range []string{"drift", "foreign-drift"} {
		t.Run(scenario, func(t *testing.T) {
			a, _, plan, job, fixture := prepareSharedDirectoryImport(t, "copy")
			if err := a.runImport(t.Context(), job, plan.ID, nil); err != nil {
				t.Fatal(err)
			}
			if _, err := a.finishJob(t.Context(), job, "succeeded", "", "", 0); err != nil {
				t.Fatal(err)
			}
			leaf := filepath.Join(fixture.Target, "BDMV/STREAM/00001.m2ts")
			stamp, err := snapshotTransferPath(leaf)
			if err != nil {
				t.Fatal(err)
			}
			owner := managedFile{Path: leaf, RootID: "root", AssetID: plan.Files[0].AssetID, MediaKey: "movie:123", Kind: "video", Snapshot: stamp}
			if scenario == "foreign-drift" {
				owner.AssetID = "foreign-asset"
			}
			if err := a.Store.Put(t.Context(), "managed_file", tokenHash(leaf), owner); err != nil {
				t.Fatal(err)
			}
			if scenario == "drift" || scenario == "foreign-drift" {
				importWrite(t, leaf, []byte("externally drifted stream"))
			}
			updated := map[string][]byte{"BDMV/STREAM/00001.m2ts": []byte("attempted replacement"), "BDMV/index.bdmv": []byte("attempted index")}
			source := appendSourceDirectory(t, a, filepath.Dir(fixture.Source), "Owned.2024.BluRay.v3", updated)
			if err := os.Mkdir(filepath.Join(source, "CERTIFICATE"), 0750); err != nil {
				t.Fatal(err)
			}
			secondJob, secondPlan, _ := submitDirectoryImport(t, a, source, "copy", 123)
			if runErr := a.runImport(t.Context(), secondJob, secondPlan.ID, nil); runErr == nil {
				data, _ := os.ReadFile(leaf)
				var record managedFile
				_ = a.Store.Get(t.Context(), "managed_file", tokenHash(leaf), &record)
				t.Fatalf("unsafe owned member replacement accepted leaf=%q record=%#v", data, record)
			}
			data, err := os.ReadFile(leaf)
			if err != nil || string(data) == "attempted replacement" {
				t.Fatalf("failed replacement wrote the new member %v", err)
			}
			if string(data) != "externally drifted stream" {
				t.Fatalf("failed replacement changed the drifted leaf %q", data)
			}
		})
	}
}
