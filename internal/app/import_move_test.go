package app

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestImportMovePublishesRealBytesAndDefersOnlyWhenRequested(t *testing.T) {
	for _, deferred := range []bool{false, true} {
		name := "immediate"
		if deferred {
			name = "deferred-directory-member"
		}
		t.Run(name, func(t *testing.T) {
			a, source, target := importTestApp(t)
			input, output := filepath.Join(source, "movie.mkv"), filepath.Join(target, "movie.mkv")
			data := bytes.Repeat([]byte("move complete bytes\x00"), 32768)
			importWrite(t, input, data)
			stamp, err := snapshotPath(input)
			if err != nil {
				t.Fatal(err)
			}
			ctx := t.Context()
			if deferred {
				ctx = context.WithValue(ctx, importMoveDeferredKey{}, true)
			}
			receipt, err := a.publishImport(ctx, "real-move", input, output, stamp, "move", func() error { return nil })
			if err != nil || receipt.State != "published" || receipt.Move == nil {
				t.Fatalf("publication: %+v %v", receipt, err)
			}
			if receipt.Move.Publication != "hardlink" || receipt.SHA256 != "" || receipt.StageSnapshot != stamp {
				t.Fatal("same-device move did not preserve the original inode without copying", receipt)
			}
			if deferred {
				if receipt.Move.State != "pending" {
					t.Fatal("directory member cleared source early", receipt.Move)
				}
				actual, err := snapshotPath(input)
				if err != nil || actual != stamp {
					t.Fatal("deferred source changed", actual, err)
				}
				receipt, err = a.finishImportMove(t.Context(), receipt.ID, func() error { return nil })
				if err != nil {
					t.Fatal(err)
				}
			}
			if receipt.Move.State != "completed" {
				t.Fatal("move returned before source cleanup", receipt.Move)
			}
			got, err := os.ReadFile(output)
			if err != nil || !bytes.Equal(got, data) {
				t.Fatal("target bytes", err)
			}
			for _, path := range []string{input, filepath.Join(source, receipt.Move.Quarantine), filepath.Join(target, receipt.Stage)} {
				if _, err = os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
					t.Fatal("completed move left an old source or stage", path, err)
				}
			}
		})
	}
}

func TestImportMoveFailedCleanupRetainsSourceAndTarget(t *testing.T) {
	for _, failure := range []string{"cancel-cleanup", "changed-target", "changed-source", "custom-source-storage"} {
		t.Run(failure, func(t *testing.T) {
			a, source, target := importTestApp(t)
			input, output := filepath.Join(source, "movie.mkv"), filepath.Join(target, "movie.mkv")
			original := []byte("original complete source")
			importWrite(t, input, original)
			stamp, err := snapshotPath(input)
			if err != nil {
				t.Fatal(err)
			}
			ctx := context.WithValue(t.Context(), importMoveDeferredKey{}, true)
			if failure == "custom-source-storage" {
				// An unbound directory member must not call storage move/copy
				// without its frozen parent list and source-storage identity.
				ctx = context.WithValue(ctx, originalTransferStorageKey{}, &originalTransferStoragePair{Source: &originalTransferStorage{}})
				_, err = a.publishImport(ctx, "failed-move", input, output, stamp, "move", func() error { return nil })
				var api *APIError
				if !errors.As(err, &api) || api.Code != "REVIEW_MOVE_STORAGE" {
					t.Fatal("custom move was disguised as copy", err)
				}
				if _, err = os.Lstat(output); !errors.Is(err, os.ErrNotExist) {
					t.Fatal("unsupported custom move published target", err)
				}
				got, err := os.ReadFile(input)
				if err != nil || !bytes.Equal(got, original) {
					t.Fatal("unsupported custom move changed source", err)
				}
				return
			}
			receipt, err := a.publishImport(ctx, "failed-move", input, output, stamp, "move", func() error { return nil })
			if err != nil {
				t.Fatal(err)
			}
			wantSource, wantTarget := original, original
			check := func() error { return nil }
			switch failure {
			case "cancel-cleanup":
				check = func() error { return context.Canceled }
			case "changed-target":
				wantTarget = []byte("external target replacement")
				if err = os.Remove(output); err != nil {
					t.Fatal(err)
				}
				importWrite(t, output, wantTarget)
			case "changed-source":
				wantSource = []byte("external source replacement")
				if err = os.Remove(input); err != nil {
					t.Fatal(err)
				}
				importWrite(t, input, wantSource)
			}
			if _, err = a.finishImportMove(t.Context(), receipt.ID, check); err == nil {
				t.Fatal("unconfirmed cleanup succeeded")
			}
			for path, want := range map[string][]byte{input: wantSource, output: wantTarget} {
				got, err := os.ReadFile(path)
				if err != nil || !bytes.Equal(got, want) {
					t.Fatal("failed cleanup lost existing bytes", path, err)
				}
			}
			var stored importReceipt
			if err = a.Store.Get(t.Context(), "import_receipt", receipt.ID, &stored); err != nil || stored.Move.State != "pending" {
				t.Fatal("failed cleanup lost resumable pending state", stored, err)
			}
		})
	}
}

func TestImportMoveColdRecoveryPreservesRecreatedSource(t *testing.T) {
	a, source, target := importTestApp(t)
	input, output := filepath.Join(source, "movie.mkv"), filepath.Join(target, "movie.mkv")
	original := []byte("durable original movie")
	importWrite(t, input, original)
	stamp, err := snapshotPath(input)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.Store.DB.Exec(`CREATE TRIGGER move_quarantine_sql_failure BEFORE UPDATE ON import_receipt
		WHEN NEW.record_kind='import_receipt' AND json_extract(NEW.payload,'$.move.state')='quarantined'
		BEGIN SELECT RAISE(FAIL,'synthetic move quarantine SQL failure'); END`); err != nil {
		t.Fatal(err)
	}
	_, err = a.publishImport(t.Context(), "cold-move", input, output, stamp, "move", func() error { return nil })
	if err == nil {
		t.Fatal("quarantine SQL failure was ignored")
	}
	var pending importReceipt
	if err = a.Store.Get(t.Context(), "import_receipt", "cold-move", &pending); err != nil || pending.State != "published" || pending.Move == nil || pending.Move.State != "pending" {
		t.Fatalf("durable boundary: %+v %v", pending, err)
	}
	if _, err = os.Lstat(input); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("source was not quarantined before simulated SQL failure", err)
	}
	quarantine := filepath.Join(source, pending.Move.Quarantine)
	if actual, err := snapshotPath(quarantine); err != nil || actual != stamp {
		t.Fatal("quarantined original lost", actual, err)
	}
	newSource := []byte("new download using the same filename")
	importWrite(t, input, newSource)
	if _, err = a.Store.DB.Exec(`DROP TRIGGER move_quarantine_sql_failure`); err != nil {
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
	// A later plugin storage selection must not hijack a native move recovery.
	ctx := context.WithValue(t.Context(), originalTransferStorageKey{}, &originalTransferStoragePair{Source: &originalTransferStorage{}})
	receipt, err := a.publishImport(ctx, "cold-move", input, output, stamp, "move", func() error { return nil })
	if err != nil || receipt.Move.State != "completed" || receipt.StageSnapshot != pending.StageSnapshot {
		t.Fatal("cold move did not resume its original publication", receipt, err)
	}
	if _, err = a.publishImport(ctx, "cold-move", input, output, stamp, "move", func() error {
		t.Error("completed move consulted a new source cleanup authorization")
		return errors.New("completed source must not be revisited")
	}); err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string][]byte{input: newSource, output: original} {
		got, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(got, want) {
			t.Fatal("recovery changed new source or original target", path, err)
		}
	}
	if _, err = os.Lstat(quarantine); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("old quarantined source not cleaned", err)
	}
}
