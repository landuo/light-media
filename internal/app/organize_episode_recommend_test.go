package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func episodeTestRule() organizeEpisodeRule {
	return organizeEpisodeRule{Name: "文件名集数", Enabled: true, Order: 1, Pattern: `(?P<title>.+)\.E(?P<ep>\d+(?:-E?\d+)?)\.(?P<ext>.+)`}
}
func episodeTestSave(t *testing.T, a *App, rules ...organizeEpisodeRule) {
	t.Helper()
	current, err := a.episodeFormatRules(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.saveEpisodeFormatRules(context.Background(), organizeEpisodeRules{current.Revision, rules}); err != nil {
		t.Fatal(err)
	}
}
func episodeTestError(t *testing.T, err error, code string) {
	t.Helper()
	var api *APIError
	if !errors.As(err, &api) || api.Code != code {
		t.Fatalf("want %s, got %v", code, err)
	}
}

func TestEpisodeRecommendRulesAPIPersistenceAndConsumer(t *testing.T) {
	a, source, _ := importTestApp(t)
	ctx := context.Background()
	paths := []string{filepath.Join(source, "剧集{固定}.E01.mkv"), filepath.Join(source, "剧集{固定}.E02-E03.mkv")}
	for _, path := range paths {
		importWrite(t, path, []byte("synthetic video"))
	}
	_, err := a.recommendEpisodeFormat(ctx, organizeEpisodeRecommendRequest{Paths: paths})
	// The automatic branch excludes the range sample; one of two media falls below MP's 60% coverage.
	episodeTestError(t, err, "EPISODE_AUTO_LOW_COVERAGE")
	// Existing recognition words are independent: they must not rewrite the physical template.
	if err := a.saveRecognitionWords(ctx, 0, []string{`剧集 => Rewritten`}); err != nil {
		t.Fatal(err)
	}
	rule := episodeTestRule()
	rule.Pattern = `(?<=剧集\{固定\}\.E)(?<ep>\d+(?:-E?\d+)?)(?=\.mkv)`
	raw, _ := json.Marshal(organizeEpisodeRules{Rules: []organizeEpisodeRule{rule}})
	got, err := a.fileManagerAPI(httptest.NewRequest("PUT", "/files/episode-format/rules", strings.NewReader(string(raw))), "episode-format/rules")
	if err != nil || got.(organizeEpisodeRules).Revision != 1 {
		t.Fatalf("save=%+v %v", got, err)
	}
	saved, err := a.episodeFormatRules(ctx)
	if err != nil || len(saved.Rules) != 1 || saved.Rules[0].Pattern != rule.Pattern {
		t.Fatalf("read=%+v %v", saved, err)
	}
	var persisted []byte
	if err := a.Store.DB.QueryRow(`SELECT payload FROM records WHERE kind=?`, organizeEpisodeRulesKind).Scan(&persisted); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(persisted), rule.Pattern) || strings.Contains(string(persisted), rule.Name) {
		t.Fatal("rules were persisted as plaintext")
	}
	_, err = a.saveEpisodeFormatRules(ctx, organizeEpisodeRules{Rules: []organizeEpisodeRule{rule}})
	episodeTestError(t, err, "EPISODE_RULE_REVISION_CONFLICT")
	body, _ := json.Marshal(organizeEpisodeRecommendRequest{Paths: paths})
	output, err := a.fileManagerAPI(httptest.NewRequest("POST", "/files/episode-format/recommend", strings.NewReader(string(body))), "episode-format/recommend")
	if err != nil {
		t.Fatal(err)
	}
	result := output.(organizeEpisodeRecommendResult)
	if result.EpisodeFormat != "剧集{{固定}}.E{ep}.mkv" || result.SampleCount != 2 || result.RuleIndex != 0 || result.SampleFile != "剧集{固定}.E01.mkv" {
		t.Fatalf("result=%+v", result)
	}
	consumer, err := organizeEpisodeTemplate(result.EpisodeFormat)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range paths {
		match := consumer.FindStringSubmatch(filepath.Base(path))
		if match == nil {
			t.Fatalf("not consumable: %s", path)
		}
		if _, err = organizeEpisodeRange(match[consumer.SubexpIndex("ep")]); err != nil {
			t.Fatal(err)
		}
	}
	var jobs int
	if err := a.Store.DB.QueryRow(`SELECT count(*) FROM jobs`).Scan(&jobs); err != nil || jobs != 0 {
		t.Fatalf("recommendation submitted work: %d %v", jobs, err)
	}
	_, words, err := a.recognitionWords(ctx)
	if err != nil || len(words) != 1 || words[0] != `剧集 => Rewritten` {
		t.Fatalf("recognition words mutated: %v %v", words, err)
	}
}

func TestEpisodeRecommendSelectionSamplingAndAuthorization(t *testing.T) {
	a, source, _ := importTestApp(t)
	ctx := context.Background()
	episodeTestSave(t, a, episodeTestRule())
	first := filepath.Join(source, "Show.E01.mkv")
	second := filepath.Join(source, "Show.E02.mkv")
	for _, name := range []string{"Show.E01.mkv", "Show.E02.mkv", "Show.E01.zh.srt", "Show.E02.flac", ".hidden.E90.mkv", "note.txt", "nested/Other.E09.mkv"} {
		importWrite(t, filepath.Join(source, name), []byte("synthetic"))
	}
	for _, paths := range [][]string{{source}, {first}} {
		out, err := a.recommendEpisodeFormat(ctx, organizeEpisodeRecommendRequest{Paths: paths})
		if err != nil || out.SampleCount != 4 || out.MatchedCount != 4 || out.EpisodeFormat != "{title}.E{ep}.{ext}" {
			t.Fatalf("directory/single=%+v %v", out, err)
		}
	}
	out, err := a.recommendEpisodeFormat(ctx, organizeEpisodeRecommendRequest{Paths: []string{first, second, first}})
	if err != nil || out.SampleCount != 2 {
		t.Fatalf("explicit selection broadened: %+v %v", out, err)
	}
	outside := filepath.Join(t.TempDir(), "Outside.E03.mkv")
	importWrite(t, outside, []byte("synthetic"))
	for _, tc := range []struct {
		in   organizeEpisodeRecommendRequest
		code string
	}{
		{organizeEpisodeRecommendRequest{}, "INVALID_EPISODE_SELECTION"},
		{organizeEpisodeRecommendRequest{Paths: []string{outside}}, "INVALID_SOURCE"},
		{organizeEpisodeRecommendRequest{Paths: []string{first, source}}, "INVALID_EPISODE_SELECTION"},
		{organizeEpisodeRecommendRequest{Paths: []string{first, filepath.Join(source, "nested/Other.E09.mkv")}}, "INVALID_EPISODE_SELECTION"},
		{organizeEpisodeRecommendRequest{Paths: []string{filepath.Join(source, "missing.mkv")}}, "EPISODE_SOURCE_UNAVAILABLE"},
		{organizeEpisodeRecommendRequest{Paths: []string{first}, HistoryIDs: []string{"missing"}}, "INVALID_EPISODE_SELECTION"},
	} {
		_, err := a.recommendEpisodeFormat(ctx, tc.in)
		episodeTestError(t, err, tc.code)
	}
	link := filepath.Join(source, "Link.E03.mkv")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	_, err = a.recommendEpisodeFormat(ctx, organizeEpisodeRecommendRequest{Paths: []string{first, link}})
	episodeTestError(t, err, "EPISODE_SOURCE_UNAVAILABLE")
	// A directory scan skips a symlink instead of granting its target access.
	out, err = a.recommendEpisodeFormat(ctx, organizeEpisodeRecommendRequest{Paths: []string{source}})
	if err != nil || out.SampleCount != 4 {
		t.Fatalf("symlink directory behavior: %+v %v", out, err)
	}
}

func TestEpisodeRecommendSizeOrderingAndNonmatchingAuxiliary(t *testing.T) {
	a, source, _ := importTestApp(t)
	ctx := context.Background()
	rule := episodeTestRule()
	rule.MinFileSizeMB = 500
	later := rule
	later.Name = "later"
	later.Order = 2
	disabled := rule
	disabled.Name = "disabled"
	disabled.Enabled = false
	disabled.Order = 0
	episodeTestSave(t, a, later, disabled, rule)
	for _, name := range []string{"Show.E01.mkv", "Show.E01.zh.srt", "Show.E02.flac", "Credits.srt", "[NCOP].mkv"} {
		importWrite(t, filepath.Join(source, name), []byte("tiny synthetic"))
	}
	out, err := a.recommendEpisodeFormat(ctx, organizeEpisodeRecommendRequest{Paths: []string{source}})
	if err != nil || out.RuleName != rule.Name || out.MatchedCount != 2 || out.SampleCount != 5 {
		t.Fatalf("size/auxiliary=%+v %v", out, err)
	}
	// MP skips a subtitle with no locatable episode, but validates recognized subtitles even when a rule matches only video.
	videoRule := rule
	videoRule.MinFileSizeMB = 0
	videoRule.Pattern = `Show\.E(?P<ep>\d+)\.mkv`
	episodeTestSave(t, a, videoRule)
	out, err = a.recommendEpisodeFormat(ctx, organizeEpisodeRecommendRequest{Paths: []string{source}})
	if err != nil || out.RuleName != "智能分析" || out.SampleCount != 2 || out.MajorityCount != 2 || out.EpisodeFormat != "Show.E{ep}.{a}" {
		t.Fatalf("incompatible video rule must fall through to auxiliary auto samples: %+v %v", out, err)
	}
	// Explicit multi-select excludes those siblings. No automatic widening or guessed numbers.
	out, err = a.recommendEpisodeFormat(ctx, organizeEpisodeRecommendRequest{Paths: []string{filepath.Join(source, "Show.E01.mkv"), filepath.Join(source, "Credits.srt")}})
	if err != nil || out.MatchedCount != 1 {
		t.Fatalf("unknown auxiliary should be ignored by compatibility: %+v %v", out, err)
	}
}

func TestEpisodeRecommendRuleValidationAndNoInventedFallback(t *testing.T) {
	a, source, _ := importTestApp(t)
	ctx := context.Background()
	for _, pattern := range []string{`(`, `\d+`, `(?P<ep>\d+)(?P<bad-name>x)`} {
		rule := episodeTestRule()
		rule.Pattern = pattern
		_, err := a.saveEpisodeFormatRules(ctx, organizeEpisodeRules{Rules: []organizeEpisodeRule{rule}})
		episodeTestError(t, err, "INVALID_EPISODE_RULE")
	}
	episodeTestSave(t, a, episodeTestRule())
	path := filepath.Join(source, "Movie.2026.1080p.12345.mkv")
	importWrite(t, path, []byte("synthetic"))
	_, err := a.recommendEpisodeFormat(ctx, organizeEpisodeRecommendRequest{Paths: []string{path}})
	episodeTestError(t, err, "EPISODE_AUTO_NOT_DETECTED")
	rule := episodeTestRule()
	rule.Pattern = `(?P<outer>Show\.E(?P<ep>\d+))\.mkv`
	episodeTestSave(t, a, rule)
	importWrite(t, filepath.Join(source, "Show.E01.mkv"), []byte("synthetic"))
	_, err = a.recommendEpisodeFormat(ctx, organizeEpisodeRecommendRequest{Paths: []string{source}})
	episodeTestError(t, err, "EPISODE_AUTO_LOW_COVERAGE")
	// Disabled-only tables use the same automatic branch as an empty table.
	rule.Enabled = false
	episodeTestSave(t, a, rule)
	_, err = a.recommendEpisodeFormat(ctx, organizeEpisodeRecommendRequest{Paths: []string{path}})
	episodeTestError(t, err, "EPISODE_AUTO_LOW_COVERAGE")
}

func TestEpisodeRecommendAuthenticatedHTTPContract(t *testing.T) {
	a, source, _ := importTestApp(t)
	path := filepath.Join(source, "Show.E01.mkv")
	importWrite(t, path, []byte("synthetic HTTP sample"))
	cookie := setupTestAdmin(t, a)
	call := func(method, route string, body any, auth bool) (int, []byte) {
		t.Helper()
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest(method, "/api/files/episode-format/"+route, strings.NewReader(string(raw)))
		req.Header.Set("Content-Type", "application/json")
		if auth {
			req.AddCookie(cookie)
		}
		out := httptest.NewRecorder()
		a.Handler().ServeHTTP(out, req)
		return out.Code, out.Body.Bytes()
	}
	if code, _ := call("GET", "rules", nil, false); code != 401 {
		t.Fatalf("anonymous rules status=%d", code)
	}
	if code, raw := call("POST", "recommend", organizeEpisodeRecommendRequest{Paths: []string{path}}, true); code != 200 || !strings.Contains(string(raw), `"episode_format":"Show.E{ep}.mkv"`) || !strings.Contains(string(raw), `"confidence":"low"`) || !strings.Contains(string(raw), `"native_verified_count":1`) {
		t.Fatalf("default automatic HTTP recommendation: %d %s", code, raw)
	}
	if code, raw := call("PUT", "rules", organizeEpisodeRules{Rules: []organizeEpisodeRule{episodeTestRule()}}, true); code != 200 {
		t.Fatalf("save: %d %s", code, raw)
	}
	code, raw := call("POST", "recommend", organizeEpisodeRecommendRequest{Paths: []string{path}}, true)
	var envelope struct {
		Data      organizeEpisodeRecommendResult `json:"data"`
		RequestID string                         `json:"request_id"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		t.Fatal(err)
	}
	if code != 200 || envelope.Data.EpisodeFormat != "{title}.E{ep}.{ext}" || envelope.Data.SampleFile != "Show.E01.mkv" || envelope.RequestID == "" {
		t.Fatalf("actual API contract: %d %s", code, raw)
	}
	unknown := filepath.Join(source, "unknown", "Movie.2026.1080p.12345.mkv")
	importWrite(t, unknown, []byte("synthetic unrecognized filename"))
	if code, raw := call("POST", "recommend", organizeEpisodeRecommendRequest{Paths: []string{unknown}}, true); code != 400 || !strings.Contains(string(raw), "EPISODE_AUTO_NOT_DETECTED") || strings.Contains(string(raw), `"data"`) {
		t.Fatalf("automatic failure must be a real business error: %d %s", code, raw)
	}
}

func TestEpisodeRecommendSourceOracle(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/history-mp-alignment-20261115/episode-format-rule-oracle.json")
	if err != nil {
		t.Fatal(err)
	}
	var oracle struct {
		Cases []struct {
			Name      string
			Pattern   string
			Files     []string
			Template  string
			ErrorCode string `json:"error_code"`
		}
	}
	if err := json.Unmarshal(raw, &oracle); err != nil {
		t.Fatal(err)
	}
	for _, tc := range oracle.Cases {
		t.Run(tc.Name, func(t *testing.T) {
			samples := []organizeEpisodeSample{}
			for _, name := range tc.Files {
				samples = append(samples, organizeEpisodeSample{path: "/synthetic/" + name, name: name, size: 150 * 1024 * 1024, media: strings.HasSuffix(name, ".mkv")})
			}
			compatibility, err := episodeCompatibilitySamples(context.Background(), samples, nil)
			if err != nil {
				t.Fatal(err)
			}
			out, err := recommendEpisodeFromRules(context.Background(), []organizeEpisodeRule{{Name: tc.Name, Enabled: true, Pattern: tc.Pattern}}, samples, compatibility)
			if tc.ErrorCode != "" {
				episodeTestError(t, err, tc.ErrorCode)
				return
			}
			if err != nil || out.EpisodeFormat != tc.Template {
				t.Fatalf("source oracle: %+v %v; want %s", out, err, tc.Template)
			}
		})
	}
}

func TestEpisodeRecommendHistoryBatchUsesOnlyCurrentSameDirectoryFiles(t *testing.T) {
	a, source, target := importTestApp(t)
	ctx := context.Background()
	episodeTestSave(t, a, episodeTestRule())
	plan, _ := historyParityPlan(t, a, source, target, "episode-batch", "Show", "failed", "error", "Show.E01.mkv")
	second := plan.Files[0]
	second.ID = "episode-batch-second"
	second.Source = filepath.Join(filepath.Dir(second.Source), "Show.E02.mkv")
	plan.Files = append(plan.Files, second)
	importWrite(t, second.Source, []byte("synthetic second"))
	importWrite(t, filepath.Join(filepath.Dir(second.Source), "Sibling.E03.mkv"), []byte("synthetic sibling"))
	if err := a.Store.Put(ctx, "import_plan", plan.ID, plan); err != nil {
		t.Fatal(err)
	}
	rewrite := func() {
		t.Helper()
		if _, err := a.Store.DB.Exec(`DELETE FROM transferhistory WHERE src IN (?,?) OR json_extract(src_fileitem,'$.file_id') IN (?,?)`, plan.Files[0].Source, second.Source, plan.Files[0].ID, second.ID); err != nil {
			t.Fatal(err)
		}
		tx, err := a.Store.DB.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		for _, file := range plan.Files {
			if err = writeTransferHistoryMPTx(ctx, tx, transferHistoryMPInput{Plan: plan, File: file, Job: Job{ID: "episode-batch-job"}, Success: false}); err != nil {
				tx.Rollback()
				t.Fatal(err)
			}
		}
		if err = tx.Commit(); err != nil {
			t.Fatal(err)
		}
	}
	rewrite()
	out, err := a.recommendEpisodeFormat(ctx, organizeEpisodeRecommendRequest{HistoryIDs: []string{second.ID, plan.Files[0].ID}})
	if err != nil || out.SampleCount != 2 || out.SampleFile != "Show.E02.mkv" {
		t.Fatalf("history selection must retain requested order and not broaden: %+v %v", out, err)
	}
	plan.Files[1].Source = filepath.Join(source, "different", "Show.E02.mkv")
	importWrite(t, plan.Files[1].Source, []byte("synthetic cross-directory"))
	if err := a.Store.Put(ctx, "import_plan", plan.ID, plan); err != nil {
		t.Fatal(err)
	}
	rewrite()
	_, err = a.recommendEpisodeFormat(ctx, organizeEpisodeRecommendRequest{HistoryIDs: []string{second.ID, plan.Files[0].ID}})
	episodeTestError(t, err, "INVALID_EPISODE_SELECTION")
}

func TestEpisodeRecommendHistoryResolvesMoveButNotCopy(t *testing.T) {
	a, source, target := importTestApp(t)
	ctx := context.Background()
	episodeTestSave(t, a, episodeTestRule())
	plan, _ := historyParityPlan(t, a, source, target, "episode-move", "Show", "succeeded", "imported", "Show.E01.mkv")
	plan.Mode, plan.Files[0].Method = "move", "move"
	importWrite(t, plan.Files[0].Target, []byte("synthetic moved"))
	if err := os.Remove(plan.Files[0].Source); err != nil {
		t.Fatal(err)
	}
	if err := a.Store.Put(ctx, "import_plan", plan.ID, plan); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Store.DB.Exec(`DELETE FROM transferhistory WHERE src=?`, plan.Files[0].Source); err != nil {
		t.Fatal(err)
	}
	mpTx, err := a.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = writeTransferHistoryMPTx(ctx, mpTx, transferHistoryMPInput{Plan: plan, File: plan.Files[0], Job: Job{ID: "recommend-move-job"}, Success: true}); err != nil {
		mpTx.Rollback()
		t.Fatal(err)
	}
	if err = mpTx.Commit(); err != nil {
		t.Fatal(err)
	}
	request := organizeEpisodeRecommendRequest{HistoryIDs: []string{plan.Files[0].ID}}
	out, err := a.recommendEpisodeFormat(ctx, request)
	if err != nil || out.MatchedCount != 1 {
		t.Fatalf("move must use target: %+v %v", out, err)
	}
	plan.Mode, plan.Files[0].Method = "copy", "copy"
	if err := a.Store.Put(ctx, "import_plan", plan.ID, plan); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Store.DB.Exec(`DELETE FROM transferhistory WHERE src=?`, plan.Files[0].Target); err != nil {
		t.Fatal(err)
	}
	mpTx, err = a.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = writeTransferHistoryMPTx(ctx, mpTx, transferHistoryMPInput{Plan: plan, File: plan.Files[0], Job: Job{ID: "recommend-copy-job"}, Success: true}); err != nil {
		mpTx.Rollback()
		t.Fatal(err)
	}
	if err = mpTx.Commit(); err != nil {
		t.Fatal(err)
	}
	_, err = a.recommendEpisodeFormat(ctx, request)
	episodeTestError(t, err, "EPISODE_SOURCE_UNAVAILABLE")
	_, err = a.recommendEpisodeFormat(ctx, organizeEpisodeRecommendRequest{HistoryIDs: []string{"missing"}})
	episodeTestError(t, err, "EPISODE_HISTORY_UNAVAILABLE")
}
