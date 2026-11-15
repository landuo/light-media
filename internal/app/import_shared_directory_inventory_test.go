package app

import (
	"path/filepath"
	"testing"
)

func TestSharedDirectoryActualMergeInventoryQualitySubset(t *testing.T) {
	a, _, _, asset, fixture := finishSharedDirectoryFixture(t, "copy")
	frozen := &qualityDownloadSnapshot{MediaKey: mediaKey(asset.Media), Season: asset.Season, Episodes: asset.Episodes}
	if err := a.freezeQualityAssets(t.Context(), frozen); err != nil || len(frozen.Assets) != 1 {
		t.Fatal("actual owned quality inventory", frozen, err)
	}
	s := Subscription{State: "active", Media: asset.Media, Season: asset.Season}
	basis, complete, err := a.ordinaryCompletionBasis(t.Context(), a.Store.DB, s, nil)
	if err != nil || !complete || len(basis.Inventory) != 1 || basis.Inventory[0].Video.SharedDirectory == nil {
		t.Fatal("ordinary owned directory inventory", basis, complete, err)
	}
	// Later unrelated files are outside both inventory and the quality snapshot.
	importWrite(t, filepath.Join(fixture.Target, "later-external.nfo"), []byte("foreign later metadata"))
	if err = a.guardQualityAssets(t.Context(), a.Store.DB, frozen); err != nil {
		t.Fatal("foreign entry invalidated quality subset", err)
	}
	if _, complete, err = a.ordinaryCompletionBasis(t.Context(), a.Store.DB, s, nil); err != nil || !complete {
		t.Fatal("foreign entry invalidated inventory subset", complete, err)
	}
	if stamp, e := a.snapshotAssetMedia(t.Context(), asset); e != nil || stamp != frozen.Assets[0].Video.Snapshot {
		t.Fatal("media aggregate includes unrelated entry", stamp, e)
	}
	importWrite(t, filepath.Join(fixture.Target, "tiny.txt"), []byte("changed owned member"))
	if err = a.guardQualityAssets(t.Context(), a.Store.DB, frozen); err == nil {
		t.Fatal("changed owned member retained quality proof")
	}
	if _, complete, err = a.ordinaryCompletionBasis(t.Context(), a.Store.DB, s, nil); err == nil || complete {
		t.Fatal("changed owned member retained completion proof", complete, err)
	}
	if now, _, e := snapshotOriginalDirectory(fixture.Source); e != nil || now != fixture.SourceSnapshot {
		t.Fatal("inventory test changed source", e)
	}
}
