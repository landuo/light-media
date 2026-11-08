package app

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

func TestOriginalLarkProviderEpisodeQualityReplacementLinuxAMD64(t *testing.T) {
	t.Setenv("RECOGNIZE_SOURCE", "bangumi")
	t.Setenv("SUBSCRIBE_MODE", "rss")
	testOriginalLarkProviderSelection(t, "provider_subscription_episode_quality")
}

func seedOriginalLarkProviderEpisodeAsset(t *testing.T, a *App, source string, owner Subscription, model json.RawMessage) Asset {
	t.Helper()
	if !owner.BestVersion || owner.BestVersionFull || owner.StartEpisode != 1 || owner.TotalEpisodes != 4 {
		t.Fatalf("Lark did not create a per-episode quality subscription: best=%t full=%t start=%d total=%d",
			owner.BestVersion, owner.BestVersionFull, owner.StartEpisode, owner.TotalEpisodes)
	}
	oldPath := filepath.Join(source, owner.Media.Title+" S01E01 720p.mkv")
	importWrite(t, oldPath, bytes.Repeat([]byte{'l'}, 1024))
	// Seed prior library history using the ordinary import planner and worker
	// with the verified provider model. This setup performs no tracker Add.
	ctx := t.Context()
	plan, err := a.previewImport(ctx, ImportRequest{
		MediaType: "tv", RootID: "root", Mode: "hardlink", Manual: true, Scraping: importFlag(false),
		Files:         []ImportFileRequest{{Path: oldPath, Season: 1, Episodes: []int{1}}},
		originalRetry: true, providerIdentity: owner.Media, originalResolvedMedia: model,
	})
	if err != nil || len(plan.Files) != 1 || plan.Files[0].Status != "ready" {
		t.Fatalf("could not plan actual prior provider episode: %+v, %v", plan.Files, err)
	}
	job, err := a.executeImport(ctx, plan.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(6 * time.Second); ; {
		job, err = scanJob(a.Store.DB.QueryRowContext(ctx, `SELECT `+jobColumns+` FROM jobs WHERE id=?`, job.ID))
		if err == nil && job.State == "succeeded" {
			break
		}
		if err != nil || job.State == "failed" || time.Now().After(deadline) {
			t.Fatalf("prior provider episode did not organize: state=%s code=%s err=%v", job.State, job.ErrorCode, err)
		}
		time.Sleep(25 * time.Millisecond)
	}
	var asset Asset
	if err := a.assetAtPath(ctx, plan.Files[0].Target, &asset); err != nil || mediaKey(asset.Media) != mediaKey(owner.Media) ||
		asset.SourcePath != oldPath || !slices.Equal(asset.Episodes, []int{1}) {
		t.Fatalf("initial library slot did not own the verified provider episode: %+v, %v", asset, err)
	}
	priorSource, sourceErr := os.Stat(oldPath)
	priorLibrary, libraryErr := os.Stat(asset.Path)
	if sourceErr != nil || libraryErr != nil || !os.SameFile(priorSource, priorLibrary) {
		t.Fatalf("initial library episode was not a real hardlink: %v, %v", sourceErr, libraryErr)
	}
	return asset
}

func assertOriginalLarkProviderEpisodeQualityAccepted(t *testing.T, a *App, owner Subscription, oldAsset Asset, root Job, hash string) {
	t.Helper()
	ctx := t.Context()
	batch, err := a.readAcquisition(ctx, a.Store.DB, root.ID)
	// A successful RSS job can accept a single episode while the other
	// episodes keep the acquisition's full-coverage marker unset.
	if err != nil || batch.Completed || batch.Subscription.ID != owner.ID || batch.Subscription.Revision != owner.Revision ||
		len(batch.Steps) != 1 || batch.Steps[0].Phase != "accepted" {
		t.Fatalf("expected one accepted episode without full coverage: complete=%t steps=%d err=%v", batch.Completed, len(batch.Steps), err)
	}
	intentID := batch.Steps[0].IntentID
	proof, err := a.qualitySnapshot(ctx, a.Store.DB, "intent", intentID, hash)
	if err != nil || proof == nil || !proof.BestVersion || proof.MediaKey != mediaKey(owner.Media) ||
		proof.AllowWholeExtra || proof.Resource.ConfirmedFullCoverage || proof.Resource.Priority != 100 ||
		!slices.Equal(proof.Episodes, []int{1}) || !slices.Equal(proof.Slots[1], []string{oldAsset.ID}) ||
		len(proof.Assets) != 1 || proof.Assets[0].Asset.ID != oldAsset.ID {
		t.Fatalf("priority decision did not bind the original provider slot to the higher release: %+v, %v", proof, err)
	}
	var updated Subscription
	if err := a.Store.Get(ctx, "subscription", owner.ID, &updated); err != nil || mediaKey(updated.Media) != mediaKey(owner.Media) ||
		updated.Revision != owner.Revision || updated.EpisodePriority["1"] != 100 || updated.EpisodePriority["2"] != 0 ||
		updated.EpisodePriority["3"] != 0 || updated.EpisodePriority["4"] != 0 {
		t.Fatalf("accepted release did not advance just E01 and keep the subscription: priorities=%v, err=%v", updated.EpisodePriority, err)
	}
	var ownerID string
	if err := a.Store.DB.QueryRowContext(ctx, `SELECT asset_id FROM asset_slots WHERE media_key=? AND season=1 AND episode=1`, mediaKey(owner.Media)).
		Scan(&ownerID); err != nil || ownerID != oldAsset.ID {
		t.Fatalf("acceptance replaced the old library slot before verified completion: %s, %v", ownerID, err)
	}
	got, err := os.ReadFile(oldAsset.Path)
	if err != nil || !bytes.Equal(got, bytes.Repeat([]byte{'l'}, 1024)) {
		t.Fatalf("acceptance altered the old managed video before completion: %v", err)
	}
}

func assertOriginalLarkProviderEpisodeReplacement(t *testing.T, a *App, oldAsset, replacement Asset) {
	t.Helper()
	if replacement.ID == oldAsset.ID || replacement.Path != oldAsset.Path || replacement.SourcePath == oldAsset.SourcePath || replacement.IntentID == "" {
		t.Fatalf("higher episode did not replace the exact older library path: old=%s new=%s", oldAsset.Path, replacement.Path)
	}
	var retired Asset
	if err := a.Store.Get(t.Context(), "asset", oldAsset.ID, &retired); err != nil || retired.State != "superseded" {
		t.Fatalf("previous provider asset was not retired after publication: %s, %v", retired.State, err)
	}
	for path, content := range map[string][]byte{
		oldAsset.SourcePath:    bytes.Repeat([]byte{'l'}, 1024),
		replacement.SourcePath: bytes.Repeat([]byte{1}, 1024),
		replacement.Path:       bytes.Repeat([]byte{1}, 1024),
	} {
		if got, err := os.ReadFile(path); err != nil || !bytes.Equal(got, content) {
			t.Fatalf("provider replacement did not preserve download sources and publish higher bytes at %s: %v", path, err)
		}
	}
	t.Log("one provider-native RSS episode advanced only its priority, kept the old slot until verified completion, replaced its real hardlink, and preserved both source files")
}
