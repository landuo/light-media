package app

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestManualIdentificationUnknownFilenameCanOrganizeWithExplicitMovieIdentity(t *testing.T) {
	a, source, target := importTestApp(t)
	identity := importFixture(t, a, "movie")
	input := filepath.Join(source, "unknown.mkv")
	contents := []byte("isolated synthetic movie without filename identity or NFO")
	importWrite(t, input, contents)
	ctx := context.Background()
	scrape := false
	request := manualOrganizeRequest{Paths: []string{input}, TargetPath: target, Mode: "hardlink", Scraping: &scrape, Preview: true}

	out, err := a.manualOrganize(ctx, request)
	if err != nil || out.Failed != 1 || out.Submitted != 0 || len(out.Items) != 1 {
		t.Fatalf("automatic unknown filename did not fail safely: %+v %v", out, err)
	}
	failure := out.Items[0]
	if failure.ErrorCode != "DIRECTORY_IDENTITY_REVIEW" ||
		!strings.Contains(failure.Error, "手动整理") ||
		!strings.Contains(failure.Error, "电影或电视剧类型") ||
		!strings.Contains(failure.Error, "查询或填写TheMovieDb编号") ||
		strings.Contains(failure.Error, "在文件管理中手动识别") {
		t.Fatalf("failure does not direct to the real manual identity fields: %+v", failure)
	}
	entries, err := os.ReadDir(target)
	if err != nil || len(entries) != 0 {
		t.Fatalf("automatic identification failure modified destination: %v %v", entries, err)
	}
	if _, err = os.Stat(strings.TrimSuffix(input, ".mkv") + ".nfo"); !os.IsNotExist(err) {
		t.Fatalf("test unexpectedly has source NFO: %v", err)
	}

	// This is the existing manual-organize form's movie type + queried TMDB ID.
	request.MediaType, request.TMDBID, request.Preview = "movie", identity.ID, false
	out, err = a.manualOrganize(ctx, request)
	if err != nil || out.Failed != 0 || out.Submitted != 1 || len(out.Items) != 1 {
		t.Fatalf("explicit identity did not submit a real import: %+v %v", out, err)
	}
	item := out.Items[0]
	if item.Media.ID != identity.ID || item.Media.MediaType != "movie" || item.Target == "" {
		t.Fatalf("explicit movie identity was not carried to import: %+v", item)
	}
	reorgRunJob(t, a, reorgJob(t, a, "import", item.PlanID))
	sourceInfo, err := os.Stat(input)
	if err != nil {
		t.Fatalf("original source was removed: %v", err)
	}
	targetInfo, err := os.Stat(item.Target)
	if err != nil || !os.SameFile(sourceInfo, targetInfo) {
		t.Fatalf("manual import did not publish the expected hardlink: %v", err)
	}
	got, err := os.ReadFile(input)
	if err != nil || !bytes.Equal(got, contents) {
		t.Fatalf("original source contents changed: %q %v", got, err)
	}
	var asset Asset
	if err = a.assetAtPath(ctx, item.Target, &asset); err != nil || asset.Media.ID != identity.ID || asset.State != "present" {
		t.Fatalf("real imported asset is not recorded: %+v %v", asset, err)
	}
}
