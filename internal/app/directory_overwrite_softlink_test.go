package app

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Exercise the same directory host used by current V3 quality imports. Both
// publications are real native plans; old ownership is never synthesized.
func TestDirectoryOverwriteSoftlinkActualSizeAndOwnership(t *testing.T) {
	for _, branch := range []string{"larger", "smaller", "broken"} {
		t.Run(branch, func(t *testing.T) {
			a, source, target := importTestApp(t)
			nativeMoveFixture(t, a)
			overwriteRule(a, source, target, "size", true)
			oldSource := filepath.Join(source, "old", "Original.Movie.2024.mkv")
			oldSubtitle := filepath.Join(source, "old", "Original.Movie.2024.zh.srt")
			next := filepath.Join(source, "new", "Original.Movie.2024.mkv")
			oldBytes, newBytes := []byte("old"), []byte("new larger media")
			if branch == "smaller" {
				// Both media lengths exceed the link text length. Comparing the
				// new payload to entry.Size would wrongly authorize this downgrade.
				oldBytes, newBytes = bytes.Repeat([]byte("o"), 8192), bytes.Repeat([]byte("n"), 4096)
			}
			importWrite(t, oldSource, oldBytes)
			importWrite(t, oldSubtitle, []byte("retained original subtitle"))
			past := time.Unix(1000, 0)
			if err := os.Chtimes(oldSource, past, past); err != nil {
				t.Fatal(err)
			}
			preview := func(path string) ImportPlan {
				t.Helper()
				plan, err := a.previewImport(t.Context(), ImportRequest{MediaType: "movie", TMDBID: 123, RootID: "root", Mode: "softlink", Scraping: importFlag(false), Files: []ImportFileRequest{{Path: path}}})
				if err != nil || len(plan.Files) != 1 || plan.Files[0].Status != "ready" || plan.Files[0].Method != "softlink" {
					t.Fatal("actual softlink overwrite plan", plan, err)
				}
				return plan
			}
			first := preview(oldSource)
			job, err := a.executeImport(t.Context(), first.ID, nil)
			if err != nil {
				t.Fatal(err)
			}
			reorgRunJob(t, a, job)
			var prior Asset
			if err = a.assetAtPath(t.Context(), first.Files[0].Target, &prior); err != nil {
				t.Fatal(err)
			}
			files := softlinkLifecycleOwned(t, a, prior)
			if len(files) != 2 {
				t.Fatal("actual original video and subtitle", files)
			}
			var oldVideo, oldSidecar managedFile
			for _, file := range files {
				if file.Softlink == nil {
					t.Fatal("old publication is not a proven softlink", file)
				}
				if file.Kind == "video" {
					oldVideo = file
				} else {
					oldSidecar = file
				}
			}
			entry, decision, err := a.readDirectoryOverwriteTarget(t.Context(), prior.Path, prior.RootID)
			if err != nil || entry.Snapshot != oldVideo.Snapshot || entry.Owner == nil || !sameManagedFile(*entry.Owner, oldVideo) || decision != oldVideo.Softlink.SourceSnapshot || decision.Size != int64(len(oldBytes)) || decision.ModNS != past.UnixNano() {
				t.Fatal("entry ownership and decision media facts were conflated", entry, decision, err)
			}
			importWrite(t, next, newBytes)
			second := preview(next)
			if second.Files[0].Target != prior.Path {
				t.Fatal("fixture did not exercise same-path overwrite", second.Files[0].Target, prior.Path)
			}
			if branch == "broken" {
				if err = os.Remove(oldSource); err != nil {
					t.Fatal(err)
				}
			}
			if branch == "larger" {
				// The real frozen owner also remains sufficient for reversible old
				// entry operations when its referent is temporarily unavailable.
				// No publication/registration receipt is manufactured here.
				entry.Backup = ".light-media-overwrite-" + tokenHash(second.ID + "/" + second.Files[0].ID + "\x00" + prior.Path)[:32] + ".part"
				held := oldSource + ".held"
				if err = os.Rename(oldSource, held); err != nil {
					t.Fatal(err)
				}
				if err = quarantineDirectoryOverwrite(entry); err != nil {
					t.Fatal("entry-only quarantine", err)
				}
				if err = quarantineDirectoryOverwrite(entry); err != nil {
					t.Fatal("entry-only quarantine resume", err)
				}
				if err = restoreDirectoryOverwrite(entry); err != nil {
					t.Fatal("entry-only restore", err)
				}
				if actual, err := snapshotOwnedTransferPath(prior.Path, oldVideo.Snapshot, oldVideo.Softlink, false); err != nil || actual != oldVideo.Snapshot {
					t.Fatal("restore did not retain original link inode", err)
				}
				if err = os.Rename(held, oldSource); err != nil {
					t.Fatal(err)
				}
			}
			job = overwriteStart(t, a, second)
			err = a.runImport(t.Context(), job, second.ID, nil)
			if branch != "larger" {
				if err == nil {
					t.Fatal("size rejection or broken old referent was ignored")
				}
				if actual, e := snapshotOwnedTransferPath(prior.Path, oldVideo.Snapshot, oldVideo.Softlink, false); e != nil || actual != oldVideo.Snapshot {
					t.Fatal("rejected overwrite changed old link entry", e)
				}
				var current Asset
				if e := a.Store.Get(t.Context(), "asset", prior.ID, &current); e != nil || current.State != "present" {
					t.Fatal("rejected overwrite changed old asset", current, e)
				}
			} else {
				if err != nil {
					t.Fatal("real larger softlink replacement", err)
				}
				var current Asset
				if err = a.assetAtPath(t.Context(), prior.Path, &current); err != nil || current.ID == prior.ID || current.SourcePath != next || current.Size != int64(len(newBytes)) {
					t.Fatal("replacement asset identity/size", current, err)
				}
				var video, sidecar managedFile
				if err = a.Store.Get(t.Context(), "managed_file", tokenHash(current.Path), &video); err != nil || video.Softlink == nil || video.Softlink.Source != next {
					t.Fatal("replacement softlink owner", video, err)
				}
				if _, err = snapshotOwnedTransferPath(current.Path, video.Snapshot, video.Softlink, true); err != nil {
					t.Fatal(err)
				}
				if err = a.Store.Get(t.Context(), "managed_file", tokenHash(oldSidecar.Path), &sidecar); err != nil || sidecar.AssetID != current.ID || sidecar.Snapshot != oldSidecar.Snapshot || !sameSoftlinkProof(sidecar.Softlink, oldSidecar.Softlink) {
					t.Fatal("retained subtitle did not transfer exact ownership", sidecar, err)
				}
				op, err := a.readDirectoryOverwriteOperation(t.Context(), second.ID+"/"+second.Files[0].ID)
				if err != nil || op.State != "complete" || op.Method != "softlink" || len(op.Targets) != 1 || op.Targets[0].Owner == nil || !sameManagedFile(*op.Targets[0].Owner, oldVideo) || op.Targets[0].Snapshot != oldVideo.Snapshot {
					t.Fatal("real sealed old-entry cleanup proof", op, err)
				}
				if _, err = os.Lstat(filepath.Join(filepath.Dir(prior.Path), op.Targets[0].Backup)); !os.IsNotExist(err) {
					t.Fatal("old link backup was not unlinked", err)
				}
				if got, err := os.ReadFile(current.Path); err != nil || !bytes.Equal(got, newBytes) {
					t.Fatal("replacement media view", err)
				}
			}
			if branch != "broken" {
				if got, err := os.ReadFile(oldSource); err != nil || !bytes.Equal(got, oldBytes) {
					t.Fatal("old referent was touched", err)
				}
			}
			if got, err := os.ReadFile(next); err != nil || !bytes.Equal(got, newBytes) {
				t.Fatal("new referent was touched", err)
			}
			if got, err := os.ReadFile(oldSubtitle); err != nil || string(got) != "retained original subtitle" {
				t.Fatal("old subtitle source was touched", err)
			}
			if _, err := snapshotOwnedTransferPath(oldSidecar.Path, oldSidecar.Snapshot, oldSidecar.Softlink, true); err != nil {
				t.Fatal("retained subtitle link was changed", err)
			}
		})
	}
}
