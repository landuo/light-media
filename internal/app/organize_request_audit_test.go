package app

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"testing"
)

func TestOrganizeRequestAuditRunsOnceAtSuccessfulBoundary(t *testing.T) {
	for _, tc := range []struct {
		name    string
		preview bool
		before  int // Rows visible after the API handler, before the middleware fallback.
	}{
		{"preview", true, 1},
		{"submit", false, 1},
		{"audit_insert_fails", false, 0},
		{"enqueue_rolls_back", false, 0},
		{"directory", true, 0},
		{"background", false, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, src, library := importTestApp(t)
			cookie := setupTestAdmin(t, a)
			importFixture(t, a, "movie")
			path := filepath.Join(src, "Test.Movie.2024.mkv")
			importWrite(t, path, []byte("synthetic movie"))
			if tc.name == "directory" {
				path = src
				originalHostMediaInstallOwnedTransport(t, a, func(r *http.Request) (int, string) {
					if r.URL.Path != "/3/movie/123" {
						t.Error("unexpected metadata request", r.URL.Path)
						return http.StatusNotFound, "{}"
					}
					return 200, `{"id":123,"title":"Test Movie","release_date":"2024-01-01","genres":[],"credits":{"cast":[],"crew":[]},"external_ids":{}}`
				})
				a.invalidateSettings()
			}
			body, err := json.Marshal(manualOrganizeRequest{Paths: []string{path}, TargetPath: library,
				Mode: "hardlink", TMDBID: 123, MediaType: "movie", Scraping: importFlag(false),
				Preview: tc.preview, Background: tc.name == "background"})
			if err != nil {
				t.Fatal(err)
			}
			switch tc.name {
			case "audit_insert_fails":
				_, err = a.Store.DB.Exec("CREATE TRIGGER reject_audit BEFORE INSERT ON audit_log BEGIN SELECT RAISE(ABORT,'audit failure'); END")
			case "enqueue_rolls_back":
				_, err = a.Store.DB.Exec("CREATE TRIGGER reject_queue BEFORE INSERT ON jobs WHEN NEW.kind='import' BEGIN SELECT RAISE(ABORT,'queue failure'); END")
			}
			if err != nil {
				t.Fatal(err)
			}
			check := func(request string, want int) {
				t.Helper()
				var all, matched int
				err := a.Store.DB.QueryRow(`SELECT count(*), count(*) FILTER (WHERE
					json_extract(payload,'$.actor')='admin' AND json_extract(payload,'$.operation')='POST.files' AND
					json_extract(payload,'$.http_status')=200 AND json_extract(payload,'$.time')!='')
					FROM audit_log WHERE json_extract(payload,'$.request_id')=?`, request).Scan(&all, &matched)
				if err != nil || all != want || matched != want {
					t.Fatalf("audit rows=%d matching=%d want=%d err=%v", all, matched, want, err)
				}
			}
			h := a.requestLogged(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				a.handler().ServeHTTP(w, r)
				check(w.Header().Get("X-Request-ID"), tc.before)
				if tc.name == "audit_insert_fails" {
					if _, err := a.Store.DB.Exec("DROP TRIGGER reject_audit"); err != nil {
						t.Fatal(err)
					}
				}
			}))
			resp := callTest(t, h, "POST", "/api/files/organize", string(body), cookie, "")
			req := resp.Header().Get("X-Request-ID")
			if tc.name == "enqueue_rolls_back" {
				if resp.Code < 400 {
					t.Fatal("aborted enqueue returned success", resp.Code, resp.Body)
				}
				check(req, 0)
				return
			}
			check(req, 1)
			var result struct{ Data manualOrganizeOutput }
			if err := json.Unmarshal(resp.Body.Bytes(), &result); resp.Code != 200 || err != nil || result.Data.Failed != 0 {
				t.Fatal("unexpected organize result", resp.Code, resp.Body, err)
			}
			if tc.name == "background" {
				if result.Data.PreparationJobID == "" {
					t.Fatal("background preparation missing")
				}
			} else if len(result.Data.Items) != 1 || result.Data.Items[0].PlanID == "" || (!tc.preview && result.Data.Items[0].JobID == "") {
				t.Fatal("plan or job missing", result.Data)
			}
		})
	}
}
