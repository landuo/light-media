package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// This server is a synthetic protocol replay, not captured/live file evidence.
func fileInspectionServer(t *testing.T, mode string) (*httptest.Server, *int) {
	t.Helper()
	fileCalls := 0
	itemReads := map[string]int{}
	root := parentRoot + "/" + prefix + syntheticID
	parents := map[string]string{"movie-own": "", "tv-big": "", "tv-zhu": "", "season-big": "tv-big", "season-zhu": "tv-zhu", "episode-big": "season-big", "episode-zhu": "season-zhu"}
	kinds := map[string]string{"movie-own": "Movie", "tv-big": "TV", "tv-zhu": "TV", "season-big": "Season", "season-zhu": "Season", "episode-big": "Episode", "episode-zhu": "Episode"}
	relative := map[string]string{"movie-own": "夜王 (2026)/夜王 (2026).mkv", "episode-big": "生活大爆炸/Season 7/生活大爆炸 S07E17.mkv", "episode-zhu": "诛仙/Season 4/诛仙 S04E05.mkv"}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		p := strings.TrimPrefix(req.URL.Path, "/v/api/v1")
		var data any
		switch {
		case p == "/login":
			data = map[string]string{"token": "synthetic-file-token"}
		case p == "/user/info":
			data = map[string]int{"is_admin": 1}
		case p == "/mdb/list":
			data = []library{{GUID: "owned-lib", Name: prefix + syntheticID, Category: "Mix", Dirs: []string{root}}}
		case p == "/mdb/owned-lib":
			if req.Method != "GET" {
				t.Error("read-only action mutated library")
			}
			data = map[string]any{"guid": "owned-lib", "name": prefix + syntheticID, "category": "Mix", "dir_list": []string{root}}
		case p == "/item/list":
			var input map[string]any
			_ = json.NewDecoder(req.Body).Decode(&input)
			if input["ancestor_guid"] != "owned-lib" {
				t.Error("unscoped item list")
			}
			data = map[string]any{"list": []any{map[string]string{"guid": "movie-own", "type": "Movie"}, map[string]string{"guid": "tv-big", "type": "TV"}, map[string]string{"guid": "tv-zhu", "type": "TV"}}}
		case p == "/item/getEditDetail":
			fileCalls++
			var input map[string]string
			_ = json.NewDecoder(req.Body).Decode(&input)
			guid := input["item_guid"]
			if req.Method != "POST" || len(input) != 1 || relative[guid] == "" {
				t.Error("unexpected file-detail route/body or non-current GUID")
			}
			m := map[string]any{"item_guid": guid, "ancestor_guid": "owned-lib", "type": kinds[guid], "files": []any{map[string]any{"file_path": root + "/" + relative[guid]}}, "private_body": "synthetic-file-password", "url": "https://private.invalid/?token=secret"}
			switch mode {
			case "absent_paths", "fallback_ok", "fallback_files_ok", "fallback_foreign_library", "fallback_escaped_path":
				m["files"] = []any{}
			case "foreign_item":
				m["item_guid"] = "existing-item"
			case "foreign_library":
				m["ancestor_guid"] = "existing-library"
			case "nested_foreign_library":
				m["files"] = []any{map[string]any{"file_path": root + "/" + relative[guid], "mdb_guid": "existing-library"}}
			case "foreign_path":
				m["files"] = []any{map[string]any{"file_path": parentRoot + "/real-library/private-video.mkv"}}
			case "path_traversal":
				m["files"] = []any{map[string]any{"file_path": root + "/../private-video.mkv"}}
			case "url_path":
				m["files"] = []any{map[string]any{"file_path": "https://private.invalid/?token=secret"}}
			case "unknown_owned_path":
				m["files"] = []any{map[string]any{"file_path": root + "/private-secret-name.mkv"}}
			}
			if strings.HasPrefix(mode, "media_itemfile_") {
				m["files"] = []any{}
			}
			data = m
		case strings.HasPrefix(p, "/media/itemfile/"):
			guid := strings.TrimPrefix(p, "/media/itemfile/")
			if req.Method != "GET" || relative[guid] == "" {
				t.Error("itemfile requested an unscoped GUID or wrong method")
			}
			row := map[string]any{"guid": "physical-file-guid", "item_guid": guid, "ancestor_guid": "owned-lib", "parent_guid": parents[guid], "item_type": kinds[guid], "file_path": root + "/" + relative[guid]}
			data = []any{row}
			switch mode {
			case "absent_paths":
				data = []any{}
			case "media_itemfile_missing":
				delete(row, "file_path")
			case "media_itemfile_multiple":
				data = []any{row, row}
			case "media_itemfile_foreign_library":
				row["ancestor_guid"] = "existing-library"
			case "media_itemfile_foreign_path":
				row["file_path"] = parentRoot + "/other-library/private-video.mkv"
			case "media_itemfile_foreign_parent":
				row["parent_guid"] = "other-parent"
			case "media_itemfile_excess":
				data = []any{row, row, row, row, row}
			}
		case strings.HasPrefix(p, "/item/"):
			guid := strings.TrimPrefix(p, "/item/")
			itemReads[guid]++
			ancestor := "owned-lib"
			if mode == "foreign_observed_ancestor" && guid == "episode-zhu" {
				ancestor = "existing-lib"
			}
			data = map[string]any{"guid": guid, "ancestor_guid": ancestor, "parent_guid": parents[guid], "type": kinds[guid], "title": "Synthetic safe title", "source": "tmdb", "tmdb_id": 972676, "source_id": "tt1418"}
			if strings.HasPrefix(mode, "fallback_") && itemReads[guid] > 1 {
				m := data.(map[string]any)
				value := root + "/" + relative[guid]
				if mode == "fallback_foreign_library" {
					m["ancestor_guid"] = "existing-library"
				} else if mode == "fallback_escaped_path" {
					value = root + "/../private-video.mkv"
				}
				m["media_stream"] = map[string]any{"path": value}
				if mode == "fallback_files_ok" {
					m["media_stream"] = map[string]any{"files": []any{map[string]any{"guid": "scoped-stream-file", "path": value}}}
				}
			}
		case strings.HasPrefix(p, "/person/list/"):
			data = map[string]any{"list": []any{}}
		case p == "/season/list/tv-big":
			data = []any{map[string]string{"guid": "season-big", "type": "Season"}}
		case p == "/season/list/tv-zhu":
			data = []any{map[string]string{"guid": "season-zhu", "type": "Season"}}
		case p == "/episode/list/season-big":
			data = []any{map[string]string{"guid": "episode-big", "type": "Episode"}}
		case p == "/episode/list/season-zhu":
			data = []any{map[string]string{"guid": "episode-zhu", "type": "Episode"}}
		default:
			t.Errorf("unexpected read/write endpoint: %s", p)
			http.NotFound(w, req)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": data})
	}))
	return srv, &fileCalls
}

func claimedReceipt(t *testing.T, host string, claim bool) {
	t.Helper()
	r, err := newReceipt(syntheticID, host+"/v", []library{})
	if err != nil {
		t.Fatal(err)
	}
	if claim {
		r.OwnedGUID = "owned-lib"
	}
	r.Phase = "prepared"
	if err := saveReceipt(r); err != nil {
		t.Fatal(err)
	}
}

func TestSyntheticInspectFilesActualReturnedPathsAndUnchangedReceipt(t *testing.T) {
	scratch(t)
	srv, calls := fileInspectionServer(t, "ok")
	defer srv.Close()
	claimedReceipt(t, srv.URL, true)
	before, _ := os.ReadFile(receiptPath(syntheticID))
	c := config{Host: srv.URL, Username: "synthetic-file-user", Password: "synthetic-file-password", ExperimentID: syntheticID}
	dry := run(context.Background(), c, "inspect_files", false)
	if dry.Status != "dry_run" || dry.Payload != nil || *calls != 0 {
		t.Fatal("dry-run did networking or planned a create")
	}
	out := run(context.Background(), c, "inspect_files", true)
	if out.Status != "observed" || !out.FilesVerified || !completeFileEvidence(out.FileEvidence) || *calls != 3 || out.Requests != 26 {
		t.Fatalf("wrong bounded evidence: %+v calls=%d", out, *calls)
	}
	for _, binding := range out.FileEvidence.Bindings {
		if binding.ActualPath != out.Root+"/"+binding.RelativePath || canonicalFiles[binding.RelativePath] == "" {
			t.Fatal("actual path was not the actual scoped backend path")
		}
	}
	after, _ := os.ReadFile(receiptPath(syntheticID))
	if !bytes.Equal(before, after) {
		t.Fatal("read-only action modified receipt")
	}
	b, _ := json.Marshal(out)
	for _, secret := range []string{c.Username, c.Password, "synthetic-file-token", "private.invalid", "private_body"} {
		if bytes.Contains(b, []byte(secret)) {
			t.Fatal("unselected HTTP values leaked")
		}
	}
}

func TestSyntheticInspectFilesRefusesForeignScopeAndUnprovenFields(t *testing.T) {
	for _, mode := range []string{"absent_paths", "foreign_observed_ancestor", "foreign_item", "foreign_library", "nested_foreign_library", "foreign_path", "path_traversal", "url_path", "unknown_owned_path"} {
		t.Run(mode, func(t *testing.T) {
			scratch(t)
			srv, calls := fileInspectionServer(t, mode)
			defer srv.Close()
			claimedReceipt(t, srv.URL, true)
			out := run(context.Background(), config{Host: srv.URL, Username: "synthetic-file-user", Password: "synthetic-file-password", ExperimentID: syntheticID}, "inspect_files", true)
			if out.FilesVerified || completeFileEvidence(out.FileEvidence) {
				t.Fatal("foreign/missing path evidence was verified")
			}
			if mode == "absent_paths" {
				if out.Status != "unverified" || out.Code != "FILES_NOT_PROVEN" || *calls != 3 || out.Requests != 32 {
					t.Fatal("absent field treated as a backend file binding")
				}
			} else if out.Status != "failed" {
				t.Fatalf("foreign/invalid field did not stop: %+v", out)
			}
			if mode == "foreign_observed_ancestor" && *calls != 0 {
				t.Fatal("getEditDetail started before the whole observed tree was scoped")
			}
			if mode != "absent_paths" && *calls > 1 {
				t.Fatal("continued after foreign library/path response")
			}
			if mode != "absent_paths" && mode != "foreign_observed_ancestor" && out.Requests != 24 {
				t.Fatal("bad edit identity/path incorrectly invoked the item fallback")
			}
			b, _ := json.Marshal(out)
			for _, secret := range []string{"private-video.mkv", "private-secret-name", "private.invalid", "synthetic-file-password"} {
				if bytes.Contains(b, []byte(secret)) {
					t.Fatal("rejected raw path or response value leaked")
				}
			}
		})
	}
}

func TestInspectFilesRequiresClaimedGUIDAndObservedSourceFieldsAreNarrow(t *testing.T) {
	scratch(t)
	srv, calls := fileInspectionServer(t, "ok")
	defer srv.Close()
	claimedReceipt(t, srv.URL, false)
	out := run(context.Background(), config{Host: srv.URL, Username: "synthetic-file-user", Password: "synthetic-file-password", ExperimentID: syntheticID}, "inspect_files", true)
	if out.Code != "OWNED_GUID_REQUIRED" || *calls != 0 {
		t.Fatal("unclaimed library was inspected")
	}
	var detail map[string]json.RawMessage
	_ = json.Unmarshal([]byte(`{"source":"https://secret.invalid","tmdb_id":"secret-token","source_id":"https://private.invalid","unknown":"private"}`), &detail)
	b, _ := json.Marshal(observedSourceFields(detail))
	if bytes.Contains(b, []byte("secret")) || bytes.Contains(b, []byte("private")) {
		t.Fatal("unknown source or raw ID leaked")
	}
	_ = json.Unmarshal([]byte(`{"source":"trim_id","tmdb_id":972676,"source_id":"tt1418"}`), &detail)
	fields := observedSourceFields(detail)
	if fields["tmdb_id"] != int64(972676) || fields["source_id"] != "tt1418" || fields["source"] != "trim_id" {
		t.Fatal("observed ID spelling/type was converted or discarded")
	}
	_ = json.Unmarshal([]byte(`{"source":"tmdb_id","source_id":972676}`), &detail)
	if observedSourceFields(detail)["source"] != "tmdb_id" {
		t.Fatal("standard source namespace was not preserved")
	}
}

func TestFileEvidenceCannotBypassSecretSanitization(t *testing.T) {
	out := output{Status: "observed", FilesVerified: true, FileEvidence: &fileEvidence{ExperimentID: syntheticID, OwnedGUID: "owned-lib", Root: parentRoot + "/" + prefix + syntheticID, Bindings: []fileBinding{{ItemGUID: "injected-credential-value", RelativePath: "夜王 (2026)/夜王 (2026).mkv", ActualPath: parentRoot + "/" + prefix + syntheticID + "/夜王 (2026)/夜王 (2026).mkv"}}}}
	sanitize(&out, []string{"injected-credential-value"})
	b, _ := json.Marshal(out)
	if out.FileEvidence != nil || out.FilesVerified || out.Status != "failed" || bytes.Contains(b, []byte("injected-credential-value")) {
		t.Fatal("credential alias bypassed sanitization or a changed path remained verified")
	}
}

func TestSyntheticFileFallbackUsesOnlyScopedKnownItemRoute(t *testing.T) {
	for _, mode := range []string{"fallback_ok", "fallback_files_ok", "fallback_foreign_library", "fallback_escaped_path"} {
		t.Run(mode, func(t *testing.T) {
			scratch(t)
			srv, calls := fileInspectionServer(t, mode)
			defer srv.Close()
			claimedReceipt(t, srv.URL, true)
			out := run(context.Background(), config{Host: srv.URL, Username: "synthetic-file-user", Password: "synthetic-file-password", ExperimentID: syntheticID}, "inspect_files", true)
			if mode == "fallback_ok" || mode == "fallback_files_ok" {
				if out.Status != "observed" || !out.FilesVerified || !completeFileEvidence(out.FileEvidence) || out.Requests != 29 || *calls != 3 {
					t.Fatalf("fallback did not produce bounded actual path evidence: %+v", out)
				}
				for _, item := range out.Items {
					if item["type"] == "Movie" || item["type"] == "Episode" {
						if item["file_detail_shape"] == nil || item["fallback_item_file_shape"] == nil || item["file_fallback_scope_verified"] != true {
							t.Fatal("both endpoint shapes or fresh scope assertion missing")
						}
					}
				}
			} else if out.Status != "failed" || out.FilesVerified || out.FileEvidence != nil || *calls != 1 || out.Requests != 25 {
				t.Fatalf("foreign fallback did not stop: %+v", out)
			}
			b, _ := json.Marshal(out)
			if bytes.Contains(b, []byte("private-video.mkv")) || bytes.Contains(b, []byte("synthetic-file-password")) {
				t.Fatal("rejected fallback path or credential leaked")
			}
		})
	}
}

func TestSyntheticLinkedFileRouteAndExactRequestBudget(t *testing.T) {
	for _, mode := range []string{"media_itemfile_ok", "media_itemfile_missing", "media_itemfile_multiple", "media_itemfile_foreign_library", "media_itemfile_foreign_path", "media_itemfile_foreign_parent", "media_itemfile_excess"} {
		t.Run(mode, func(t *testing.T) {
			scratch(t)
			srv, calls := fileInspectionServer(t, mode)
			defer srv.Close()
			claimedReceipt(t, srv.URL, true)
			out := run(context.Background(), config{Host: srv.URL, Username: "synthetic-file-user", Password: "synthetic-file-password", ExperimentID: syntheticID}, "inspect_files", true)
			if mode == "media_itemfile_ok" {
				if out.Status != "observed" || !out.FilesVerified || !completeFileEvidence(out.FileEvidence) || out.Requests != 32 || *calls != 3 {
					t.Fatalf("linked files were not observed at the unchanged budget: %+v", out)
				}
				for _, item := range out.Items {
					if item["type"] == "Movie" || item["type"] == "Episode" {
						if item["file_detail_shape"] == nil || item["fallback_item_file_shape"] == nil || item["media_itemfile_shape"] == nil {
							t.Fatal("one of the three endpoint diagnostics was lost")
						}
					}
				}
			} else if mode == "media_itemfile_missing" || mode == "media_itemfile_multiple" {
				if out.Status != "unverified" || out.FilesVerified || len(out.FileEvidence.Bindings) != 0 || out.Requests != 32 {
					t.Fatal("missing/ambiguous attachment promoted to a binding")
				}
			} else if out.Status != "failed" || out.FileEvidence != nil || out.FilesVerified || *calls != 1 || out.Requests != 26 {
				t.Fatalf("bad linked-file response did not stop immediately: %+v", out)
			}
			b, _ := json.Marshal(out)
			if bytes.Contains(b, []byte("private-video.mkv")) || bytes.Contains(b, []byte("synthetic-file-password")) {
				t.Fatal("rejected linked path or credentials leaked")
			}
		})
	}
	a := api{requests: 32}
	if _, _, err := a.linkedFiles(context.Background(), receipt{}, observedNode{guid: "owned-item"}); err == nil || err.Error() != "REQUEST_LIMIT" || a.requests != 32 {
		t.Fatal("linked-file fallback expanded the request cap")
	}
}
