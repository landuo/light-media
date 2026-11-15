package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestSharedDirectorySoftlinkLinkedNFOActualColdRebase(t *testing.T) {
	options := sharedDirectoryFixtureOptions{Contents: map[string][]byte{"BDMV/STREAM/00001.m2ts": []byte("owned stream")}, MissingLeaf: true, LinkedNFO: true}
	a, _, plan, importJob, fixture := prepareSharedDirectoryImportConfigured(t, "softlink", options, true)
	if err := a.runImport(t.Context(), importJob, plan.ID, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := a.finishJob(t.Context(), importJob, "succeeded", "", "", 0); err != nil {
		t.Fatal(err)
	}
	if err := a.Store.Get(t.Context(), "import_plan", plan.ID, &plan); err != nil {
		t.Fatal(err)
	}
	var asset Asset
	if err := a.Store.Get(t.Context(), "asset", plan.Files[0].AssetID, &asset); err != nil {
		t.Fatal(err)
	}
	scrape, err := scanJob(a.Store.DB.QueryRow(`SELECT `+jobColumns+` FROM jobs WHERE kind='scrape' AND json_extract(payload,'$.id')=? LIMIT 1`, asset.ID))
	if err != nil {
		t.Fatal(err)
	}
	runSoftlinkDirectoryJob(t, a, scrape.ID)
	nfo := filepath.Join(asset.Path, filepath.Base(asset.Path)+".nfo")
	priorLink, sourceNFO, err := snapshotSoftlinkEntry(nfo)
	if err != nil || sourceNFO != filepath.Join(fixture.Source, filepath.Base(nfo)) {
		t.Fatal("non-overwrite retained actual source NFO link", err)
	}
	var payload map[string]json.RawMessage
	if err = json.Unmarshal(scrape.Payload, &payload); err != nil {
		t.Fatal(err)
	}
	var event map[string]any
	if err = json.Unmarshal(payload["original_metadata_event"], &event); err != nil {
		t.Fatal(err)
	}
	event["overwrite"] = true
	job, err := a.enqueue(t.Context(), "scrape", map[string]any{"id": asset.ID, "original_metadata_event": event}, scrape.WorkflowID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.Store.DB.Exec(`CREATE TRIGGER shared_linked_nfo_fail BEFORE UPDATE ON records WHEN NEW.kind='managed_file' AND NEW.id='` + tokenHash(asset.Path) + `' BEGIN SELECT RAISE(FAIL,'shared linked NFO metadata interrupted'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err = a.Store.DB.Exec(`UPDATE jobs SET state='running',generation=generation+1 WHERE id=?`, job.ID); err != nil {
		t.Fatal(err)
	}
	job, err = scanJob(a.Store.DB.QueryRow(`SELECT `+jobColumns+` FROM jobs WHERE id=?`, job.ID))
	if err != nil {
		t.Fatal(err)
	}
	if err = a.safeRun(t.Context(), job); err == nil {
		t.Fatal("actual metadata owner commit did not pause")
	}
	before, err := snapshotPath(nfo)
	if err != nil || sameInode(before, priorLink) {
		t.Fatal("NFO not replaced with actual ordinary metadata", err)
	}
	var pending originalMetadataDirectory
	if err = a.Store.Get(t.Context(), "original_metadata_directory", asset.ID, &pending); err != nil || len(pending.Writes) != 1 || pending.Owned.SharedDirectory == nil {
		t.Fatal("actual overwrite journal", pending, err)
	}
	write := pending.Writes[filepath.Base(nfo)]
	if write.Previous == nil || *write.Previous != priorLink || write.Snapshot != before {
		t.Fatal("old-link and actual ordinary NFO proofs", write)
	}
	if _, err = a.Store.DB.Exec(`DROP TRIGGER shared_linked_nfo_fail`); err != nil {
		t.Fatal(err)
	}
	a.wg.Wait()
	settings, dir := a.Settings(), a.Dir
	if err = a.Close(); err != nil {
		t.Fatal(err)
	}
	a, err = New(Options{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	a.config = settings
	if err = a.initializeOriginalPlugins(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err = a.recoverOriginalMetadataDirectory(t.Context(), asset); err != nil {
		t.Fatal("shared linked-NFO cold rebase", err)
	}
	var owned managedFile
	if err = a.Store.Get(t.Context(), "managed_file", tokenHash(asset.Path), &owned); err != nil || owned.SharedDirectory == nil || owned.Softlink != nil || len(owned.SharedDirectory.Members) != 2 || owned.SharedDirectory.Members[filepath.Base(nfo)].Softlink != nil {
		t.Fatal("NFO proof remained link", owned, err)
	}
	if after, e := snapshotPath(nfo); e != nil || after != before {
		t.Fatal("cold recovery rewrote actual NFO", e)
	}
	var receipt importReceipt
	if err = a.Store.Get(t.Context(), "import_receipt", plan.ID+"/"+plan.Files[0].ID, &receipt); err != nil || receipt.StageSnapshot != owned.Snapshot || !sameSharedDirectoryProof(receipt.SharedDirectory, owned.SharedDirectory) {
		t.Fatal("shared proof atomic rebase", err)
	}
	if err = a.validateSharedSoftlinkPublication(t.Context(), receipt); err != nil {
		t.Fatal("original method proof lost after metadata", err)
	}
	if err = a.checkAsset(t.Context(), asset.ID); err != nil {
		t.Fatal(err)
	}
	if now, _, e := snapshotOriginalDirectory(fixture.Source); e != nil || now != fixture.SourceSnapshot {
		t.Fatal("explicit NFO overwrite followed source reference", now, e)
	}
	if _, err = a.finishJob(t.Context(), job, "failed", "test", "verified actual metadata recovery", 0); err != nil {
		t.Fatal(err)
	}
	item, err := a.transferHistoryItem(t.Context(), plan.Files[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.deleteTransferHistory(t.Context(), item, historyDeleteRequest{Confirm: true, FileID: item.executionFileID(), DeleteTarget: true}); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Lstat(nfo); !os.IsNotExist(err) {
		t.Fatal("owned ordinary NFO retained", err)
	}
	if now, _, e := snapshotOriginalDirectory(fixture.Source); e != nil || now != fixture.SourceSnapshot {
		t.Fatal("target delete altered source NFO", e)
	}
	checkSharedPreserved(t, fixture)
}
