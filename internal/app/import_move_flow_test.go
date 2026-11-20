package app

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func nativeMoveFixture(t *testing.T, a *App) {
	t.Helper()
	importFixture(t, a, "movie")
	installMetadataFixture(t, a, "native-move", []string{"/3/movie/123"}, func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"id":123,"title":"Move Fixture","original_title":"Move Fixture","release_date":"2024-01-01","genres":[],"credits":{"cast":[],"crew":[]},"external_ids":{}}`))}, nil
	})
}

func TestImportMoveDoesNotReuseCopiedSourceAsCompletedMove(t *testing.T) {
	a, source, target := importTestApp(t)
	nativeMoveFixture(t, a)
	path := filepath.Join(source, "Existing.2024.mkv")
	importWrite(t, path, []byte("existing original source"))
	request := manualOrganizeRequest{Paths: []string{path}, MediaType: "movie", TMDBID: 123, Mode: "hardlink", TargetPath: target, Renaming: importFlag(false), Scraping: importFlag(false)}
	out, err := a.manualOrganize(t.Context(), request)
	if err != nil || out.Failed != 0 || out.Submitted == 0 {
		t.Fatalf("hardlink %+v %v", out, err)
	}
	for _, item := range out.Items {
		if item.JobID != "" {
			_, job := historyUpsertAttempt(t, a, item.JobID)
			runEpisodeGroupJob(t, a, job)
		}
	}
	request.Mode = "move"
	out, err = a.manualOrganize(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range out.Items {
		if item.Status == "already_imported" {
			t.Fatal("copy/link cannot acknowledge a source-clearing move", item)
		}
		if item.JobID != "" {
			if _, err = a.Store.DB.Exec(`UPDATE jobs SET state='running',generation=generation+1 WHERE id=?`, item.JobID); err != nil {
				t.Fatal(err)
			}
			job, e := a.jobByID(context.Background(), item.JobID)
			if e != nil {
				t.Fatal(e)
			}
			e = a.safeRun(t.Context(), job)
			var denied *APIError
			if !errors.As(e, &denied) || denied.Code != "TRANSFER_OVERWRITE_DENIED" {
				t.Fatalf("existing-target policy must reject explicitly: %v", e)
			}
			if _, e = a.finishJob(t.Context(), job, "failed", denied.Code, denied.Message, 0); e != nil {
				t.Fatal(e)
			}
		}
	}
	// The fixture's default never-overwrite policy rejects this transfer.
	// It must not acknowledge a successful move that preserved the source.
	for _, h := range historyParityReadPage(t, a, "").Items {
		if h.Mode == "move" && h.Status == "success" {
			if _, e := os.Lstat(path); !os.IsNotExist(e) {
				t.Fatal("false successful move retained source", e)
			}
		}
	}
}

func TestManualOrganizeNativeMove(t *testing.T) {
	for _, disc := range []bool{false, true} {
		name := "file"
		if disc {
			name = "disc"
		}
		t.Run(name, func(t *testing.T) {
			a, source, target := importTestApp(t)
			nativeMoveFixture(t, a)
			ctx := context.Background()
			path := filepath.Join(source, "Moving.2024.mkv")
			if disc {
				path = filepath.Join(source, "Moving.2024")
				importWrite(t, filepath.Join(path, "BDMV/STREAM/00001.m2ts"), []byte("synthetic moving disc"))
				importWrite(t, filepath.Join(path, "BDMV/index.bdmv"), []byte("owned disc index"))
				if e := os.Mkdir(filepath.Join(path, "CERTIFICATE"), 0750); e != nil {
					t.Fatal(e)
				}
			} else {
				importWrite(t, path, []byte("synthetic moving video"))
				importWrite(t, strings.TrimSuffix(path, ".mkv")+".zh.srt", []byte("synthetic moving subtitle"))
			}
			request := manualOrganizeRequest{Paths: []string{path}, MediaType: "movie", TMDBID: 123, TargetPath: target, Mode: "move", Scraping: importFlag(false), Preview: true}
			preview, e := a.manualOrganize(ctx, request)
			if e != nil || preview.Failed != 0 {
				t.Fatalf("move preview %+v %v", preview, e)
			}
			if _, e = os.Stat(path); e != nil {
				t.Fatal("preview moved source", e)
			}
			request.Preview = false
			out, e := a.manualOrganize(ctx, request)
			if e != nil || out.Failed != 0 || out.Submitted == 0 {
				t.Fatalf("move submission %+v %v", out, e)
			}
			for _, item := range out.Items {
				if item.JobID != "" {
					_, job := historyUpsertAttempt(t, a, item.JobID)
					runEpisodeGroupJob(t, a, job)
				}
			}
			if disc {
				_, entries, e := snapshotOriginalDirectory(path)
				if e != nil {
					t.Fatal(e)
				}
				for _, entry := range entries {
					if !entry.Directory {
						t.Fatalf("move retained source member %+v", entry)
					}
				}
				if len(entries) != 3 {
					t.Fatalf("MP retains BDMV, STREAM and empty CERTIFICATE: %+v", entries)
				}
			} else if _, e = os.Lstat(path); !os.IsNotExist(e) {
				t.Fatal("move retained selected source", e)
			}
			page := historyParityReadPage(t, a, "")
			if page.Total == 0 {
				t.Fatal("move lost history")
			}
			for _, h := range page.Items {
				if h.Status != "success" || h.Mode != "move" || h.organizeSource() != h.Target {
					t.Fatalf("move history %+v", h)
				}
				if _, e = os.Stat(h.Target); e != nil {
					t.Fatal("target missing", e)
				}
			}
		})
	}
}
