package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"light-media/internal/catalog"
)

func TestEpisodeScopesQualityIdentityLegacyCompatibility(t *testing.T) {
	file := PlanFile{ID: "file", Source: "/source/episode.mkv", Target: "/library/episode.mkv", Method: "hardlink", Season: 4, Episodes: []int{16}, Size: 12, Device: 1, Inode: 2, ModNS: 3}
	plan := ImportPlan{ID: "plan", IntentID: "intent", Media: catalog.Media{ID: 123, MediaType: "tv"}, RootID: "root", Mode: "hardlink", Files: []PlanFile{file}}
	// Deliberately reproduce the pre-scopes identity layout, rather than using
	// a new-model marshal as the compatibility oracle.
	type legacyFile struct {
		ID, Source, Target, Method string
		Season                     int
		Episodes                   []int
		SourceSnapshot             importSnapshot
	}
	legacy := tokenHash(string(mustCleanSeedJSON(struct {
		Plan, Intent, Media, Root, Mode, Directory string
		Files                                      []legacyFile
	}{plan.ID, plan.IntentID, mediaKey(plan.Media), plan.RootID, plan.Mode, "", []legacyFile{{file.ID, file.Source, file.Target, file.Method, file.Season, file.Episodes, planSnapshot(file)}}})))
	for _, version := range []int{0, 1} {
		plan.DirectorySourceVersion = version
		want := legacy
		if version != 0 {
			want = tokenHash(legacy + "\x00directory_source=" + jsonNumber(int64(version)))
		}
		for _, scopes := range [][]canonicalEpisodeScope{nil, {}} {
			plan.Files[0].EpisodeScopes = scopes
			if got := qualityImportSHA(plan); got != want {
				t.Fatalf("empty scopes changed legacy hash, version=%d scopes=%#v: %s != %s", version, scopes, got, want)
			}
		}
	}
	plan.Files[0].EpisodeScopes = []canonicalEpisodeScope{{Season: 4, Episodes: []int{16}}, {Season: 0, Episodes: []int{2}}}
	frozenHash := qualityImportSHA(plan)
	asset := Asset{ID: "asset", RootID: "root", Media: plan.Media, Path: file.Target, SourcePath: file.Source, State: "present", Season: file.Season, Episodes: file.Episodes, EpisodeScopes: cloneEpisodeScopes(plan.Files[0].EpisodeScopes)}
	frozen := asset
	asset.EpisodeScopes = cloneEpisodeScopes(asset.EpisodeScopes)
	asset.EpisodeScopes[1].Episodes[0] = 3
	plan.Files[0].EpisodeScopes = cloneEpisodeScopes(asset.EpisodeScopes)
	if qualityImportSHA(plan) == frozenHash || reorganizeSameAsset(asset, frozen) {
		t.Fatal("secondary-season mutation was omitted from a sealed identity")
	}
	if !reorganizeSameAsset(frozen, frozen) {
		t.Fatal("unchanged canonical scope failed identity comparison")
	}
}

func episodeScopesQualityFixture(t *testing.T) (*App, Asset, PlanFile, qualityDownloadSnapshot, managedFile) {
	t.Helper()
	a, source, library := importTestApp(t)
	identity := importFixture(t, a, "tv")
	videoPath, subtitlePath := filepath.Join(library, "group.mkv"), filepath.Join(library, "group.zh.srt")
	importWrite(t, videoPath, []byte("one file, standard S04E16 and S00E02"))
	importWrite(t, subtitlePath, []byte("subtitle for both canonical episodes"))
	scopes := []canonicalEpisodeScope{{Season: 4, Episodes: []int{16}}, {Season: 0, Episodes: []int{2}}}
	asset := Asset{ID: "cross-season-asset", Media: identity, RootID: "root", Path: videoPath, SourcePath: filepath.Join(source, "old.mkv"), State: "present", Season: 4, Episodes: []int{16}, EpisodeScopes: scopes}
	stamp, err := snapshotPath(subtitlePath)
	if err != nil {
		t.Fatal(err)
	}
	sidecar := managedFile{Path: subtitlePath, RootID: asset.RootID, AssetID: asset.ID, MediaKey: mediaKey(identity), Kind: "subtitle", Snapshot: stamp}
	if err = a.Store.Put(t.Context(), "asset", asset.ID, asset); err != nil {
		t.Fatal(err)
	}
	if err = a.Store.Put(t.Context(), "managed_file", tokenHash(subtitlePath), sidecar); err != nil {
		t.Fatal(err)
	}
	file := PlanFile{ID: "replacement", Source: filepath.Join(source, "new.mkv"), Target: videoPath, Season: 4, Episodes: []int{16}, EpisodeScopes: cloneEpisodeScopes(scopes)}
	proof := qualityDownloadSnapshot{BestVersion: true, MediaKey: mediaKey(identity), Season: 4, Episodes: []int{16}, Slots: map[int][]string{16: {asset.ID}}, Assets: []qualityAssetSnapshot{{Asset: asset, RootPath: library}}}
	return a, asset, file, proof, sidecar
}

func TestEpisodeScopesQualityOwnersRequireWholePhysicalFile(t *testing.T) {
	a, asset, full, proof, sidecar := episodeScopesQualityFixture(t)
	cases := []struct {
		name string
		file PlanFile
		pass bool
	}{
		{"full-cross-season", full, true},
		{"first-season-only", PlanFile{Target: full.Target, Season: 4, Episodes: []int{16}}, false},
		{"same-episode-numbers-wrong-season", PlanFile{Target: full.Target, Season: 4, Episodes: []int{16, 2}}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			owner, err := qualityTargetOwner(&proof, tc.file)
			if tc.pass {
				if err != nil || owner == nil || owner.Asset.ID != asset.ID {
					t.Fatalf("full video ownership: %+v %v", owner, err)
				}
			} else {
				assertEpisodeScopePartialError(t, err)
				if owner != nil {
					t.Fatal("partial replacement acquired video ownership")
				}
			}
			sideOwner, err := a.qualitySidecarOwner(t.Context(), a.Store.DB, &proof, tc.file, sidecar.Path)
			if tc.pass {
				if err != nil || sideOwner == nil || *sideOwner != sidecar {
					t.Fatalf("full subtitle ownership: %+v %v", sideOwner, err)
				}
			} else {
				assertEpisodeScopePartialError(t, err)
				if sideOwner != nil {
					t.Fatal("partial replacement acquired subtitle ownership")
				}
			}
		})
	}
}

func assertEpisodeScopePartialError(t *testing.T, err error) {
	t.Helper()
	var api *APIError
	if !errors.As(err, &api) || api.Code != "QUALITY_PARTIAL_PATH_CONFLICT" {
		t.Fatalf("expected whole-file coverage conflict, got %v", err)
	}
}

func TestEpisodeScopesDirectoryOverwriteWholeFileGuard(t *testing.T) {
	a, asset, full, _, sidecar := episodeScopesQualityFixture(t)
	for _, source := range []string{"active-transfer", "persisted-plan"} {
		for _, complete := range []bool{false, true} {
			name := source + "/partial"
			if complete {
				name = source + "/full"
			}
			t.Run(name, func(t *testing.T) {
				file := full
				if !complete {
					file.EpisodeScopes = nil
					file.Episodes = []int{16, 2} // Same numbers do not prove S00 coverage.
				}
				ctx := t.Context()
				if source == "active-transfer" {
					ctx = context.WithValue(ctx, originalTransferWriteContextKey{}, &originalTransferWriteContext{File: file, Media: asset.Media})
				} else {
					plan := ImportPlan{ID: "scoped-plan", Media: asset.Media, Files: []PlanFile{file}}
					if err := a.Store.Put(ctx, "import_plan", plan.ID, plan); err != nil {
						t.Fatal(err)
					}
				}
				for _, owner := range []managedFile{{Path: asset.Path, AssetID: asset.ID, RootID: asset.RootID, Kind: "video"}, sidecar} {
					op := directoryOverwriteOperation{ID: "scoped-plan/" + file.ID, Targets: []directoryOverwriteTarget{{Path: owner.Path, Owner: &owner}}}
					if owner.Kind != "video" {
						op.ID += "/side-0"
					}
					err := a.guardDirectoryOverwriteInventory(ctx, op)
					if complete {
						if err != nil {
							t.Fatal(err)
						}
					} else {
						assertEpisodeScopePartialError(t, err)
					}
					if _, err := os.Stat(owner.Path); err != nil {
						t.Fatal("coverage preflight mutated old physical file", err)
					}
				}
			})
		}
	}
}
