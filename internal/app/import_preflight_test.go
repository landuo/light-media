package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"light-media/internal/catalog"
)

func TestImportBatchReusesProbeOnlyForRecentSameRootAndBatch(t *testing.T) {
	dir, err := os.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer dir.Close()
	probe := importTargetProbe{}
	stamp := time.Unix(1000, 0)
	check := func(batch string, wantReuse bool) {
		t.Helper()
		if err := os.Chtimes(dir.Name(), stamp, stamp); err != nil {
			t.Fatal(err)
		}
		info, err := dir.Stat()
		if err != nil {
			t.Fatal(err)
		}
		ctx := context.WithValue(t.Context(), organizeFileKey{}, organizeFileWork{Job: Job{ParentJobID: "parent"}, BatchID: batch})
		if err := probe.check(ctx, dir, info); err != nil {
			t.Fatal(err)
		}
		info, err = dir.Stat()
		if err != nil || info.ModTime().Equal(stamp) != wantReuse {
			t.Fatalf("batch %q reuse=%t, info=%v, err=%v", batch, wantReuse, info, err)
		}
	}
	check("first", false)
	check("first", true)
	check("second", false)
	probe.checkedAt = time.Now().Add(-organizeProbeMaxAge)
	check("second", false)
	if err := os.Chmod(dir.Name(), 0750); err != nil {
		t.Fatal(err)
	}
	check("second", false)
}

func TestImportPreflightSpaceAndProbeLeavesNoFormalTarget(t *testing.T) {
	a, source, target := importTestApp(t)
	importFixture(t, a, "movie")
	input := filepath.Join(source, "Test (2024).mkv")
	importWrite(t, input, []byte("video bytes"))
	plan, err := a.previewImport(context.Background(), ImportRequest{MediaType: "movie", TMDBID: 123, RootID: "root", Mode: "copy", Files: []ImportFileRequest{{Path: input}}})
	if err != nil {
		t.Fatal(err)
	}
	if plan.SpaceAvailable <= 0 || plan.SpaceNeeded <= int64(len("video bytes")) || plan.SpaceNeeded > plan.SpaceAvailable || len(plan.Warnings) == 0 {
		t.Fatalf("missing space estimate %+v", plan)
	}
	entries, err := os.ReadDir(target)
	if err != nil || len(entries) != 0 {
		t.Fatalf("preview wrote formal or leftover probe files: %+v %v", entries, err)
	}
	if plan.Files[0].Method != "copy" {
		t.Fatal("actual method missing")
	}
	if err = a.setControl(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	if _, err = a.previewImport(context.Background(), ImportRequest{MediaType: "movie", TMDBID: 123, RootID: "root", Mode: "copy", Files: []ImportFileRequest{{Path: input}}}); err == nil {
		t.Fatal("paused preview wrote permission probe")
	}
}

func TestSourceScanNFOIdentityAndNumbersAreHintsWithoutOverride(t *testing.T) {
	a, source, _ := importTestApp(t)
	identity := importFixture(t, a, "tv")
	show := filepath.Join(source, "show")
	video := filepath.Join(show, "Season 4", "unhelpful-file.mkv")
	importWrite(t, video, []byte("video"))
	episode := catalog.Episode{ID: 7000, SeasonNumber: 4, EpisodeNumber: 5, Name: "单集名"}
	showNFO, err := assetNFO(identity, nil)
	if err != nil {
		t.Fatal(err)
	}
	episodeNFO, err := assetNFO(identity, &episode)
	if err != nil {
		t.Fatal(err)
	}
	importWrite(t, filepath.Join(show, "tvshow.nfo"), showNFO)
	importWrite(t, filepath.Join(filepath.Dir(video), "unhelpful-file.nfo"), episodeNFO)
	result, err := a.scanFiles(context.Background(), source, true)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(result)
	var scanned struct {
		Files []importScanFile `json:"files"`
	}
	json.Unmarshal(raw, &scanned)
	if len(scanned.Files) != 1 || scanned.Files[0].InferredMedia == nil {
		t.Fatalf("no NFO hint: %s", raw)
	}
	hint := scanned.Files[0]
	if hint.InferredMedia.ID != identity.ID || hint.InferredMedia.ID == episode.ID || hint.InferredMedia.EpisodeID != episode.ID || hint.IdentitySource != "nfo" || *hint.Numbering.Season != 4 || hint.Numbering.Episodes[0] != 5 {
		t.Fatalf("NFO evidence conflated identity %+v", hint)
	}
	if hint.Parsed.Status == "parsed" {
		t.Fatal("filename unexpectedly supplied identity")
	}
	other := catalog.Media{ID: 456, MediaType: "movie", Title: "人工明确选择", Year: 2025}
	importCache(t, a, "media:movie:456", other)
	plan, err := a.previewImport(context.Background(), ImportRequest{MediaType: "movie", TMDBID: 456, RootID: "root", Mode: "copy", Files: []ImportFileRequest{{Path: video}}})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Media.ID != 456 || len(plan.Warnings) < 2 {
		t.Fatal("NFO silently overrode user choice or hid conflict")
	}
}

func TestImportCompletionQueuesOnlyCurrentActiveSubscription(t *testing.T) {
	for _, state := range []string{"active", "paused", "cancelled"} {
		t.Run(state, func(t *testing.T) {
			a, _, _ := importTestApp(t)
			identity := importFixture(t, a, "movie")
			intent := Intent{ID: "intent", Media: identity, RootID: "root", State: "downloaded", SubscriptionID: "sub", SubscriptionRevision: 3}
			subscription := Subscription{ID: "sub", Media: identity, Revision: 3, State: state}
			a.Store.Put(context.Background(), "intent", intent.ID, intent)
			a.Store.Put(context.Background(), "subscription", subscription.ID, subscription)
			a.Store.Put(context.Background(), "asset", "asset", Asset{ID: "asset", Media: identity, State: "present"})
			a.Store.DB.Exec(`INSERT INTO asset_slots VALUES(?,?,?,?)`, mediaKey(identity), 0, 0, "asset")
			tx, err := a.Store.DB.BeginTx(context.Background(), nil)
			if err != nil {
				t.Fatal(err)
			}
			plan := ImportPlan{ID: "plan", IntentID: intent.ID, RootID: "root", Media: identity, WorkflowID: "wf"}
			if err = a.importCompleteIntent(context.Background(), tx, plan); err != nil {
				tx.Rollback()
				t.Fatal(err)
			}
			if err = tx.Commit(); err != nil {
				t.Fatal(err)
			}
			var count int
			a.Store.DB.QueryRow(`SELECT COUNT(*) FROM jobs WHERE kind='subscription'`).Scan(&count)
			if (state == "active") != (count == 1) {
				t.Fatalf("state=%s jobs=%d", state, count)
			}
			var after Subscription
			a.Store.Get(context.Background(), "subscription", subscription.ID, &after)
			if after.State != state {
				t.Fatal("completion reactivated subscription")
			}
		})
	}
}
