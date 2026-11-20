package app

import (
	"context"
	"strconv"
	"testing"

	"light-media/internal/catalog"
)

func TestMPTransferHistorySingleTableDeleteInsertNewID(t *testing.T) {
	a := newTestApp(t)
	plan := ImportPlan{ID: "plan_single", WorkflowID: "wf_single", RootID: "root", Mode: "copy", Media: catalog.Media{ID: 123, MediaType: "movie", Title: "单表测试"}, Category: "外语电影", EpisodeGroup: ""}
	file := PlanFile{ID: "file_single", Source: "/source/Test.2024.mkv", Target: "/library/Test (2024)/Test (2024).mkv", Method: "copy", Size: 42}
	job := Job{ID: "job_single"}
	ctx := t.Context()
	write := func(success bool, message string) int64 {
		t.Helper()
		tx, err := a.Store.DB.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		if err = writeTransferHistoryMPTx(ctx, tx, transferHistoryMPInput{Plan: plan, File: file, Job: job, Success: success, Message: message}); err != nil {
			tx.Rollback()
			t.Fatal(err)
		}
		if err = tx.Commit(); err != nil {
			t.Fatal(err)
		}
		var id int64
		if err = a.Store.DB.QueryRow(`SELECT id FROM transferhistory WHERE src=?`, file.Source).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	first := write(true, "")
	var rows int
	if err := a.Store.DB.QueryRow(`SELECT count(*) FROM transferhistory`).Scan(&rows); err != nil || rows != 1 {
		t.Fatal("single row", rows, err)
	}
	second := write(false, "测试失败")
	if second == first {
		t.Fatalf("MP semantics require a new ID after reorganize: %d", first)
	}
	if err := a.Store.DB.QueryRow(`SELECT count(*) FROM transferhistory`).Scan(&rows); err != nil || rows != 1 {
		t.Fatal("delete+insert must keep one row per src", rows, err)
	}
	var status int
	var errmsg, title, dest, mode string
	if err := a.Store.DB.QueryRow(`SELECT status,errmsg,title,dest,mode FROM transferhistory WHERE id=?`, second).Scan(&status, &errmsg, &title, &dest, &mode); err != nil {
		t.Fatal(err)
	}
	if status != 0 || errmsg != "测试失败" || title != "单表测试" || dest != file.Target || mode != "copy" {
		t.Fatalf("row fields status=%d errmsg=%q title=%q dest=%q mode=%q", status, errmsg, title, dest, mode)
	}
	items, total, err := a.transferHistoryMPPage(ctx, 1, 50)
	if err != nil || total != 1 || len(items) != 1 || items[0].ID != strconv.FormatInt(second, 10) || items[0].Media.Title != "单表测试" || items[0].Status != "failed" || items[0].Target != file.Target || items[0].PlanID != plan.ID || items[0].FileID != file.ID {
		t.Fatalf("MP reader projection total=%d items=%+v err=%v", total, items, err)
	}
	if _, err := a.Store.DB.ExecContext(context.Background(), `SELECT 1`); err != nil {
		t.Fatal(err)
	}
}

func TestMPTransferHistorySingleTableRealImportTarget(t *testing.T) {
	a, _, unused, _ := originalTransferModuleFixture(t, "copy", "")
	source := unused.Files[0].Source
	out, err := a.manualOrganize(t.Context(), manualOrganizeRequest{Paths: []string{source}, TargetPath: a.Settings().Roots[0].Path, Mode: "copy", MediaType: "movie", TMDBID: 123, Scraping: importFlag(false)})
	if err != nil || out.Submitted != 1 || out.Failed != 0 {
		t.Fatal("actual organize submission", out, err)
	}
	job, err := a.jobByID(context.Background(), out.Items[0].JobID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.Store.DB.Exec(`UPDATE jobs SET state='running',generation=generation+1 WHERE id=?`, job.ID); err != nil {
		t.Fatal(err)
	}
	if job, err = a.jobByID(context.Background(), job.ID); err != nil {
		t.Fatal(err)
	}
	if err = a.runImport(t.Context(), job, out.Items[0].PlanID, nil); err != nil {
		t.Fatal("actual import", err)
	}
	if _, err = a.finishJob(t.Context(), job, "succeeded", "", "", 0); err != nil {
		t.Fatal(err)
	}
	items, total, err := a.transferHistoryMPPage(t.Context(), 1, 50)
	if err != nil || total != 1 || len(items) != 1 || items[0].Target == "" || items[0].Status != "success" || items[0].Media.Title != "Owned" || items[0].PlanID == "" || items[0].FileID == "" {
		t.Fatalf("actual MP history total=%d items=%+v err=%v", total, items, err)
	}
}
