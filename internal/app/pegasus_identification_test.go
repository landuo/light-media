package app

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"light-media/internal/catalog"
	"light-media/internal/media"
)

const pegasusReleaseFilename = "[飞驰人生3].Pegasus.3.2026.2160p.WEB-DL.60Fps.HDRVivid.H265.10bit.AAC-UBWEB.mp4"

func TestManualOrganizePegasusBilingualReleasePublishesHardlinkWithoutNFO(t *testing.T) {
	parsed := media.Parse(pegasusReleaseFilename)
	if parsed.Status != media.Parsed || parsed.Type != "movie" || parsed.Title != "Pegasus 3" || parsed.Year != 2026 || !reflect.DeepEqual(parsed.AlternateTitles, []string{"飞驰人生3"}) {
		t.Fatalf("release lost its bilingual identity or year: %+v", parsed)
	}
	a, source, target := importTestApp(t)
	// Official Chinese metadata can retain the Chinese title in both fields,
	// even when the filename's primary title and the first query are English.
	identity := catalog.Media{ID: 1462229, MediaType: "movie", Title: "飞驰人生3", OriginalTitle: "飞驰人生3", Year: 2026}
	for _, title := range []string{"Pegasus 3", "飞驰人生3"} {
		importCache(t, a, "directory-identify:movie:"+title, catalog.Page{Page: 1, TotalPages: 1, TotalResults: 1, Results: []catalog.Media{identity}})
	}
	importCache(t, a, "media:movie:1462229", identity)
	input := filepath.Join(source, pegasusReleaseFilename)
	contents := []byte("isolated synthetic Pegasus 3 video, not real media")
	importWrite(t, input, contents)
	if _, err := os.Stat(strings.TrimSuffix(input, ".mp4") + ".nfo"); !os.IsNotExist(err) {
		t.Fatalf("fixture unexpectedly has NFO identity: %v", err)
	}
	scrape := false
	ctx := context.Background()
	out, err := a.manualOrganize(ctx, manualOrganizeRequest{Paths: []string{input}, TargetPath: target, Mode: "hardlink", Scraping: &scrape})
	if err != nil || out.Failed != 0 || out.Submitted != 1 || len(out.Items) != 1 {
		t.Fatalf("automatic bilingual release recognition failed: %+v %v", out, err)
	}
	item := out.Items[0]
	if item.Media.ID != identity.ID || item.Media.MediaType != "movie" || item.Media.Year != 2026 || item.Target == "" {
		t.Fatalf("wrong identity selected for the complete release filename: %+v", item)
	}
	reorgRunJob(t, a, reorgJob(t, a, "import", item.PlanID))
	src, err := os.Stat(input)
	if err != nil {
		t.Fatalf("original release was removed: %v", err)
	}
	dst, err := os.Stat(item.Target)
	if err != nil || !os.SameFile(src, dst) {
		t.Fatalf("expected hardlink was not published: %v", err)
	}
	got, err := os.ReadFile(input)
	if err != nil || !bytes.Equal(got, contents) {
		t.Fatalf("original source contents changed: %v", err)
	}
	var asset Asset
	if err = a.assetAtPath(ctx, item.Target, &asset); err != nil || asset.Media.ID != identity.ID || asset.Media.Year != 2026 || asset.State != "present" {
		t.Fatalf("published asset does not retain the correct film and year: %+v %v", asset, err)
	}
}

func TestManualOrganizePegasusBilingualSearchRequiresOneDistinctMatchingIdentity(t *testing.T) {
	english := catalog.Media{ID: 1462229, MediaType: "movie", Title: "Pegasus 3", OriginalTitle: "Pegasus 3", Year: 2026}
	chinese := catalog.Media{ID: 1462229, MediaType: "movie", Title: "飞驰人生3", OriginalTitle: "飞驰人生3", Year: 2026}
	for _, ambiguous := range []bool{false, true} {
		name := "same ID is deduplicated and unrelated results are ignored"
		if ambiguous {
			name = "different matching IDs are rejected"
		}
		t.Run(name, func(t *testing.T) {
			a, source, target := importTestApp(t)
			chineseResult := chinese
			if ambiguous {
				chineseResult.ID = 900002
			}
			oldFilm := english
			oldFilm.ID, oldFilm.Year = 900003, 2019
			unrelated := catalog.Media{ID: 900004, MediaType: "movie", Title: "别的电影", OriginalTitle: "Other Movie", Year: 2026}
			importCache(t, a, "directory-identify:movie:Pegasus 3", catalog.Page{Page: 1, TotalPages: 1, TotalResults: 3, Results: []catalog.Media{english, oldFilm, unrelated}})
			importCache(t, a, "directory-identify:movie:飞驰人生3", catalog.Page{Page: 1, TotalPages: 1, TotalResults: 1, Results: []catalog.Media{chineseResult}})
			importCache(t, a, "media:movie:1462229", chinese)
			input := filepath.Join(source, pegasusReleaseFilename)
			importWrite(t, input, []byte("isolated synthetic bilingual identity test"))
			scrape := false
			out, err := a.manualOrganize(context.Background(), manualOrganizeRequest{Paths: []string{input}, TargetPath: target, Mode: "hardlink", Scraping: &scrape, Preview: true})
			if err != nil || len(out.Items) != 1 || out.Submitted != 0 {
				t.Fatalf("unexpected preview response: %+v %v", out, err)
			}
			if ambiguous {
				if out.Failed != 1 || out.Items[0].Status != "failed" || out.Items[0].ErrorCode != "DIRECTORY_IDENTITY_REVIEW" {
					t.Fatalf("distinct bilingual matches were not rejected: %+v", out)
				}
			} else if out.Failed != 0 || out.Items[0].Status != "ready" || out.Items[0].Media.ID != english.ID || out.Items[0].Media.Year != 2026 {
				t.Fatalf("duplicate identity or unrelated results caused a wrong match: %+v", out)
			}
			entries, err := os.ReadDir(target)
			if err != nil || len(entries) != 0 {
				t.Fatalf("identity preview or rejection wrote to the target: %v %v", entries, err)
			}
			if _, err = os.Stat(input); err != nil {
				t.Fatalf("identity preview or rejection changed the source: %v", err)
			}
		})
	}
}
