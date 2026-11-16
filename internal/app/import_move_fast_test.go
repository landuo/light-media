package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestImportMoveLinkFallbackAndLegacyCopyReceipt(t *testing.T) {
	for _, code := range []unix.Errno{unix.EXDEV, unix.EOPNOTSUPP} {
		t.Run(code.Error(), func(t *testing.T) {
			a, source, target := importTestApp(t)
			input, output := filepath.Join(source, "fallback.mkv"), filepath.Join(target, "fallback.mkv")
			data := bytes.Repeat([]byte("actual copy fallback\x00"), 8192)
			importWrite(t, input, data)
			stamp, err := snapshotPath(input)
			if err != nil {
				t.Fatal(err)
			}
			calls := 0
			a.importSourceLink = func(int, string, int, string, int) error { calls++; return code }
			ctx := context.WithValue(t.Context(), importMoveDeferredKey{}, true)
			receipt, err := a.publishImport(ctx, "move-fallback", input, output, stamp, "move", nil)
			digest := sha256.Sum256(data)
			if err != nil || receipt.Move == nil || receipt.Move.Publication != "copy" || receipt.Move.State != "pending" || calls != 1 || receipt.SHA256 != hex.EncodeToString(digest[:]) || sameInode(stamp, receipt.StageSnapshot) {
				t.Fatalf("copy fallback: %+v %v calls=%d", receipt, err, calls)
			}
			// Old move receipts omitted Publication and must still resume as copy.
			receipt.Move.Publication = ""
			if err = a.Store.Put(t.Context(), "import_receipt", receipt.ID, receipt); err != nil {
				t.Fatal(err)
			}
			receipt, err = a.finishImportMove(t.Context(), receipt.ID, nil)
			if err != nil || receipt.Move.State != "completed" {
				t.Fatal(receipt, err)
			}
			if _, err = os.Lstat(input); !os.IsNotExist(err) {
				t.Fatal("old source retained", err)
			}
			got, err := os.ReadFile(output)
			if err != nil || !bytes.Equal(got, data) {
				t.Fatal("copy bytes", err)
			}
		})
	}
}

func TestImportMoveSameDeviceSpaceAndInode(t *testing.T) {
	for _, disc := range []bool{false, true} {
		name := "file-and-subtitle"
		if disc {
			name = "disc"
		}
		t.Run(name, func(t *testing.T) {
			a, source, targetRoot := importTestApp(t)
			nativeMoveFixture(t, a)
			path := filepath.Join(source, "Fast.2024.mkv")
			physical := path
			if disc {
				path = filepath.Join(source, "Fast.2024")
				physical = filepath.Join(path, "BDMV/STREAM/00001.m2ts")
				importWrite(t, filepath.Join(path, "BDMV/index.bdmv"), []byte("index"))
			}
			importWrite(t, physical, nil)
			if err := os.Truncate(physical, 64<<20); err != nil {
				t.Fatal(err)
			}
			if !disc {
				importWrite(t, filepath.Join(source, "Fast.2024.zh.srt"), nil)
				if err := os.Truncate(filepath.Join(source, "Fast.2024.zh.srt"), 8<<20); err != nil {
					t.Fatal(err)
				}
			}
			original, err := snapshotPath(physical)
			if err != nil {
				t.Fatal(err)
			}
			out, err := a.manualOrganize(t.Context(), manualOrganizeRequest{Paths: []string{path}, MediaType: "movie", TMDBID: 123, Mode: "move", TargetPath: targetRoot, Scraping: importFlag(false)})
			if err != nil || out.Failed != 0 || len(out.Items) != 1 || out.Items[0].JobID == "" {
				t.Fatalf("manual move %+v %v", out, err)
			}
			plan, job := historyUpsertAttempt(t, a, out.Items[0].JobID)
			if len(plan.Files) != 1 || plan.Files[0].Status != "ready" {
				t.Fatalf("move plan %+v", plan)
			}
			if plan.SpaceNeeded >= 1<<20 {
				t.Fatal("same-device move budgeted a second media copy", plan.SpaceNeeded)
			}
			var meta importMetadata
			if err = a.Store.Get(t.Context(), "import_meta", plan.ID, &meta); err != nil {
				t.Fatal(err)
			}
			crossBytes := importSpaceEstimate(plan, meta, original.Device+1)
			if crossBytes < 64<<20 {
				t.Fatal("different device did not budget copy", crossBytes)
			}
			runEpisodeGroupJob(t, a, job)
			if err = a.Store.Get(t.Context(), "import_plan", plan.ID, &plan); err != nil {
				t.Fatal(err)
			}
			target := plan.Files[0].Target
			if disc {
				target = filepath.Join(target, "BDMV/STREAM/00001.m2ts")
			}
			stamp, err := snapshotPath(target)
			if err != nil || stamp != original {
				t.Fatal("same-device move copied or changed the video inode", stamp, original, err)
			}
			if _, err = os.Lstat(physical); !os.IsNotExist(err) {
				t.Fatal("source was not moved", err)
			}
		})
	}
}

func TestImportMoveRenamePublicationColdRecovery(t *testing.T) {
	a, source, target := importTestApp(t)
	input, output := filepath.Join(source, "publication.mkv"), filepath.Join(target, "publication.mkv")
	importWrite(t, input, []byte("rename publication recovery"))
	stamp, err := snapshotPath(input)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.Store.DB.Exec(`CREATE TRIGGER move_publish_sql_failure BEFORE UPDATE ON import_receipt WHEN NEW.record_kind='import_receipt' AND json_extract(NEW.payload,'$.state')='published' BEGIN SELECT RAISE(FAIL,'synthetic publication failure'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err = a.publishImport(t.Context(), "rename-move", input, output, stamp, "move", nil); err == nil {
		t.Fatal("SQL failure ignored")
	}
	var pending importReceipt
	if err = a.Store.Get(t.Context(), "import_receipt", "rename-move", &pending); err != nil || pending.State != "staged" {
		t.Fatal(pending, err)
	}
	if _, err = os.Lstat(filepath.Join(target, pending.Stage)); !os.IsNotExist(err) {
		t.Fatal("rename did not consume stage", err)
	}
	if actual, e := snapshotPath(output); e != nil || actual != stamp {
		t.Fatal("published original inode missing", actual, e)
	}
	if actual, e := snapshotPath(input); e != nil || actual != stamp {
		t.Fatal("source cleared before durable publication", actual, e)
	}
	if _, err = a.Store.DB.Exec(`DROP TRIGGER move_publish_sql_failure`); err != nil {
		t.Fatal(err)
	}
	config, dataDir := a.Settings(), a.Dir
	if err = a.Close(); err != nil {
		t.Fatal(err)
	}
	a, err = New(Options{DataDir: dataDir})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	a.config = config
	receipt, err := a.publishImport(t.Context(), "rename-move", input, output, stamp, "move", nil)
	if err != nil || receipt.Move.State != "completed" || receipt.StageSnapshot != stamp {
		t.Fatal(receipt, err)
	}
	if _, err = os.Lstat(input); !os.IsNotExist(err) {
		t.Fatal("source not cleared on recovery", err)
	}
}

// Opt in with a newly created, exclusive mounted volume below workspace work/.
// The test requires an actual different device, not an injected EXDEV response.
func TestImportMoveActualCrossFilesystem(t *testing.T) {
	volume := os.Getenv("LM_MOVE_CROSS_DEVICE_ROOT")
	if volume == "" {
		t.Skip("owned cross-filesystem volume not configured")
	}
	root, err := filepath.Abs("../..")
	if err != nil || !filepath.IsAbs(volume) || !within(filepath.Join(root, "work"), volume) {
		t.Fatal("use an exclusive workspace work volume")
	}
	a, source, _ := importTestApp(t)
	target, err := os.MkdirTemp(volume, "native-move-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(target)
	input, output := filepath.Join(source, "cross.mkv"), filepath.Join(target, "cross.mkv")
	data := bytes.Repeat([]byte("actual separate filesystem\x00"), 65537)
	importWrite(t, input, data)
	modified := time.Unix(1700000000, 0) // representable by HFS+ as well as APFS
	if err = os.Chtimes(input, modified.Add(-time.Hour), modified); err != nil {
		t.Fatal(err)
	}
	original, err := snapshotPath(input)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	targetDir, err := originalDirectoryIdentity(info)
	if err != nil || original.Device == targetDir.Device {
		t.Fatal("fixture is not a different filesystem", err)
	}
	receipt, err := a.publishImport(t.Context(), "actual-cross-device", input, output, original, "move", nil)
	digest := sha256.Sum256(data)
	if err != nil || receipt.Move == nil || receipt.Move.Publication != "copy" || receipt.Move.State != "completed" || receipt.SHA256 != hex.EncodeToString(digest[:]) || receipt.StageSnapshot.Device != targetDir.Device {
		t.Fatalf("cross move %+v %v", receipt, err)
	}
	got, err := os.ReadFile(output)
	if err != nil || !bytes.Equal(got, data) {
		t.Fatal("cross-device bytes", err)
	}
	if _, err = os.Lstat(input); !os.IsNotExist(err) {
		t.Fatal("cross-device source retained", err)
	}
	if receipt.StageSnapshot.ModNS != original.ModNS {
		t.Fatal("actual cross-filesystem move changed source modification time", receipt.StageSnapshot.ModNS, original.ModNS)
	}
	t.Logf("actual devices source=%d target=%d bytes=%d sha256=%s mtime_ns=%d", original.Device, targetDir.Device, len(data), receipt.SHA256, receipt.StageSnapshot.ModNS)
}
