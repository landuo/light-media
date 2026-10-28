package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestOriginalScrapeRetryPreservesSourceNumbers(t *testing.T) {
	a := newTestApp(t)
	ctx := context.WithValue(context.Background(), jobTraceKey{}, jobTrace{Job: "owned-parent", Request: "owned-quoted-\"request"})
	raw := json.RawMessage(`{"mediainfo":{"tmdb_id":9007199254740993},"meta":{"tmdbid":9223372036854775807},"file_list":[]}`)
	tx, e := a.Store.DB.BeginTx(ctx, nil)
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback()
	batch := originalScrapeBatch{ID: "owned-closed-batch", Closed: true, Flushed: true}
	if e = txPut(ctx, tx, "original_scrape_batch", batch.ID, batch); e != nil {
		t.Fatal(e)
	}
	j, e := enqueueTx(ctx, tx, "quality_cleanup", map[string]any{"id": "owned-cleanup", "original_scrape_batch": batch.ID, "original_metadata_event": raw}, "")
	if e != nil {
		t.Fatal(e)
	}
	var initial map[string]json.RawMessage
	if e = json.Unmarshal(j.Payload, &initial); e != nil {
		t.Fatal(e)
	}
	if string(initial["original_metadata_event"]) != string(raw) {
		t.Fatal("queue changed source integer precision", string(initial["original_metadata_event"]))
	}
	if j.ParentJobID != "owned-parent" || j.RequestID != "owned-quoted-\"request" {
		t.Fatal("queue trace fields changed", j.ParentJobID, j.RequestID)
	}
	if _, e = tx.Exec(`UPDATE jobs SET state='failed' WHERE id=?`, j.ID); e != nil {
		t.Fatal(e)
	}
	if e = tx.Commit(); e != nil {
		t.Fatal(e)
	}
	j.State = "failed"
	if _, e = a.retryOriginalScrapeJob(ctx, j); e != nil {
		t.Fatal(e)
	}
	var encoded []byte
	if e = a.Store.DB.QueryRow(`SELECT payload FROM jobs WHERE id=?`, j.ID).Scan(&encoded); e != nil {
		t.Fatal(e)
	}
	var fields map[string]json.RawMessage
	if e = json.Unmarshal(encoded, &fields); e != nil {
		t.Fatal(e)
	}
	if string(fields["original_metadata_event"]) != string(raw) {
		t.Fatal("source integer precision changed", string(fields["original_metadata_event"]))
	}
	tx, e = a.Store.DB.BeginTx(ctx, nil)
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback()
	if e = deferOriginalScrapeTargetTx(ctx, tx, Job{Payload: encoded}, "quality_cleanup", "owned-cleanup", raw, true); e != nil {
		t.Fatal(e)
	}
	if e = tx.QueryRow(`SELECT payload FROM jobs WHERE id=?`, j.ID).Scan(&encoded); e != nil {
		t.Fatal(e)
	}
	if e = json.Unmarshal(encoded, &fields); e != nil {
		t.Fatal(e)
	}
	if string(fields["original_metadata_event"]) != string(raw) {
		t.Fatal("deferred context precision changed", string(fields["original_metadata_event"]))
	}
	if e = tx.Commit(); e != nil {
		t.Fatal(e)
	}
}

// A persisted v2 quality cleanup receipt, using actual old/new owned files.
// New quality imports use v3 directory overwrite; this is explicitly synthetic
// legacy recovery setup, not a claimed new downloader/quality acquisition run.
func seedOriginalLegacyQualityCleanup(t *testing.T, a *App, parent Job, plan ImportPlan, old Asset, enabled bool) {
	t.Helper()
	ctx := context.Background()
	var replacement Asset
	if err := a.assetAtPath(ctx, plan.Files[0].Target, &replacement); err != nil {
		t.Fatal(err)
	}
	var newVideo managedFile
	if err := a.Store.Get(ctx, "managed_file", tokenHash(replacement.Path), &newVideo); err != nil {
		t.Fatal(err)
	}
	root, err := a.importRoot(replacement.RootID)
	if err != nil {
		t.Fatal(err)
	}
	p := qualityCleanup{Version: 2, ID: plan.ID, NewAsset: replacement.ID, NewPath: replacement.Path, NewRoot: replacement.RootID, NewSnapshot: newVideo.Snapshot, NewFiles: []managedFile{newVideo}, State: "pending", RootPaths: map[string]string{replacement.RootID: root.Path}, OldAssets: []string{old.ID}}
	oldRoot, err := a.importRoot(old.RootID)
	if err != nil {
		t.Fatal(err)
	}
	p.RootPaths[old.RootID] = oldRoot.Path
	rows, err := a.Store.DB.Query(`SELECT payload FROM records WHERE kind='managed_file' AND json_extract(payload,'$.asset_id')=?`, old.ID)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var raw []byte
		var f managedFile
		if err = rows.Scan(&raw); err != nil {
			t.Fatal(err)
		}
		if err = json.Unmarshal(raw, &f); err != nil {
			t.Fatal(err)
		}
		p.Files = append(p.Files, f)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		t.Fatal(err)
	}
	job := reorgJob(t, a, "reorganize_cleanup", plan.ID)
	var fields struct {
		Event json.RawMessage `json:"original_metadata_event"`
	}
	if err = json.Unmarshal(job.Payload, &fields); err != nil {
		t.Fatal(err)
	}
	tx, err := a.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err = a.writeQualitySealedTx(ctx, tx, "subscription_quality_cleanup", p.ID, p); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(`UPDATE jobs SET kind='quality_cleanup' WHERE id=?`, job.ID); err != nil {
		t.Fatal(err)
	}
	if err = deferOriginalScrapeTargetTx(ctx, tx, parent, "quality_cleanup", plan.ID, fields.Event, enabled); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

// Expected contract: fixed TransferChain records only successful transfers,
// waits for all pending tasks and emits one target. The existing target cleanup
// is part of publication; its failure must not become a scrape success.
func TestOriginalScrapeDeferredReorganization(t *testing.T) {
	for _, kind := range []string{"reorganize_cleanup", "quality_cleanup"} {
		for _, mode := range []string{"success", "cold-pending", "cleanup-failed", "disabled", "sql-retry"} {
			t.Run(kind+"/"+mode, func(t *testing.T) {
				a, source, old := reorgFixture(t)
				ctx := context.Background()
				full, err := sourceTMDBMediaInfo(json.RawMessage(`{"id":123,"title":"Owned","original_title":"Owned","release_date":"2024-01-01","genres":[],"credits":{"cast":[],"crew":[]},"external_ids":{}}`), "movie", "image.tmdb.org", "")
				if err != nil {
					t.Fatal(err)
				}
				observer := installOwnedTransferObserver(t, a, nil, nil, map[string]any{"mediainfo": full})
				read := func() []json.RawMessage {
					t.Helper()
					a.wg.Wait()
					response := ownedInputAPI(t, a, "GET", "/api/v1/plugin/"+observer+"/observations", "")
					var rows []struct {
						Type string
						Data json.RawMessage
					}
					if json.Unmarshal(response.Body.Bytes(), &rows) != nil {
						t.Fatal("observations", response.Body.String())
					}
					var result []json.RawMessage
					for _, row := range rows {
						if row.Type == "metadata.scrape" {
							result = append(result, row.Data)
						}
					}
					return result
				}
				target := filepath.Join(filepath.Dir(filepath.Dir(old.Path)), "deferred-library")
				if err = os.MkdirAll(target, 0750); err != nil {
					t.Fatal(err)
				}
				plan, err := a.previewImport(ctx, ImportRequest{Manual: true, ReorganizeAssetID: old.ID, MediaType: "movie", TMDBID: 123, TargetPath: target, Mode: "copy", Scraping: importFlag(mode != "disabled"), Files: []ImportFileRequest{{Path: source}}})
				if err != nil || len(plan.Files) != 1 || plan.Files[0].Status != "ready" {
					t.Fatal("preview", plan, err)
				}
				job := overwriteStart(t, a, plan)
				if err = a.runImport(ctx, job, plan.ID, nil); err != nil {
					t.Fatal(err)
				}
				if kind == "quality_cleanup" {
					seedOriginalLegacyQualityCleanup(t, a, job, plan, old, mode != "disabled")
				}
				if _, err = a.finishJob(ctx, job, "succeeded", "", "", 0); err != nil {
					t.Fatal(err)
				}
				if len(read()) != 0 {
					t.Fatal("event before cleanup")
				}
				var replacement Asset
				if err = a.assetAtPath(ctx, plan.Files[0].Target, &replacement); err != nil {
					t.Fatal(err)
				}
				var count int
				if err = a.Store.DB.QueryRow(`SELECT COUNT(*) FROM jobs WHERE kind='scrape' AND json_extract(payload,'$.id')=?`, replacement.ID).Scan(&count); err != nil || count != 0 {
					t.Fatal("premature core consumer", count, err)
				}
				if mode == "cold-pending" {
					dir := a.Dir
					if err = a.Close(); err != nil {
						t.Fatal(err)
					}
					a, err = New(Options{DataDir: dir})
					if err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() { a.Close() })
					if err = a.initializeOriginalPlugins(ctx); err != nil {
						t.Fatal(err)
					}
					if err = a.recoverOriginalScrapeBatches(ctx); err != nil {
						t.Fatal(err)
					}
					if len(read()) != 0 {
						t.Fatal("cold pending event")
					}
				}
				cleanup := reorgJob(t, a, kind, plan.ID)
				if _, err = a.Store.DB.Exec(`UPDATE jobs SET state='running',generation=generation+1 WHERE id=?`, cleanup.ID); err != nil {
					t.Fatal(err)
				}
				cleanup, err = scanJob(a.Store.DB.QueryRow(`SELECT `+jobColumns+` FROM jobs WHERE id=?`, cleanup.ID))
				if err != nil {
					t.Fatal(err)
				}
				if mode == "cleanup-failed" {
					importWrite(t, replacement.Path, []byte("changed replacement rejects cleanup"))
				}
				if mode == "sql-retry" {
					if _, err = a.Store.DB.Exec(`CREATE TRIGGER owned_deferred_failure BEFORE INSERT ON records WHEN NEW.kind='original_scrape_batch' BEGIN SELECT RAISE(ABORT,'owned deferred commit failure'); END`); err != nil {
						t.Fatal(err)
					}
				}
				err = a.safeRun(ctx, cleanup)
				if (err != nil) != (mode == "cleanup-failed" || mode == "sql-retry") {
					t.Fatal("cleanup", err)
				}
				state := "succeeded"
				if err != nil {
					state = "failed"
				}
				if mode == "sql-retry" {
					if _, err = a.Store.DB.Exec(`DROP TRIGGER owned_deferred_failure`); err != nil {
						t.Fatal(err)
					}
				}
				if _, err = a.finishJob(ctx, cleanup, state, "", "", 0); err != nil {
					t.Fatal(err)
				}
				if mode == "sql-retry" {
					if len(read()) != 0 {
						t.Fatal("failed cleanup transaction emitted event")
					}
					login := callTest(t, a.Handler(), "POST", "/api/auth/login", `{"username":"admin","password":"a-strong-test-password"}`, nil, "")
					if login.Code != 200 || len(login.Result().Cookies()) == 0 {
						t.Fatal("actual login", login.Code)
					}
					response := callTest(t, a.Handler(), "POST", "/api/jobs/"+cleanup.ID+"/retry", "{}", login.Result().Cookies()[0], "")
					if response.Code != 200 {
						t.Fatal("actual explicit retry", response.Code, response.Body.String())
					}
					retry := reorgJob(t, a, kind, plan.ID)
					if originalScrapeBatchID(retry) == originalScrapeBatchID(cleanup) {
						t.Fatal("retry reopened finished batch")
					}
					if _, err = a.Store.DB.Exec(`UPDATE jobs SET state='running',generation=generation+1 WHERE id=?`, retry.ID); err != nil {
						t.Fatal(err)
					}
					retry, err = scanJob(a.Store.DB.QueryRow(`SELECT `+jobColumns+` FROM jobs WHERE id=?`, retry.ID))
					if err != nil {
						t.Fatal(err)
					}
					if err = a.safeRun(ctx, retry); err != nil {
						t.Fatal("cleanup commit recovery", err)
					}
					if _, err = a.finishJob(ctx, retry, "succeeded", "", "", 0); err != nil {
						t.Fatal(err)
					}
				}
				want := 1
				if mode == "cleanup-failed" || mode == "disabled" {
					want = 0
				}
				events := read()
				if len(events) != want {
					t.Fatal("source event count after cleanup", len(events), want)
				}
				if err = a.Store.DB.QueryRow(`SELECT COUNT(*) FROM jobs WHERE kind='scrape' AND json_extract(payload,'$.id')=?`, replacement.ID).Scan(&count); err != nil || count != want {
					t.Fatal("same core consumer once", count, err)
				}
				if want == 1 {
					var event struct {
						Files []string `json:"file_list"`
					}
					if json.Unmarshal(events[0], &event) != nil || len(event.Files) != 1 || event.Files[0] != replacement.Path {
						t.Fatal("successful target only", string(events[0]))
					}
					reorgRunJob(t, a, reorgJob(t, a, "scrape", replacement.ID))
					if raw, e := os.ReadFile(replacement.Path[:len(replacement.Path)-len(filepath.Ext(replacement.Path))] + ".nfo"); e != nil || len(raw) == 0 {
						t.Fatal("actual NFO", e)
					}
				}
				_, err = os.Stat(old.Path)
				if mode == "cleanup-failed" {
					if err != nil {
						t.Fatal("old retained", err)
					}
				} else if !os.IsNotExist(err) {
					t.Fatal("old cleaned", err)
				}
				if err = a.recoverOriginalScrapeBatches(ctx); err != nil {
					t.Fatal(err)
				}
				if len(read()) != want {
					t.Fatal("completed batch replay")
				}
			})
		}
	}
}
