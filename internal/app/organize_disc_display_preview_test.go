package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInteractiveDiscPreviewValidatesMembersWhenPlanning(t *testing.T) {
	a, source, library := importTestApp(t)
	ctx := t.Context()
	tv := importFixture(t, a, "tv")
	organizeGroupFixture(t, a)
	disc := filepath.Join(source, "Fallout.S01.2160p.UHD.Blu-ray", "Disc 1")
	importWrite(t, filepath.Join(disc, "BDMV", "index.bdmv"), []byte("index"))
	stream := filepath.Join(disc, "BDMV", "STREAM", "00001.m2ts")
	importWrite(t, stream, []byte("stream"))
	season := 2
	req := manualOrganizeRequest{Paths: []string{disc}, MediaType: "tv", TMDBID: tv.ID, Mode: "hardlink", TargetPath: library,
		Season: &season, Scraping: importFlag(false), Interactive: true, Preview: true}
	collect := func(req manualOrganizeRequest) importScanFile {
		t.Helper()
		result, err := a.manualOrganizeScan(ctx, disc, req)
		if err != nil {
			t.Fatal(err)
		}
		files := result.(map[string]any)["files"].([]importScanFile)
		if len(files) != 1 || files[0].Path != disc || !files[0].isOriginalDisc() {
			t.Fatalf("incorrect disc selection: %+v", files)
		}
		return files[0]
	}
	if file := collect(req); file.Snapshot != nil {
		t.Fatal("display selection walked the disc before planning")
	}
	result, err := a.manualOrganize(ctx, req)
	if err != nil || len(result.Items) != 1 || result.Submitted != 0 || result.Items[0].Status != "ready" ||
		result.Items[0].Media.ID != tv.ID || filepath.Base(result.Items[0].Target) != "Disc 1" || filepath.Base(filepath.Dir(result.Items[0].Target)) != "Season 2" {
		t.Fatalf("valid display preview with season override: %+v %v", result, err)
	}
	executable := req
	executable.Interactive = false
	queued := req
	queued.Preview = false
	for _, input := range []manualOrganizeRequest{executable, queued} {
		file := collect(input)
		if file.Snapshot == nil || file.Snapshot.Directory == "" || file.Size != 11 || file.Snapshot.Size != file.Size {
			t.Fatalf("lost executable directory snapshot: %+v", file)
		}
	}

	foreign := filepath.Join(t.TempDir(), "outside.m2ts")
	importWrite(t, foreign, []byte("outside"))
	if err := os.Symlink(foreign, filepath.Join(filepath.Dir(stream), "00002.m2ts")); err != nil {
		t.Fatal(err)
	}
	_, scanErr := a.manualOrganizeScan(ctx, disc, executable)
	if scanErr == nil {
		t.Fatal("executable scan accepted a symlink")
	}
	// Enumeration stays shallow, but the final display preview must reject
	// exactly the same member as an executable scan, before any transfer.
	if file := collect(req); file.Snapshot != nil {
		t.Fatal("display selection prevalidated invalid disc members")
	}
	raw, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	reply := callTest(t, a.Handler(), "POST", "/api/files/organize", string(raw), setupTestAdmin(t, a), "")
	var response struct{ Data manualOrganizeOutput }
	if err := json.Unmarshal(reply.Body.Bytes(), &response); err != nil || reply.Code != 200 {
		t.Fatalf("preview response: %d %v", reply.Code, err)
	}
	result = response.Data
	if result.Failed != 1 || result.Submitted != 0 || len(result.Items) != 1 ||
		result.Items[0].Status != "failed" || result.Items[0].ErrorCode != "INVALID_FILE" ||
		!strings.Contains(result.Items[0].Error, "符号链接") || strings.Contains(result.Items[0].Error, disc) || strings.Contains(reply.Body.String(), foreign) {
		t.Fatalf("display preview accepted a symlink or exposed its path: %+v", result)
	}
	if children, err := os.ReadDir(library); err != nil || len(children) != 0 {
		t.Fatalf("preview wrote library files: %v %v", children, err)
	}
}

func TestDiscTemporaryNamesAppearOnlyInTheirPreviewAndAllowManualTransfer(t *testing.T) {
	a, source, library := importTestApp(t)
	ctx := t.Context()
	tv := importFixture(t, a, "tv")
	organizeGroupFixture(t, a)
	t.Setenv("DOWNLOAD_TMPEXT", `[".!qb",".workpiece"]`)
	release := filepath.Join(source, "Fallout.S01.2160p.UHD.Blu-ray")
	disc := filepath.Join(release, "Disc 3")
	for _, path := range []string{
		"Disc 1/BDMV/STREAM/00001.m2ts",
		"Disc 3/BDMV/STREAM/00001.m2ts",
		"Disc 3/BDMV/STREAM/00002.m2ts.!qB",
		"Disc 3/BDMV/index.bdmv.WORKPIECE",
		"Disc 3/BDMV/notes.part",
		"Disc 3/BDMV/folder.!qb/notes.txt",
	} {
		importWrite(t, filepath.Join(release, path), []byte(path))
	}
	season := 1
	request := manualOrganizeRequest{Paths: []string{release}, TargetPath: library, MediaType: "tv", TMDBID: tv.ID, Season: &season, Mode: "hardlink", Scraping: importFlag(false), Interactive: true, Preview: true}
	out, err := a.manualOrganize(ctx, request)
	if err != nil || out.Failed != 0 || out.Submitted != 0 || len(out.Items) != 2 {
		t.Fatalf("preview: %+v, %v", out, err)
	}
	for _, item := range out.Items {
		want := 0
		if item.Source == disc {
			want = 2
		}
		if item.Status != "ready" || item.TemporaryFiles != want || item.Media.ID != tv.ID {
			t.Fatalf("%s: expected %d temporary names, got %+v", item.Source, want, item)
		}
	}

	// The operator can explicitly transfer a stable selection with such names.
	request.Paths = []string{disc}
	request.Preview, request.Interactive = false, false
	out, err = a.manualOrganize(ctx, request)
	if err != nil || out.Failed != 0 || out.Submitted != 1 || len(out.Items) != 1 {
		t.Fatalf("manual transfer: %+v, %v", out, err)
	}
	_, job := historyUpsertAttempt(t, a, out.Items[0].JobID)
	runEpisodeGroupJob(t, a, job)
	member := "BDMV/STREAM/00002.m2ts.!qB"
	src, srcErr := os.Stat(filepath.Join(disc, member))
	dst, dstErr := os.Stat(filepath.Join(out.Items[0].Target, member))
	if srcErr != nil || dstErr != nil || !os.SameFile(src, dst) {
		t.Fatalf("explicit transfer did not hardlink its selected member: %v %v", srcErr, dstErr)
	}
}
