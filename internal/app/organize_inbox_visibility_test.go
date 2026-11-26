package app

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

// Real dialog requests must be queryable and cancellable while the main
// writer is occupied. The ID stays valid when the inbox later hands it off.
func TestOrganizeInboxVisibleAndCancellableBeforeHandoff(t *testing.T) {
	a, source, target := importTestApp(t)
	cookie := setupTestAdmin(t, a)
	path := filepath.Join(source, "Fallout.S02E08.mkv")
	importWrite(t, path, []byte("episode"))
	writer, err := a.Store.DB.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Rollback()
	req := func(method, url string, form any, data any) {
		t.Helper()
		var body []byte
		if form != nil {
			var err error
			body, err = json.Marshal(form)
			if err != nil {
				t.Fatal(err)
			}
		}
		ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
		defer cancel()
		r := httptest.NewRequestWithContext(ctx, method, url, bytes.NewReader(body))
		r.Header.Set("Origin", "http://example.com")
		r.Header.Set("Content-Type", "application/json")
		r.AddCookie(cookie)
		w := httptest.NewRecorder()
		a.Handler().ServeHTTP(w, r)
		var result struct{ Data json.RawMessage }
		if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &result) != nil {
			t.Fatalf("%s %s returned %d %s", method, url, w.Code, w.Body)
		}
		if err := json.Unmarshal(result.Data, data); err != nil {
			t.Fatal(err)
		}
	}
	submit := func(paths []string) manualOrganizeOutput {
		t.Helper()
		var out manualOrganizeOutput
		req(http.MethodPost, "/api/files/organize", manualOrganizeRequest{Interactive: true, Paths: paths, TargetPath: target, Mode: "hardlink"}, &out)
		return out
	}
	single := submit([]string{path})
	if len(single.Items) != 1 || single.Items[0].JobID == "" {
		t.Fatal(single)
	}
	file := single.Items[0].JobID
	// A pre-upgrade preparation may still coexist with new per-file rows.
	legacy, err := a.submitManualOrganize(t.Context(), manualOrganizeRequest{Interactive: true, Background: true, Paths: []string{source}, TargetPath: target, Mode: "hardlink"})
	if err != nil {
		t.Fatal(err)
	}
	prep := legacy.PreparationJobID
	if prep == "" {
		t.Fatal("missing preparation")
	}
	queue := func(wantFile, wantPrep bool) {
		t.Helper()
		var list struct {
			Groups       []struct{ ID string }
			Preparations []organizeQueuePreparation
		}
		req(http.MethodGet, "/api/history/transfers/queue", nil, &list)
		var fileIDs, prepIDs, wantFiles, wantPreps []string
		for _, entry := range list.Groups {
			fileIDs = append(fileIDs, entry.ID)
		}
		for _, entry := range list.Preparations {
			prepIDs = append(prepIDs, entry.ID)
		}
		if wantFile {
			wantFiles = []string{file}
		}
		if wantPrep {
			wantPreps = []string{prep}
		}
		if !slices.Equal(fileIDs, wantFiles) || !slices.Equal(prepIDs, wantPreps) {
			t.Fatalf("unexpected queue %+v", list)
		}
	}
	state := func(id, want string) {
		t.Helper()
		for _, suffix := range []string{"", "?view=organize"} {
			var out struct {
				Job    Job
				Events json.RawMessage
			}
			req(http.MethodGet, "/api/jobs/"+id+suffix, nil, &out)
			if out.Job.ID != id || out.Job.State != want || (suffix == "") != (out.Events != nil) {
				t.Fatalf("%s: %+v", suffix, out)
			}
		}
	}
	queue(true, true)
	state(file, "queued")
	state(prep, "queued")
	if _, err = a.cancelOrganizeQueueFile(t.Context(), file); err != nil {
		t.Fatal(err)
	}
	queue(false, true)
	state(file, "cancelled")
	action := func(id, name string) {
		t.Helper()
		r := httptest.NewRequest(http.MethodPost, "/api/jobs/"+id+"/"+name, nil)
		result, err := a.jobAPI(httptest.NewRecorder(), r, id+"/"+name)
		if err != nil || !result.(map[string]bool)["accepted"] {
			t.Fatal(result, err)
		}
	}
	action(file, "retry")
	action(prep, "cancel")
	queue(true, false)
	state(file, "queued")
	state(prep, "cancelled")
	stagedFile, err := a.pendingOrganizeJob(t.Context(), file)
	if err != nil {
		t.Fatal(err)
	}
	if err := writer.Rollback(); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if more, err := a.deliverOrganizeInbox(t.Context()); err != nil || !more {
			t.Fatal(more, err)
		}
	}
	queue(true, false)
	state(file, "queued")
	state(prep, "cancelled")
	// Model a crash after the main commit and before removal of the receipt.
	raw, err := json.Marshal(stagedFile)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.organizeInbox.db.Exec(`INSERT INTO request(id,kind,subject,request_key,job,state) VALUES(?,?,?,?,?,'queued')`, file, stagedFile.Kind, path, "overlap", raw); err != nil {
		t.Fatal(err)
	}
	queue(true, false)
	action(file, "cancel")
	queue(false, false)
	state(file, "cancelled")
}
