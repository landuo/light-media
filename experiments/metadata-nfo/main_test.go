package main

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"light-media/internal/metadata"
)

// Episode IDs and dates are synthetic test data, never captured API evidence.
func syntheticReport() metadata.Report {
	return metadata.Report{Status: "ok", Evidence: "synthetic_http_roundtrip", Requests: 5, Results: []metadata.Result{
		{Sample: "movie-nightking", Type: "movie", RequestedSourceID: 1602727, Status: "ok", Source: "tmdb", SourceID: 1602727, Title: "夜王", Year: 2026, Date: "2026-01-01"},
		{Sample: "series-bigbang", Type: "tv", RequestedSourceID: 1418, Status: "ok", Source: "tmdb", SourceID: 1418, Title: "生活大爆炸", Year: 2007, Date: "2007-01-01"},
		{Sample: "episode-bigbang", Type: "episode", RequestedSourceID: 1418, Status: "ok", Source: "tmdb", SourceID: 900000001, ParentSourceID: 1418, ParentContext: "verified_series_and_fixed_request_route", Title: "合成 <单集> & 标题", Year: 2014, Date: "2014-01-01", Season: 7, Episode: 17},
		{Sample: "series-zhuxian", Type: "tv", RequestedSourceID: 206484, Status: "ok", Source: "tmdb", SourceID: 206484, Title: "诛仙", Year: 2022, Date: "2022-01-01"},
		{Sample: "episode-zhuxian", Type: "episode", RequestedSourceID: 206484, Status: "ok", Source: "tmdb", SourceID: 900000002, ParentSourceID: 206484, ParentContext: "verified_response_show_id_and_request_route", Title: "合成国漫单集", Year: 2026, Date: "2026-01-01", Season: 4, Episode: 5},
	}}
}

func reportBytes(t *testing.T, value any) []byte {
	t.Helper()
	b, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
func testWork(t *testing.T) string {
	t.Helper()
	parent, err := filepath.EvalSymlinks(t.TempDir()) // macOS /var is an OS symlink.
	if err != nil {
		t.Fatal(err)
	}
	work := filepath.Join(parent, "work")
	if err := os.Mkdir(work, 0700); err != nil {
		t.Fatal(err)
	}
	return work
}

type nfoXML struct {
	XMLName   xml.Name
	Title     string `xml:"title"`
	ShowTitle string `xml:"showtitle"`
	Year      int    `xml:"year"`
	Aired     string `xml:"aired"`
	Season    int    `xml:"season"`
	Episode   int    `xml:"episode"`
	UniqueIDs []struct {
		Type    string `xml:"type,attr"`
		Default bool   `xml:"default,attr"`
		Value   string `xml:",chardata"`
	} `xml:"uniqueid"`
}

func TestFiveNFOIdentitiesAndCanonicalLayout(t *testing.T) {
	report := syntheticReport()
	p, err := buildPlan(reportBytes(t, report))
	if err != nil {
		t.Fatal(err)
	}
	if p.NFOCount != 5 || len(p.Files) != 5 || p.Status != "dry_run" || p.NetworkRequests != 0 || p.VideoCount != 0 || p.ImageCount != 0 || p.ActorCount != 0 {
		t.Fatalf("wrong plan boundaries: %+v", p)
	}
	for i, file := range p.Files {
		var doc nfoXML
		if err := xml.Unmarshal(file.content, &doc); err != nil {
			t.Fatal(err)
		}
		want := report.Results[i]
		root := map[string]string{"movie": "movie", "tv": "tvshow", "episode": "episodedetails"}[want.Type]
		if file.Path != specs[i].path || doc.XMLName.Local != root || doc.Title != want.Title || doc.Year != want.Year || file.Date != want.Date || len(doc.UniqueIDs) != 1 || doc.UniqueIDs[0].Type != "tmdb" || !doc.UniqueIDs[0].Default {
			t.Fatalf("wrong NFO: %+v", doc)
		}
		if want.Type == "episode" {
			if doc.Season != want.Season || doc.Episode != want.Episode || doc.ShowTitle != specs[i].showTitle || doc.Aired != want.Date || file.ParentKey == "" {
				t.Fatal("episode parent or season identity lost")
			}
			if i == 2 && (doc.UniqueIDs[0].Value != "900000001" || file.SourceKey != "tmdb:episode:900000001" || file.ParentKey != "tmdb:tv:1418") {
				t.Fatal("episode uniqueid copied from request/parent instead of its own SourceID")
			}
		} else if doc.Season != 0 || doc.Episode != 0 || doc.Aired != "" || bytes.Contains(file.content, []byte("<aired>")) || bytes.Contains(file.content, []byte("<season>")) || bytes.Contains(file.content, []byte("<episode>")) {
			t.Fatal("show/movie contains an episode-only date or numbering tag")
		}
		if bytes.Contains(file.content, []byte("<actor>")) || bytes.Contains(file.content, []byte("<thumb>")) {
			t.Fatal("unsupplied actor/image data generated")
		}
	}
	work := testWork(t)
	root := filepath.Join(work, "new-canonical")
	if err := applyPlan(&p, root, work); err != nil || p.Status != "generated" {
		t.Fatalf("apply: %v", err)
	}
	count := 0
	err = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			count++
			if filepath.Ext(path) != ".nfo" {
				t.Fatal("unexpected video/image/other file")
			}
			info, _ := d.Info()
			if info.Mode().Perm() != 0600 {
				t.Fatal("NFO mode must be 0600")
			}
		}
		return nil
	})
	if err != nil || count != 5 {
		t.Fatalf("file count: %d, %v", count, err)
	}
}

func TestTypedIdentityAllowsSameNumericShowAndEpisodeID(t *testing.T) {
	report := syntheticReport()
	report.Results[2].SourceID = 1418 // Different TMDB types may share a number.
	p, err := buildPlan(reportBytes(t, report))
	if err != nil {
		t.Fatal(err)
	}
	if p.Files[1].SourceKey != "tmdb:tv:1418" || p.Files[2].SourceKey != "tmdb:episode:1418" || p.Files[1].SourceKey == p.Files[2].SourceKey {
		t.Fatal("typed namespace identities were merged")
	}
	var doc nfoXML
	if xml.Unmarshal(p.Files[2].content, &doc) != nil || doc.UniqueIDs[0].Value != "1418" || doc.Season != 7 || doc.Episode != 17 {
		t.Fatal("explicit episode identity not preserved")
	}
}

func TestInvalidReportProducesZeroFiles(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*metadata.Report)
	}{
		{"incomplete", func(r *metadata.Report) { r.Status = "incomplete" }},
		{"unverified", func(r *metadata.Report) { r.Results[2].Status = "unverified" }},
		{"wrong_request_count", func(r *metadata.Report) { r.Requests = 4 }},
		{"missing", func(r *metadata.Report) { r.Results = r.Results[:4] }},
		{"extra", func(r *metadata.Report) { r.Results = append(r.Results, r.Results[0]) }},
		{"duplicate", func(r *metadata.Report) { r.Results[2] = r.Results[0] }},
		{"unknown_sample", func(r *metadata.Report) { r.Results[2].Sample = "custom-episode" }},
		{"unknown_evidence", func(r *metadata.Report) { r.Evidence = "captured" }},
		{"not_requested", func(r *metadata.Report) { r.Evidence = "not_requested" }},
		{"mixed_source", func(r *metadata.Report) { r.Results[2].Source = "imdb" }},
		{"failed_reason", func(r *metadata.Report) { r.Results[2].Reason = "parent_not_verified" }},
		{"movie_ID", func(r *metadata.Report) { r.Results[0].SourceID = 1 }},
		{"movie_name", func(r *metadata.Report) { r.Results[0].Title = "另一个电影" }},
		{"movie_year", func(r *metadata.Report) { r.Results[0].Date = "2025-01-01"; r.Results[0].Year = 2025 }},
		{"show_name", func(r *metadata.Report) { r.Results[1].Title = "另一个剧" }},
		{"wrong_type", func(r *metadata.Report) { r.Results[2].Type = "tv" }},
		{"missing_own_ID", func(r *metadata.Report) { r.Results[2].SourceID = 0 }},
		{"negative_own_ID", func(r *metadata.Report) { r.Results[2].SourceID = -1 }},
		{"requested_parent", func(r *metadata.Report) { r.Results[2].RequestedSourceID = 206484 }},
		{"response_parent", func(r *metadata.Report) { r.Results[2].ParentSourceID = 206484 }},
		{"parent_context", func(r *metadata.Report) { r.Results[2].ParentContext = "assumed_parent" }},
		{"season", func(r *metadata.Report) { r.Results[2].Season = 1 }},
		{"episode", func(r *metadata.Report) { r.Results[4].Episode = 6 }},
		{"date_year", func(r *metadata.Report) { r.Results[2].Year = 2015 }},
		{"bad_date", func(r *metadata.Report) { r.Results[2].Date = "2014-02-30" }},
		{"control_title", func(r *metadata.Report) { r.Results[2].Title = "title\nprivate" }},
		{"URL_title", func(r *metadata.Report) { r.Results[2].Title = "https://host/private" }},
		{"duplicate_episode_identity", func(r *metadata.Report) { r.Results[4].SourceID = r.Results[2].SourceID }},
		{"show_carries_episode", func(r *metadata.Report) { r.Results[1].Episode = 17 }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := syntheticReport()
			tc.mutate(&r)
			work := testWork(t)
			root := filepath.Join(work, "must-not-exist")
			var out bytes.Buffer
			if runCLI([]string{"--apply", "--out-dir", root}, bytes.NewReader(reportBytes(t, r)), &out, work) == 0 {
				t.Fatal("invalid report accepted")
			}
			if _, err := os.Lstat(root); !os.IsNotExist(err) {
				t.Fatal("invalid report created an output directory")
			}
		})
	}
	// This is the real unconfigured producer result, with zero network calls.
	r := metadata.Run(context.Background(), metadata.Credentials{})
	work := testWork(t)
	root := filepath.Join(work, "unconfigured")
	var out bytes.Buffer
	if r.Requests != 0 || runCLI([]string{"--apply", "--out-dir", root}, bytes.NewReader(reportBytes(t, r)), &out, work) == 0 {
		t.Fatal("unconfigured report promoted to verified")
	}
	if _, err := os.Lstat(root); !os.IsNotExist(err) {
		t.Fatal("unconfigured report created files")
	}
}

func TestStrictSchemaAndInputBound(t *testing.T) {
	body := string(reportBytes(t, syntheticReport()))
	for _, input := range []string{
		`{"api_key":"not-accepted",` + body[1:],
		`{"host":"https://not-accepted.invalid",` + body[1:],
		`{"status":"incomplete",` + body[1:],
		`{"STATUS":"unverified",` + body[1:],
		strings.Replace(body, `"source_id":900000001`, `"Source_ID":0,"source_id":900000001`, 1),
		strings.Replace(body, `"status":"ok"`, `"ſtatus":"unverified","status":"ok"`, 1),
		strings.Replace(body, `"sample":"movie-nightking"`, `"evidence":"live_request","sample":"movie-nightking"`, 1),
		body + `{}`,
		`null`,
		strings.Repeat(" ", maxInput+1),
	} {
		var out bytes.Buffer
		if runCLI(nil, strings.NewReader(input), &out, testWork(t)) == 0 {
			t.Fatal("unknown/mixed/duplicated/oversized schema accepted")
		}
		if strings.Contains(out.String(), "not-accepted") {
			t.Fatal("untrusted input appeared in failure output")
		}
	}
}

func TestDryRunAndDeclaredEvidenceNeverProveRemoteAuthenticity(t *testing.T) {
	for _, evidence := range []string{"synthetic_http_roundtrip", "live_request"} {
		r := syntheticReport()
		r.Evidence = evidence // A JSON declaration alone is not a real API call.
		input := inputReport{Report: r, RunID: "untrusted-label-not-to-echo", Logging: &logSummary{Status: "ok", Written: 12, AttemptedBytes: 4000, BudgetBytes: 262144}}
		input.TraceTargetID = "untrusted-target-not-to-echo"
		work := testWork(t)
		root := filepath.Join(work, "dry-run-does-not-write")
		var out bytes.Buffer
		if runCLI([]string{"--out-dir", root}, bytes.NewReader(reportBytes(t, input)), &out, work) != 0 {
			t.Fatal(out.String())
		}
		var p plan
		if json.Unmarshal(out.Bytes(), &p) != nil || p.Status != "dry_run" || p.InputEvidence != evidence || p.SyntheticInput != (evidence == "synthetic_http_roundtrip") || p.InputSourceAuthenticity != "not_independently_verified" || p.Validation != "supplied_validated_report" || p.FNOSReadback != "unverified" {
			t.Fatal("declared evidence was promoted to remote/library verification")
		}
		entries, _ := os.ReadDir(work)
		if len(entries) != 0 || strings.Contains(out.String(), root) || strings.Contains(out.String(), "untrusted-") {
			t.Fatal("dry run wrote files or exposed input/output path labels")
		}
	}
}

func TestApplyRefusesExistingOutsideAndSymlinkDirectories(t *testing.T) {
	work := testWork(t)
	outside := filepath.Join(filepath.Dir(work), "outside-private-path")
	if os.Mkdir(outside, 0700) != nil {
		t.Fatal("fixture")
	}
	existing := filepath.Join(work, "existing")
	if os.Mkdir(existing, 0700) != nil {
		t.Fatal("fixture")
	}
	sentinel := filepath.Join(existing, "sentinel")
	if os.WriteFile(sentinel, []byte("preserved"), 0600) != nil {
		t.Fatal("fixture")
	}
	linked := filepath.Join(work, "linked")
	if err := os.Symlink(outside, linked); err != nil {
		t.Fatal(err)
	}
	for _, root := range []string{"", work, existing, linked, outside, filepath.Join(work, "nested", "new")} {
		p, _ := buildPlan(reportBytes(t, syntheticReport()))
		if err := applyPlan(&p, root, work); err == nil {
			t.Fatalf("unsafe output accepted: %s", root)
		}
	}
	content, _ := os.ReadFile(sentinel)
	entries, _ := os.ReadDir(outside)
	if string(content) != "preserved" || len(entries) != 0 {
		t.Fatal("existing/outside data changed")
	}
	var out bytes.Buffer
	if runCLI([]string{"--apply", "--out-dir", outside}, bytes.NewReader(reportBytes(t, syntheticReport())), &out, work) == 0 || strings.Contains(out.String(), outside) {
		t.Fatal("outside path accepted or echoed")
	}
	p, _ := buildPlan(reportBytes(t, syntheticReport()))
	fresh := filepath.Join(work, "fresh")
	if err := applyPlan(&p, fresh, work); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(filepath.Join(fresh, filepath.FromSlash(specs[0].path)))
	if err := applyPlan(&p, fresh, work); err == nil {
		t.Fatal("second apply overwrote an existing output root")
	}
	after, _ := os.ReadFile(filepath.Join(fresh, filepath.FromSlash(specs[0].path)))
	if !bytes.Equal(before, after) {
		t.Fatal("second apply changed existing NFO")
	}
}
