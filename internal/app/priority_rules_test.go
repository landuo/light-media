package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"light-media/internal/catalog"
	"light-media/internal/priorityrules"
)

func priorityText(s string) *string { return &s }
func prioritySettings() *priorityrules.Configuration {
	return &priorityrules.Configuration{SearchGroups: []string{"quality"}, SubscribeGroups: []string{"subscription"}, Groups: []priorityrules.Group{{Name: "quality", RuleString: priorityText("WEBDL & 4K > WEBDL & 1080P")}, {Name: "subscription", RuleString: priorityText("BLURAY")}}, CustomRules: []priorityrules.Definition{{"id": "PrivatePattern", "name": "合成规则", "include": "synthetic-priority-private-pattern"}}, Mode: priorityrules.Rust}
}
func priorityListing() string {
	parts := []string{}
	for i, title := range []string{"Synthetic.S01E01.1080p.WEB-DL-Low", "Synthetic.S01E01.2160p.WEB-DL-High", "Synthetic.S01E01.2160p.BluRay"} {
		parts = append(parts, strings.ReplaceAll(workflowHTML(title, "", true), "id=101", "id="+fmt.Sprint(i+1)))
	}
	return keywordFixtureListing(parts...)
}
func priorityCandidateIDs(out SearchOutput) []string {
	ids := []string{}
	for _, c := range out.Candidates {
		ids = append(ids, c.Resource.ID)
	}
	return ids
}
func TestPriorityRulesRealSearchSettingsAndSubscriptionScopes(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	cookie := setupTestAdmin(t, a)
	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/torrents.php" {
			t.Error("priority filtering fetched private material", r.URL.Path)
		}
		io.WriteString(w, priorityListing())
	}))
	defer site.Close()
	workflowConfigure(t, a, site.URL, "http://127.0.0.1:1")
	c := a.Settings()
	c.DefaultRoot = ""
	if err := a.saveSettings(ctx, c); err != nil {
		t.Fatal(err)
	}
	for _, section := range priorityRuleSections {
		raw, _ := json.Marshal(map[string]any{"revision": 0, "value": ruleSection(*prioritySettings(), section)})
		response := callTest(t, a.Handler(), "PUT", "/api/settings/rules/"+section, string(raw), cookie, "")
		if response.Code != 200 {
			t.Fatal("priority section rejected", section, response.Code, response.Body.String())
		}
	}
	public := callTest(t, a.Handler(), "GET", "/api/settings", "", cookie, "")
	if public.Code != 200 || !strings.Contains(public.Body.String(), `"priority_rules"`) {
		t.Fatal("saved rule configuration unavailable", public.Code)
	}
	var sealed []byte
	if err := a.Store.DB.QueryRow(`SELECT payload FROM records WHERE kind='config' AND id='main'`).Scan(&sealed); err != nil || strings.Contains(string(sealed), "synthetic-priority-private-pattern") {
		t.Fatal("priority config not encrypted", err)
	}
	var progress SearchOutput
	ctx = context.WithValue(ctx, searchProgressKey{}, func(out SearchOutput) { progress = out })
	out, err := a.searchResources(ctx, SearchRequest{Query: "Synthetic", Policy: Policy{PreferredGroups: []string{"Low"}}})
	if err != nil || !reflect.DeepEqual(priorityCandidateIDs(out), []string{"2", "1"}) || !reflect.DeepEqual(priorityCandidateIDs(progress), []string{"2", "1"}) || out.Candidates[0].Resource.PriorityOrder != 100 || out.Candidates[1].Resource.PriorityOrder != 99 {
		t.Fatal("real keyword search did not apply priority before preference", priorityCandidateIDs(out), err)
	}
	out, err = a.searchResources(ctx, SearchRequest{Query: "Synthetic", Policy: Policy{FilterGroups: []string{"subscription"}}})
	if err != nil || !reflect.DeepEqual(priorityCandidateIDs(out), []string{"3"}) {
		t.Fatal("explicit group did not replace global default", priorityCandidateIDs(out), err)
	}
	identity := catalog.Media{ID: 123, MediaType: "tv", Title: "Synthetic", OriginalTitle: "Synthetic", Year: 2026, OriginalLanguage: "zh"}
	season := catalog.Season{SeriesID: 123, SeasonNumber: 1, Episodes: []catalog.Episode{{ID: 1, SeasonNumber: 1, EpisodeNumber: 1, AirDate: "2020-01-01"}}}
	for key, value := range map[string]any{"media:tv:123": identity, "season:123:1": season} {
		if err = a.Store.Put(ctx, "cache", key, map[string]any{"at": time.Now(), "data": value}); err != nil {
			t.Fatal(err)
		}
	}
	out, err = a.search(ctx, SearchRequest{MediaType: "tv", TMDBID: 123, Season: 1, Episodes: []int{1}}, true)
	if err != nil || !reflect.DeepEqual(priorityCandidateIDs(out), []string{"3"}) {
		t.Fatal("automatic subscription used search defaults", priorityCandidateIDs(out), err)
	}
	var count int
	a.Store.DB.QueryRow(`SELECT COUNT(*) FROM records WHERE kind IN ('intent','native_download','material_blob')`).Scan(&count)
	if count != 0 {
		t.Fatal("rule search created a download", count)
	}
	dir := a.Dir
	if err = a.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := New(Options{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if got := reopened.Settings().PriorityRules; got == nil || got.Mode != priorityrules.Rust || !reflect.DeepEqual(got.SearchGroups, []string{"quality"}) {
		t.Fatal("priority rules did not survive actual restart")
	}
}
func TestPriorityRuleFailureClearsProgressAndConfigurationSnapshotIsFrozen(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	entered, release := make(chan struct{}), make(chan struct{})
	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-release
		io.WriteString(w, priorityListing())
	}))
	defer site.Close()
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	workflowConfigure(t, a, site.URL, "http://127.0.0.1:1")
	c := a.Settings()
	c.DefaultRoot = ""
	c.PriorityRules = prioritySettings()
	if err := a.saveSettings(ctx, c); err != nil {
		t.Fatal(err)
	}
	result := make(chan SearchOutput, 1)
	failure := make(chan error, 1)
	go func() {
		out, err := a.searchResources(ctx, SearchRequest{Query: "Synthetic"})
		result <- out
		failure <- err
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("real search did not start")
	}
	c = a.Settings()
	c.PriorityRules.Groups[0].RuleString = priorityText("BLURAY")
	if err := a.saveSettings(ctx, c); err != nil {
		t.Fatal(err)
	}
	close(release)
	select {
	case out := <-result:
		if err := <-failure; err != nil || !reflect.DeepEqual(priorityCandidateIDs(out), []string{"2", "1"}) {
			t.Fatal("in-flight snapshot changed", priorityCandidateIDs(out), err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("real search did not finish")
	}
	// A separate immediate server avoids reusing the one-shot synchronization.
	badSite := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, priorityListing()) }))
	defer badSite.Close()
	c = a.Settings()
	c.Sites[0].BaseURL = badSite.URL
	c.PriorityRules.Groups[0].RuleString = priorityText("Broken")
	c.PriorityRules.CustomRules = append(c.PriorityRules.CustomRules, priorityrules.Definition{"id": "Broken", "include": "synthetic-priority-private-broken-pattern("})
	if err := a.saveSettings(ctx, c); err != nil {
		t.Fatal(err)
	}
	var last SearchOutput
	ctx = context.WithValue(ctx, searchProgressKey{}, func(out SearchOutput) { last = out })
	out, err := a.searchResources(ctx, SearchRequest{Query: "Synthetic"})
	if pluginErrorCode(err) != "PRIORITY_RULE_FAILED" || len(out.Candidates) != 0 || len(last.Candidates) != 0 {
		t.Fatal("rule failure returned raw accepted resources", err, len(out.Candidates), len(last.Candidates))
	}
	if !strings.Contains(err.Error(), "quality") || !strings.Contains(err.Error(), "Broken") || !strings.Contains(err.Error(), "优先级 1") {
		t.Fatal("rule failure cannot be located", err)
	}
	if !a.logs.flush(ctx) {
		t.Fatal("rule diagnostic log not flushed")
	}
	page, logErr := a.queryLogs(ctx, map[string]string{"module": "rules"})
	raw, _ := json.Marshal(page.Entries)
	if logErr != nil || !strings.Contains(string(raw), "Broken") || !strings.Contains(string(raw), "quality") || strings.Contains(string(raw), "synthetic-priority-private-broken-pattern") {
		t.Fatal("rule log is missing safe location or exposed its private pattern", logErr)
	}
}
func TestPriorityConfigurationAndPolicyValidation(t *testing.T) {
	for _, config := range []priorityrules.Configuration{{Mode: "unknown"}, {CustomRules: []priorityrules.Definition{{"id": "X", "command": "arbitrary"}}}, {Groups: []priorityrules.Group{{Name: ""}}}} {
		if validateSettings(Settings{PriorityRules: &config}) == nil {
			t.Fatal("unsupported rule settings silently accepted")
		}
	}
	if validateSubscriptionPolicy(Policy{FilterGroups: []string{"invalid\nname"}}) == nil {
		t.Fatal("invalid subscription group reference accepted")
	}
}
