package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestImportSoftlinkOrdinaryCompletionAndSharedInventoryReaders(t *testing.T) {
	for _, change := range []string{"none", "missing-referent", "replaced-entry"} {
		t.Run(change, func(t *testing.T) {
			a, _, asset, _ := softlinkLifecycleFixture(t, "softlink")
			workflowConfigure(t, a, "http://127.0.0.1:1", "http://127.0.0.1:1")
			s, err := a.saveSubscription(t.Context(), "", SubscriptionRequest{MediaType: "movie", TMDBID: asset.Media.ID, DownloaderID: "fixture-qb", RootID: asset.RootID, Schedule: fridaySchedule(t)})
			if err != nil {
				t.Fatal("actual subscription", err)
			}
			p := qualityDownloadSnapshot{Version: 1, MediaKey: mediaKey(asset.Media), Season: asset.Season, Episodes: []int{0}}
			if err = a.freezeQualityAssets(t.Context(), &p); err != nil || len(p.Assets) != 1 || p.Assets[0].Video.Softlink == nil {
				t.Fatal("freeze existing real softlink inventory", p, err)
			}
			// Decode the frozen record to ensure ownership comparison uses values,
			// not pointer addresses shared with the in-memory publication.
			raw, err := json.Marshal(p)
			if err != nil {
				t.Fatal(err)
			}
			var frozen qualityDownloadSnapshot
			if err = json.Unmarshal(raw, &frozen); err != nil {
				t.Fatal(err)
			}
			readInOneTransaction := func(wantComplete bool) {
				t.Helper()
				a.Store.DB.SetMaxOpenConns(1)
				ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
				defer cancel()
				tx, err := a.Store.DB.BeginTx(ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback()
				basis, complete, err := a.ordinaryCompletionBasis(ctx, tx, s, nil)
				if wantComplete {
					if err != nil || !complete || basis == nil || len(basis.Inventory) != 1 || basis.Inventory[0].Video == nil || basis.Inventory[0].Video.Softlink == nil || basis.Inventory[0].Snapshot != frozen.Assets[0].Video.Snapshot || !sameManagedFile(*basis.Inventory[0].Video, frozen.Assets[0].Video) {
						t.Fatalf("ordinary transactional proof %+v complete=%v err=%v", basis, complete, err)
					}
					if err = a.guardQualityAssets(ctx, tx, &frozen); err != nil {
						t.Fatal("shared transactional inventory", err)
					}
				} else {
					if err == nil || complete {
						t.Fatal("changed softlink still completes subscription", complete, err)
					}
					if err = a.guardQualityAssets(ctx, tx, &frozen); err == nil {
						t.Fatal("changed softlink accepted by shared acquisition reader")
					}
				}
			}
			readInOneTransaction(true)
			switch change {
			case "missing-referent":
				if err = os.Remove(frozen.Assets[0].Video.Softlink.Source); err != nil {
					t.Fatal(err)
				}
			case "replaced-entry":
				// Keep the former inode alive so the filesystem cannot reuse it for the
				// same-text replacement; this is an identity test, not a timing race.
				hold := filepath.Join(filepath.Dir(asset.Path), "old-entry.held")
				if err = os.Rename(asset.Path, hold); err != nil {
					t.Fatal(err)
				}
				if err = os.Symlink(frozen.Assets[0].Video.Softlink.Source, asset.Path); err != nil {
					t.Fatal(err)
				}
			}
			if change != "none" {
				readInOneTransaction(false)
			}
		})
	}
}
