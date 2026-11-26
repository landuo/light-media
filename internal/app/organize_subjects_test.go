package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"
)

func TestOrganizeNewSelectionsBypassBusyMainReaders(t *testing.T) {
	a, source, target := importTestApp(t)
	cookie := setupTestAdmin(t, a)
	path := filepath.Join(source, "Fallout.S02E01.mkv")
	importWrite(t, path, []byte("fixture"))
	if _, err := a.transferMediaClassifier(t.Context()); err != nil {
		t.Fatal(err)
	}
	old := filepath.Join(source, "Fallout.S02E02.mkv")
	importWrite(t, old, []byte("previous fixture"))
	repeat := manualOrganizeRequest{Interactive: true, Paths: []string{old}, TargetPath: target, Mode: "hardlink"}
	oldOut, _, err := a.queueInteractiveOrganizeFile(t.Context(), repeat)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.deliverOrganizeInbox(t.Context()); err != nil {
		t.Fatal(err)
	}
	// The browser uses this authenticated POST. Login has its own bootstrap
	// reader. Reserve all ordinary readers and the main transactional writer.
	readers := a.Store.reader()
	readers.SetMaxOpenConns(1)
	reader, err := readers.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	writer, err := a.Store.DB.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Rollback()
	waits := readers.Stats().WaitCount
	blocked := make(chan error, 1)
	lookupCtx, stop := context.WithTimeout(t.Context(), 4*time.Second)
	defer stop()
	go func() {
		out, _, err := a.queueInteractiveOrganizeFile(lookupCtx, repeat)
		if err == nil && (len(out.Items) != 1 || out.Items[0].JobID != oldOut.Items[0].JobID) {
			err = fmt.Errorf("duplicate ID changed: %+v", out)
		}
		blocked <- err
	}()
	until := time.Now().Add(time.Second)
	for readers.Stats().WaitCount == waits && time.Now().Before(until) {
		time.Sleep(time.Millisecond)
	}
	if readers.Stats().WaitCount == waits {
		t.Fatal("existing source did not reach authoritative reader")
	}
	// A single new source does not need the saturated reader. Selecting
	// a directory now enumerates its members, including existing claims.
	{
		form := manualOrganizeRequest{Interactive: true, Paths: []string{path}, TargetPath: target, Mode: "hardlink"}
		body, err := json.Marshal(form)
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(t.Context(), time.Second)
		req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/api/files/organize", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Origin", "http://example.com")
		req.AddCookie(cookie)
		response := httptest.NewRecorder()
		a.Handler().ServeHTTP(response, req)
		cancel()
		var out struct{ Data manualOrganizeOutput }
		if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &out) != nil || out.Data.PreparationJobID == "" && (len(out.Data.Items) != 1 || out.Data.Items[0].JobID == "") {
			t.Fatalf("browser submission waited for main DB: %d %s", response.Code, response.Body)
		}
	}
	reader.Close()
	if err := <-blocked; err != nil {
		t.Fatal(err)
	}
}

func TestOrganizeMainOwnerRestoredAndRetried(t *testing.T) {
	a, source, target := importTestApp(t)
	ctx := t.Context()
	if err := a.saveSettings(ctx, a.Settings()); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(source, "Fallout.S02E05.mkv")
	importWrite(t, path, []byte("fixture"))
	form := manualOrganizeRequest{Interactive: true, Paths: []string{path}, TargetPath: target, Mode: "hardlink"}
	out, _, err := a.queueInteractiveOrganizeFile(ctx, form)
	if err != nil {
		t.Fatal(err)
	}
	id := out.Items[0].JobID
	if _, err = a.deliverOrganizeInbox(ctx); err != nil {
		t.Fatal(err)
	}
	if err = a.Close(); err != nil {
		t.Fatal(err)
	}
	a, err = New(Options{DataDir: a.Dir})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	check := func() {
		t.Helper()
		if known, _ := a.organizeInbox.subjects.lookup(path); !known {
			t.Fatal("previously accepted file missing from active hint")
		}
		repeat, _, err := a.queueInteractiveOrganizeFile(ctx, form)
		if err != nil || len(repeat.Items) != 1 || repeat.Items[0].JobID != id {
			t.Fatalf("active job not reused: %+v, %v", repeat, err)
		}
		different := form
		different.Mode = "copy"
		if _, _, err := a.queueInteractiveOrganizeFile(ctx, different); err == nil {
			t.Fatal("accepted second set of options for active source")
		}
	}
	check()
	if _, err = a.Store.DB.Exec("UPDATE jobs SET state='cancelled' WHERE id=?", id); err != nil {
		t.Fatal(err)
	}
	if err = a.restoreOrganizeSubjects(ctx); err != nil {
		t.Fatal(err)
	}
	if known, _ := a.organizeInbox.subjects.lookup(path); known {
		t.Fatal("terminal jobs leaked into restored active set")
	}
	j, err := scanJob(a.Store.DB.QueryRow("SELECT "+jobColumns+" FROM jobs WHERE id=?", id))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.retryOriginalScrapeJob(ctx, j); err != nil {
		t.Fatal(err)
	}
	check()
}
