package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDiscWriteAfterPlanningExplainsFailureAndAllowsFreshSelection(t *testing.T) {
	a, source, library := importTestApp(t)
	ctx := t.Context()
	tv := importFixture(t, a, "tv")
	organizeGroupFixture(t, a)
	season := 1
	disc := filepath.Join(source, "Test.Film.S01.BluRay", "Disc 3")
	member := filepath.Join(disc, "BDMV", "STREAM", "00001.m2ts")
	partial := member + ".!qB"
	importWrite(t, partial, []byte("downloading"))
	req := manualOrganizeRequest{Paths: []string{disc}, TargetPath: library, Mode: "hardlink", MediaType: "tv", TMDBID: tv.ID, Season: &season, Scraping: importFlag(false)}
	planJob := func() (ImportPlan, Job) {
		t.Helper()
		out, err := a.manualOrganize(ctx, req)
		if err != nil || out.Submitted != 1 || out.Failed != 0 || len(out.Items) != 1 {
			t.Fatalf("submit: %+v, %v", out, err)
		}
		var plan ImportPlan
		if err := a.Store.Get(ctx, "import_plan", out.Items[0].PlanID, &plan); err != nil {
			t.Fatal(err)
		}
		return plan, overwriteStart(t, a, plan)
	}
	old, job := planJob()
	importWrite(t, partial, []byte("new bytes since the plan"))
	err := a.runImport(ctx, job, old.ID, nil)
	if err == nil || !strings.Contains(err.Error(), "盘内有 1 个下载临时文件") {
		t.Fatalf("lost original cause: %v", err)
	}
	if err := os.Rename(partial, member); err != nil {
		t.Fatal(err)
	}
	fresh, job := planJob()
	if err := a.runImport(ctx, job, fresh.ID, nil); err != nil {
		t.Fatal(err)
	}
	want, err := os.Stat(member)
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.Stat(filepath.Join(fresh.Files[0].Target, "BDMV", "STREAM", "00001.m2ts"))
	if err != nil || !os.SameFile(want, got) {
		t.Fatalf("completed member not hardlinked: %v", err)
	}
}
