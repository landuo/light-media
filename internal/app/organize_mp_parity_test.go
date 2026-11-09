package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"

	"light-media/internal/catalog"
)

// Source oracle: pinned transfer.py queues independent TransferTask instances;
// transhandler.py uses parsed episode numbers even when tmdb_episodes is empty.
// Exercise real hardlinks and persisted jobs, not successful mock callbacks.
func TestMPOrganizePackIsolatesBlockedFailedAndCancelledFiles(t *testing.T) {
	for _, scenario := range []string{"blocked-before-queue", "failed-during-transfer", "ordinary-io-error", "cancel-one-file"} {
		t.Run(scenario, func(t *testing.T) {
			a, source, _ := importTestApp(t)
			ctx := context.Background()
			identity := importFixture(t, a, "tv")
			importCache(t, a, "season:123:1", catalog.Season{ID: 1, SeriesID: 123, SeasonNumber: 1})
			files := []ImportFileRequest{}
			for _, ep := range []int{248, 249, 250} {
				path := filepath.Join(source, fmt.Sprintf("Test.Show.S01E%d.mkv", ep))
				data := []byte("isolated actual video bytes")
				if ep == 248 && scenario == "blocked-before-queue" {
					data = nil
				}
				importWrite(t, path, data)
				files = append(files, ImportFileRequest{Path: path, Season: 1, Episodes: []int{ep}})
			}
			plan, err := a.previewImport(ctx, ImportRequest{MediaType: "tv", TMDBID: identity.ID, RootID: "root", Mode: "hardlink", Scraping: importFlag(false), Files: files})
			if err != nil {
				t.Fatal(err)
			}
			if plan.Files[1].Status != "ready" || plan.Files[2].Status != "ready" {
				t.Fatalf("TMDB episode absence blocked parsed episode numbers: %+v", plan.Files)
			}
			if scenario == "blocked-before-queue" {
				if plan.Files[0].Status != "blocked" {
					t.Fatal("empty file fixture was not rejected")
				}
				if err = a.enqueueCompletedImportFiles(ctx, plan); err == nil {
					t.Fatal("blocked file did not report its failure")
				}
			} else {
				if _, err = a.executeImport(ctx, plan.ID, nil); err != nil {
					t.Fatal(err)
				}
				if scenario == "cancel-one-file" {
					if _, err = a.cancelOrganizeQueueFile(ctx, plan.Files[0].ID); err != nil {
						t.Fatal(err)
					}
				} else if scenario == "ordinary-io-error" {
					a.importSourceLink = func(from int, name string, to int, next string, flags int) error {
						if name == filepath.Base(files[0].Path) {
							return unix.EIO
						}
						return unix.Linkat(from, name, to, next, flags)
					}
				} else if err = os.Remove(files[0].Path); err != nil {
					t.Fatal(err)
				}
			}
			for {
				job, err := a.claimJob(ctx, importJobs)
				if errors.Is(err, sql.ErrNoRows) {
					break
				}
				if err != nil {
					t.Fatal(err)
				}
				a.runClaimedJob(ctx, job)
			}
			for _, file := range plan.Files[1:] {
				src, e1 := os.Stat(file.Source)
				dst, e2 := os.Stat(file.Target)
				if e1 != nil || e2 != nil || !os.SameFile(src, dst) {
					t.Fatalf("sibling %v was lost: source=%v target=%v", file.Episodes, e1, e2)
				}
			}
			if _, err := os.Stat(plan.Files[0].Target); err == nil {
				t.Fatal("failed/cancelled file published")
			}
			page := historyParityReadPage(t, a, "")
			states := map[string]int{}
			for _, item := range page.Items {
				states[item.Status]++
			}
			if page.Total != 3 || states["success"] != 2 || states["failed"] != 1 {
				t.Fatalf("missing independent results: total=%d states=%v", page.Total, states)
			}
			if groups := historyParityReadQueue(t, a); len(groups) != 0 {
				t.Fatal("finished group remains in queue")
			}
			var jobsBefore, jobsAfter, automaticRetries int
			_ = a.Store.DB.QueryRow(`SELECT COUNT(*) FROM jobs WHERE kind='import'`).Scan(&jobsBefore)
			if err := a.enqueueCompletedImportFiles(ctx, plan); err != nil {
				t.Fatal(err)
			}
			_ = a.Store.DB.QueryRow(`SELECT COUNT(*) FROM jobs WHERE kind='import'`).Scan(&jobsAfter)
			_ = a.Store.DB.QueryRow(`SELECT COUNT(*) FROM jobs WHERE kind='import' AND (state='retry_wait' OR attempt>1)`).Scan(&automaticRetries)
			if jobsBefore != jobsAfter || automaticRetries != 0 {
				t.Fatal("polling retried a terminal file")
			}
		})
	}
}
