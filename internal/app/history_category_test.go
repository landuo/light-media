package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHistoryCategoryDoesNotRequireCategoryFoldersOrMetadataRequests(t *testing.T) {
	for _, tc := range []struct {
		name, want, policy string
		original           bool
		incomplete         bool
	}{
		{name: "source DTO", want: "原识别分类", original: true},
		{name: "known local metadata", want: "华语电影"},
		{name: "unknown metadata", incomplete: true},
		{name: "missing custom field", policy: "movie:\n  高预算:\n    budget: '100'\n  其他:\n"},
		{name: "invalid reload retains active policy", policy: "invalid policy", want: "华语电影"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, source, target := importTestApp(t)
			identity := importFixture(t, a, "movie")
			identity.CategoryMetadataKnown = !tc.incomplete
			importCache(t, a, "media:movie:123", identity)
			calls := useCategoryDetailResponse(t, a, 500, map[string]any{"error": "unexpected metadata request"})
			if tc.policy != "" {
				if err := os.WriteFile(filepath.Join(a.Dir, "category.yaml"), []byte(tc.policy), 0600); err != nil {
					t.Fatal(err)
				}
				// The invalid policy may fail reload; display-only classification must not
				// turn the optional category-folder switch back on or block the transfer.
				_, _ = a.reloadMediaCategoryPolicy()
			}
			path := filepath.Join(source, "Test.2024.mkv")
			importWrite(t, path, []byte("synthetic category history fixture"))
			request := ImportRequest{MediaType: "movie", TMDBID: 123, RootID: "root", Mode: "hardlink", Scraping: importFlag(false), LibraryTypeFolder: importFlag(false), Files: []ImportFileRequest{{Path: path}}}
			if tc.original {
				request.originalResolvedMedia = json.RawMessage(`{"source":"themoviedb","type":"电影","tmdb_id":123,"title":"测试影片","release_date":"2024-01-01","category":"原识别分类"}`)
			}
			ctx := context.Background()
			plan, err := a.previewImport(ctx, request)
			if err != nil || len(plan.Files) != 1 || plan.Files[0].Status != "ready" || plan.Category != tc.want || *calls != 0 {
				t.Fatalf("category=%q want=%q calls=%d plan=%+v err=%v", plan.Category, tc.want, *calls, plan, err)
			}
			rel, err := filepath.Rel(target, plan.Files[0].Target)
			if err != nil || strings.HasPrefix(rel, "华语电影/") || strings.HasPrefix(rel, "原识别分类/") {
				t.Fatalf("disabled category folder changed target: %q %v", rel, err)
			}
			job, err := a.executeImport(ctx, plan.ID, nil)
			if err != nil {
				t.Fatal(err)
			}
			reorgRunJob(t, a, job)
			var history transferHistoryItem
			for _, item := range historyParityReadPage(t, a, "page_size=1000").Items {
				if item.FileID == plan.Files[0].ID {
					history = item
					break
				}
			}
			if history.ID == "" || history.Category != tc.want {
				t.Fatalf("stored classification lost: %+v", history)
			}
		})
	}
}
