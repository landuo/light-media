package app

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

type sharedDirectoryFixture struct {
	Source         string
	Observer       string
	Target         string
	SourceSnapshot importSnapshot
	RootSnapshot   importSnapshot
	Preserved      map[string]importSnapshot
	Contents       map[string][]byte
}

type sharedDirectoryFixtureOptions struct {
	Contents     map[string][]byte
	Storage      bool
	StorageMode  string
	MissingLeaf  bool
	OccupiedLeaf string
	LinkedNFO    bool
}

func prepareSharedDirectoryImport(t *testing.T, mode string, scrape ...bool) (*App, *originalPluginInstance, ImportPlan, Job, sharedDirectoryFixture) {
	t.Helper()
	return prepareSharedDirectoryImportConfigured(t, mode, sharedDirectoryFixtureOptions{}, scrape...)
}
func prepareSharedDirectoryImportConfigured(t *testing.T, mode string, options sharedDirectoryFixtureOptions, scrape ...bool) (*App, *originalPluginInstance, ImportPlan, Job, sharedDirectoryFixture) {
	t.Helper()
	a, object, unused, cfg := originalTransferModuleFixture(t, mode, "")
	source := filepath.Join(filepath.Dir(unused.Files[0].Source), "Owned.2024.BluRay")
	contents := map[string][]byte{"BDMV/STREAM/00001.m2ts": []byte("owned stream"), "BDMV/index.bdmv": []byte("owned index"), "tiny.txt": []byte("x")}
	if options.Contents != nil {
		contents = options.Contents
	}
	for rel, data := range contents {
		importWrite(t, filepath.Join(source, rel), data)
	}
	if err := os.Mkdir(filepath.Join(source, "CERTIFICATE"), 0750); err != nil {
		t.Fatal(err)
	}
	cfg["modules"].(map[string]string)["transfer"] = "module_execute"
	cfg["result"] = nil
	raw, _ := json.Marshal(cfg)
	if _, err := a.pluginStore.saveOriginalPluginConfiguration(t.Context(), object.installation, raw); err != nil {
		t.Fatal(err)
	}
	if err := a.initOriginalLoadedPlugin(t.Context(), object.installation.InstanceID); err != nil {
		t.Fatal(err)
	}
	login := callTest(t, a.Handler(), "POST", "/api/auth/login", `{"username":"admin","password":"a-strong-test-password"}`, nil, "")
	if login.Code != 200 {
		t.Fatal("existing fixture login", login.Code)
	}
	var cookie *http.Cookie
	for _, value := range login.Result().Cookies() {
		if value.Name == "lm_session" {
			cookie = value
		}
	}
	if cookie == nil {
		t.Fatal("missing fixture login session")
	}
	observerConfig := map[string]any{}
	if options.Storage {
		observerConfig = map[string]any{"storage": true, "storage_root": filepath.Dir(filepath.Dir(source)), "storage_mode": options.StorageMode}
	}
	observer := installOwnedTransferObserverWithCookie(t, a, cookie, nil, nil, observerConfig)
	overwriteRule(a, filepath.Dir(source), a.Settings().Roots[0].Path, "never", true)
	a.config.Directories[0].MediaType = "movie"
	if err := a.saveSettings(t.Context(), a.Settings()); err != nil {
		t.Fatal(err)
	}
	request := manualOrganizeRequest{Paths: []string{source}, TargetPath: a.Settings().Roots[0].Path, Mode: mode, MediaType: "movie", TMDBID: 123, Scraping: importFlag(len(scrape) > 0 && scrape[0]), Preview: true}
	preview, err := a.manualOrganize(t.Context(), request)
	if err != nil || preview.Failed != 0 || len(preview.Items) != 1 {
		t.Fatal("directory naming preview", preview, err)
	}
	target := preview.Items[0].Target
	if options.LinkedNFO {
		relative := filepath.Base(target) + ".nfo"
		contents[relative] = []byte("<movie><title>original source NFO</title></movie>")
		importWrite(t, filepath.Join(source, relative), contents[relative])
	}
	if !options.MissingLeaf {
		occupied := options.OccupiedLeaf
		if occupied == "" {
			occupied = "BDMV/STREAM/00001.m2ts"
		}
		importWrite(t, filepath.Join(target, occupied), bytes.Repeat([]byte("old"), 100))
	}
	preserved := map[string]importSnapshot{}
	for _, rel := range []string{"external.nfo", "unrelated/Other.version.mkv"} {
		path := filepath.Join(target, rel)
		importWrite(t, path, []byte("unrelated original "+rel))
		stamp, e := snapshotPath(path)
		if e != nil {
			t.Fatal(e)
		}
		preserved[rel] = stamp
	}
	if err = os.Mkdir(filepath.Join(target, "unrelated", "empty"), 0750); err != nil {
		t.Fatal(err)
	}
	root, _, err := snapshotOriginalDirectory(target)
	if err != nil {
		t.Fatal(err)
	}
	sourceStamp, _, err := snapshotOriginalDirectory(source)
	if err != nil {
		t.Fatal(err)
	}
	request.Preview = false
	out, err := a.manualOrganize(t.Context(), request)
	if err != nil || out.Failed != 0 || out.Submitted != 1 || len(out.Items) != 1 {
		t.Fatal("directory actual submission", out, err)
	}
	var plan ImportPlan
	if err = a.Store.Get(t.Context(), "import_plan", out.Items[0].PlanID, &plan); err != nil || len(plan.Files) != 1 {
		t.Fatal(plan, err)
	}
	if _, err = a.Store.DB.Exec(`UPDATE jobs SET state='running',generation=generation+1 WHERE id=?`, out.Items[0].JobID); err != nil {
		t.Fatal(err)
	}
	job, err := scanJob(a.Store.DB.QueryRow(`SELECT `+jobColumns+` FROM jobs WHERE id=?`, out.Items[0].JobID))
	if err != nil {
		t.Fatal(err)
	}
	return a, object, plan, job, sharedDirectoryFixture{Source: source, Observer: observer, Target: target, SourceSnapshot: sourceStamp, RootSnapshot: root, Preserved: preserved, Contents: contents}
}

func TestImportDirectoryMergeNativePreservesUnrelatedAndColdPublication(t *testing.T) {
	for _, branch := range []string{"copy", "hardlink", "copy-cold"} {
		t.Run(branch, func(t *testing.T) {
			method := branch
			if branch == "copy-cold" {
				method = "copy"
			}
			a, object, plan, job, fixture := prepareSharedDirectoryImport(t, method)
			key := plan.ID + "/" + plan.Files[0].ID
			if branch == "copy-cold" {
				_, err := a.Store.DB.Exec(`CREATE TRIGGER merge_child_pause AFTER UPDATE ON records WHEN NEW.kind='import_receipt' AND NEW.id LIKE '` + key + `/directory/%' AND json_extract(NEW.payload,'$.state')='published' BEGIN UPDATE jobs SET state='cancelled',generation=generation+1 WHERE id='` + job.ID + `'; END`)
				if err != nil {
					t.Fatal(err)
				}
			}
			err := a.runImport(t.Context(), job, plan.ID, nil)
			var firstTarget string
			var firstStamp importSnapshot
			if branch == "copy-cold" {
				if !originalTransferInterrupted(err) {
					t.Fatal("real publication did not pause", err)
				}
				var parent importReceipt
				if e := a.Store.Get(t.Context(), "import_receipt", key, &parent); e != nil || parent.SharedDirectory == nil || parent.DirectoryMerge == nil {
					t.Fatal(parent, e)
				}
				for _, relative := range parent.DirectoryFiles {
					var child importReceipt
					if e := a.Store.Get(t.Context(), "import_receipt", key+"/directory/"+tokenHash(relative), &child); e == nil && child.State == "published" {
						firstTarget, firstStamp = child.Target, child.StageSnapshot
						break
					}
				}
				if firstTarget == "" {
					t.Fatal("no real prefix")
				}
				if _, e := a.Store.DB.Exec(`DROP TRIGGER merge_child_pause`); e != nil {
					t.Fatal(e)
				}
				a.wg.Wait()
				settings, dir := a.Settings(), a.Dir
				if e := a.Close(); e != nil {
					t.Fatal(e)
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
				if _, err = a.Store.DB.Exec(`UPDATE jobs SET state='running',generation=generation+1 WHERE id=?`, job.ID); err != nil {
					t.Fatal(err)
				}
				job, err = scanJob(a.Store.DB.QueryRow(`SELECT `+jobColumns+` FROM jobs WHERE id=?`, job.ID))
				if err != nil {
					t.Fatal(err)
				}
				err = a.runImport(t.Context(), job, plan.ID, nil)
			}
			if err != nil {
				t.Fatal("existing-root native publication", err)
			}
			if _, err = a.finishJob(t.Context(), job, "succeeded", "", "", 0); err != nil {
				t.Fatal(err)
			}
			var receipt importReceipt
			if err = a.Store.Get(t.Context(), "import_receipt", key, &receipt); err != nil || receipt.State != "published" || receipt.SharedDirectory == nil || len(receipt.SharedDirectory.Members) != 3 {
				t.Fatal("member-scoped completion", receipt, err)
			}
			if _, err = snapshotSharedDirectory(fixture.Target, receipt.StageSnapshot, receipt.SharedDirectory, true); err != nil {
				t.Fatal(err)
			}
			root, _, err := snapshotOriginalDirectory(fixture.Target)
			if err != nil || !sameInode(root, fixture.RootSnapshot) {
				t.Fatal("existing root replaced", root, err)
			}
			for rel, before := range fixture.Preserved {
				now, e := snapshotPath(filepath.Join(fixture.Target, rel))
				if e != nil || now != before {
					t.Fatal("unrelated entry changed", rel, e)
				}
				if _, ok := receipt.SharedDirectory.Members[rel]; ok {
					t.Fatal("unrelated entry adopted", rel)
				}
			}
			for rel, data := range fixture.Contents {
				target := filepath.Join(fixture.Target, rel)
				if got, e := os.ReadFile(target); e != nil || !bytes.Equal(got, data) {
					t.Fatal("actual merged leaf", rel, e)
				}
				src, e1 := os.Stat(filepath.Join(fixture.Source, rel))
				dst, e2 := os.Stat(target)
				if e1 != nil || e2 != nil || os.SameFile(src, dst) != (method == "hardlink") {
					t.Fatal("physical method", rel, e1, e2)
				}
				if target == firstTarget {
					if stamp, e := snapshotPath(target); e != nil || stamp != firstStamp {
						t.Fatal("cold recovery replaced successful leaf", e)
					}
				}
			}
			if now, _, e := snapshotOriginalDirectory(fixture.Source); e != nil || now != fixture.SourceSnapshot {
				t.Fatal("source mutated", now, e)
			}
			if receipt.StageSnapshot.Size != 24 || receipt.DirectoryStreamSize == nil || *receipt.DirectoryStreamSize != 12 {
				t.Fatal("unrelated content mixed into owned/STREAM size", receipt.StageSnapshot, receipt.DirectoryStreamSize)
			}
			for _, child := range receipt.DirectoryMerge.Children {
				if child.Before != nil && !child.Cleaned {
					t.Fatal("old leaf backup not cleaned", child)
				}
			}
			var asset Asset
			if err = a.assetAtPath(t.Context(), fixture.Target, &asset); err != nil {
				t.Fatal(err)
			}
			var owned managedFile
			if err = a.Store.Get(t.Context(), "managed_file", tokenHash(asset.Path), &owned); err != nil || owned.SharedDirectory == nil || owned.Snapshot != receipt.StageSnapshot {
				t.Fatal("shared ownership not committed", owned, err)
			}
			page := historyParityReadPage(t, a, "")
			if page.Total != 1 || page.Items[0].Status != "success" {
				t.Fatal("root history", page)
			}
			a.wg.Wait()
			response := ownedInputAPI(t, a, "GET", "/api/v1/plugin/"+fixture.Observer+"/observations", "")
			var observed []struct{ Type string }
			if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &observed) != nil {
				t.Fatal("actual chain observer", response.Code)
			}
			intercepts, overwrites := 0, 0
			for _, event := range observed {
				if event.Type == "transfer.intercept" {
					intercepts++
				}
				if event.Type == "transfer.overwrite.check" {
					overwrites++
				}
			}
			wantIntercepts := 1
			if branch == "copy-cold" {
				wantIntercepts = 2
			}
			if overwrites != 0 || intercepts != wantIntercepts {
				t.Fatal("directory incorrectly used leaf policies/events", intercepts, overwrites)
			}
			if branch != "copy-cold" {
				a.wg.Wait()
				events := ownedModuleRead(t, object).TransferEvents
				matched := 0
				for _, raw := range events {
					var event struct {
						Type string
						Data map[string]any
					}
					if json.Unmarshal(raw, &event) == nil && event.Type == "transfer.complete" {
						matched++
						info := sourceMap(event.Data["transferinfo"])
						if len(sourceRows(info["file_list_new"])) != 3 {
							t.Fatal("callback includes unrelated entries", info)
						}
					}
				}
				if matched != 1 {
					t.Fatal("single root completion", matched)
				}
			}
		})
	}
}
