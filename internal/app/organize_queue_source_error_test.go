package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestQueuedOrganizeSourceReportsActualChange(t *testing.T) {
	for _, tc := range []struct {
		name, code, reason string
		disc               bool
	}{
		{"disc-changing", "REVIEW_SOURCE_CHANGED", "1 个下载临时文件", true},
		{"disc-stable", "", "", true},
		{"disc-replaced", "REVIEW_SOURCE_CHANGED", "原盘目录已被替换", true},
		{"file-removed", "TRANSFER_SOURCE_MISSING", "源路径已不存在", false},
		{"file-changed", "REVIEW_SOURCE_CHANGED", "源文件在入队后发生变化", false},
		{"file-no-longer-allowed", "INVALID_SOURCE", "允许整理的目录", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, source, library := importTestApp(t)
			ctx := t.Context()
			show := importFixture(t, a, "tv")
			organizeGroupFixture(t, a)
			season := 1
			path := filepath.Join(source, "Fallout.S01E01.mkv")
			temporary := ""
			if tc.disc {
				path = filepath.Join(source, "Fallout.S01.2160p.UHD.Blu-ray", "Disc 3")
				importWrite(t, filepath.Join(path, "BDMV", "index.bdmv"), []byte("index"))
				importWrite(t, filepath.Join(path, "BDMV", "STREAM", "00001.m2ts"), []byte("selected stream"))
				temporary = filepath.Join(path, "BDMV", "STREAM", "00002.m2ts.!qB")
				importWrite(t, temporary, []byte("unselected stream"))
			} else {
				importWrite(t, path, []byte("selected episode"))
			}
			request := manualOrganizeRequest{Interactive: true, Paths: []string{path}, TargetPath: library, MediaType: "tv", TMDBID: show.ID, Season: &season, Mode: "hardlink", Scraping: importFlag(false)}
			queued, err := a.queueSelectedManualFiles(ctx, request)
			if err != nil || queued.Failed != 0 || queued.Submitted != 1 {
				t.Fatalf("submit: %+v %v", queued, err)
			}
			switch tc.name {
			case "disc-changing":
				importWrite(t, temporary, []byte("changed while queued"))
			case "disc-replaced":
				if err := os.Rename(path, path+".old"); err != nil {
					t.Fatal(err)
				}
				importWrite(t, filepath.Join(path, "BDMV", "index.bdmv"), []byte("another disc"))
			case "file-removed":
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			case "file-changed":
				importWrite(t, path, []byte("changed after enqueue"))
			case "file-no-longer-allowed":
				a.config.AllowedRoots = []string{library}
				a.invalidateSettings()
			}
			if delivered, err := a.deliverOrganizeInbox(ctx); err != nil || !delivered {
				t.Fatalf("handoff: %t %v", delivered, err)
			}
			job, err := a.claim(ctx)
			if err != nil || job.ID != queued.Items[0].JobID || job.Kind != "organize_file" {
				t.Fatalf("claim: %+v %v", job, err)
			}
			a.runClaimedJob(ctx, job)
			if tc.code == "" {
				job, err = a.claimJob(ctx, importJobs)
				if err != nil || job.Kind != "import" || job.ID != queued.Items[0].JobID {
					t.Fatalf("unchanged selective download did not advance: %+v %v", job, err)
				}
				a.runClaimedJob(ctx, job)
			}
			var code, message, state string
			if err := a.Store.DB.QueryRow("SELECT state,error_code,error_message FROM jobs WHERE id=?", job.ID).Scan(&state, &code, &message); err != nil {
				t.Fatal(err)
			}
			var status int
			var historyMessage string
			if err := a.Store.DB.QueryRow("SELECT status,errmsg FROM transferhistory WHERE src=?", path).Scan(&status, &historyMessage); err != nil {
				t.Fatal(err)
			}
			if tc.code == "" {
				if status != 1 || code != "" {
					t.Fatalf("stable disc: state=%s history=%d error=%s %s", state, status, code, message)
				}
			} else if status != 0 || state != "failed" || code != tc.code || !strings.Contains(message, tc.reason) || historyMessage != message {
				t.Fatalf("reason: state=%s history=%d code=%s error=%q history error=%q", state, status, code, message, historyMessage)
			}
		})
	}
}
